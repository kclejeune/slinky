package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func TestComputeCacheKeyNativeMode(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte("template content"), 0o644); err != nil {
		t.Fatal(err)
	}

	key, err := ComputeCacheKey("test", &config.FileConfig{
		Render:   "native",
		Template: tplFile,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if key.FilePath != "test" {
		t.Errorf("FilePath = %q, want %q", key.FilePath, "test")
	}
	if key.String() == "" {
		t.Error("expected non-empty string representation")
	}
}

func TestComputeCacheKeySameContentSameKey(t *testing.T) {
	tmpDir := t.TempDir()

	tpl1 := filepath.Join(tmpDir, "a.tpl")
	tpl2 := filepath.Join(tmpDir, "b.tpl")
	content := []byte("identical content")
	if err := os.WriteFile(tpl1, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl2, content, 0o644); err != nil {
		t.Fatal(err)
	}

	key1, _ := ComputeCacheKey("test", &config.FileConfig{Template: tpl1}, nil)
	key2, _ := ComputeCacheKey("test", &config.FileConfig{Template: tpl2}, nil)

	if key1.Hash != key2.Hash {
		t.Error("same template content should produce the same hash")
	}
}

func TestComputeCacheKeyDifferentContentDifferentKey(t *testing.T) {
	tmpDir := t.TempDir()

	tpl1 := filepath.Join(tmpDir, "a.tpl")
	tpl2 := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tpl1, []byte("content A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl2, []byte("content B"), 0o644); err != nil {
		t.Fatal(err)
	}

	key1, _ := ComputeCacheKey("test", &config.FileConfig{Template: tpl1}, nil)
	key2, _ := ComputeCacheKey("test", &config.FileConfig{Template: tpl2}, nil)

	if key1.Hash == key2.Hash {
		t.Error("different template content should produce different hashes")
	}
}

func TestComputeCacheKeyCommandMode(t *testing.T) {
	key, err := ComputeCacheKey("test", &config.FileConfig{
		Render:  "command",
		Command: "op",
		Args:    []string{"inject", "-i", "template.tpl"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if key.FilePath != "test" {
		t.Errorf("FilePath = %q, want %q", key.FilePath, "test")
	}
}

func TestComputeCacheKeyWithEnv(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &config.FileConfig{Template: tplFile}

	keyNoEnv, _ := ComputeCacheKey("test", fc, nil)
	keyWithEnv, _ := ComputeCacheKey("test", fc, map[string]string{"TOKEN": "abc"})

	if keyNoEnv.Hash == keyWithEnv.Hash {
		t.Error("env should change the cache key")
	}
}

func TestComputeCacheKeyEnvOrder(t *testing.T) {
	tmpDir := t.TempDir()
	tplFile := filepath.Join(tmpDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &config.FileConfig{Template: tplFile}

	key1, _ := ComputeCacheKey("test", fc, map[string]string{"A": "1", "B": "2"})
	key2, _ := ComputeCacheKey("test", fc, map[string]string{"B": "2", "A": "1"})

	if key1.Hash != key2.Hash {
		t.Error("env key order should not affect cache key (sorted)")
	}
}
