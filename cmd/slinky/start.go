package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/control"
	"github.com/kclejeune/slinky/internal/mount"
	"github.com/kclejeune/slinky/internal/resolver"
	"github.com/kclejeune/slinky/internal/symlink"
)

func startCmd() *cobra.Command {
	var daemonize bool
	var mountBackend string

	cmd := &cobra.Command{
		Use:     "start",
		Short:   "Start the daemon (foreground, use -d to daemonize)",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if mountBackend != "" && mountBackend != "fuse" && mountBackend != "tmpfs" && mountBackend != "fifo" {
				return fmt.Errorf("invalid mount backend %q: must be \"fuse\", \"tmpfs\", or \"fifo\"", mountBackend)
			}

			if daemonize {
				return daemonizeStart(mountBackend)
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if mountBackend != "" {
				cfg.Settings.Mount.Backend = config.BackendType(mountBackend)
			}

			ageCipher, err := cipher.NewAgeEphemeral()
			if err != nil {
				return fmt.Errorf("initializing cipher: %w", err)
			}

			secretCache := cache.New(ageCipher)
			defer secretCache.Stop()

			symlinkMgr := symlink.NewManager()

			// Initialize context manager with onChange callback.
			configNames := slinkycontext.ResolveProjectConfigNames(cfg)
			var backend mount.Backend
			ctxMgr := slinkycontext.NewManager(cfg, configNames, func(eff map[string]*slinkycontext.EffectiveFile) {
				// On context change: reconcile symlinks and reconfigure mount backend.
				files := make(map[string]*config.FileConfig, len(eff))
				for name, ef := range eff {
					files[name] = ef.FileConfig
				}
				if err := symlinkMgr.ReconcileWithConfig(files, cfg.Settings.Mount.MountPoint, string(cfg.Settings.Symlink.Conflict), cfg.Settings.Symlink.BackupExtension); err != nil {
					slog.Error("symlink reconcile failed", "error", err)
				}
				if backend != nil {
					if err := backend.Reconfigure(); err != nil {
						slog.Error("backend reconfigure failed", "error", err)
					}
				}
			})

			secretResolver := resolver.New(cfg, secretCache, ctxMgr)

			var backendErr error
			backend, backendErr = mount.NewBackend(cfg, secretResolver, ctxMgr)
			if backendErr != nil {
				return fmt.Errorf("initializing mount backend: %w", backendErr)
			}

			// Ensure mount point exists (for FUSE; tmpfs creates its own).
			if cfg.Settings.Mount.Backend == config.BackendFUSE {
				if err := os.MkdirAll(cfg.Settings.Mount.MountPoint, 0o700); err != nil {
					return fmt.Errorf("creating mount point: %w", err)
				}
			}

			if err := symlinkMgr.Setup(cfg, cfg.Settings.Mount.MountPoint); err != nil {
				return fmt.Errorf("setting up symlinks: %w", err)
			}

			ctlServer := control.NewServer("", ctxMgr)
			ctlCtx, ctlCancel := context.WithCancel(context.Background())
			defer ctlCancel()
			go func() {
				if err := ctlServer.Serve(ctlCtx); err != nil {
					slog.Error("control socket error", "error", err)
				}
			}()

			reaper := slinkycontext.NewReaper(ctxMgr)
			go reaper.Run(ctlCtx)

			pidFile, err := acquirePIDLock()
			if err != nil {
				return fmt.Errorf("acquiring PID lock: %w", err)
			}
			defer func() {
				pidFile.Close()
				os.Remove(pidFilePath())
			}()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				sig := <-sigCh
				slog.Info("received signal, shutting down", "signal", sig)
				symlinkMgr.Cleanup()
				ctlCancel()
				cancel()
			}()

			slog.Info("starting slinky",
				"backend", cfg.Settings.Mount.Backend,
				"mount_point", cfg.Settings.Mount.MountPoint,
				"files", len(cfg.Files),
				"control_socket", ctlServer.SocketPath(),
			)

			return backend.Mount(ctx)
		},
	}

	cmd.Flags().BoolVarP(&daemonize, "daemonize", "d", false, "run in background")
	cmd.Flags().StringVarP(&mountBackend, "mount", "m", "", "mount backend to use (fuse, tmpfs); overrides config")
	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the running daemon",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPID()
			if err != nil {
				return fmt.Errorf("reading PID file: %w (is the daemon running?)", err)
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

// daemonizeStart re-execs the current binary as a detached background process.
func daemonizeStart(mountBackend string) error {
	// Check if already running via socket liveness first.
	if _, err := control.NewClient("").Status(); err == nil {
		return fmt.Errorf("slinky is already running (daemon responded on control socket)")
	}

	// Fall back to PID file check.
	if pid, err := readPID(); err == nil {
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				return fmt.Errorf("slinky is already running (pid %d)", pid)
			}
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// Build args: "start" with --config if specified, but without -d.
	args := []string{"start"}
	if cfgFile != "" {
		absPath, err := filepath.Abs(cfgFile)
		if err != nil {
			return fmt.Errorf("resolving config path: %w", err)
		}
		args = append(args, "--config", absPath)
	}
	if verbose {
		args = append(args, "--verbose")
	}
	if mountBackend != "" {
		args = append(args, "--mount", mountBackend)
	}

	// Ensure state dir exists for log file.
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	logFile, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	c := exec.Command(exe, args...)
	c.Stdout = logFile
	c.Stderr = logFile
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := c.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting background process: %w", err)
	}
	logFile.Close()

	fmt.Fprintf(os.Stderr, "slinky daemon started (pid %d)\n", c.Process.Pid)
	fmt.Fprintf(os.Stderr, "  log: %s\n", logFilePath())
	return nil
}
