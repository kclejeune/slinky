package symlink

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kclejeune/slinky/internal/config"
)

type Manager struct {
	managed   map[string]string
	backups   map[string]string
	conflict  config.ConflictMode
	backupExt string
}

func NewManager(symlinkCfg config.SymlinkConfig) *Manager {
	return &Manager{
		managed:   make(map[string]string),
		backups:   make(map[string]string),
		conflict:  symlinkCfg.Conflict,
		backupExt: symlinkCfg.BackupExtension,
	}
}

func (m *Manager) Setup(cfg *config.Config, mountPoint string) error {
	for name, fc := range cfg.Files {
		if fc.Symlink == "" {
			continue
		}

		target := filepath.Join(mountPoint, name)
		link := config.ExpandPath(fc.Symlink)

		if err := m.createSymlink(target, link); err != nil {
			return fmt.Errorf("creating symlink for %q: %w", name, err)
		}

		m.managed[name] = link
		slog.Info("symlink created", "link", link, "target", target)
	}
	return nil
}

func (m *Manager) Cleanup() {
	for name, link := range m.managed {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			slog.Error("failed to remove symlink", "link", link, "error", err)
		} else {
			slog.Info("symlink removed", "link", link, "file", name)
		}

		if backupPath, ok := m.backups[link]; ok {
			if err := os.Rename(backupPath, link); err != nil {
				slog.Error("failed to restore backup", "backup", backupPath, "link", link, "error", err)
			} else {
				slog.Info("backup restored", "backup", backupPath, "link", link)
			}
			delete(m.backups, link)
		}
	}
	m.managed = make(map[string]string)
}

func (m *Manager) createSymlink(target, link string) error {
	dir := filepath.Dir(link)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating parent directory %q: %w", dir, err)
	}

	info, err := os.Lstat(link)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("symlink target %q is a directory, refusing to replace", link)
		}

		// Existing symlinks are always safe to replace (re-pointing).
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("removing existing symlink %q: %w", link, err)
			}
		} else {
			switch m.conflict {
			case config.ConflictBackup:
				backupPath := link + m.backupExt
				if err := os.Rename(link, backupPath); err != nil {
					return fmt.Errorf("backing up %q to %q: %w", link, backupPath, err)
				}
				m.backups[link] = backupPath
				slog.Info("backed up existing file", "original", link, "backup", backupPath)
			default:
				return fmt.Errorf("existing file at symlink path %q (conflict mode: %s)", link, m.conflict)
			}
		}
	}

	return os.Symlink(target, link)
}
