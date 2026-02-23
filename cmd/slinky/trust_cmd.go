package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/trust"
)

func allowCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:     "allow [directory]",
		Short:   "Trust a project config for activation",
		GroupID: "context",
		Long: `Mark a project's .slinky.toml as trusted.

Project configs can execute arbitrary commands via the exec template function.
Before a project config can be activated, it must be explicitly trusted.
If the config file changes, re-approval is required.

If no directory is specified, the current directory is used. By default only the
config in the given directory is trusted. Use --all to also trust configs in
parent directories.`,
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

			globalCfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
			paths := discoverPaths(dir, configNames, all)

			if len(paths) == 0 {
				fmt.Fprintf(os.Stderr, "no project configs found from %s\n", dir)
				return nil
			}

			store := trust.NewStore(trust.DefaultStorePath())
			for _, p := range paths {
				// Validate the config before trusting it so users don't
				// approve a malformed file that would fail at activation.
				if _, err := config.LoadProjectConfig(p, configNames); err != nil {
					return fmt.Errorf("validating %s: %w", p, err)
				}
				if err := store.Allow(p); err != nil {
					return fmt.Errorf("allowing %s: %w", p, err)
				}
				fmt.Fprintf(os.Stderr, "allowed %s\n", p)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "trust configs in parent directories too")
	return cmd
}

func denyCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:     "deny [directory]",
		Short:   "Revoke trust for a project config",
		GroupID: "context",
		Long: `Remove trust for a project's .slinky.toml.

After denying, the project config will not be activated until re-allowed.
If no directory is specified, the current directory is used. Use --all to also
revoke trust for configs in parent directories.`,
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

			globalCfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			configNames := slinkycontext.ResolveProjectConfigNames(globalCfg)
			paths := discoverPaths(dir, configNames, all)

			if len(paths) == 0 {
				fmt.Fprintf(os.Stderr, "no project configs found from %s\n", dir)
				return nil
			}

			store := trust.NewStore(trust.DefaultStorePath())
			for _, p := range paths {
				if err := store.Deny(p); err != nil {
					return fmt.Errorf("denying %s: %w", p, err)
				}
				fmt.Fprintf(os.Stderr, "denied %s\n", p)
			}

			return nil
		},
	}

	cmd.Flags().
		BoolVarP(&all, "all", "a", false, "revoke trust for configs in parent directories too")
	return cmd
}

// discoverPaths returns the project config paths to operate on.
// When all is false, only the config in dir itself is returned (if any).
// When all is true, all layers from dir up to $HOME are returned.
func discoverPaths(dir string, configNames []string, all bool) []string {
	layers := slinkycontext.DiscoverLayers(dir, configNames)
	if all || len(layers) == 0 {
		return layers
	}
	// DiscoverLayers returns shallowest-first; the deepest (dir-local) config is last.
	return layers[len(layers)-1:]
}
