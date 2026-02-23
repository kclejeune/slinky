package render

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/go-sprout/sprout"
	"github.com/go-sprout/sprout/registry/encoding"
	"github.com/go-sprout/sprout/registry/std"
	sproutstrings "github.com/go-sprout/sprout/registry/strings"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"

	"github.com/kclejeune/slinky/internal/config"
)

const Timeout = 10 * time.Second

// EnvLookup resolves environment variables. nil means os.LookupEnv.
type EnvLookup func(string) (string, bool)

type TemplateRenderer interface {
	Render(cfg *config.FileConfig, envLookup EnvLookup, envOverrides map[string]string) ([]byte, error)
}

var sharedNativeRenderer = &NativeRenderer{}

func NewRenderer(fc *config.FileConfig) TemplateRenderer {
	switch fc.Render {
	case "command":
		return &CommandRenderer{}
	default:
		return sharedNativeRenderer
	}
}

// NativeRenderer uses Go's text/template with sprout functions and
// custom builtins: env, envDefault, file, exec.
// Parsed template texts are cached so that repeated renders (e.g. FUSE
// open()) skip disk I/O and re-parsing. The cache is invalidated when
// the template file's mtime changes.
type NativeRenderer struct {
	mu    sync.RWMutex
	cache map[string]cachedTemplate // tplPath → cached entry
}

type cachedTemplate struct {
	text  string
	mtime time.Time
}

func (r *NativeRenderer) loadTemplate(tplPath string) (string, error) {
	info, err := os.Stat(tplPath)
	if err != nil {
		return "", fmt.Errorf("reading template %q: %w", tplPath, err)
	}
	mtime := info.ModTime()

	r.mu.RLock()
	if cached, ok := r.cache[tplPath]; ok && cached.mtime.Equal(mtime) {
		r.mu.RUnlock()
		return cached.text, nil
	}
	r.mu.RUnlock()

	data, err := os.ReadFile(tplPath)
	if err != nil {
		return "", fmt.Errorf("reading template %q: %w", tplPath, err)
	}
	text := string(data)

	r.mu.Lock()
	if r.cache == nil {
		r.cache = make(map[string]cachedTemplate)
	}
	r.cache[tplPath] = cachedTemplate{text: text, mtime: mtime}
	r.mu.Unlock()

	return text, nil
}

func (r *NativeRenderer) Render(cfg *config.FileConfig, envLookup EnvLookup, envOverrides map[string]string) ([]byte, error) {
	tplPath := config.ExpandPath(cfg.Template)
	tplText, err := r.loadTemplate(tplPath)
	if err != nil {
		return nil, err
	}

	funcMap, err := buildFuncMap(envLookup, envOverrides)
	if err != nil {
		return nil, fmt.Errorf("building template functions: %w", err)
	}

	tmpl, err := template.New(cfg.Name).Funcs(funcMap).Parse(tplText)
	if err != nil {
		return nil, fmt.Errorf("parsing template %q: %w", tplPath, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return nil, fmt.Errorf("executing template %q: %w", tplPath, err)
	}

	return buf.Bytes(), nil
}

func buildFuncMap(envLookup EnvLookup, envOverrides map[string]string) (template.FuncMap, error) {
	if envLookup == nil {
		envLookup = os.LookupEnv
	}

	handler := sprout.New()

	if err := handler.AddRegistries(
		std.NewRegistry(),
		sproutstrings.NewRegistry(),
		encoding.NewRegistry(),
	); err != nil {
		return nil, err
	}

	funcMap := handler.Build()

	funcMap["env"] = func(key string) (string, error) {
		val, ok := envLookup(key)
		if !ok {
			return "", fmt.Errorf("required environment variable %q is not set", key)
		}
		return val, nil
	}
	funcMap["envDefault"] = func(key, fallback string) string {
		val, ok := envLookup(key)
		if ok && val != "" {
			return val
		}
		return fallback
	}
	funcMap["file"] = fileFunc
	funcMap["exec"] = makeExecFunc(envOverrides)

	return funcMap, nil
}

func fileFunc(path string) (string, error) {
	slog.Debug("template function: file", "path", path)
	path = config.ExpandPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file %q: %w", path, err)
	}
	return string(data), nil
}

// makeExecFunc returns a template function that runs a command and returns
// its stdout. envOverrides are merged into the process environment so that
// commands invoked from templates see the activating shell's PATH and other
// variables.
func makeExecFunc(envOverrides map[string]string) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		slog.Debug("template function: exec", "command", name, "args", args)
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()

		cmdPath, err := resolveCommand(name, envOverrides)
		if err != nil {
			return "", fmt.Errorf("exec %q: %w", name, err)
		}

		cmd := exec.CommandContext(ctx, cmdPath, args...)
		cmd.Env = mergeEnv(os.Environ(), envOverrides)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("exec %q: %w (stderr: %s)", name, err, strings.TrimSpace(stderr.String()))
		}

		return strings.TrimRight(stdout.String(), "\n"), nil
	}
}

type CommandRenderer struct{}

func (r *CommandRenderer) Render(cfg *config.FileConfig, _ EnvLookup, envOverrides map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	args := make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		args[i] = config.ExpandPath(arg)
	}

	cmdPath, err := resolveCommand(cfg.Command, envOverrides)
	if err != nil {
		return nil, fmt.Errorf("command %q: %w", cfg.Command, err)
	}

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Env = mergeEnv(os.Environ(), envOverrides)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("command %q: %w (stderr: %s)", cfg.Command, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func resolveCommand(name string, envOverrides map[string]string) (string, error) {
	var env []string
	if len(envOverrides) == 0 {
		env = os.Environ()
	} else {
		env = mergeEnv(os.Environ(), envOverrides)
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("resolveCommand: Getwd failed, falling back to /", "error", err)
		cwd = "/"
	}
	return interp.LookPathDir(cwd, expand.ListEnviron(env...), name)
}

// mergeEnv merges overrides into base environment entries, replacing existing
// keys rather than appending duplicates. Returns nil if overrides is empty
// (exec.Cmd interprets nil Env as "inherit process env").
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
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
