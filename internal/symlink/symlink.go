// Package symlink manages the creation and cleanup of symlinks from
// conventional paths (e.g., ~/.netrc) to the mounted secret files.
package symlink

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kclejeune/slinky/internal/config"
)

type Manager struct {
	mu      sync.Mutex
	managed map[string]string // file name → link path
}

func NewManager() *Manager {
	return &Manager{
		managed: make(map[string]string),
	}
}

// Setup creates symlinks for all configured files that have a symlink path.
func (m *Manager) Setup(cfg *config.Config, mountPoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, fc := range cfg.Files {
		if fc.Symlink == "" {
			continue
		}

		target := filepath.Join(mountPoint, name)
		link := config.ExpandPath(fc.Symlink)

		if err := m.createSymlink(target, link, mountPoint, string(cfg.Settings.Symlink.Conflict), cfg.Settings.Symlink.BackupExtension); err != nil {
			return fmt.Errorf("creating symlink for %q: %w", name, err)
		}

		m.managed[name] = link
		slog.Info("symlink created", "link", link, "target", target)
	}
	return nil
}

// ReconcileWithConfig diffs managed symlinks against a new set of files.
func (m *Manager) ReconcileWithConfig(newFiles map[string]*config.FileConfig, mountPoint, conflict, backupExt string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, link := range m.managed {
		fc, ok := newFiles[name]
		if !ok || fc.Symlink == "" || config.ExpandPath(fc.Symlink) != link {
			if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Error("failed to remove stale symlink", "link", link, "error", err)
			} else {
				slog.Info("stale symlink removed", "link", link, "file", name)
			}
			delete(m.managed, name)
		}
	}

	for name, fc := range newFiles {
		if fc.Symlink == "" {
			continue
		}

		link := config.ExpandPath(fc.Symlink)
		if existingLink, ok := m.managed[name]; ok && existingLink == link {
			continue // already managed, same link path
		}

		target := filepath.Join(mountPoint, name)
		if err := m.createSymlink(target, link, mountPoint, conflict, backupExt); err != nil {
			return fmt.Errorf("creating symlink for %q: %w", name, err)
		}

		m.managed[name] = link
		slog.Info("symlink created", "link", link, "target", target)
	}

	return nil
}

// Cleanup removes all managed symlinks.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, link := range m.managed {
		if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Error("failed to remove symlink", "link", link, "error", err)
		} else {
			slog.Info("symlink removed", "link", link, "file", name)
		}
	}
	m.managed = make(map[string]string)
}

func (m *Manager) createSymlink(target, link, mountPoint, conflict, backupExt string) error {
	dir := filepath.Dir(link)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating parent directory %q: %w", dir, err)
	}

	info, err := os.Lstat(link)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("symlink target %q is a directory, refusing to replace", link)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Existing symlink: check if it points into our mount.
			dest, readErr := os.Readlink(link)
			if readErr == nil && isUnderMount(dest, mountPoint) {
				// Our managed symlink — always safe to replace.
				if err := os.Remove(link); err != nil {
					return fmt.Errorf("removing managed symlink %q: %w", link, err)
				}
			} else {
				// Foreign symlink: respect conflict mode.
				if err := handleConflict(link, conflict, backupExt); err != nil {
					return err
				}
			}
		} else {
			// Regular file: respect conflict mode.
			if err := handleConflict(link, conflict, backupExt); err != nil {
				return err
			}
		}
	}

	return os.Symlink(target, link)
}

// handleConflict handles an existing non-managed file at the symlink path
// according to the configured conflict mode.
func handleConflict(link, conflict, backupExt string) error {
	switch conflict {
	case "backup":
		backupPath := link + backupExt
		if err := os.Rename(link, backupPath); err != nil {
			return fmt.Errorf("backing up %q to %q: %w", link, backupPath, err)
		}
		slog.Info("backed up existing file", "original", link, "backup", backupPath)
		return nil
	default: // "error"
		return fmt.Errorf(
			"file conflict at %q: existing file is not managed by slinky; "+
				"back it up or remove it manually, or set conflict = \"backup\"",
			link,
		)
	}
}

// isUnderMount checks if path is under the given mount point directory.
func isUnderMount(path, mountPoint string) bool {
	path = filepath.Clean(path)
	mountPoint = filepath.Clean(mountPoint)
	return strings.HasPrefix(path, mountPoint+string(filepath.Separator)) || path == mountPoint
}
