package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := ExpandPath(tt.input)
		if got != tt.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Settings.Mount.Backend != BackendFUSE {
		t.Errorf("default backend = %q, want %q", cfg.Settings.Mount.Backend, BackendFUSE)
	}
	if cfg.Settings.Cache.Cipher != CipherAgeEphemeral {
		t.Errorf("default cipher = %q, want %q", cfg.Settings.Cache.Cipher, CipherAgeEphemeral)
	}
	if cfg.Settings.Cache.DefaultTTL != Duration(5*time.Minute) {
		t.Errorf("default TTL = %v, want %v", cfg.Settings.Cache.DefaultTTL, Duration(5*time.Minute))
	}
}

func TestLoadValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte("machine github.com\n  login user\n  password token\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgContent := `
[settings.mount]
backend = "fuse"
mount_point = "` + tmpDir + `/mount"

[settings.cache]
cipher = "age-ephemeral"
default_ttl = "10m"

[files.netrc]
template = "` + tplFile + `"
mode = 384
ttl = "15m"
symlink = "` + tmpDir + `/link"
`
	cfgFile := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Settings.Mount.Backend != BackendFUSE {
		t.Errorf("backend = %q, want %q", cfg.Settings.Mount.Backend, BackendFUSE)
	}

	fc, ok := cfg.Files["netrc"]
	if !ok {
		t.Fatal("expected 'netrc' file config")
	}
	if fc.Name != "netrc" {
		t.Errorf("file name = %q, want %q", fc.Name, "netrc")
	}
	if fc.Render != "native" {
		t.Errorf("render = %q, want %q", fc.Render, "native")
	}
}

func TestBackendTypeUnmarshalText(t *testing.T) {
	var b BackendType
	if err := b.UnmarshalText([]byte("fuse")); err != nil {
		t.Errorf("unexpected error for 'fuse': %v", err)
	}
	if b != BackendFUSE {
		t.Errorf("got %q, want %q", b, BackendFUSE)
	}

	if err := b.UnmarshalText([]byte("tmpfs")); err != nil {
		t.Errorf("unexpected error for 'tmpfs': %v", err)
	}
	if b != BackendTmpfs {
		t.Errorf("got %q, want %q", b, BackendTmpfs)
	}

	if err := b.UnmarshalText([]byte("fifo")); err != nil {
		t.Errorf("unexpected error for 'fifo': %v", err)
	}
	if b != BackendFIFO {
		t.Errorf("got %q, want %q", b, BackendFIFO)
	}

	if err := b.UnmarshalText([]byte("invalid")); err == nil {
		t.Error("expected error for invalid backend")
	}
}

func TestValidateInvalidCipher(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Cache.Cipher = CipherType("invalid")
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid cipher")
	}
}

func TestValidateNonPositiveTTL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Cache.DefaultTTL = Duration(0)
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero TTL")
	}

	cfg.Settings.Cache.DefaultTTL = Duration(-1 * time.Second)
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative TTL")
	}
}

func TestDurationUnmarshalText(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("5m")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != Duration(5*time.Minute) {
		t.Errorf("got %v, want %v", d, Duration(5*time.Minute))
	}

	if err := d.UnmarshalText([]byte("not-a-duration")); err == nil {
		t.Error("expected error for invalid duration string")
	}
}

func TestFileConfigValidateNativeNoTemplate(t *testing.T) {
	fc := &FileConfig{Render: "native"}
	if err := fc.Validate("test"); err == nil {
		t.Error("expected error for native mode without template")
	}
}

func TestFileConfigValidateCommandNoCommand(t *testing.T) {
	fc := &FileConfig{Render: "command"}
	if err := fc.Validate("test"); err == nil {
		t.Error("expected error for command mode without command")
	}
}

func TestFileTTL(t *testing.T) {
	defaultTTL := Duration(5 * time.Minute)

	fc := &FileConfig{TTL: Duration(10 * time.Minute)}
	d := fc.FileTTL(defaultTTL)
	if d != 10*time.Minute {
		t.Errorf("FileTTL = %v, want 10m", d)
	}

	fc2 := &FileConfig{}
	d2 := fc2.FileTTL(defaultTTL)
	if d2 != 5*time.Minute {
		t.Errorf("FileTTL (default) = %v, want 5m", d2)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Error("DefaultConfigPath() returned empty string")
	}
}

func TestSymlinkDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgContent := `
[settings.mount]
backend = "fuse"
mount_point = "` + tmpDir + `/mount"

[settings.cache]
cipher = "age-ephemeral"
default_ttl = "5m"

[files.test]
template = "` + tplFile + `"
`
	cfgFile := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Settings.Symlink.Conflict != ConflictError {
		t.Errorf("default conflict = %q, want %q", cfg.Settings.Symlink.Conflict, ConflictError)
	}
	if cfg.Settings.Symlink.BackupExtension != ".bkp" {
		t.Errorf("default backup_extension = %q, want %q", cfg.Settings.Symlink.BackupExtension, ".bkp")
	}
}

func TestValidateInvalidSymlinkConflict(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Symlink.Conflict = ConflictMode("invalid")
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid symlink conflict mode")
	}
}

func TestProjectRoot(t *testing.T) {
	configNames := []string{".slinky.toml", "slinky.toml", ".slinky/config.toml", "slinky/config.toml"}

	// Direct file: .slinky.toml in /foo/bar → root is /foo/bar
	root := ProjectRoot("/foo/bar/.slinky.toml", configNames)
	if root != "/foo/bar" {
		t.Errorf("ProjectRoot for direct file = %q, want %q", root, "/foo/bar")
	}

	// Subdirectory file: .slinky/config.toml in /foo/bar/.slinky/ → root is /foo/bar
	root = ProjectRoot("/foo/bar/.slinky/config.toml", configNames)
	if root != "/foo/bar" {
		t.Errorf("ProjectRoot for subdir file = %q, want %q", root, "/foo/bar")
	}
}

func TestResolveProjectPath(t *testing.T) {
	got := ResolveProjectPath("/absolute/path", "/project")
	if got != "/absolute/path" {
		t.Errorf("ResolveProjectPath(absolute) = %q, want %q", got, "/absolute/path")
	}

	got = ResolveProjectPath("templates/netrc.tpl", "/project")
	if got != "/project/templates/netrc.tpl" {
		t.Errorf("ResolveProjectPath(relative) = %q, want %q", got, "/project/templates/netrc.tpl")
	}
}

func TestLoadProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configNames := []string{".slinky.toml", "slinky.toml", ".slinky/config.toml", "slinky/config.toml"}

	tplFile := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	projConfig := `[files.netrc]
template = "` + tplFile + `"
mode = 384
`
	projFile := filepath.Join(tmpDir, ".slinky.toml")
	if err := os.WriteFile(projFile, []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := LoadProjectConfig(projFile, configNames)
	if err != nil {
		t.Fatalf("LoadProjectConfig() error: %v", err)
	}

	fc, ok := files["netrc"]
	if !ok {
		t.Fatal("expected 'netrc' file config")
	}
	if fc.Name != "netrc" {
		t.Errorf("Name = %q, want %q", fc.Name, "netrc")
	}
	if fc.Render != "native" {
		t.Errorf("Render = %q, want %q", fc.Render, "native")
	}
}

func TestLoadProjectConfigRejectsSettings(t *testing.T) {
	tmpDir := t.TempDir()
	configNames := []string{".slinky.toml"}

	projConfig := `[settings]
foo = "bar"

[files.netrc]
template = "test.tpl"
`
	projFile := filepath.Join(tmpDir, ".slinky.toml")
	if err := os.WriteFile(projFile, []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProjectConfig(projFile, configNames)
	if err == nil {
		t.Error("expected error when project config contains [settings]")
	}
}

func TestLoadProjectConfigRelativePaths(t *testing.T) {
	tmpDir := t.TempDir()
	configNames := []string{".slinky.toml"}

	tplFile := filepath.Join(tmpDir, "templates", "netrc.tpl")
	if err := os.MkdirAll(filepath.Dir(tplFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tplFile, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	projConfig := `[files.netrc]
template = "templates/netrc.tpl"
symlink = "output/netrc"
`
	projFile := filepath.Join(tmpDir, ".slinky.toml")
	if err := os.WriteFile(projFile, []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := LoadProjectConfig(projFile, configNames)
	if err != nil {
		t.Fatalf("LoadProjectConfig() error: %v", err)
	}

	fc := files["netrc"]
	if fc.Template != filepath.Join(tmpDir, "templates", "netrc.tpl") {
		t.Errorf("Template = %q, want %q", fc.Template, filepath.Join(tmpDir, "templates", "netrc.tpl"))
	}
	if fc.Symlink != filepath.Join(tmpDir, "output", "netrc") {
		t.Errorf("Symlink = %q, want %q", fc.Symlink, filepath.Join(tmpDir, "output", "netrc"))
	}
}
