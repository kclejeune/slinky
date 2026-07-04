package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	svc "github.com/kardianos/service"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/control"
)

// statusJSON is the machine-readable output of "slinky status --json".
type statusJSON struct {
	Running    bool                `json:"running"`
	PID        int                 `json:"pid,omitempty"`
	LogFile    string              `json:"log_file,omitempty"`
	ManagedBy  string              `json:"managed_by,omitempty"`
	ConfigHash string              `json:"config_hash,omitempty"`
	ActiveDirs []string            `json:"active_dirs,omitempty"`
	Files      []string            `json:"files,omitempty"`
	Layers     map[string][]string `json:"layers,omitempty"`
	Sessions   map[string][]int    `json:"sessions,omitempty"`
}

func statusCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Show daemon status and active contexts",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try the control socket first for richer info.
			client := control.NewClient("")
			resp, err := client.Status()

			if asJSON {
				return printStatusJSON(resp, err == nil)
			}

			if err == nil {
				fmt.Printf("slinky is running\n")
				printPID()
				fmt.Printf("  log: %s\n", logFilePath())
				fmt.Printf("  managed by: %s\n", managedByLabel())
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
				printServiceHint()
				return nil
			}

			// Check if process exists.
			proc, err := os.FindProcess(pid)
			if err != nil {
				fmt.Println("slinky is not running")
				printServiceHint()
				return nil
			}

			// On Unix, FindProcess always succeeds. Send signal 0 to check.
			if err := proc.Signal(unix.Signal(0)); err != nil {
				fmt.Println("slinky is not running (stale PID file)")
				printServiceHint()
				return nil
			}

			fmt.Printf("slinky is running (pid %d)\n", pid)
			fmt.Printf("  log: %s\n", logFilePath())
			fmt.Printf("  managed by: %s\n", managedByLabel())
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output status as JSON")
	return cmd
}

// printStatusJSON writes machine-readable status to stdout. When the daemon
// is not reachable over the control socket, only {"running": false} (plus
// any PID-file evidence) is emitted.
func printStatusJSON(resp *control.StatusResponse, socketOK bool) error {
	out := statusJSON{}

	if socketOK {
		slices.Sort(resp.Files)
		out = statusJSON{
			Running:    true,
			LogFile:    logFilePath(),
			ManagedBy:  managedByLabel(),
			ConfigHash: resp.ConfigHash,
			ActiveDirs: resp.ActiveDirs,
			Files:      resp.Files,
			Layers:     resp.Layers,
			Sessions:   resp.Sessions,
		}
		if pid, err := readPID(); err == nil {
			out.PID = pid
		}
	} else if pid, err := readPID(); err == nil {
		if proc, err := os.FindProcess(pid); err == nil {
			if err := proc.Signal(unix.Signal(0)); err == nil {
				// Process alive but socket unreachable (e.g. still starting).
				out.Running = true
				out.PID = pid
				out.LogFile = logFilePath()
				out.ManagedBy = managedByLabel()
			}
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printPID reads the PID file and prints the daemon PID if available.
func printPID() {
	if pid, err := readPID(); err == nil {
		fmt.Printf("  pid: %d\n", pid)
	}
}

// managedByLabel returns a human-readable label for how the daemon is managed.
func managedByLabel() string {
	if s, installed := serviceInstalled(); installed {
		label := "OS service"
		if unit := serviceUnitPath(); unit != "" {
			label += " (" + unit + ")"
		}
		status, err := s.Status()
		if err == nil {
			switch status {
			case svc.StatusRunning:
				return label
			case svc.StatusStopped:
				return label + " [stopped]"
			}
		}
		return label
	}
	return "direct (PID file)"
}

// serviceUnitPath returns the platform-specific service unit/plist file path,
// or empty string if unknown. kardianos/service does not expose this, so we
// replicate the well-known paths.
func serviceUnitPath() string {
	platform := svc.Platform()
	home, _ := os.UserHomeDir()

	switch {
	case strings.HasPrefix(platform, "darwin"):
		if home != "" {
			return filepath.Join(home, "Library", "LaunchAgents", serviceName+".plist")
		}
	case strings.Contains(platform, "systemd"):
		if home != "" {
			return filepath.Join(home, ".config", "systemd", "user", serviceName+".service")
		}
	}
	return ""
}

// printServiceHint prints a note when the daemon is not running but a service is installed.
func printServiceHint() {
	if s, installed := serviceInstalled(); installed {
		status, err := s.Status()
		if err == nil && status == svc.StatusStopped {
			fmt.Printf("  note: OS service is installed but stopped\n")
		}
	}
}
