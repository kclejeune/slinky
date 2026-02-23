package fifo

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

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
	if err := os.WriteFile(tplFile, []byte("machine github.com login {{ env \"TEST_USER\" }} password {{ env \"TEST_TOKEN\" }}\n"), 0o644); err != nil {
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
				Backend:    config.BackendFIFO,
				MountPoint: mountPoint,
			},
			Cache: config.CacheConfig{
				Cipher:     config.CipherEphemeral,
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

	return New(cfg, r, nil), tmpDir
}

func TestName(t *testing.T) {
	b, _ := testBackend(t)
	if got := b.Name(); got != "fifo" {
		t.Errorf("Name() = %q, want %q", got, "fifo")
	}
}

func TestMountCreatesDir(t *testing.T) {
	b, _ := testBackend(t)
	ctx := t.Context()

	if err := os.MkdirAll(b.mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	b.reconcileFIFOs(ctx)

	info, err := os.Stat(b.mountPoint)
	if err != nil {
		t.Fatalf("mount point not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("mount point is not a directory")
	}
}

func TestFIFOsCreated(t *testing.T) {
	b, _ := testBackend(t)
	ctx := t.Context()

	if err := os.MkdirAll(b.mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	b.reconcileFIFOs(ctx)

	for name := range b.cfg.Files {
		path := filepath.Join(b.mountPoint, name)
		var st unix.Stat_t
		if err := unix.Stat(path, &st); err != nil {
			t.Errorf("FIFO %q not found: %v", name, err)
			continue
		}
		if st.Mode&unix.S_IFMT != unix.S_IFIFO {
			t.Errorf("%q is not a FIFO (mode 0%o)", name, st.Mode)
		}
	}
}

func TestServeLoopWritesSecret(t *testing.T) {
	t.Setenv("TEST_USER", "alice")
	t.Setenv("TEST_TOKEN", "s3cr3t")

	b, _ := testBackend(t)
	b.cfg.Files = map[string]*config.FileConfig{
		"netrc": b.cfg.Files["netrc"],
	}

	ctx := t.Context()

	mountDone := make(chan error, 1)
	go func() {
		mountDone <- b.Mount(ctx)
	}()

	fifoPath := filepath.Join(b.mountPoint, "netrc")
	waitForFIFO(t, fifoPath, 2*time.Second)

	f, err := os.Open(fifoPath)
	if err != nil {
		t.Fatalf("opening FIFO for reading: %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading from FIFO: %v", err)
	}

	got := string(data)
	if got == "" {
		t.Error("read empty data from FIFO")
	}
	if !strings.Contains(got, "alice") || !strings.Contains(got, "s3cr3t") {
		t.Errorf("unexpected FIFO content: %q", got)
	}
}

func TestMountContextCancellation(t *testing.T) {
	b, _ := testBackend(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- b.Mount(ctx)
	}()

	fifoPath := filepath.Join(b.mountPoint, "netrc")
	waitForFIFO(t, fifoPath, 2*time.Second)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Mount returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Mount did not return after context cancellation")
	}

	if _, err := os.Lstat(fifoPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("FIFO still exists after teardown: %v", err)
	}
}

func TestReconfigure(t *testing.T) {
	b, tmpDir := testBackend(t)
	b.cfg.Files = map[string]*config.FileConfig{
		"netrc": b.cfg.Files["netrc"],
	}

	ctx := t.Context()

	done := make(chan error, 1)
	go func() { done <- b.Mount(ctx) }()

	netrcPath := filepath.Join(b.mountPoint, "netrc")
	waitForFIFO(t, netrcPath, 2*time.Second)

	newTpl := filepath.Join(tmpDir, "new.tpl")
	if err := os.WriteFile(newTpl, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	b.cfgMu.Lock()
	b.cfg.Files["newfile"] = &config.FileConfig{
		Name:     "newfile",
		Render:   "native",
		Template: newTpl,
		Mode:     0o600,
		TTL:      config.Duration(5 * time.Minute),
	}
	b.cfgMu.Unlock()

	if err := b.Reconfigure(); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}

	newPath := filepath.Join(b.mountPoint, "newfile")
	waitForFIFO(t, newPath, 2*time.Second)

	b.cfgMu.Lock()
	delete(b.cfg.Files, "netrc")
	b.cfgMu.Unlock()
	if err := b.Reconfigure(); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(netrcPath); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Error("netrc FIFO still present after removal")
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNestedDirFIFO(t *testing.T) {
	b, _ := testBackend(t)
	b.cfg.Files = map[string]*config.FileConfig{
		"docker/config.json": b.cfg.Files["docker/config.json"],
	}

	ctx := t.Context()

	if err := os.MkdirAll(b.mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	b.reconcileFIFOs(ctx)

	fifoPath := filepath.Join(b.mountPoint, "docker", "config.json")
	var st unix.Stat_t
	if err := unix.Stat(fifoPath, &st); err != nil {
		t.Fatalf("nested FIFO not found: %v", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFIFO {
		t.Errorf("not a FIFO (mode 0%o)", st.Mode)
	}
}

func TestReconfigureNoPanic(t *testing.T) {
	b, _ := testBackend(t)
	if err := b.Reconfigure(); err != nil {
		t.Errorf("Reconfigure: %v", err)
	}
	if err := b.Reconfigure(); err != nil {
		t.Errorf("second Reconfigure: %v", err)
	}
}

func waitForFIFO(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Lstat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for FIFO %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
