// Package fifo implements a named-pipe mount backend.
//
// Each configured secret gets a FIFO at the mount point. When a consumer
// opens the pipe for reading, the backend resolves the secret and streams
// it through the kernel pipe buffer. Plaintext is zeroed immediately after
// the write completes.
//
// No special privileges are required — only a writable directory and mkfifo.
package fifo

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

	"golang.org/x/sys/unix"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/resolver"
)

// Backend implements mount.Backend using named pipes (FIFOs).
type Backend struct {
	mountPoint string
	cfg        *config.Config
	resolver   *resolver.SecretResolver
	ctxMgr     *slinkycontext.Manager

	cfgMu sync.RWMutex // guards cfg.Files when ctxMgr is nil

	mu      sync.Mutex
	fifos   map[string]string             // file name → absolute FIFO path
	cancels map[string]context.CancelFunc // per-FIFO serve goroutine cancel

	reconfigCh chan struct{}
}

// New creates a new FIFO backend. ctxMgr may be nil.
func New(cfg *config.Config, r *resolver.SecretResolver, ctxMgr *slinkycontext.Manager) *Backend {
	return &Backend{
		mountPoint: cfg.Settings.Mount.MountPoint,
		cfg:        cfg,
		resolver:   r,
		ctxMgr:     ctxMgr,
		fifos:      make(map[string]string),
		cancels:    make(map[string]context.CancelFunc),
		reconfigCh: make(chan struct{}, 1),
	}
}

// Mount creates the mount directory, creates FIFOs for all effective files,
// and blocks until ctx is cancelled.
func (b *Backend) Mount(ctx context.Context) error {
	if err := os.MkdirAll(b.mountPoint, 0o700); err != nil {
		return fmt.Errorf("fifo: creating mount point %q: %w", b.mountPoint, err)
	}

	slog.Info("fifo backend mounted", "path", b.mountPoint)

	b.reconcileFIFOs(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("fifo: context cancelled, cleaning up")
			b.teardown()
			return nil
		case <-b.reconfigCh:
			slog.Info("fifo: reconfigure triggered")
			b.reconcileFIFOs(ctx)
		}
	}
}

// Unmount tears down all FIFOs.
func (b *Backend) Unmount() error {
	b.teardown()
	return nil
}

// Reconfigure signals the backend that the effective file set has changed.
func (b *Backend) Reconfigure() error {
	select {
	case b.reconfigCh <- struct{}{}:
	default:
	}
	return nil
}

// Name returns the backend identifier.
func (b *Backend) Name() string {
	return "fifo"
}

func (b *Backend) effectiveFiles() map[string]*config.FileConfig {
	if b.ctxMgr != nil {
		eff := b.ctxMgr.Effective()
		files := make(map[string]*config.FileConfig, len(eff))
		for name, ef := range eff {
			files[name] = ef.FileConfig
		}
		return files
	}
	b.cfgMu.RLock()
	snap := make(map[string]*config.FileConfig, len(b.cfg.Files))
	maps.Copy(snap, b.cfg.Files)
	b.cfgMu.RUnlock()
	return snap
}

// reconcileFIFOs diffs the effective file set against running FIFOs:
// removes stale FIFOs and creates new ones.
func (b *Backend) reconcileFIFOs(ctx context.Context) {
	files := b.effectiveFiles()

	b.mu.Lock()
	defer b.mu.Unlock()

	for name, path := range b.fifos {
		if _, ok := files[name]; !ok {
			if cancel, exists := b.cancels[name]; exists {
				cancel()
				delete(b.cancels, name)
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Error("fifo: remove failed", "name", name, "path", path, "error", err)
			} else {
				slog.Debug("fifo: removed", "name", name)
			}
			delete(b.fifos, name)
		}
	}

	b.cleanEmptyDirs()

	for name, fc := range files {
		if _, exists := b.fifos[name]; exists {
			continue
		}

		fifoPath := filepath.Join(b.mountPoint, name)

		if dir := filepath.Dir(fifoPath); dir != b.mountPoint {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				slog.Error("fifo: mkdir failed", "dir", dir, "error", err)
				continue
			}
		}

		mode := fc.Mode
		if mode == 0 {
			mode = 0o600
		}
		if err := unix.Mkfifo(fifoPath, mode); err != nil && !errors.Is(err, fs.ErrExist) {
			slog.Error("fifo: mkfifo failed", "name", name, "path", fifoPath, "error", err)
			continue
		}

		childCtx, cancel := context.WithCancel(ctx)
		b.cancels[name] = cancel
		b.fifos[name] = fifoPath

		go b.serveLoop(childCtx, name, fifoPath)
		slog.Debug("fifo: created", "name", name, "path", fifoPath)
	}
}

// serveLoop polls for readers on the FIFO and writes resolved secret content.
// Uses O_NONBLOCK to avoid blocking on the open call; ENXIO indicates no
// reader is present yet.
func (b *Backend) serveLoop(ctx context.Context, name, fifoPath string) {
	for {
		if ctx.Err() != nil {
			return
		}

		f, err := os.OpenFile(fifoPath, os.O_WRONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			if isENXIO(err) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			if errors.Is(err, fs.ErrNotExist) {
				return
			}
			slog.Error("fifo: open error", "name", name, "error", err)
			return
		}

		content, resolveErr := b.resolver.Resolve(name)
		if resolveErr != nil {
			slog.Error("fifo: resolve error", "name", name, "error", resolveErr)
			f.Close()
			continue
		}

		_, writeErr := f.Write(content)

		clear(content)
		f.Close()

		if writeErr != nil {
			slog.Warn("fifo: write error", "name", name, "error", writeErr)
		}
	}
}

func (b *Backend) teardown() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for name, cancel := range b.cancels {
		cancel()
		slog.Debug("fifo: cancelled serve goroutine", "name", name)
	}
	b.cancels = make(map[string]context.CancelFunc)

	for name, path := range b.fifos {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Error("fifo: remove on teardown failed", "name", name, "path", path, "error", err)
		}
		delete(b.fifos, name)
	}

	b.cleanEmptyDirs()

	if err := os.Remove(b.mountPoint); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Debug("fifo: mount point not removed", "error", err)
	}
}

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
		os.Remove(dir)
	}
}

func isENXIO(err error) bool {
	return errors.Is(err, unix.ENXIO)
}
