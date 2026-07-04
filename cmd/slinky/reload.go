package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/control"
)

func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "reload",
		Short:   "Ask the running daemon to reload its config",
		GroupID: "daemon",
		Long: `Ask the running daemon to re-read its config file from disk.

The daemon also reloads automatically when the config file changes
(via a file watcher) and on SIGHUP; this command forces a reload
immediately over the control socket and reports the result.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient("")
			resp, err := client.Reload()
			if err != nil {
				return err
			}

			if !resp.OK {
				return fmt.Errorf("reload failed: %s", resp.Error)
			}

			if resp.Changed {
				fmt.Fprintln(os.Stderr, "config reloaded")
			} else {
				fmt.Fprintln(os.Stderr, "config unchanged")
			}
			return nil
		},
	}
}
