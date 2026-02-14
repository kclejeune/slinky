package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/control"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show daemon status and active contexts",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try the control socket first for richer info.
			client := control.NewClient("")
			resp, err := client.Status()
			if err == nil {
				fmt.Printf("slinky is running\n")
				if len(resp.ActiveDirs) > 0 {
					fmt.Printf("  active dirs:\n")
					for _, d := range resp.ActiveDirs {
						fmt.Printf("    %s\n", d)
						if layers, ok := resp.Layers[d]; ok && len(layers) > 0 {
							fmt.Printf("      layers: %s\n", strings.Join(layers, ", "))
						}
						if pids, ok := resp.Sessions[d]; ok && len(pids) > 0 {
							pidStrs := make([]string, len(pids))
							for i, p := range pids {
								pidStrs[i] = strconv.Itoa(p)
							}
							fmt.Printf("      sessions: %s\n", strings.Join(pidStrs, ", "))
						}
					}
				}
				if len(resp.Files) > 0 {
					slices.Sort(resp.Files)
					fmt.Printf("  files: %s\n", strings.Join(resp.Files, ", "))
				}
				return nil
			}

			// Fall back to PID file check.
			pid, err := readPID()
			if err != nil {
				fmt.Println("slinky is not running")
				return nil
			}

			// Check if process exists.
			proc, err := os.FindProcess(pid)
			if err != nil {
				fmt.Println("slinky is not running")
				return nil
			}

			// On Unix, FindProcess always succeeds. Send signal 0 to check.
			if err := proc.Signal(unix.Signal(0)); err != nil {
				fmt.Println("slinky is not running (stale PID file)")
				return nil
			}

			fmt.Printf("slinky is running (pid %d)\n", pid)
			return nil
		},
	}
}
