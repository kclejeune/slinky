package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAllowAndTrust(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")
	configPath := filepath.Join(dir, ".slinky.toml")

	if err := os.WriteFile(configPath, []byte("[files.test]\ntemplate = \"test.tpl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(storePath)

	// Initially untrusted.
	trusted, err := s.IsTrusted(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Error("expected untrusted initially")
	}

	// Allow it.
	if err := s.Allow(configPath); err != nil {
		t.Fatal(err)
	}

	// Now trusted.
	trusted, err = s.IsTrusted(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Error("expected trusted after Allow")
	}

	// Modify the file — should become untrusted.
	if err := os.WriteFile(configPath, []byte("[files.test]\ntemplate = \"changed.tpl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trusted, err = s.IsTrusted(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Error("expected untrusted after file modification")
	}
}

func TestStoreDeny(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")
	configPath := filepath.Join(dir, ".slinky.toml")

	if err := os.WriteFile(configPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(storePath)
	if err := s.Allow(configPath); err != nil {
		t.Fatal(err)
	}

	if err := s.Deny(configPath); err != nil {
		t.Fatal(err)
	}

	trusted, err := s.IsTrusted(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Error("expected untrusted after Deny")
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")
	configPath := filepath.Join(dir, ".slinky.toml")

	if err := os.WriteFile(configPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Allow with first store instance.
	s1 := NewStore(storePath)
	if err := s1.Allow(configPath); err != nil {
		t.Fatal(err)
	}

	// Check with fresh store instance (re-reads from disk).
	s2 := NewStore(storePath)
	trusted, err := s2.IsTrusted(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Error("expected trusted from fresh store instance")
	}
}

func TestCheckPaths(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")
	config1 := filepath.Join(dir, "a.toml")
	config2 := filepath.Join(dir, "b.toml")

	if err := os.WriteFile(config1, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config2, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(storePath)

	// Neither trusted.
	untrusted, err := s.CheckPaths([]string{config1, config2})
	if err != nil {
		t.Fatal(err)
	}
	if untrusted == "" {
		t.Error("expected untrusted path")
	}

	// Allow both.
	if err := s.Allow(config1); err != nil {
		t.Fatal(err)
	}
	if err := s.Allow(config2); err != nil {
		t.Fatal(err)
	}

	untrusted, err = s.CheckPaths([]string{config1, config2})
	if err != nil {
		t.Fatal(err)
	}
	if untrusted != "" {
		t.Errorf("expected all trusted, got untrusted: %q", untrusted)
	}
}

func TestEmptyPaths(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "trusted.json"))

	untrusted, err := s.CheckPaths(nil)
	if err != nil {
		t.Fatal(err)
	}
	if untrusted != "" {
		t.Error("expected empty for nil paths")
	}
}
