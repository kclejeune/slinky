package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/secrets"
)

func TestNativeRendererEnv(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(
		tplFile,
		[]byte(`token={{ env "TEST_SECRET_TOKEN" }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_SECRET_TOKEN", "abc123")

	r := &NativeRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, nil, nil, "")
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
	if err := os.WriteFile(
		tplFile,
		[]byte(`{{ env "DEFINITELY_NOT_SET_12345" }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	_, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, nil, nil, "")
	if err == nil {
		t.Error("expected error for missing env var")
	}
}

func TestNativeRendererEnvDefault(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(
		tplFile,
		[]byte(`host={{ envDefault "UNSET_HOST_VAR_12345" "fallback.example.com" }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, nil, nil, "")
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
	if err := os.WriteFile(
		tplFile,
		[]byte(`data={{ file "`+dataFile+`" | trimAll "\n" }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	r := &NativeRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "data=file-contents" {
		t.Errorf("Render() = %q, want %q", result, "data=file-contents")
	}
}

func TestCommandRenderer(t *testing.T) {
	r := &CommandRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Command: "echo",
		Args:    []string{"hello world"},
	}, nil, nil, "")
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
	_, err := r.Render("test", &config.FileConfig{
		Command: "false",
	}, nil, nil, "")
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
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, lookup, nil, "")
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if string(result) != "token=from_lookup" {
		t.Errorf("Render() = %q, want %q", result, "token=from_lookup")
	}
}

func TestNativeRendererCustomDelims(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	// The literal {{ }} must pass through untouched with custom delims.
	if err := os.WriteFile(
		tplFile,
		[]byte(`token=<< env "TEST_DELIM_TOKEN" >> raw={{ untouched }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_DELIM_TOKEN", "xyz789")

	r := &NativeRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
		Delims:   []string{"<<", ">>"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := "token=xyz789 raw={{ untouched }}"
	if string(result) != want {
		t.Errorf("Render() = %q, want %q", result, want)
	}
}

func TestNativeRendererProviderFuncs(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stub provider binaries that echo predictable values.
	for bin, out := range map[string]string{
		"fnox":       "fnox-value",
		"secretspec": "spec-value",
		"op":         "op-value",
	} {
		script := "#!/bin/sh\necho " + out + "\n"
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tplFile := filepath.Join(tmpDir, "test.tpl")
	tpl := `a={{ fnox "KEY_A" }} b={{ secretspec "KEY_B" }} c={{ op "op://v/i/f" }}`
	if err := os.WriteFile(tplFile, []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	secrets.Configure(config.IntegrationsSettings{
		OnePassword: config.OnePasswordSettings{Auth: config.OPAuthCLI},
	}, "test")
	defer secrets.Configure(config.IntegrationsSettings{}, "test")

	r := &NativeRenderer{}
	result, err := r.Render("test", &config.FileConfig{
		Template: tplFile,
	}, nil, map[string]string{
		"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "")
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := "a=fnox-value b=spec-value c=op-value"
	if string(result) != want {
		t.Errorf("Render() = %q, want %q", result, want)
	}
}

func TestExtractParsesProviderFuncs(t *testing.T) {
	// Templates using provider functions must still parse for env var
	// extraction (unknown functions would be a parse error).
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	tpl := `x={{ fnox "A" }} y={{ env "REAL_VAR" }} z={{ op "op://v/i/f" }}`
	if err := os.WriteFile(tplFile, []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := ExtractEnvVars("test", &config.FileConfig{Template: tplFile})
	if vars == nil {
		t.Fatal("ExtractEnvVars() = nil, want var set")
	}
	if !vars["REAL_VAR"] {
		t.Errorf("vars = %v, want REAL_VAR", vars)
	}
}
