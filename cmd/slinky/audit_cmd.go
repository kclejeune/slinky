package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/audit"
	"github.com/kclejeune/slinky/internal/config"
)

func auditCmd() *cobra.Command {
	var follow bool
	var lines int
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "audit",
		Short:   "Show the secret read audit trail",
		GroupID: "debug",
		Long: `Show the audit trail of secret file reads.

Auditing is enabled in the global config:

  [settings.audit]
  enabled = true
  # log = "~/.local/state/slinky/audit.log"  (default)

Each entry records which file was read via which backend. The FUSE
backend also records the calling process (PID, UID, and process name);
FIFO readers are anonymous. Reads of tmpfs-backed files happen entirely
in the kernel and are not observable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := auditLogPath()

			f, err := os.Open(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf(
						"no audit log found at %s (is [settings.audit] enabled?)",
						path,
					)
				}
				return fmt.Errorf("opening audit log: %w", err)
			}
			defer f.Close()

			if lines > 0 {
				if err := seekToLastNLines(f, lines); err != nil {
					return err
				}
			}

			if err := printAuditEntries(f, asJSON); err != nil {
				return err
			}

			if !follow {
				return nil
			}

			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(200 * time.Millisecond):
				}

				if err := printAuditEntries(f, asJSON); err != nil {
					return err
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow audit log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "show last N entries (0 = entire log)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON lines")
	return cmd
}

// auditLogPath resolves the audit log path from the global config, falling
// back to the default when the config is unavailable or unset.
func auditLogPath() string {
	if cfg, err := config.Load(cfgFile); err == nil && cfg.Settings.Audit.Log != "" {
		return config.ExpandPath(cfg.Settings.Audit.Log)
	}
	return audit.DefaultLogPath()
}

// printAuditEntries reads JSONL events from r until EOF, printing each in
// either raw or human-readable form. Unparseable lines are printed as-is.
func printAuditEntries(r io.Reader, asJSON bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if asJSON {
			fmt.Printf("%s\n", line)
			continue
		}

		var ev audit.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			fmt.Printf("%s\n", line)
			continue
		}
		fmt.Println(formatAuditEvent(ev))
	}
	return scanner.Err()
}

func formatAuditEvent(ev audit.Event) string {
	caller := "reader unknown"
	if ev.PID > 0 {
		caller = fmt.Sprintf("pid=%d uid=%d", ev.PID, ev.UID)
		if ev.Process != "" {
			caller += " process=" + ev.Process
		}
	}
	return fmt.Sprintf(
		"%s  %-5s %-20s %s (%s)",
		ev.Time.Format(time.RFC3339),
		ev.Event,
		ev.File,
		caller,
		ev.Backend,
	)
}
