package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func TestNativeRendererEnv(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte(`token={{ env "TEST_SECRET_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_SECRET_TOKEN", "abc123")

	r := &NativeRenderer{}
	result, err := r.Render(&config.FileConfig{
		Name:     "test",
		Template: tplFile,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "token=abc123" {
		t.Errorf("Render() = %q, want %q", result, "token=abc123")
	}
}

func TestNativeRendererEnvMissing(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte(`{{ env "DEFINITELY_NOT_SET_12345" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	_, err := r.Render(&config.FileConfig{
		Name:     "test",
		Template: tplFile,
	}, nil, nil)
	if err == nil {
		t.Error("expected error for missing env var")
	}
}

func TestNativeRendererEnvDefault(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte(`host={{ envDefault "UNSET_HOST_VAR_12345" "fallback.example.com" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	result, err := r.Render(&config.FileConfig{
		Name:     "test",
		Template: tplFile,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "host=fallback.example.com" {
		t.Errorf("Render() = %q, want %q", result, "host=fallback.example.com")
	}
}

func TestNativeRendererFile(t *testing.T) {
	tmpDir := t.TempDir()

	dataFile := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(dataFile, []byte("file-contents\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte(`data={{ file "`+dataFile+`" | trimAll "\n" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	result, err := r.Render(&config.FileConfig{
		Name:     "test",
		Template: tplFile,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "data=file-contents" {
		t.Errorf("Render() = %q, want %q", result, "data=file-contents")
	}
}

func TestCommandRenderer(t *testing.T) {
	r := &CommandRenderer{}
	result, err := r.Render(&config.FileConfig{
		Name:    "test",
		Command: "echo",
		Args:    []string{"hello world"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	got := strings.TrimSpace(string(result))
	if got != "hello world" {
		t.Errorf("Render() = %q, want %q", got, "hello world")
	}
}

func TestCommandRendererFailure(t *testing.T) {
	r := &CommandRenderer{}
	_, err := r.Render(&config.FileConfig{
		Name:    "test",
		Command: "false",
	}, nil, nil)
	if err == nil {
		t.Error("expected error for failed command")
	}
}

func TestNewRendererFactory(t *testing.T) {
	native := NewRenderer(&config.FileConfig{Render: "native"})
	if _, ok := native.(*NativeRenderer); !ok {
		t.Errorf("expected NativeRenderer, got %T", native)
	}

	cmd := NewRenderer(&config.FileConfig{Render: "command"})
	if _, ok := cmd.(*CommandRenderer); !ok {
		t.Errorf("expected CommandRenderer, got %T", cmd)
	}
}

func TestNativeRendererWithEnvLookup(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte(`token={{ env "CUSTOM_VAR" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	lookup := func(key string) (string, bool) {
		if key == "CUSTOM_VAR" {
			return "from_lookup", true
		}
		return "", false
	}

	r := &NativeRenderer{}
	result, err := r.Render(&config.FileConfig{
		Name:     "test",
		Template: tplFile,
	}, lookup, nil)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "token=from_lookup" {
		t.Errorf("Render() = %q, want %q", result, "token=from_lookup")
	}
}
