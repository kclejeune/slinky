package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	svc "github.com/kardianos/service"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/control"
	"github.com/kclejeune/slinky/internal/mount"
	"github.com/kclejeune/slinky/internal/reload"
	"github.com/kclejeune/slinky/internal/render"
	"github.com/kclejeune/slinky/internal/resolver"
	"github.com/kclejeune/slinky/internal/symlink"
	"github.com/kclejeune/slinky/internal/trust"
)

func startCmd() *cobra.Command {
	var foreground bool
	var mountBackend string

	cmd := &cobra.Command{
		Use:     "start",
		Short:   "Start the daemon in the background",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if mountBackend != "" && mountBackend != "auto" && mountBackend != "fuse" &&
				mountBackend != "tmpfs" &&
				mountBackend != "fifo" {
				return fmt.Errorf(
					"invalid mount backend %q: must be \"auto\", \"fuse\", \"tmpfs\", or \"fifo\"",
					mountBackend,
				)
			}

			if foreground {
				return runForeground(mountBackend)
			}

			return daemonizeStart(mountBackend)
		},
	}

	cmd.Flags().
		BoolVarP(&foreground, "foreground", "f", false, "run in the foreground instead of daemonizing")
	cmd.Flags().
		StringVarP(&mountBackend, "mount", "m", "", "mount backend (auto, fuse, tmpfs, fifo); overrides config")
	return cmd
}

func runCmd() *cobra.Command {
	var mountBackend string

	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Run the daemon in the foreground",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if mountBackend != "" && mountBackend != "auto" && mountBackend != "fuse" &&
				mountBackend != "tmpfs" &&
				mountBackend != "fifo" {
				return fmt.Errorf(
					"invalid mount backend %q: must be \"auto\", \"fuse\", \"tmpfs\", or \"fifo\"",
					mountBackend,
				)
			}

			return runForeground(mountBackend)
		},
	}

	cmd.Flags().
		StringVarP(&mountBackend, "mount", "m", "", "mount backend (auto, fuse, tmpfs, fifo); overrides config")
	return cmd
}

// runForeground runs the daemon in the current process (foreground).
func runForeground(mountBackend string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if mountBackend != "" {
		cfg.Settings.Mount.Backend = config.BackendType(mountBackend)
	}

	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	daemonLogFile, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer daemonLogFile.Close()
	logWriter := io.MultiWriter(os.Stderr, daemonLogFile)
	setupLoggingWithWriter(logWriter)

	// Atomic pointer avoids lock contention with context manager's onChange callback.
	var currentCfg atomic.Pointer[config.Config]
	currentCfg.Store(cfg)

	ageCipher, err := cipher.New(string(cfg.Settings.Cache.Cipher))
	if err != nil {
		return fmt.Errorf("initializing cipher: %w", err)
	}

	secretCache := cache.New(ageCipher)
	defer secretCache.Stop()

	symlinkMgr := symlink.NewManager()

	trustStore := trust.NewStore(trust.DefaultStorePath())

	// Initialize context manager with onChange callback.
	configNames := slinkycontext.ResolveProjectConfigNames(cfg)
	var backend mount.Backend
	var tplWatcher *render.Watcher
	ctxMgr := slinkycontext.NewManager(
		cfg,
		configNames,
		func(eff map[string]*slinkycontext.EffectiveFile) {
			latestCfg := currentCfg.Load()
			files := make(map[string]*config.FileConfig, len(eff))
			for name, ef := range eff {
				files[name] = ef.FileConfig
			}
			if err := symlinkMgr.ReconcileWithConfig(
				files,
				latestCfg.Settings.Mount.MountPoint,
				string(latestCfg.Settings.Symlink.Conflict),
				latestCfg.Settings.Symlink.BackupExtension,
			); err != nil {
				slog.Error("symlink reconcile failed", "error", err)
			}
			if backend != nil {
				if err := backend.Reconfigure(); err != nil {
					slog.Error("backend reconfigure failed", "error", err)
				}
			}
			if tplWatcher != nil {
				for _, fc := range files {
					if fc.Template != "" {
						tplWatcher.Watch(config.ExpandPath(fc.Template))
					}
				}
			}
		},
	)

	ctxMgr.SetTrustStore(trustStore)

	secretResolver := resolver.New(cfg, secretCache, ctxMgr)

	var backendErr error
	backend, backendErr = mount.NewBackend(cfg, secretResolver, ctxMgr)
	if backendErr != nil {
		return fmt.Errorf("initializing mount backend: %w", backendErr)
	}

	// Ensure mount point exists (FUSE needs it; tmpfs creates its own; fifo creates its own).
	if err := os.MkdirAll(cfg.Settings.Mount.MountPoint, 0o700); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	if err := symlinkMgr.Setup(cfg, cfg.Settings.Mount.MountPoint); err != nil {
		return fmt.Errorf("setting up symlinks: %w", err)
	}

	// Watch template files for hot-reload.
	var watchErr error
	tplWatcher, watchErr = render.NewWatcher(func() {
		if backend != nil {
			if err := backend.Reconfigure(); err != nil {
				slog.Error("reconfigure on template change failed", "error", err)
			}
		}
	})
	if watchErr != nil {
		slog.Warn("template watcher unavailable", "error", watchErr)
	} else {
		defer tplWatcher.Close()
		// Watch initial template files.
		for _, fc := range cfg.Files {
			if fc.Template != "" {
				tplWatcher.Watch(config.ExpandPath(fc.Template))
			}
		}
		go tplWatcher.Run()
	}

	cfgPath := cfgFile
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	// restartCh signals the mount loop to reinitialize the backend
	// when mount_backend or mount_point changes at runtime.
	restartCh := make(chan struct{}, 1)

	// Non-blocking send coalesces rapid config changes into a single restart.
	dispatcher := reload.New(func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})

	// Prologues: always run before rules.
	dispatcher.OnAlways(func(_, new *config.Config) {
		secretResolver.UpdateConfig(new)
	})
	dispatcher.OnAlways(func(_, new *config.Config) {
		backend.UpdateConfig(new)
	})
	dispatcher.OnAlways(func(_, new *config.Config) {
		currentCfg.Store(new)
	})

	// Rule 1: update global context when files or project config names change.
	dispatcher.Register(reload.Rule{
		Name: "update-global-context",
		Kind: reload.Callback,
		Match: func(diff *config.DiffResult) bool {
			return diff.FilesChanged() ||
				!slices.Equal(
					diff.OldSettings.ProjectConfigNames,
					diff.NewSettings.ProjectConfigNames,
				)
		},
		Handle: func(_, new *config.Config, _ *config.DiffResult) {
			newConfigNames := slinkycontext.ResolveProjectConfigNames(new)
			ctxMgr.UpdateGlobal(new, newConfigNames)
		},
	})

	// Rule 2: reconcile symlinks and backend when other settings change.
	dispatcher.Register(reload.Rule{
		Name: "reconcile-symlinks-and-backend",
		Kind: reload.Callback,
		Match: func(diff *config.DiffResult) bool {
			return diff.HasChanges() && !diff.FilesChanged() &&
				slices.Equal(
					diff.OldSettings.ProjectConfigNames,
					diff.NewSettings.ProjectConfigNames,
				)
		},
		Handle: func(_, _ *config.Config, _ *config.DiffResult) {
			latestCfg := currentCfg.Load()
			files := ctxMgr.EffectiveFileConfigs()
			if err := symlinkMgr.ReconcileWithConfig(
				files,
				latestCfg.Settings.Mount.MountPoint,
				string(latestCfg.Settings.Symlink.Conflict),
				latestCfg.Settings.Symlink.BackupExtension,
			); err != nil {
				slog.Error("symlink reconcile after config reload failed", "error", err)
			}
			if err := backend.Reconfigure(); err != nil {
				slog.Error("backend reconfigure after config reload failed", "error", err)
			}
		},
	})

	// Rule 3: update template watcher on any change.
	dispatcher.Register(reload.Rule{
		Name: "update-template-watcher",
		Kind: reload.Callback,
		Match: func(diff *config.DiffResult) bool {
			return diff.HasChanges()
		},
		Handle: func(_, new *config.Config, _ *config.DiffResult) {
			if tplWatcher != nil {
				for _, fc := range new.Files {
					if fc.Template != "" {
						tplWatcher.Watch(config.ExpandPath(fc.Template))
					}
				}
			}
		},
	})

	// Rule 4: swap cache cipher.
	dispatcher.Register(reload.Rule{
		Name: "swap-cache-cipher",
		Kind: reload.Callback,
		Match: func(diff *config.DiffResult) bool {
			return diff.OldSettings.Cache.Cipher != diff.NewSettings.Cache.Cipher
		},
		Handle: func(_, new *config.Config, _ *config.DiffResult) {
			newCipher, err := cipher.New(string(new.Settings.Cache.Cipher))
			if err != nil {
				slog.Error("failed to initialize new cache cipher, keeping current", "error", err)
			} else {
				secretCache.SwapCipher(newCipher)
				slog.Info("cache cipher hot-reloaded", "cipher", new.Settings.Cache.Cipher)
			}
		},
	})

	// Rule 5: restart mount backend.
	dispatcher.Register(reload.Rule{
		Name: "restart-mount",
		Kind: reload.Kill,
		Match: func(diff *config.DiffResult) bool {
			return diff.OldSettings.Mount.Backend != diff.NewSettings.Mount.Backend ||
				diff.OldSettings.Mount.MountPoint != diff.NewSettings.Mount.MountPoint
		},
	})

	cfgWatcher, cfgWatchErr := config.NewConfigWatcher(cfgPath, cfg, dispatcher.Dispatch)
	if cfgWatchErr != nil {
		slog.Warn("config watcher unavailable", "error", cfgWatchErr)
	} else {
		defer cfgWatcher.Close()
		go cfgWatcher.Run()
	}

	ctlServer := control.NewServer("", ctxMgr)
	ctlServer.SetCache(secretCache)
	ctlServer.SetConfigHashFunc(func() string {
		h, err := currentCfg.Load().Hash()
		if err != nil {
			slog.Error("config hash failed", "error", err)
			return ""
		}
		return h
	})
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
	signal.Notify(sigCh, unix.SIGINT, unix.SIGTERM, unix.SIGHUP)
	go func() {
		for sig := range sigCh {
			switch sig {
			case unix.SIGHUP:
				slog.Info("received SIGHUP, reloading config")
				if cfgWatcher != nil {
					go cfgWatcher.ForceReload()
				}
			default:
				slog.Info("received signal, shutting down", "signal", sig)
				symlinkMgr.Cleanup()
				ctlCancel()
				cancel()
				return
			}
		}
	}()

	slog.Info("starting slinky",
		"backend", cfg.Settings.Mount.Backend,
		"mount_point", cfg.Settings.Mount.MountPoint,
		"files", len(cfg.Files),
		"control_socket", ctlServer.SocketPath(),
	)

	// Mount loop: runs backend.Mount in a goroutine so the mount
	// can be reinitialized when the config watcher detects changes
	// to mount_backend or mount_point.
	for {
		mountCtx, mountCancel := context.WithCancel(ctx)
		mountDone := make(chan error, 1)
		go func() {
			mountDone <- backend.Mount(mountCtx)
		}()

		select {
		case err := <-mountDone:
			mountCancel()
			return err

		case <-restartCh:
			if ctx.Err() != nil {
				// Shutdown in progress, don't restart.
				mountCancel()
				<-mountDone
				return nil
			}

			slog.Info("reinitializing mount backend")
			mountCancel()
			<-mountDone

			newCfg := currentCfg.Load()

			symlinkMgr.Cleanup()

			var backendErr error
			backend, backendErr = mount.NewBackend(newCfg, secretResolver, ctxMgr)
			if backendErr != nil {
				return fmt.Errorf("reinitializing mount backend: %w", backendErr)
			}

			if err := os.MkdirAll(newCfg.Settings.Mount.MountPoint, 0o700); err != nil {
				return fmt.Errorf("creating mount point: %w", err)
			}
			if err := symlinkMgr.Setup(newCfg, newCfg.Settings.Mount.MountPoint); err != nil {
				return fmt.Errorf("setting up symlinks: %w", err)
			}

			if tplWatcher != nil {
				for _, fc := range newCfg.Files {
					if fc.Template != "" {
						tplWatcher.Watch(config.ExpandPath(fc.Template))
					}
				}
			}

			slog.Info("mount backend reinitialized",
				"backend", newCfg.Settings.Mount.Backend,
				"mount_point", newCfg.Settings.Mount.MountPoint,
			)
		}
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the running daemon",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If an OS service is installed and running, delegate to it.
			if s, installed := serviceInstalled(); installed {
				status, err := s.Status()
				if err == nil && status == svc.StatusRunning {
					if err := s.Stop(); err != nil {
						return fmt.Errorf("stopping service: %w", err)
					}
					fmt.Fprintln(os.Stderr, "service stopped")
					return nil
				}
			}

			// Fall through to direct PID+SIGTERM.
			pid, err := readPID()
			if err != nil {
				return fmt.Errorf("reading PID file: %w (is the daemon running?)", err)
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("finding process %d: %w", pid, err)
			}

			if err := proc.Signal(unix.SIGTERM); err != nil {
				return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
			}

			fmt.Fprintf(os.Stderr, "sent SIGTERM to slinky daemon (pid %d)\n", pid)
			return nil
		},
	}
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "restart",
		Short:   "Restart the running daemon",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If an OS service is installed and running, delegate to it.
			if s, installed := serviceInstalled(); installed {
				status, err := s.Status()
				if err == nil && status == svc.StatusRunning {
					if err := s.Restart(); err != nil {
						return fmt.Errorf("restarting service: %w", err)
					}
					fmt.Fprintln(os.Stderr, "service restarted")
					return nil
				}
			}

			// Best-effort stop via PID+SIGTERM, then re-daemonize.
			if pid, err := readPID(); err == nil {
				proc, err := os.FindProcess(pid)
				if err == nil {
					if err := proc.Signal(unix.SIGTERM); err == nil {
						fmt.Fprintf(os.Stderr, "sent SIGTERM to slinky daemon (pid %d)\n", pid)
						waitForShutdown(pid, 10*time.Second)
					}
				}
			}

			return daemonizeStart("")
		},
	}
}

// daemonizeStart re-execs the current binary as a detached background process.
// If an OS service is installed, it delegates to the service manager instead.
func daemonizeStart(mountBackend string) error {
	// Check if already running via socket liveness first.
	if _, err := control.NewClient("").Status(); err == nil {
		return fmt.Errorf("slinky is already running (daemon responded on control socket)")
	}

	// Fall back to PID file check.
	if pid, err := readPID(); err == nil {
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(unix.Signal(0)); err == nil {
				return fmt.Errorf("slinky is already running (pid %d)", pid)
			}
		}
	}

	// If an OS service is installed, delegate to it.
	if s, installed := serviceInstalled(); installed {
		if err := s.Start(); err != nil {
			return fmt.Errorf("starting service: %w", err)
		}
		fmt.Fprintln(os.Stderr, "service started")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// Build args: "run" with --config if specified.
	args := []string{"run"}
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

// waitForShutdown polls until the given process exits or the timeout elapses.
func waitForShutdown(pid int, timeout time.Duration) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			slog.Warn("timed out waiting for daemon shutdown", "pid", pid)
			return
		case <-ticker.C:
			proc, err := os.FindProcess(pid)
			if err != nil {
				return
			}
			if err := proc.Signal(unix.Signal(0)); err != nil {
				return // process exited
			}
		}
	}
}
