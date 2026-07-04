// Package secrets integrates external secret managers as template
// functions. Each integration resolves a single secret reference at render
// time; the rendered output inherits slinky's encrypted cache and TTL, so
// providers are consulted once per render, not once per read.
//
// Integrations:
//   - fnox (https://fnox.jdx.dev) — via the fnox CLI
//   - secretspec (https://secretspec.dev) — via the secretspec CLI
//   - 1Password — in-process via the official Go SDK (desktop-app or
//     service-account auth) with transparent fallback to the op CLI
//
// The active settings are process-global (mirroring log/slog and audit):
// callers invoke Configure at startup and on config reload.
package secrets

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"

	"github.com/kclejeune/slinky/internal/config"
)

// DefaultTimeout bounds a single provider resolution. It is deliberately
// generous: interactive auth (a 1Password desktop authorization prompt or
// biometric unlock) requires a human in the loop.
const DefaultTimeout = 2 * time.Minute

// Options carries per-render context into a provider call.
type Options struct {
	// WorkDir is the directory the provider runs in. Project-scoped files
	// pass their activation directory so tools like fnox and secretspec
	// discover the project's own config (fnox.toml, secretspec.toml).
	// Empty means the current process directory.
	WorkDir string
	// Env holds activation-time environment overrides, merged over the
	// daemon's own environment for provider subprocesses.
	Env map[string]string
}

type state struct {
	settings config.IntegrationsSettings
	version  string
}

var current atomic.Pointer[state]

// Configure installs the integration settings and the slinky version
// (reported to 1Password as integration info). Safe to call again on
// config reload.
func Configure(s config.IntegrationsSettings, version string) {
	current.Store(&state{settings: s, version: version})
	resetSDKClients()
}

// Settings returns the currently configured integration settings.
func Settings() config.IntegrationsSettings {
	if st := current.Load(); st != nil {
		return st.settings
	}
	return config.IntegrationsSettings{}
}

func integrationVersion() string {
	if st := current.Load(); st != nil && st.version != "" {
		return st.version
	}
	return "dev"
}

// envValue looks up key in the activation env overrides, then the process
// environment.
func envValue(key string, opts Options) (string, bool) {
	if v, ok := opts.Env[key]; ok {
		return v, true
	}
	return os.LookupEnv(key)
}

// runCLI executes a provider binary and returns its stdout with trailing
// newlines removed. extraEnv is applied over the activation env, which is
// applied over the process env. The binary is resolved against the merged
// PATH so activation-time PATH entries (mise shims, homebrew) are honored
// even when the daemon runs as a service with a minimal environment.
func runCLI(
	ctx context.Context,
	bin string,
	args []string,
	opts Options,
	extraEnv map[string]string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	env := mergeEnv(os.Environ(), opts.Env)
	env = mergeEnv(env, extraEnv)

	cwd := opts.WorkDir
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			cwd = "/"
		}
	}

	cmdPath, err := interp.LookPathDir(cwd, expand.ListEnviron(env...), bin)
	if err != nil {
		return "", fmt.Errorf("%s: %w (is it installed and on PATH?)", bin, err)
	}

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Env = env
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"%s %s: %w (stderr: %s)",
			bin,
			strings.Join(args, " "),
			err,
			strings.TrimSpace(stderr.String()),
		)
	}

	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

// mergeEnv merges overrides into base "KEY=VALUE" entries, replacing
// existing keys.
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}

	env := slices.Clone(base)
	existing := make(map[string]int, len(env))
	for i, entry := range env {
		if k, _, ok := strings.Cut(entry, "="); ok {
			existing[k] = i
		}
	}

	for k, v := range overrides {
		if idx, ok := existing[k]; ok {
			env[idx] = k + "=" + v
		} else {
			env = append(env, k+"="+v)
		}
	}
	return env
}
