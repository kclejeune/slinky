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

	if err := os.WriteFile(
		configPath,
		[]byte("[files.test]\ntemplate = \"test.tpl\"\n"),
		0o644,
	); err != nil {
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
	if err := os.WriteFile(
		configPath,
		[]byte("[files.test]\ntemplate = \"changed.tpl\"\n"),
		0o644,
	); err != nil {
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

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")

	current := filepath.Join(dir, "current.toml")
	stale := filepath.Join(dir, "stale.toml")
	missing := filepath.Join(dir, "missing.toml")

	for _, p := range []string{current, stale, missing} {
		if err := os.WriteFile(p, []byte("[files.test]\ntemplate = \"a.tpl\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := NewStore(storePath)
	for _, p := range []string{current, stale, missing} {
		if err := s.Allow(p); err != nil {
			t.Fatal(err)
		}
	}

	// Mutate one file and remove another.
	if err := os.WriteFile(stale, []byte("[files.test]\ntemplate = \"b.tpl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("List() returned %d entries, want 3", len(entries))
	}

	if got := entries[current]; got != EntryCurrent {
		t.Errorf("entries[current] = %q, want %q", got, EntryCurrent)
	}
	if got := entries[stale]; got != EntryStale {
		t.Errorf("entries[stale] = %q, want %q", got, EntryStale)
	}
	if got := entries[missing]; got != EntryMissing {
		t.Errorf("entries[missing] = %q, want %q", got, EntryMissing)
	}
}

func TestStoreListEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "trusted.json"))

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("List() returned %d entries, want 0", len(entries))
	}
}

func TestStorePrune(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.json")

	kept := filepath.Join(dir, "kept.toml")
	gone := filepath.Join(dir, "gone.toml")

	for _, p := range []string{kept, gone} {
		if err := os.WriteFile(p, []byte("[files.test]\ntemplate = \"a.tpl\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := NewStore(storePath)
	for _, p := range []string{kept, gone} {
		if err := s.Allow(p); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != gone {
		t.Errorf("Prune() removed = %v, want [%s]", removed, gone)
	}

	// Kept entry still trusted; pruned entry no longer in the store.
	trusted, err := s.IsTrusted(kept)
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Error("expected kept config to remain trusted after Prune")
	}

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries[gone]; ok {
		t.Error("pruned entry still present in List()")
	}

	// Prune with nothing to remove is a no-op.
	removed, err = s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("second Prune() removed = %v, want none", removed)
	}
}
