package tmpfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/resolver"
)

func testBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	tmpDir := t.TempDir()

	tplDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tplFile := filepath.Join(tplDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte("machine github.com\n  login {{ env \"TEST_USER\" }}\n  password {{ env \"TEST_TOKEN\" }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dockerTplFile := filepath.Join(tplDir, "docker.tpl")
	if err := os.WriteFile(dockerTplFile, []byte(`{"auths":{"ghcr.io":{"auth":"{{ env "TEST_TOKEN" }}"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mountPoint := filepath.Join(tmpDir, "mount")

	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{
				Backend:    config.BackendTmpfs,
				MountPoint: mountPoint,
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
			"docker/config.json": {
				Name:     "docker/config.json",
				Render:   "native",
				Template: dockerTplFile,
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
	r := resolver.New(cfg, c, nil)

	b := &Backend{
		mountPoint: mountPoint,
		cfg:        cfg,
		resolver:   r,
		mounter:    &dirMounter{path: mountPoint},
		rendered:   make(map[string]string),
		reconfigCh: make(chan struct{}, 1),
	}

	return b, tmpDir
}

func TestRenderAll(t *testing.T) {
	t.Setenv("TEST_USER", "octocat")
	t.Setenv("TEST_TOKEN", "ghp_secret123")

	b, _ := testBackend(t)

	if err := b.mounter.Mount(); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}
	t.Cleanup(func() { _ = b.mounter.Unmount() })

	if err := b.renderAll(); err != nil {
		t.Fatalf("renderAll() error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(b.mountPoint, "netrc"))
	if err != nil {
		t.Fatalf("reading netrc: %v", err)
	}
	expected := "machine github.com\n  login octocat\n  password ghp_secret123\n"
	if string(content) != expected {
		t.Errorf("netrc content = %q, want %q", content, expected)
	}

	dockerContent, err := os.ReadFile(filepath.Join(b.mountPoint, "docker", "config.json"))
	if err != nil {
		t.Fatalf("reading docker/config.json: %v", err)
	}
	expectedDocker := `{"auths":{"ghcr.io":{"auth":"ghp_secret123"}}}`
	if string(dockerContent) != expectedDocker {
		t.Errorf("docker/config.json content = %q, want %q", dockerContent, expectedDocker)
	}
}

func TestRenderFileMode(t *testing.T) {
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_TOKEN", "token")

	b, _ := testBackend(t)
	if err := b.mounter.Mount(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.mounter.Unmount() })

	if err := b.renderFile("netrc", b.cfg.Files["netrc"]); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(b.mountPoint, "netrc"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestScrubAll(t *testing.T) {
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_TOKEN", "token")

	b, _ := testBackend(t)
	if err := b.mounter.Mount(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.mounter.Unmount() })

	if err := b.renderAll(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(b.mountPoint, "netrc")); err != nil {
		t.Fatal("netrc should exist before scrub")
	}

	b.scrubAll()

	if _, err := os.Stat(filepath.Join(b.mountPoint, "netrc")); !os.IsNotExist(err) {
		t.Error("netrc should not exist after scrub")
	}
	if _, err := os.Stat(filepath.Join(b.mountPoint, "docker", "config.json")); !os.IsNotExist(err) {
		t.Error("docker/config.json should not exist after scrub")
	}
}

func TestAtomicWriteIsAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	b := &Backend{mountPoint: tmpDir}

	dest := filepath.Join(tmpDir, "test-file")
	content := []byte("atomic content")

	if err := b.atomicWrite(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "atomic content" {
		t.Errorf("content = %q, want %q", got, "atomic content")
	}

	if err := b.atomicWrite(dest, []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(dest)
	if string(got) != "updated" {
		t.Errorf("content = %q, want %q", got, "updated")
	}
}

func TestAtomicWriteCreatesSubdirs(t *testing.T) {
	tmpDir := t.TempDir()
	b := &Backend{mountPoint: tmpDir}

	dest := filepath.Join(tmpDir, "a", "b", "c", "file.txt")
	if err := b.atomicWrite(dest, []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "nested" {
		t.Errorf("content = %q, want %q", got, "nested")
	}
}

func TestScrubFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "secret")
	if err := os.WriteFile(path, []byte("sensitive data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := scrubFile(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed after scrub")
	}
}

func TestScrubFileNonexistent(t *testing.T) {
	if err := scrubFile("/nonexistent/path/file"); err != nil {
		t.Errorf("scrubFile on nonexistent path should not error, got: %v", err)
	}
}

func TestMinTTL(t *testing.T) {
	b := &Backend{
		cfg: &config.Config{
			Settings: config.Settings{
				Cache: config.CacheConfig{DefaultTTL: config.Duration(10 * time.Minute)},
			},
			Files: map[string]*config.FileConfig{
				"a": {TTL: config.Duration(15 * time.Minute)},
				"b": {TTL: config.Duration(3 * time.Minute)},
				"c": {TTL: config.Duration(8 * time.Minute)},
			},
		},
	}

	got := b.minTTL()
	if got != 3*time.Minute {
		t.Errorf("minTTL = %v, want 3m", got)
	}
}

func TestMinTTLDefaultFallback(t *testing.T) {
	b := &Backend{
		cfg: &config.Config{
			Settings: config.Settings{
				Cache: config.CacheConfig{DefaultTTL: config.Duration(7 * time.Minute)},
			},
			Files: map[string]*config.FileConfig{
				"a": {},
			},
		},
	}

	got := b.minTTL()
	if got != 7*time.Minute {
		t.Errorf("minTTL = %v, want 7m", got)
	}
}

func TestMountContextCancellation(t *testing.T) {
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_TOKEN", "token")

	b, _ := testBackend(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- b.Mount(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	content, err := os.ReadFile(filepath.Join(b.mountPoint, "netrc"))
	if err != nil {
		t.Fatalf("file should exist after mount: %v", err)
	}
	if len(content) == 0 {
		t.Error("file should have content")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Mount() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Mount() did not return after context cancellation")
	}

	if _, err := os.Stat(b.mountPoint); !os.IsNotExist(err) {
		t.Error("mount point should be removed after unmount")
	}
}

func TestFileNames(t *testing.T) {
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_TOKEN", "token")

	b, _ := testBackend(t)
	if err := b.mounter.Mount(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.mounter.Unmount() })

	if err := b.renderAll(); err != nil {
		t.Fatal(err)
	}

	names := b.FileNames()
	if len(names) != 2 {
		t.Errorf("FileNames() returned %d names, want 2", len(names))
	}
}

func TestRefreshLoopReRendersFiles(t *testing.T) {
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_TOKEN", "original")

	tmpDir := t.TempDir()
	tplDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tplFile := filepath.Join(tplDir, "test.tpl")
	if err := os.WriteFile(tplFile, []byte("token={{ env \"TEST_TOKEN\" }}"), 0o644); err != nil {
		t.Fatal(err)
	}

	mountPoint := filepath.Join(tmpDir, "mount")

	cfg := &config.Config{
		Settings: config.Settings{
			Mount: config.MountConfig{
				Backend:    config.BackendTmpfs,
				MountPoint: mountPoint,
			},
			Cache: config.CacheConfig{
				Cipher:     config.CipherAgeEphemeral,
				DefaultTTL: config.Duration(50 * time.Millisecond),
			},
		},
		Files: map[string]*config.FileConfig{
			"test": {
				Name:     "test",
				Render:   "native",
				Template: tplFile,
				Mode:     0o600,
				TTL:      config.Duration(50 * time.Millisecond),
			},
		},
	}

	ageCipher, _ := cipher.NewAgeEphemeral()
	c := cache.New(ageCipher)
	t.Cleanup(c.Stop)
	r := resolver.New(cfg, c, nil)

	b := &Backend{
		mountPoint: mountPoint,
		cfg:        cfg,
		resolver:   r,
		mounter:    &dirMounter{path: mountPoint},
		rendered:   make(map[string]string),
		reconfigCh: make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- b.Mount(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	content, _ := os.ReadFile(filepath.Join(mountPoint, "test"))
	if string(content) != "token=original" {
		t.Errorf("initial content = %q, want %q", content, "token=original")
	}

	t.Setenv("TEST_TOKEN", "refreshed")

	time.Sleep(1500 * time.Millisecond)

	content, _ = os.ReadFile(filepath.Join(mountPoint, "test"))
	if string(content) != "token=refreshed" {
		t.Errorf("refreshed content = %q, want %q", content, "token=refreshed")
	}

	cancel()
	<-done
}
