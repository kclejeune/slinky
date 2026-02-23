package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/control"
	"github.com/kclejeune/slinky/internal/mount"
	"github.com/kclejeune/slinky/internal/resolver"
	"github.com/kclejeune/slinky/internal/trust"
)

func doctorCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"dr"},
		Short:   "Diagnose common issues",
		GroupID: "debug",
		Long: `Run a series of checks to diagnose common issues:

  - Global config validity
  - Project config discovery and validation
  - Trust status of project configs
  - FUSE availability
  - Template rendering (dry-run)
  - Symlink target accessibility
  - Daemon status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}

			var issues int

			// 1. Global config.
			globalCfg, err := config.Load(cfgFile)
			if err != nil {
				printCheck(false, "global config: %v", err)
				issues++
				// Cannot continue without global config.
				return printSummary(issues)
			}
			printCheck(true, "global config loaded")

			// 2. FUSE availability.
			if mount.FUSEAvailable() {
				printCheck(true, "FUSE available")
			} else if mount.TmpfsAvailable() {
				printCheck(
					false,
					"FUSE not available (will use tmpfs fallback with backend = \"auto\")",
				)
			} else {
				printCheck(
					false,
					"FUSE and tmpfs not available (will use fifo fallback with backend = \"auto\")",
				)
			}

			// 3. Project configs.
			configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
			layers := slinkycontext.DiscoverLayers(dir, configNames)

			if len(layers) == 0 {
				printCheck(true, "no project configs found from %s", dir)
			} else {
				for _, layerPath := range layers {
					files, err := config.LoadProjectConfig(layerPath, configNames)
					if err != nil {
						printCheck(false, "project config %s: %v", layerPath, err)
						issues++
						continue
					}
					printCheck(true, "project config %s (%d files)", layerPath, len(files))

					for name, fc := range files {
						if err := fc.Validate(name); err != nil {
							printCheck(false, "  file %q: %v", name, err)
							issues++
						}
					}
				}
			}

			// 4. Trust status.
			store := trust.NewStore(trust.DefaultStorePath())
			for _, layerPath := range layers {
				trusted, err := store.IsTrusted(layerPath)
				if err != nil {
					printCheck(false, "trust check %s: %v", layerPath, err)
					issues++
				} else if trusted {
					printCheck(true, "trusted: %s", layerPath)
				} else {
					printCheck(false, "untrusted: %s (run \"slinky allow\")", layerPath)
					issues++
				}
			}

			// 5. Template rendering (dry-run global files).
			ageCipher, cipherErr := cipher.NewAgeEphemeral()
			if cipherErr != nil {
				printCheck(false, "cipher init: %v", cipherErr)
				issues++
			} else {
				secretCache := cache.New(ageCipher)
				defer secretCache.Stop()

				secretResolver := resolver.New(globalCfg, secretCache, nil)
				for _, name := range slices.Sorted(maps.Keys(globalCfg.Files)) {
					if _, renderErr := secretResolver.RenderOnly(name); renderErr != nil {
						printCheck(false, "render %q: %v", name, renderErr)
						issues++
					} else {
						printCheck(true, "render %q: ok", name)
					}
				}
			}

			// 6. Symlink targets.
			mountPoint := globalCfg.Settings.Mount.MountPoint
			for _, name := range slices.Sorted(maps.Keys(globalCfg.Files)) {
				fc := globalCfg.Files[name]
				if fc.Symlink == "" {
					continue
				}
				link := config.ExpandPath(fc.Symlink)
				target := filepath.Join(mountPoint, name)
				info, err := os.Lstat(link)
				if err != nil {
					printCheck(true, "symlink %s: not yet created (expected at daemon start)", link)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					dest, readErr := os.Readlink(link)
					if readErr != nil {
						printCheck(false, "symlink %s: cannot read link: %v", link, readErr)
						issues++
					} else if dest == target {
						// Symlink points to the right place — verify the target is readable.
						if f, openErr := os.Open(link); openErr != nil {
							printCheck(
								false,
								"symlink %s -> %s: not readable: %v",
								link,
								dest,
								openErr,
							)
							issues++
						} else {
							f.Close()
							printCheck(true, "symlink %s -> %s", link, dest)
						}
					} else {
						printCheck(false, "symlink %s -> %s (expected %s)", link, dest, target)
						issues++
					}
				} else {
					conflict := string(globalCfg.Settings.Symlink.Conflict)
					printCheck(
						false,
						"symlink %s: existing file (conflict mode: %s)",
						link,
						conflict,
					)
					issues++
				}
			}

			// 7. Daemon status.
			client := control.NewClient("")
			if _, err := client.Status(); err == nil {
				printCheck(true, "daemon is running")
			} else {
				printCheck(false, "daemon is not running")
				// Not counted as an issue — might be intentional.
			}

			return printSummary(issues)
		},
	}

	cmd.Flags().
		StringVarP(&dir, "directory", "d", "", "directory to check (default: current directory)")
	return cmd
}

func printCheck(ok bool, format string, args ...any) {
	prefix := "ok"
	if !ok {
		prefix = "!!"
	}
	msg := fmt.Sprintf(format, args...)
	// Indent continuation lines.
	msg = strings.ReplaceAll(msg, "\n", "\n      ")
	fmt.Fprintf(os.Stderr, "  [%s] %s\n", prefix, msg)
}

func printSummary(issues int) error {
	fmt.Fprintln(os.Stderr)
	if issues == 0 {
		fmt.Fprintln(os.Stderr, "No issues found.")
		return nil
	}
	return fmt.Errorf("%d issue(s) found", issues)
}
