package render

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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

type TemplateRenderer interface {
	Render(cfg *config.FileConfig) ([]byte, error)
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
// open()) skip disk I/O and re-parsing.
type NativeRenderer struct {
	mu    sync.RWMutex
	cache map[string]string // tplPath → template text
}

func (r *NativeRenderer) loadTemplate(tplPath string) (string, error) {
	r.mu.RLock()
	if text, ok := r.cache[tplPath]; ok {
		r.mu.RUnlock()
		return text, nil
	}
	r.mu.RUnlock()

	data, err := os.ReadFile(tplPath)
	if err != nil {
		return "", fmt.Errorf("reading template %q: %w", tplPath, err)
	}
	text := string(data)

	r.mu.Lock()
	if r.cache == nil {
		r.cache = make(map[string]string)
	}
	r.cache[tplPath] = text
	r.mu.Unlock()

	return text, nil
}

func (r *NativeRenderer) Render(cfg *config.FileConfig) ([]byte, error) {
	tplPath := config.ExpandPath(cfg.Template)
	tplText, err := r.loadTemplate(tplPath)
	if err != nil {
		return nil, err
	}

	funcMap, err := buildFuncMap()
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

func buildFuncMap() (template.FuncMap, error) {
	handler := sprout.New()

	if err := handler.AddRegistries(
		std.NewRegistry(),
		sproutstrings.NewRegistry(),
		encoding.NewRegistry(),
	); err != nil {
		return nil, err
	}

	funcMap := handler.Build()

	funcMap["env"] = envFunc
	funcMap["envDefault"] = envDefaultFunc
	funcMap["file"] = fileFunc
	funcMap["exec"] = execFunc

	return funcMap, nil
}

func envFunc(key string) (string, error) {
	val, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("required environment variable %q is not set", key)
	}
	return val, nil
}

func envDefaultFunc(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
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

func execFunc(name string, args ...string) (string, error) {
	slog.Debug("template function: exec", "command", name, "args", args)
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	cmdPath, err := resolveCommand(name)
	if err != nil {
		return "", fmt.Errorf("exec %q: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("exec %q: %w (stderr: %s)", name, err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimRight(stdout.String(), "\n"), nil
}

type CommandRenderer struct{}

func (r *CommandRenderer) Render(cfg *config.FileConfig) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	args := make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		args[i] = config.ExpandPath(arg)
	}

	cmdPath, err := resolveCommand(cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("command %q: %w", cfg.Command, err)
	}

	cmd := exec.CommandContext(ctx, cmdPath, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("command %q: %w (stderr: %s)", cfg.Command, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func resolveCommand(name string) (string, error) {
	env := os.Environ()
	cwd, _ := os.Getwd()
	return interp.LookPathDir(cwd, expand.ListEnviron(env...), name)
}
