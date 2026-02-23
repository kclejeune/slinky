package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/control"
	"github.com/kclejeune/slinky/internal/render"
)

func activateCmd() *cobra.Command {
	var hook bool
	var session int

	cmd := &cobra.Command{
		Use:     "activate [directory]",
		Short:   "Activate a directory context",
		GroupID: "context",
		Long: `Activate a directory context by discovering .slinky.toml files
walking up from the given directory (or $PWD if not specified).

The current shell environment is captured and forwarded to the daemon,
so template functions like env() use the variables from the activation
context rather than the daemon's own environment.

The calling shell's PID is automatically detected and used for session
reference counting. The daemon tracks which shell sessions hold references
to each activation and only fully removes it when the last session
deactivates or the reaper detects the process has exited. Activating a
new directory automatically removes the session from any previously
activated directories.

Typically called from shell hooks (mise, direnv):

  # mise.toml
  [hooks.enter]
  run = "slinky activate --hook"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}

			// Use the session leader PID (Getsid) rather than the direct
			// parent (Getppid). When called from shell hooks (e.g. mise),
			// the parent may be a transient intermediate process that exits
			// immediately, causing the reaper to kill the session prematurely.
			// The session leader is typically the login shell or terminal,
			// which stays alive as long as the terminal window is open.
			if session < 0 {
				sid, err := unix.Getsid(0)
				if err != nil {
					sid = os.Getppid() // fallback
				}
				session = sid
			}

			env := filterActivationEnv(dir)

			client := control.NewClient("")
			resp, err := client.Activate(dir, env, session)
			if err != nil {
				if hook {
					fmt.Fprintf(os.Stderr, "warning: %v\n", err)
					return nil
				}
				return err
			}

			if !resp.OK {
				return fmt.Errorf("activation failed: %s", resp.Error)
			}

			// Report render probe warnings/errors.
			if len(resp.Warnings) > 0 {
				if hook {
					for _, w := range resp.Warnings {
						fmt.Fprintf(os.Stderr, "warning: %s\n", w)
					}
				} else {
					for _, w := range resp.Warnings {
						fmt.Fprintf(os.Stderr, "error: %s\n", w)
					}
					return fmt.Errorf("activation succeeded but %d file(s) failed to render", len(resp.Warnings))
				}
			}

			// Check if the daemon's config matches the on-disk config.
			status, statusErr := client.Status()
			if statusErr == nil && status.ConfigHash != "" {
				diskCfg, loadErr := config.Load(cfgFile)
				if loadErr == nil {
					diskHash, hashErr := diskCfg.Hash()
					if hashErr == nil && diskHash != status.ConfigHash {
						fmt.Fprintf(os.Stderr, "warning: daemon config is out of date (run `slinky stop && slinky start` or send SIGHUP to reload)\n")
					}
				}
			}

			if !hook {
				fmt.Fprintf(os.Stderr, "activated context: %s (%d files)\n", dir, len(resp.Files))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&hook, "hook", false, "shell hook mode: suppress output and warn instead of failing")
	cmd.Flags().IntVar(&session, "session", -1, "shell session PID for reference counting (default: auto-detect parent PID; 0 to disable)")
	return cmd
}

func deactivateCmd() *cobra.Command {
	var hook bool
	var session int

	cmd := &cobra.Command{
		Use:     "deactivate [directory]",
		Short:   "Deactivate a directory context",
		GroupID: "context",
		Long: `Deactivate a previously activated directory context.

If no directory is specified, the current working directory ($PWD) is used.

The calling shell's PID is automatically detected. Only that session's
reference is removed; the activation persists until all sessions have left.
Use --session 0 to force-remove an activation regardless of other sessions.

Typically called from shell hooks when leaving a directory:

  # mise.toml
  [hooks.leave]
  run = "slinky deactivate --hook"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}

			// Auto-detect session PID (see activateCmd for rationale).
			if session < 0 {
				sid, err := unix.Getsid(0)
				if err != nil {
					sid = os.Getppid()
				}
				session = sid
			}

			client := control.NewClient("")
			resp, err := client.Deactivate(dir, session)
			if err != nil {
				if hook {
					fmt.Fprintf(os.Stderr, "warning: %v\n", err)
					return nil
				}
				return err
			}

			if !resp.OK {
				return fmt.Errorf("deactivation failed: %s", resp.Error)
			}

			if !hook {
				fmt.Fprintf(os.Stderr, "deactivated context: %s (%d files remaining)\n", dir, len(resp.Files))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&hook, "hook", false, "shell hook mode: suppress output and warn instead of failing")
	cmd.Flags().IntVar(&session, "session", -1, "shell session PID for reference counting (default: auto-detect parent PID; 0 to disable)")
	return cmd
}

// envAllowlist contains keys that are always forwarded to the daemon,
// even if no template references them. These are commonly needed for
// path resolution, user identification, and XDG conventions.
var envAllowlist = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "PATH": true,
	"SHELL": true, "TERM": true, "LANG": true,
}

// filterActivationEnv captures the current process environment, filtered to
// only the variable names referenced by templates in the global config and any
// project configs discovered from dir. Keys on the envAllowlist and any key
// starting with "XDG_" are always included.
func filterActivationEnv(dir string) map[string]string {
	// Capture full env first.
	fullEnv := make(map[string]string)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			fullEnv[k] = v
		}
	}

	// Collect the union of referenced env var names from all file configs.
	referenced := make(map[string]bool)

	globalCfg, err := config.Load(cfgFile)
	if err != nil {
		slog.Warn("env filtering: config load failed, forwarding full environment", "error", err)
		return fullEnv
	}

	for name, fc := range globalCfg.Files {
		vars := render.ExtractEnvVars(name, fc)
		if vars == nil {
			slog.Warn("env filtering: template extraction failed, forwarding full environment", "file", name)
			return fullEnv
		}
		for k := range vars {
			referenced[k] = true
		}
	}

	// Discover project layers and extract from each.
	configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
	layers := slinkycontext.DiscoverLayers(dir, configNames)
	for _, layerPath := range layers {
		files, err := config.LoadProjectConfig(layerPath, configNames)
		if err != nil {
			continue
		}
		for name, fc := range files {
			vars := render.ExtractEnvVars(name, fc)
			if vars == nil {
				slog.Warn("env filtering: project template extraction failed, forwarding full environment", "file", name)
				return fullEnv
			}
			for k := range vars {
				referenced[k] = true
			}
		}
	}

	// Build filtered env: referenced keys + allowlist + XDG_* keys.
	filtered := make(map[string]string, len(referenced)+len(envAllowlist))
	for k, v := range fullEnv {
		if referenced[k] || envAllowlist[k] || strings.HasPrefix(k, "XDG_") {
			filtered[k] = v
		}
	}
	return filtered
}
