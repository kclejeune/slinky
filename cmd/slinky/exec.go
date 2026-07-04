package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/control"
)

func execCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:     "exec [--dir directory] -- command [args...]",
		Short:   "Run a command with a directory context activated",
		GroupID: "context",
		Long: `Activate a directory context for the duration of a single command.

The context is activated with the current environment before the command
runs, and deactivated when it exits — no shell hooks required. Other
sessions holding the same activation are unaffected: only this
invocation's reference is added and removed.

The command's exit code is propagated.

Examples:

  # Run docker push with this project's registry credentials
  slinky exec -- docker push ghcr.io/me/image

  # Combine with an env injector for one-shot secret materialization
  op run --env-file=.env -- slinky exec -- npm publish`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := dir
			if target == "" {
				var err error
				target, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
			}
			target, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolving directory: %w", err)
			}

			// Use our own PID as the session so the daemon's reaper cleans
			// up the activation if this process dies before deactivating.
			session := os.Getpid()
			env := filterActivationEnv(target)

			client := control.NewClient("")
			resp, err := client.Activate(target, env, session)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("activation failed: %s", resp.Error)
			}
			for _, w := range resp.Warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}

			exitCode, runErr := runChild(args)

			if _, err := client.Deactivate(target, session); err != nil {
				fmt.Fprintf(os.Stderr, "warning: deactivation failed: %v\n", err)
			}

			if runErr != nil {
				return runErr
			}
			if exitCode != 0 {
				// Propagate the child's exit code after cleanup.
				os.Exit(exitCode)
			}
			return nil
		},
	}

	cmd.Flags().
		StringVarP(&dir, "dir", "d", "", "directory context to activate (default: current directory)")
	// Stop flag parsing at the first positional arg so the child command's
	// own flags pass through without requiring "--".
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// runChild runs the command with inherited stdio, relaying SIGINT/SIGTERM,
// and returns its exit code.
func runChild(args []string) (int, error) {
	child := exec.Command(args[0], args[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGINT, unix.SIGTERM)
	defer signal.Stop(sigCh)

	if err := child.Start(); err != nil {
		return 0, fmt.Errorf("starting %q: %w", args[0], err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				_ = child.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()

	err := child.Wait()
	close(done)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("running %q: %w", args[0], err)
	}
	return 0, nil
}
