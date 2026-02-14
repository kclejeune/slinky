package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	svc "github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// Service management via kardianos/service.

const serviceName = "dev.slinky"

// svcProgram is a no-op service.Interface. We only use kardianos/service for
// install/uninstall and OS-level start/stop, not for wrapping the run loop.
type svcProgram struct{}

func (p *svcProgram) Start(s svc.Service) error { return nil }
func (p *svcProgram) Stop(s svc.Service) error  { return nil }

func newServiceConfig(configPath string) *svc.Config {
	args := []string{"start"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	return &svc.Config{
		Name:        serviceName,
		DisplayName: "slinky",
		Description: "Ephemeral, on-demand secret file materialization daemon",
		Arguments:   args,
		Option: svc.KeyValue{
			"UserService": true,
			"KeepAlive":   true,
			"RunAtLoad":   true,
		},
	}
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
	cmd.AddCommand(serviceStartCmd())
	cmd.AddCommand(serviceStopCmd())
	cmd.AddCommand(serviceRestartCmd())
	cmd.AddCommand(serviceStatusCmd())
	return cmd
}

func serviceInstallCmd() *cobra.Command {
	var noStart bool

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

	cmd.Flags().BoolVar(&noStart, "no-start", false, "skip starting the service after installation")
	return cmd
}

func serviceUninstallCmd() *cobra.Command {
	var noStop bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the slinky OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if !noStop {
				if err := s.Stop(); err != nil {
					slog.Warn("failed to stop service before uninstall", "error", err)
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

func serviceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the installed OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if err := s.Start(); err != nil {
				return fmt.Errorf("starting service: %w", err)
			}

			fmt.Fprintln(os.Stderr, "service started")
			return nil
		},
	}
}

func serviceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if err := s.Stop(); err != nil {
				return fmt.Errorf("stopping service: %w", err)
			}

			fmt.Fprintln(os.Stderr, "service stopped")
			return nil
		},
	}
}

func serviceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the slinky OS service",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			if err := s.Restart(); err != nil {
				return fmt.Errorf("restarting service: %w", err)
			}

			fmt.Fprintln(os.Stderr, "service restarted")
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show OS service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc.New(&svcProgram{}, newServiceConfig(""))
			if err != nil {
				return fmt.Errorf("creating service: %w", err)
			}

			status, err := s.Status()
			if err != nil {
				return fmt.Errorf("querying service status: %w", err)
			}

			switch status {
			case svc.StatusRunning:
				fmt.Println("running")
			case svc.StatusStopped:
				fmt.Println("stopped")
			default:
				fmt.Println("unknown")
			}
			return nil
		},
	}
}
