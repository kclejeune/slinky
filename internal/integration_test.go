package internal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/resolver"
)

// TestDaemonLifecycleIntegration wires up config → cipher → cache → context
// manager → resolver to verify the full activate → resolve → deactivate cycle.
func TestDaemonLifecycleIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	tplDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tplFile := filepath.Join(tplDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte("machine github.com\n  login {{ env \"TEST_USER\" }}\n  password {{ env \"TEST_TOKEN\" }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{
				Backend:    config.BackendFUSE,
				MountPoint: filepath.Join(tmpDir, "mount"),
			},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
				DefaultTTL: config.Duration(5 * time.Minute),
			},
			Symlink: config.SymlinkConfig{Conflict: config.ConflictError, BackupExtension: "~"},
		},
		Files: make(map[string]*config.FileConfig),
	}

	projDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := `[files.netrc]
template = "` + tplFile + `"
mode = 384
`
	if err := os.WriteFile(filepath.Join(projDir, ".slinky.toml"), []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	ageCipher, err := cipher.NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	secretCache := cache.New(ageCipher)
	t.Cleanup(secretCache.Stop)

	configNames := slinkycontext.ResolveProjectConfigNames(cfg)
	ctxMgr := slinkycontext.NewManager(cfg, configNames, nil)

	r := resolver.New(cfg, secretCache, ctxMgr)

	env := map[string]string{
		"TEST_USER":  "octocat",
		"TEST_TOKEN": "ghp_secret123",
	}
	names, err := ctxMgr.Activate(projDir, env, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one effective file after activation")
	}

	content, err := r.Resolve("netrc")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	expected := "machine github.com\n  login octocat\n  password ghp_secret123\n"
	if string(content) != expected {
		t.Errorf("Resolve() = %q, want %q", content, expected)
	}

	acts := ctxMgr.Activations()
	if _, ok := acts[projDir]; !ok {
		t.Errorf("expected activation for %q", projDir)
	}

	eff := ctxMgr.Effective()
	if _, ok := eff["netrc"]; !ok {
		t.Error("expected 'netrc' in effective file set")
	}

	_, err = ctxMgr.Deactivate(projDir, 0)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}

	acts = ctxMgr.Activations()
	if len(acts) != 0 {
		t.Errorf("expected 0 activations after deactivate, got %d", len(acts))
	}

	_, err = r.Resolve("netrc")
	if err == nil {
		t.Error("expected error resolving deactivated file")
	}
	if !strings.Contains(err.Error(), "unknown file") {
		t.Errorf("unexpected error: %v", err)
	}
}
