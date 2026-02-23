package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeValidConfig(t *testing.T, dir string, extra string) string {
	t.Helper()
	tplFile := filepath.Join(dir, "test.tpl")
	if _, err := os.Stat(tplFile); os.IsNotExist(err) {
		if err := os.WriteFile(tplFile, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	content := `
[settings.mount]
backend = "fuse"
mount_point = "` + dir + `/mount"

[settings.cache]
cipher = "ephemeral"
default_ttl = "5m"

[files.test]
template = "` + tplFile + `"
` + extra

	cfgFile := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgFile
}

func TestConfigWatcherDetectsChange(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := writeValidConfig(t, tmpDir, "")

	initial, err := Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var called bool
	var gotDiff *DiffResult

	cw, err := NewConfigWatcher(cfgFile, initial, func(old, new *Config, diff *DiffResult) {
		mu.Lock()
		called = true
		gotDiff = diff
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	go cw.Run()

	// Modify config: change default_ttl.
	writeValidConfig(t, tmpDir, "") // same content but let's change TTL
	tplFile := filepath.Join(tmpDir, "test.tpl")
	content := `
[settings.mount]
backend = "fuse"
mount_point = "` + tmpDir + `/mount"

[settings.cache]
cipher = "ephemeral"
default_ttl = "10m"

[files.test]
template = "` + tplFile + `"
`
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounced reload.
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		done := called
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for config reload callback")
		case <-time.After(50 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if gotDiff == nil {
		t.Fatal("expected diff, got nil")
	}
	if gotDiff.OldSettings.Cache.DefaultTTL == gotDiff.NewSettings.Cache.DefaultTTL {
		t.Error("expected DefaultTTL to differ in diff")
	}
}

func TestConfigWatcherForceReload(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := writeValidConfig(t, tmpDir, "")

	initial, err := Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var called bool

	cw, err := NewConfigWatcher(cfgFile, initial, func(old, new *Config, diff *DiffResult) {
		mu.Lock()
		called = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	// Change config before ForceReload.
	tplFile := filepath.Join(tmpDir, "test.tpl")
	content := `
[settings.mount]
backend = "fuse"
mount_point = "` + tmpDir + `/mount"

[settings.cache]
cipher = "ephemeral"
default_ttl = "15m"

[files.test]
template = "` + tplFile + `"
`
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cw.ForceReload()

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Error("ForceReload should trigger callback")
	}
}

func TestConfigWatcherInvalidConfigKeepsCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := writeValidConfig(t, tmpDir, "")

	initial, err := Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var called bool

	cw, err := NewConfigWatcher(cfgFile, initial, func(old, new *Config, diff *DiffResult) {
		mu.Lock()
		called = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	// Write invalid TOML.
	if err := os.WriteFile(cfgFile, []byte("invalid [[[ toml"), 0o644); err != nil {
		t.Fatal(err)
	}

	cw.ForceReload()

	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Error("callback should not be called for invalid config")
	}
}

func TestConfigWatcherNoChangeNoop(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := writeValidConfig(t, tmpDir, "")

	initial, err := Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var called bool

	cw, err := NewConfigWatcher(cfgFile, initial, func(old, new *Config, diff *DiffResult) {
		mu.Lock()
		called = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	// ForceReload with no changes.
	cw.ForceReload()

	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Error("callback should not be called when config unchanged")
	}
}
