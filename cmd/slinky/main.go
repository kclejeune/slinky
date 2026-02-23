// Package main is the CLI entry point for slinky.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	cfgFile string
	verbose bool
	quiet   bool
)

func main() {
	root := &cobra.Command{
		Use:   "slinky",
		Short: "Ephemeral, on-demand secret file materialization",
		Long:  `slinky presents templated secret files at stable filesystem paths without ever persisting plaintext to disk.`,
	}

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		setupLogging()
		return nil
	}

	root.PersistentFlags().
		StringVarP(&cfgFile, "config", "c", "", "config file (default: ~/.config/slinky/config.toml)")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress informational output")
	root.MarkFlagsMutuallyExclusive("verbose", "quiet")

	root.AddGroup(
		&cobra.Group{ID: "daemon", Title: "Daemon:"},
		&cobra.Group{ID: "context", Title: "Context:"},
		&cobra.Group{ID: "service", Title: "Service:"},
		&cobra.Group{ID: "debug", Title: "Debug:"},
	)

	root.AddCommand(startCmd())
	root.AddCommand(runCmd())
	root.AddCommand(stopCmd())
	root.AddCommand(restartCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(activateCmd())
	root.AddCommand(deactivateCmd())
	root.AddCommand(allowCmd())
	root.AddCommand(denyCmd())
	root.AddCommand(serviceCmd())
	root.AddCommand(logsCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(renderCmd())
	root.AddCommand(cacheCmd())
	root.AddCommand(cfgCmd())

	if err := fang.Execute(context.Background(), root); err != nil {
		os.Exit(1)
	}
}

func setupLogging() {
	setupLoggingWithWriter(os.Stderr)
}

func setupLoggingWithWriter(w io.Writer) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	} else if quiet {
		level = slog.LevelWarn
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	})))
}

// stateDir returns the slinky state directory under XDG_STATE_HOME.
func stateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "slinky")
}

func pidFilePath() string {
	return filepath.Join(stateDir(), "pid")
}

func logFilePath() string {
	return filepath.Join(stateDir(), "daemon.log")
}

// readPID reads and parses the PID file.
func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parsing PID: %w", err)
	}
	return pid, nil
}

// acquirePIDLock opens the PID file with an exclusive flock. Returns the
// locked file (caller must defer close+remove) or an error if another
// daemon holds the lock.
func acquirePIDLock() (*os.File, error) {
	path := pidFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening PID file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another slinky daemon is running (could not lock %s)", path)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
