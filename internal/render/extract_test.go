package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func writeTpl(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tpl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractEnvVarsBasicEnv(t *testing.T) {
	path := writeTpl(t, `{{ env "GITHUB_TOKEN" }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil vars")
	}
	if !vars["GITHUB_TOKEN"] {
		t.Error("expected GITHUB_TOKEN in vars")
	}
	if len(vars) != 1 {
		t.Errorf("expected 1 var, got %d", len(vars))
	}
}

func TestExtractEnvVarsEnvDefault(t *testing.T) {
	path := writeTpl(t, `{{ envDefault "API_HOST" "localhost" }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil vars")
	}
	if !vars["API_HOST"] {
		t.Error("expected API_HOST in vars")
	}
	if len(vars) != 1 {
		t.Errorf("expected 1 var, got %d", len(vars))
	}
}

func TestExtractEnvVarsMultiple(t *testing.T) {
	path := writeTpl(t, `machine example.com
login {{ env "USER" }}
password {{ env "TOKEN" }}
host {{ envDefault "HOST" "default" }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil vars")
	}
	if len(vars) != 3 {
		t.Errorf("expected 3 vars, got %d", len(vars))
	}
	for _, key := range []string{"USER", "TOKEN", "HOST"} {
		if !vars[key] {
			t.Errorf("expected %s in vars", key)
		}
	}
}

func TestExtractEnvVarsPiped(t *testing.T) {
	path := writeTpl(t, `{{ env "SECRET" | trimAll "\n" }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil vars")
	}
	if !vars["SECRET"] {
		t.Error("expected SECRET in vars")
	}
}

func TestExtractEnvVarsIfElse(t *testing.T) {
	path := writeTpl(t, `{{ if env "ENABLE" }}on{{ else }}{{ envDefault "FALLBACK" "off" }}{{ end }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil vars")
	}
	if !vars["ENABLE"] {
		t.Error("expected ENABLE in vars")
	}
	if !vars["FALLBACK"] {
		t.Error("expected FALLBACK in vars")
	}
	if len(vars) != 2 {
		t.Errorf("expected 2 vars, got %d", len(vars))
	}
}

func TestExtractEnvVarsCommandMode(t *testing.T) {
	cfg := &config.FileConfig{Render: "command", Command: "echo"}

	vars := ExtractEnvVars("test", cfg)
	if vars != nil {
		t.Error("expected nil for command mode")
	}
}

func TestExtractEnvVarsNoEnvCalls(t *testing.T) {
	path := writeTpl(t, `static content only`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	vars := ExtractEnvVars("test", cfg)
	if vars == nil {
		t.Fatal("expected non-nil (empty) vars, got nil")
	}
	if len(vars) != 0 {
		t.Errorf("expected 0 vars, got %d", len(vars))
	}
}

func TestExtractEnvVarsMissingTemplate(t *testing.T) {
	cfg := &config.FileConfig{Render: "native", Template: "/nonexistent/template.tpl"}

	vars := ExtractEnvVars("test", cfg)
	if vars != nil {
		t.Error("expected nil for missing template (safe fallback)")
	}
}

func TestFilterEnvBasic(t *testing.T) {
	path := writeTpl(t, `{{ env "KEEP_ME" }}`)
	cfg := &config.FileConfig{Render: "native", Template: path}

	env := map[string]string{
		"KEEP_ME": "yes",
		"DROP_ME": "no",
		"PATH":    "/usr/bin",
		"HOME":    "/home/user",
		"TERM":    "xterm",
		"OLDPWD":  "/tmp",
		"SHELL":   "/bin/bash",
	}

	filtered := FilterEnv("test", cfg, env)
	if filtered == nil {
		t.Fatal("expected non-nil filtered env")
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 var, got %d", len(filtered))
	}
	if filtered["KEEP_ME"] != "yes" {
		t.Errorf("KEEP_ME = %q, want %q", filtered["KEEP_ME"], "yes")
	}
	if _, ok := filtered["DROP_ME"]; ok {
		t.Error("DROP_ME should have been filtered out")
	}
}

func TestFilterEnvNilEnv(t *testing.T) {
	cfg := &config.FileConfig{Render: "native", Template: "irrelevant"}

	filtered := FilterEnv("test", cfg, nil)
	if filtered != nil {
		t.Error("expected nil for nil env")
	}
}

func TestFilterEnvCommandMode(t *testing.T) {
	cfg := &config.FileConfig{Render: "command", Command: "echo"}

	env := map[string]string{"FOO": "bar", "PATH": "/usr/bin", "HOME": "/home/user"}
	filtered := FilterEnv("test", cfg, env)
	if filtered == nil {
		t.Fatal("expected non-nil env for command mode")
	}
	if filtered["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want %q", filtered["PATH"], "/usr/bin")
	}
	if filtered["HOME"] != "/home/user" {
		t.Errorf("HOME = %q, want %q", filtered["HOME"], "/home/user")
	}
	if _, ok := filtered["FOO"]; ok {
		t.Error("FOO should have been filtered out (not in allowlist)")
	}
}

func TestFilterEnvMissingTemplateFallback(t *testing.T) {
	cfg := &config.FileConfig{Render: "native", Template: "/nonexistent/template.tpl"}

	env := map[string]string{"FOO": "bar", "BAZ": "qux"}
	filtered := FilterEnv("test", cfg, env)

	if len(filtered) != 2 {
		t.Errorf("expected original env (2 vars), got %d", len(filtered))
	}
	if filtered["FOO"] != "bar" {
		t.Error("expected original env to be returned unchanged")
	}
}
