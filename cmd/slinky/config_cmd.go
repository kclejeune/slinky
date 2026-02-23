package main

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/control"
	"github.com/kclejeune/slinky/internal/resolver"
)

const defaultGlobalConfigTemplate = `# slinky global configuration
# See: https://github.com/kclejeune/slinky

[settings.mount]
backend = "auto"           # "auto", "fuse", "tmpfs", or "fifo"
mount_point = "~/.secrets.d"

[settings.cache]
cipher = "ephemeral"            # "ephemeral", "auto", "keychain", "keyring", or "keyctl" (Linux only)
default_ttl = "5m"

# Define secret files below. Example:
#
# [files.netrc]
# template = "~/.config/slinky/templates/netrc.tpl"
# symlink = "~/.netrc"
# mode = 0o600
# ttl = "10m"
`

const defaultProjectConfigTemplate = `# slinky project configuration
# Only [files.*] sections are allowed here; [settings] is
# set in the global config (~/.config/slinky/config.toml).
#
# Example:
#
# [files.netrc]
# template = "~/.config/slinky/templates/netrc.tpl"
# symlink = "~/.netrc"
# mode = 0o600
# ttl = "10m"
`

func cfgInitCmd() *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a config file with defaults",
		Long: `Create a new slinky config file with commented defaults.

By default, creates a project config (.slinky.toml) in the current
directory. Use -g/--global to create the global config instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			var content string

			if global {
				path = cfgFile
				if path == "" {
					path = config.DefaultConfigPath()
				}
				path = config.ExpandPath(path)
				content = defaultGlobalConfigTemplate
			} else {
				dir, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
				path = filepath.Join(dir, slinkycontext.DefaultProjectConfigNames[0])
				content = defaultProjectConfigTemplate
			}

			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("config file already exists: %s", path)
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("creating config directory: %w", err)
			}

			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return fmt.Errorf("writing config file: %w", err)
			}

			fmt.Fprintf(os.Stderr, "created %s\n", path)
			return nil
		},
	}

	cmd.Flags().
		BoolVarP(&global, "global", "g", false, "create the global config instead of a project config")
	return cmd
}

func cfgCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"cfg"},
		Short:   "Show the resolved config hierarchy for a directory",
		GroupID: "debug",
		Long: `Inspect the config hierarchy without running the daemon.

Shows the global config, discovered project configs, and the effective
file set with which layer contributes each file (deepest wins).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}

			// Resolve global config path.
			globalPath := cfgFile
			if globalPath == "" {
				globalPath = config.DefaultConfigPath()
			}
			globalPath = config.ExpandPath(globalPath)

			// Load global config.
			globalCfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("global config: %w", err)
			}

			// Print global config.
			fmt.Printf("Global: %s\n", globalPath)
			globalNames := sortedFileNames(globalCfg.Files)
			for i, name := range globalNames {
				fc := globalCfg.Files[name]
				prefix := "├──"
				if i == len(globalNames)-1 {
					prefix = "└──"
				}
				fmt.Printf("%s %-20s %s\n", prefix, name, fileSource(fc))
			}

			// Discover project layers.
			configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
			layers := slinkycontext.DiscoverLayers(dir, configNames)

			effective := make(map[string]string)
			for _, name := range globalNames {
				effective[name] = globalPath
			}

			// Print each project layer.
			for _, layerPath := range layers {
				files, err := config.LoadProjectConfig(layerPath, configNames)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", layerPath, err)
					continue
				}

				fmt.Printf("\nProject: %s\n", layerPath)
				names := sortedFileNames(files)
				for i, name := range names {
					fc := files[name]
					prefix := "├──"
					if i == len(names)-1 {
						prefix = "└──"
					}
					override := ""
					if _, exists := effective[name]; exists {
						override = "  (overrides global)"
					}
					fmt.Printf("%s %-20s %s%s\n", prefix, name, fileSource(fc), override)
					effective[name] = layerPath
				}
			}

			// Print effective summary.
			fmt.Printf("\nEffective:\n")
			effNames := slices.Sorted(maps.Keys(effective))
			for _, name := range effNames {
				fmt.Printf("  %-20s <- %s\n", name, effective[name])
			}

			return nil
		},
	}

	cmd.Flags().
		StringVarP(&dir, "directory", "d", "", "directory to resolve from (default: current directory)")

	cmd.AddCommand(cfgInitCmd())
	cmd.AddCommand(cfgEditCmd())
	cmd.AddCommand(cfgValidateCmd())
	cmd.AddCommand(hookCmd())

	return cmd
}

func cfgValidateCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config files without starting the daemon",
		Long: `Check global and project config files for errors.

Validates TOML syntax, required fields, template paths, render modes,
and template parsing. Exits non-zero if any errors are found.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}

			globalCfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("global config: %w", err)
			}
			fmt.Fprintf(os.Stderr, "global config: ok\n")

			configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
			layers := slinkycontext.DiscoverLayers(dir, configNames)

			var errs []error
			for _, layerPath := range layers {
				files, err := config.LoadProjectConfig(layerPath, configNames)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", layerPath, err))
					continue
				}
				for name, fc := range files {
					if err := fc.Validate(name); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", layerPath, err))
					}
				}
				if len(files) == 0 {
					fmt.Fprintf(os.Stderr, "%s: ok (no files)\n", layerPath)
				} else {
					fmt.Fprintf(os.Stderr, "%s: ok (%d files)\n", layerPath, len(files))
				}
			}

			// Probe-render global files to catch template syntax errors.
			ageCipher, cipherErr := cipher.NewAgeEphemeral()
			if cipherErr != nil {
				return fmt.Errorf("initializing cipher: %w", cipherErr)
			}
			secretCache := cache.New(ageCipher)
			defer secretCache.Stop()

			secretResolver := resolver.New(globalCfg, secretCache, nil)
			for name := range globalCfg.Files {
				if _, renderErr := secretResolver.RenderOnly(name); renderErr != nil {
					errs = append(errs, fmt.Errorf("render %q: %w", name, renderErr))
				}
			}

			if len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "error: %v\n", e)
				}
				return fmt.Errorf("%d validation error(s)", len(errs))
			}

			fmt.Fprintf(os.Stderr, "all configs valid\n")
			return nil
		},
	}

	cmd.Flags().
		StringVarP(&dir, "directory", "d", "", "directory to resolve project configs from (default: current directory)")
	return cmd
}

func cfgEditCmd() *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR",
		Long: `Open a slinky config file in your editor.

By default, discovers all config files (global + project layers walking
up from the current directory). If multiple configs are found, you are
prompted to choose one. If only one exists, it opens directly.

Use -g/--global to skip discovery and open the global config directly.

The editor is determined by $EDITOR, falling back to $VISUAL.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor == "" {
				return fmt.Errorf("$EDITOR is not set")
			}

			globalPath := cfgFile
			if globalPath == "" {
				globalPath = config.DefaultConfigPath()
			}
			globalPath = config.ExpandPath(globalPath)

			var target string

			if global {
				target = globalPath
			} else {
				// Build the full list: global + project layers (deepest last).
				var candidates []string

				// Only include global if it exists.
				if _, err := os.Stat(globalPath); err == nil {
					candidates = append(candidates, globalPath)
				}

				dir, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}

				configNames := slinkycontext.DefaultProjectConfigNames
				globalCfg, err := config.Load(cfgFile)
				if err == nil {
					configNames = slinkycontext.ResolveProjectConfigNames(globalCfg)
				}

				layers := slinkycontext.DiscoverLayers(dir, configNames)
				candidates = append(candidates, layers...)

				switch len(candidates) {
				case 0:
					// No configs found — open the global path so the editor creates it.
					target = globalPath
				case 1:
					target = candidates[0]
				default:
					fmt.Fprintf(os.Stderr, "Multiple config files found:\n")
					for i, c := range candidates {
						label := ""
						if c == globalPath {
							label = " (global)"
						}
						fmt.Fprintf(os.Stderr, "  [%d] %s%s\n", i+1, c, label)
					}
					fmt.Fprintf(os.Stderr, "Select [1-%d]: ", len(candidates))

					scanner := bufio.NewScanner(os.Stdin)
					if !scanner.Scan() {
						return fmt.Errorf("no selection")
					}
					choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
					if err != nil || choice < 1 || choice > len(candidates) {
						return fmt.Errorf("invalid selection")
					}
					target = candidates[choice-1]
				}
			}

			c := exec.Command(editor, target)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}

	cmd.Flags().BoolVarP(&global, "global", "g", false, "open the global config directly")
	return cmd
}

func sortedFileNames(files map[string]*config.FileConfig) []string {
	return slices.Sorted(maps.Keys(files))
}

func fileSource(fc *config.FileConfig) string {
	switch fc.Render {
	case "command":
		parts := []string{fc.Command}
		parts = append(parts, fc.Args...)
		return "command  " + strings.Join(parts, " ")
	default:
		return "native  " + fc.Template
	}
}

func renderCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "render <name>",
		Short:   "Render a single file to stdout (debug)",
		GroupID: "debug",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// We don't need a real cipher/cache for render-only.
			ageCipher, err := cipher.NewAgeEphemeral()
			if err != nil {
				return fmt.Errorf("initializing cipher: %w", err)
			}

			secretCache := cache.New(ageCipher)
			defer secretCache.Stop()

			secretResolver := resolver.New(cfg, secretCache, nil)
			content, err := secretResolver.RenderOnly(args[0])
			if err != nil {
				return err
			}

			_, err = os.Stdout.Write(content)
			return err
		},
	}
}

func cacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cache",
		Short:   "Cache management commands",
		GroupID: "debug",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear all cached entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient("")
			resp, err := client.CacheClear()
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("cache clear failed")
			}
			fmt.Fprintln(os.Stderr, "cache cleared")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:     "stats",
		Aliases: []string{"info"},
		Short:   "Show cache statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient("")
			resp, err := client.CacheStats()
			if err != nil {
				return err
			}

			fmt.Printf("cipher:  %s\n", resp.Cipher)
			fmt.Printf("entries: %d\n", len(resp.Entries))

			if len(resp.Entries) > 0 {
				fmt.Println()
				keys := slices.Sorted(maps.Keys(resp.Entries))
				for _, k := range keys {
					info := resp.Entries[k]
					fmt.Printf(
						"  %-30s  age=%-10s ttl=%-10s %s\n",
						k,
						info.Age,
						info.TTL,
						info.State,
					)
				}
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List cached entry keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient("")
			resp, err := client.CacheStats()
			if err != nil {
				return err
			}

			keys := slices.Sorted(maps.Keys(resp.Entries))
			for _, k := range keys {
				fmt.Println(k)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Decrypt and print a cached entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient("")
			resp, err := client.CacheGet(args[0])
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Print(resp.Value)
			return nil
		},
	})

	return cmd
}
