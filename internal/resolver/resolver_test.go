package resolver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
)

func setupResolver(t *testing.T) (*SecretResolver, string) {
	t.Helper()
	tmpDir := t.TempDir()

	tplFile := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte(`machine github.com
  login {{ env "TEST_GH_USER" }}
  password {{ env "TEST_GH_TOKEN" }}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{
				Backend:    config.BackendFUSE,
				MountPoint: filepath.Join(tmpDir, "mount"),
			},
			Cache: config.CacheConfig{
				Cipher:     config.CipherAgeEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: map[string]*config.FileConfig{
			"netrc": {
				Name:     "netrc",
				Render:   "native",
				Template: tplFile,
				Mode:     0o600,
				TTL:      config.Duration(5 * time.Minute),
			},
		},
	}

	ageCipher, err := cipher.NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}

	c := cache.New(ageCipher)
	t.Cleanup(c.Stop)

	return New(cfg, c, nil), tmpDir
}

func TestResolve(t *testing.T) {
	t.Setenv("TEST_GH_USER", "testuser")
	t.Setenv("TEST_GH_TOKEN", "ghp_testtoken123")

	r, _ := setupResolver(t)

	content, err := r.Resolve("netrc")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	expected := "machine github.com\n  login testuser\n  password ghp_testtoken123\n"
	if string(content) != expected {
		t.Errorf("Resolve() = %q, want %q", content, expected)
	}
}

func TestResolveCacheHit(t *testing.T) {
	t.Setenv("TEST_GH_USER", "testuser")
	t.Setenv("TEST_GH_TOKEN", "ghp_testtoken123")

	r, _ := setupResolver(t)

	_, err := r.Resolve("netrc")
	if err != nil {
		t.Fatal(err)
	}

	content, err := r.Resolve("netrc")
	if err != nil {
		t.Fatal(err)
	}

	expected := "machine github.com\n  login testuser\n  password ghp_testtoken123\n"
	if string(content) != expected {
		t.Errorf("Resolve() = %q, want %q", content, expected)
	}
}

func TestResolveUnknownFile(t *testing.T) {
	r, _ := setupResolver(t)

	_, err := r.Resolve("nonexistent")
	if err == nil {
		t.Error("expected error for unknown file")
	}
}

func TestRenderOnly(t *testing.T) {
	t.Setenv("TEST_GH_USER", "testuser")
	t.Setenv("TEST_GH_TOKEN", "ghp_testtoken123")

	r, _ := setupResolver(t)

	content, err := r.RenderOnly("netrc")
	if err != nil {
		t.Fatalf("RenderOnly() error: %v", err)
	}

	expected := "machine github.com\n  login testuser\n  password ghp_testtoken123\n"
	if string(content) != expected {
		t.Errorf("RenderOnly() = %q, want %q", content, expected)
	}
}

func TestResolveStaleTriggersRefresh(t *testing.T) {
	t.Setenv("TEST_GH_USER", "testuser")
	t.Setenv("TEST_GH_TOKEN", "ghp_testtoken123")

	tmpDir := t.TempDir()

	tplFile := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte(`token={{ env "TEST_GH_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{
				Backend:    config.BackendFUSE,
				MountPoint: filepath.Join(tmpDir, "mount"),
			},
			Cache: config.CacheConfig{
				Cipher:     config.CipherAgeEphemeral,
				DefaultTTL: config.Duration(50 * time.Millisecond),
			},
		},
		Files: map[string]*config.FileConfig{
			"netrc": {
				Name:     "netrc",
				Render:   "native",
				Template: tplFile,
				Mode:     0o600,
				TTL:      config.Duration(50 * time.Millisecond),
			},
		},
	}

	ageCipher, err := cipher.NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	c := cache.New(ageCipher)
	t.Cleanup(c.Stop)

	r := New(cfg, c, nil)

	_, err = r.Resolve("netrc")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Millisecond)

	content, err := r.Resolve("netrc")
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != "token=ghp_testtoken123" {
		t.Errorf("stale resolve = %q, want %q", content, "token=ghp_testtoken123")
	}
}
