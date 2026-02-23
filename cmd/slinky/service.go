package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	svc "github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// Service management via kardianos/service.
const serviceName = "slinky"

// svcProgram is a no-op service.Interface. We only use kardianos/service for
// install/uninstall and OS-level start/stop, not for wrapping the run loop.
type svcProgram struct{}

func (p *svcProgram) Start(s svc.Service) error { return nil }
func (p *svcProgram) Stop(s svc.Service) error  { return nil }

func newServiceConfig(configPath string) *svc.Config {
	args := []string{"run"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	return &svc.Config{
		Name:        serviceName,
		DisplayName: "slinky",
		Description: "Ephemeral, on-demand secret file materialization daemon",
		Arguments:   args,
		Option: svc.KeyValue{
			"UserService":  true,
			"KeepAlive":    true,
			"RunAtLoad":    true,
			"LogOutput":    true,
			"LogDirectory": stateDir(),
		},
	}
}

// serviceInstalled checks whether the slinky OS service is installed.
// Returns the service handle and true if installed, or nil and false otherwise.
func serviceInstalled() (svc.Service, bool) {
	s, err := svc.New(&svcProgram{}, newServiceConfig(""))
	if err != nil {
		return nil, false
	}
	_, err = s.Status()
	if errors.Is(err, svc.ErrNotInstalled) {
		return nil, false
	}
	// Any other error (or nil) means the service definition exists.
	return s, true
}

func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Aliases: []string{"svc"},
		Short:   "Manage the slinky OS service (launchd/systemd)",
		GroupID: "service",
	}

	cmd.AddCommand(serviceInstallCmd())
	cmd.AddCommand(serviceUninstallCmd())
	cmd.AddCommand(serviceShowCmd())
	return cmd
}

func serviceInstallCmd() *cobra.Command {
	var noStart bool
	var force bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install slinky as an OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve config to absolute path so the service definition is stable.
			configPath := cfgFile
			if configPath != "" {
				abs, err := filepath.Abs(configPath)
				if err != nil {
					return fmt.Errorf("resolving config path: %w", err)
				}
				configPath = abs
			}

			s, err := svc.New(&svcProgram{}, newServiceConfig(configPath))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if _, already := serviceInstalled(); already {
				if !force {
					fmt.Fprintln(os.Stderr, "service already installed (use --force to reinstall)")
					return nil
				}
				fmt.Fprintln(os.Stderr, "service already installed, reinstalling")
				_ = s.Stop()
				if err := s.Uninstall(); err != nil {
					return fmt.Errorf("uninstalling existing service: %w", err)
				}
			}

			if err := s.Install(); err != nil {
				return fmt.Errorf("installing service: %w", err)
			}
			fmt.Fprintln(os.Stderr, "service installed")

			if !noStart {
				if err := s.Start(); err != nil {
					return fmt.Errorf("starting service: %w", err)
				}
				fmt.Fprintln(os.Stderr, "service started")
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "reinstall the service if already installed")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "skip starting the service after installation")
	return cmd
}

func serviceUninstallCmd() *cobra.Command {
	var noStop bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the slinky OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Make uninstall idempotent: no-op if not installed.
			if _, installed := serviceInstalled(); !installed {
				fmt.Fprintln(os.Stderr, "service not installed, nothing to do")
				return nil
			}

			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if !noStop {
				if err := s.Stop(); err != nil {
					fmt.Fprintf(os.Stderr, "failed to stop service before uninstall: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "service stopped")
				}
			}

			if err := s.Uninstall(); err != nil {
				return fmt.Errorf("uninstalling service: %w", err)
			}

			fmt.Fprintln(os.Stderr, "service uninstalled")
			return nil
		},
	}

	cmd.Flags().BoolVar(&noStop, "no-stop", false, "skip stopping the service before uninstalling")
	return cmd
}

func serviceShowCmd() *cobra.Command {
	var raw bool

	cmd := &cobra.Command{
		Use:     "show",
		Aliases: []string{"cat", "print"},
		Short:   "Show the installed service unit (launchctl print / systemctl cat)",
		RunE: func(cmd *cobra.Command, args []string) error {
			platform := svc.Platform()

			if !raw {
				switch {
				case strings.HasPrefix(platform, "darwin"):
					return launchctlPrint()
				case strings.Contains(platform, "systemd"):
					return systemctlCat()
				}
			}

			// --raw or unsupported platform: dump the unit file directly.
			return showRawUnit()
		},
	}

	cmd.Flags().BoolVar(&raw, "raw", false, "print the raw unit file instead of formatted output")
	return cmd
}

func launchctlPrint() error {
	uid := os.Getuid()
	for _, ns := range []string{"gui", "user"} {
		domain := fmt.Sprintf("%s/%d/%s", ns, uid, serviceName)
		c := exec.Command("launchctl", "print", domain)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err == nil {
			return nil
		}
	}
	// Neither namespace worked; fall back to raw plist.
	return showRawUnit()
}

func systemctlCat() error {
	c := exec.Command("systemctl", "--user", "cat", serviceName+".service")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return showRawUnit()
	}
	return nil
}

func showRawUnit() error {
	path := serviceUnitPath()
	if path == "" {
		return fmt.Errorf("unsupported platform %q", svc.Platform())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("service not installed (no unit file at %s)", path)
		}
		return fmt.Errorf("reading unit file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "# %s\n", path)
	_, err = os.Stdout.Write(data)
	return err
}

func logsCmd() *cobra.Command {
	var follow bool
	var lines int

	cmd := &cobra.Command{
		Use:     "log",
		Aliases: []string{"logs"},
		Short:   "Show daemon log output",
		GroupID: "daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := logFilePath()

			f, err := os.Open(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("no log file found at %s (has the daemon been started with -d?)", path)
				}
				return fmt.Errorf("opening log file: %w", err)
			}
			defer f.Close()

			if lines > 0 {
				if err := seekToLastNLines(f, lines); err != nil {
					return err
				}
			}

			if _, err := io.Copy(os.Stdout, f); err != nil {
				return fmt.Errorf("reading log file: %w", err)
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

				if _, err := io.Copy(os.Stdout, f); err != nil {
					return fmt.Errorf("reading log file: %w", err)
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "show last N lines (0 = entire file)")
	return cmd
}

// seekToLastNLines seeks the file to the start of the last n lines.
func seekToLastNLines(f *os.File, n int) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}

	// Read from the end in chunks to find newline positions.
	const chunkSize = 8192
	found := 0
	offset := size

	for offset > 0 && found <= n {
		readSize := min(int64(chunkSize), offset)
		offset -= readSize

		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, offset); err != nil {
			return err
		}

		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				found++
				if found > n {
					// Seek past this newline.
					_, err := f.Seek(offset+int64(i)+1, io.SeekStart)
					return err
				}
			}
		}
	}

	// Fewer than n lines in the file — start from the beginning.
	_, err = f.Seek(0, io.SeekStart)
	return err
}
