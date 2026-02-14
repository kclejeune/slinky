package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show daemon status",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			pidFile := pidFilePath()
			data, err := os.ReadFile(pidFile)
			if err != nil {
				fmt.Println("slinky is not running")
				return nil
			}

			pid, err := strconv.Atoi(string(data))
			if err != nil {
				fmt.Println("slinky is not running (invalid PID file)")
				return nil
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				fmt.Println("slinky is not running")
				return nil
			}

			// On Unix, FindProcess always succeeds; signal 0 checks liveness.
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				fmt.Println("slinky is not running (stale PID file)")
				return nil
			}

			fmt.Printf("slinky is running (pid %d)\n", pid)
			return nil
		},
	}
}
