// Package tmpfs implements a RAM-backed mount backend.
//
// On Linux this uses a real tmpfs mount (requires mount privileges).
// On macOS it uses a RAM disk via hdiutil + diskutil.
// On unmount, all files are zero-overwritten before deletion.
package tmpfs

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/resolver"
)

type Backend struct {
	mountPoint string
	cfg        *config.Config
	resolver   *resolver.SecretResolver
	mounter    platformMounter

	mu       sync.Mutex
	rendered map[string]string // file name -> absolute path
}

func New(cfg *config.Config, r *resolver.SecretResolver) *Backend {
	return &Backend{
		mountPoint: cfg.Settings.Mount.MountPoint,
		cfg:        cfg,
		resolver:   r,
		mounter:    newPlatformMounter(cfg.Settings.Mount.MountPoint),
		rendered:   make(map[string]string),
	}
}

func (b *Backend) Mount(ctx context.Context) error {
	if err := b.mounter.Mount(); err != nil {
		return fmt.Errorf("mounting tmpfs at %q: %w", b.mountPoint, err)
	}

	slog.Info("tmpfs mounted", "path", b.mountPoint)

	if err := b.renderAll(); err != nil {
		_ = b.mounter.Unmount()
		return fmt.Errorf("initial render: %w", err)
	}

	refreshInterval := max(b.minTTL()/2, 1*time.Second)

	slog.Info("starting refresh loop", "interval", refreshInterval)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("context cancelled, cleaning up tmpfs")
			b.scrubAll()
			if err := b.mounter.Unmount(); err != nil {
				slog.Error("unmount error", "error", err)
			}
			return nil
		case <-ticker.C:
			if err := b.renderAll(); err != nil {
				slog.Error("refresh render failed", "error", err)
			}
		}
	}
}

func (b *Backend) Unmount() error {
	b.scrubAll()
	return b.mounter.Unmount()
}

func (b *Backend) Name() string {
	return string(config.BackendTmpfs)
}

func (b *Backend) renderAll() error {
	for name := range b.cfg.Files {
		if err := b.renderFile(name); err != nil {
			return fmt.Errorf("rendering %q: %w", name, err)
		}
	}
	return nil
}

func (b *Backend) renderFile(name string) error {
	content, err := b.resolver.Resolve(name)
	if err != nil {
		return err
	}

	fc := b.cfg.Files[name]
	destPath := filepath.Join(b.mountPoint, name)

	if err := b.atomicWrite(destPath, content, os.FileMode(fc.Mode)); err != nil {
		return fmt.Errorf("writing %q: %w", destPath, err)
	}

	b.mu.Lock()
	b.rendered[name] = destPath
	b.mu.Unlock()

	slog.Debug("rendered file", "name", name, "path", destPath)
	return nil
}

func (b *Backend) atomicWrite(dest string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".slinky-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("setting mode on temp file: %w", err)
	}

	if _, err := tmp.Write(content); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	success = true
	return nil
}

func (b *Backend) scrubAll() {
	b.mu.Lock()
	rendered := maps.Clone(b.rendered)
	b.mu.Unlock()

	for name, path := range rendered {
		if err := scrubFile(path); err != nil {
			slog.Error("scrub failed", "file", name, "path", path, "error", err)
		} else {
			slog.Debug("scrubbed file", "file", name, "path", path)
		}
	}

	b.cleanEmptyDirs()
}

func scrubFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("opening for scrub: %w", err)
	}
	zeros := make([]byte, info.Size())
	if _, err := f.Write(zeros); err != nil {
		_ = f.Close()
		return fmt.Errorf("zeroing: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing zeros: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing after scrub: %w", err)
	}

	return os.Remove(path)
}

// cleanEmptyDirs walks bottom-up so nested empty dirs are removed correctly.
func (b *Backend) cleanEmptyDirs() {
	var dirs []string
	_ = filepath.Walk(b.mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == b.mountPoint {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})

	slices.Reverse(dirs)
	for _, dir := range dirs {
		_ = os.Remove(dir) // Only succeeds if empty.
	}
}

func (b *Backend) minTTL() time.Duration {
	shortest := time.Duration(0)
	for _, fc := range b.cfg.Files {
		ttl := fc.FileTTL(b.cfg.Settings.Cache.DefaultTTL)
		if shortest == 0 || ttl < shortest {
			shortest = ttl
		}
	}
	if shortest == 0 {
		shortest = 5 * time.Minute
	}
	return shortest
}

type platformMounter interface {
	Mount() error
	Unmount() error
}

// dirMounter is a non-privileged fallback used for testing.
// Real implementations are in tmpfs_linux.go and tmpfs_darwin.go.
type dirMounter struct {
	path string
}

func (m *dirMounter) Mount() error {
	return os.MkdirAll(m.path, 0o700)
}

func (m *dirMounter) Unmount() error {
	return os.RemoveAll(m.path)
}

func (b *Backend) FileNames() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Collect(maps.Keys(b.rendered))
}
