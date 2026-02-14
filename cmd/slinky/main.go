package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
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

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default: ~/.config/slinky/config.toml)")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress informational output")
	root.MarkFlagsMutuallyExclusive("verbose", "quiet")

	root.AddGroup(
		&cobra.Group{ID: "daemon", Title: "Daemon:"},
		&cobra.Group{ID: "debug", Title: "Debug:"},
	)

	root.AddCommand(startCmd())
	root.AddCommand(stopCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(renderCmd())
	root.AddCommand(cacheCmd())

	if err := fang.Execute(context.Background(), root); err != nil {
		os.Exit(1)
	}
}

func setupLogging() {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	} else if quiet {
		level = slog.LevelWarn
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))
}

func pidFilePath() string {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		home, _ := os.UserHomeDir()
		stateDir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateDir, "slinky", "pid")
}

func writePIDFile() error {
	path := pidFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func removePIDFile() {
	_ = os.Remove(pidFilePath())
}
