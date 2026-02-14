package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/mount"
	"github.com/kclejeune/slinky/internal/resolver"
	"github.com/kclejeune/slinky/internal/symlink"
)

func startCmd() *cobra.Command {
	var daemonize bool

	cmd := &cobra.Command{
		Use:     "start",
		Short:   "Start the slinky daemon",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if daemonize {
				return fmt.Errorf("daemonize (-d) is not yet implemented")
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			cacheCipher, err := cipher.New(string(cfg.Settings.Cache.Cipher))
			if err != nil {
				return fmt.Errorf("initializing cipher: %w", err)
			}

			secretCache := cache.New(cacheCipher)
			defer secretCache.Stop()

			secretResolver := resolver.New(cfg, secretCache)

			backend, err := mount.NewBackend(cfg, secretResolver)
			if err != nil {
				return fmt.Errorf("initializing mount backend: %w", err)
			}

			// Only FUSE needs the mount point pre-created; tmpfs creates its own.
			if cfg.Settings.Mount.Backend == config.BackendFUSE {
				if err := os.MkdirAll(cfg.Settings.Mount.MountPoint, 0o700); err != nil {
					return fmt.Errorf("creating mount point: %w", err)
				}
			}

			symlinkMgr := symlink.NewManager(cfg.Settings.Symlink)
			if err := symlinkMgr.Setup(cfg, cfg.Settings.Mount.MountPoint); err != nil {
				return fmt.Errorf("setting up symlinks: %w", err)
			}

			if err := writePIDFile(); err != nil {
				slog.Warn("failed to write PID file", "error", err)
			}
			defer removePIDFile()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				sig := <-sigCh
				slog.Info("received signal, shutting down", "signal", sig)
				symlinkMgr.Cleanup()
				cancel()
			}()

			slog.Info("starting slinky",
				"backend", cfg.Settings.Mount.Backend,
				"mount_point", cfg.Settings.Mount.MountPoint,
				"files", len(cfg.Files),
			)

			return backend.Mount(ctx)
		},
	}

	cmd.Flags().BoolVarP(&daemonize, "daemonize", "d", false, "run in background")
	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the running slinky daemon",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			pidFile := pidFilePath()
			data, err := os.ReadFile(pidFile)
			if err != nil {
				return fmt.Errorf("reading PID file: %w (is the daemon running?)", err)
			}

			pid, err := strconv.Atoi(string(data))
			if err != nil {
				return fmt.Errorf("parsing PID: %w", err)
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("finding process %d: %w", pid, err)
			}

			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
			}

			fmt.Fprintf(os.Stderr, "sent SIGTERM to slinky daemon (pid %d)\n", pid)
			return nil
		},
	}
}
