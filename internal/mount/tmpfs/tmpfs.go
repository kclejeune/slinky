// Package tmpfs implements a RAM-backed mount backend.
//
// On Linux this uses a real tmpfs mount (requires mount privileges).
// On macOS it uses a RAM disk via hdiutil + diskutil.
// On unmount, all files are zero-overwritten before deletion.
package tmpfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/fsutil"
	"github.com/kclejeune/slinky/internal/resolver"
)

type Backend struct {
	mountPoint string
	cfg        *config.Config
	resolver   *resolver.SecretResolver
	ctxMgr     *slinkycontext.Manager
	mounter    platformMounter

	cfgMu sync.RWMutex // guards cfg

	mu       sync.Mutex
	rendered map[string]string // file name -> absolute path of written file

	// reconfigCh receives signals when the context changes.
	reconfigCh chan struct{}
}

// New creates a new tmpfs backend.
func New(cfg *config.Config, r *resolver.SecretResolver, ctxMgr *slinkycontext.Manager) *Backend {
	return &Backend{
		mountPoint: cfg.Settings.Mount.MountPoint,
		cfg:        cfg,
		resolver:   r,
		ctxMgr:     ctxMgr,
		mounter:    newPlatformMounter(cfg.Settings.Mount.MountPoint),
		rendered:   make(map[string]string),
		reconfigCh: make(chan struct{}, 1),
	}
}

// Mount mounts the RAM filesystem, renders all files, and blocks until
// ctx is cancelled.
func (b *Backend) Mount(ctx context.Context) error {
	if err := b.mounter.Mount(); err != nil {
		return fmt.Errorf("mounting tmpfs at %q: %w", b.mountPoint, err)
	}

	slog.Info("tmpfs mounted", "path", b.mountPoint)

	if err := b.renderAll(); err != nil {
		_ = b.mounter.Unmount()
		return fmt.Errorf("initial render: %w", err)
	}

	curInterval := b.refreshInterval()

	slog.Info("starting refresh loop", "interval", curInterval)

	ticker := time.NewTicker(curInterval)
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
		case <-b.reconfigCh:
			slog.Info("reconfigure triggered, re-rendering")
			if err := b.reconcileFiles(); err != nil {
				slog.Error("reconcile render failed", "error", err)
			}
			// Recalculate refresh interval when the file set changes.
			if newInterval := b.refreshInterval(); newInterval != curInterval {
				slog.Info("refresh interval changed", "old", curInterval, "new", newInterval)
				curInterval = newInterval
				ticker.Reset(curInterval)
			}
		}
	}
}

// Unmount scrubs files and unmounts.
func (b *Backend) Unmount() error {
	b.scrubAll()
	return b.mounter.Unmount()
}

// Reconfigure signals that the effective file set has changed.
func (b *Backend) Reconfigure() error {
	select {
	case b.reconfigCh <- struct{}{}:
	default:
		// Already pending, no need to queue another.
	}
	return nil
}

func (b *Backend) UpdateConfig(cfg *config.Config) {
	b.cfgMu.Lock()
	b.cfg = cfg
	b.cfgMu.Unlock()
}

func (b *Backend) Name() string {
	return "tmpfs"
}

func (b *Backend) effectiveFileNames() map[string]*config.FileConfig {
	if b.ctxMgr != nil {
		return b.ctxMgr.EffectiveFileConfigs()
	}
	b.cfgMu.RLock()
	snap := make(map[string]*config.FileConfig, len(b.cfg.Files))
	maps.Copy(snap, b.cfg.Files)
	b.cfgMu.RUnlock()
	return snap
}

// renderAll resolves and writes every effective file.
func (b *Backend) renderAll() error {
	files := b.effectiveFileNames()
	for name, fc := range files {
		if err := b.renderFile(name, fc); err != nil {
			slog.Warn("skipping file render", "file", name, "error", err)
		}
	}
	return nil
}

// reconcileFiles scrubs stale files and renders new/changed ones.
func (b *Backend) reconcileFiles() error {
	files := b.effectiveFileNames()

	// Scrub files that are no longer effective.
	b.mu.Lock()
	toRemove := make(map[string]string)
	for name, path := range b.rendered {
		if _, ok := files[name]; !ok {
			toRemove[name] = path
		}
	}
	for name := range toRemove {
		delete(b.rendered, name)
	}
	b.mu.Unlock()

	var scrubFailed bool
	for name, path := range toRemove {
		if err := scrubFile(path); err != nil {
			slog.Error("scrub failed during reconcile", "file", name, "path", path, "error", err)
			scrubFailed = true
		} else {
			slog.Debug("scrubbed removed file", "file", name)
		}
	}

	fsutil.CleanEmptyDirs(b.mountPoint)

	// Render all current effective files.
	for name, fc := range files {
		if err := b.renderFile(name, fc); err != nil {
			slog.Error("render failed during reconcile", "file", name, "error", err)
		}
	}

	if scrubFailed {
		return fmt.Errorf("one or more files could not be securely scrubbed")
	}
	return nil
}

// renderFile resolves and atomically writes a single file.
func (b *Backend) renderFile(name string, fc *config.FileConfig) error {
	content, err := b.resolver.Resolve(name)
	if err != nil {
		return err
	}
	defer clear(content)

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

// atomicWrite writes content to a temp file and renames it to dest.
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
			tmp.Close()
			os.Remove(tmpPath)
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

// scrubAll overwrites every rendered file with zeros before deleting it,
// preventing secret recovery from RAM-backed storage after unmount.
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

	fsutil.CleanEmptyDirs(b.mountPoint)
}

// scrubFile zeros the file content and removes it.
func scrubFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("opening for scrub: %w", err)
	}
	defer f.Close()

	var buf [32 * 1024]byte // fixed 32KB stack buffer to avoid OOM on large files
	remaining := info.Size()
	for remaining > 0 {
		n := min(int64(len(buf)), remaining)
		if _, err := f.Write(buf[:n]); err != nil {
			return fmt.Errorf("zeroing: %w", err)
		}
		remaining -= n
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing zeros: %w", err)
	}

	return os.Remove(path)
}

// refreshInterval returns half the minimum TTL, clamped to at least 1s.
// Refresh at half the TTL provides a safety margin: secrets are re-rendered
// before consumers see expired data.
func (b *Backend) refreshInterval() time.Duration {
	return max(b.minTTL()/2, 1*time.Second)
}

func (b *Backend) minTTL() time.Duration {
	files := b.effectiveFileNames()

	b.cfgMu.RLock()
	defaultTTL := b.cfg.Settings.Cache.DefaultTTL
	b.cfgMu.RUnlock()

	minVal := time.Duration(0)
	for _, fc := range files {
		ttl := fc.FileTTL(defaultTTL)
		if minVal == 0 || ttl < minVal {
			minVal = ttl
		}
	}
	if minVal <= 0 {
		minVal = 5 * time.Minute
	}
	return minVal
}

// platformMounter abstracts platform-specific mount/unmount.
type platformMounter interface {
	Mount() error
	Unmount() error
}

// dirMounter is a fallback that just creates a directory (used for testing).
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
