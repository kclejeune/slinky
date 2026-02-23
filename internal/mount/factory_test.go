package mount

import (
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/resolver"
)

func testResolver(t *testing.T) *resolver.SecretResolver {
	t.Helper()
	cfg := &config.Config{
		Settings: config.Settings{
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*config.FileConfig),
	}
	ageCipher, err := cipher.NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	c := cache.New(ageCipher)
	t.Cleanup(c.Stop)
	return resolver.New(cfg, c, nil)
}

func TestNewBackendFuse(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{Backend: config.BackendFUSE, MountPoint: "/tmp/test"},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*config.FileConfig),
	}

	b, err := NewBackend(cfg, testResolver(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "fuse" {
		t.Errorf("Name() = %q, want %q", b.Name(), "fuse")
	}
}

func TestNewBackendTmpfs(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{Backend: config.BackendTmpfs, MountPoint: "/tmp/test"},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*config.FileConfig),
	}

	b, err := NewBackend(cfg, testResolver(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "tmpfs" {
		t.Errorf("Name() = %q, want %q", b.Name(), "tmpfs")
	}
}

func TestNewBackendFifo(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{Backend: config.BackendFIFO, MountPoint: "/tmp/test"},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*config.FileConfig),
	}

	b, err := NewBackend(cfg, testResolver(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "fifo" {
		t.Errorf("Name() = %q, want %q", b.Name(), "fifo")
	}
}

func TestNewBackendAuto(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{Backend: config.BackendAuto, MountPoint: "/tmp/test"},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*config.FileConfig),
	}

	b, err := NewBackend(cfg, testResolver(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Auto should resolve to "fuse", "tmpfs", or "fifo" depending on system.
	name := b.Name()
	if name != "fuse" && name != "tmpfs" && name != "fifo" {
		t.Errorf("Name() = %q, want \"fuse\", \"tmpfs\", or \"fifo\"", name)
	}
}

func TestFUSEAvailable(t *testing.T) {
	// Just verify it doesn't panic and returns a boolean.
	_ = FUSEAvailable()
}

func TestTmpfsAvailable(t *testing.T) {
	// Just verify it doesn't panic and returns a boolean.
	_ = TmpfsAvailable()
}

func TestNewBackendInvalid(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{Backend: config.BackendType("invalid")},
		},
	}

	_, err := NewBackend(cfg, testResolver(t), nil)
	if err == nil {
		t.Error("expected error for invalid backend")
	}
}
