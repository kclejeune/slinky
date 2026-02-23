package symlink

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func defaultSymlinkCfg() config.SymlinkConfig {
	return config.SymlinkConfig{Conflict: config.ConflictError, BackupExtension: ".bkp"}
}

func defaultConfig(mountPoint string, files map[string]*config.FileConfig) *config.Config {
	return &config.Config{
		Settings: config.Settings{
			Mount:   config.MountConfig{MountPoint: mountPoint},
			Symlink: defaultSymlinkCfg(),
		},
		Files: files,
	}
}

func TestSetupAndCleanup(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}

	mountedFile := filepath.Join(mountPoint, "netrc")
	if err := os.WriteFile(mountedFile, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, ".netrc")

	cfg := defaultConfig(mountPoint, map[string]*config.FileConfig{
		"netrc": {
			Symlink: linkPath,
		},
	})

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink() error: %v", err)
	}
	expectedTarget := filepath.Join(mountPoint, "netrc")
	if target != expectedTarget {
		t.Errorf("symlink target = %q, want %q", target, expectedTarget)
	}

	mgr.Cleanup()

	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Error("symlink should be removed after Cleanup")
	}
}

func TestSetupCreatesParentDirs(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "config.json"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "deep", "nested", "config.json")

	cfg := defaultConfig(mountPoint, map[string]*config.FileConfig{
		"config.json": {
			Symlink: linkPath,
		},
	})

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	if _, err := os.Lstat(linkPath); err != nil {
		t.Errorf("symlink should exist at %q", linkPath)
	}
}

func TestSetupExistingFileErrorMode(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "netrc"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, ".netrc")
	if err := os.WriteFile(linkPath, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Symlink: config.SymlinkConfig{Conflict: config.ConflictError, BackupExtension: ".bkp"},
		},
		Files: map[string]*config.FileConfig{
			"netrc": {
				Symlink: linkPath,
			},
		},
	}

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err == nil {
		t.Fatal("expected error when existing file conflicts in error mode")
	}

	data, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("original file should still exist: %v", err)
	}
	if string(data) != "old content" {
		t.Errorf("original content = %q, want %q", data, "old content")
	}
}

func TestSetupExistingFileBackupMode(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "netrc"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, ".netrc")
	if err := os.WriteFile(linkPath, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Symlink: config.SymlinkConfig{Conflict: config.ConflictBackup, BackupExtension: ".bkp"},
		},
		Files: map[string]*config.FileConfig{
			"netrc": {
				Symlink: linkPath,
			},
		},
	}

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}

	backupPath := linkPath + ".bkp"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("backup file should exist at %q: %v", backupPath, err)
	}
	if string(data) != "old content" {
		t.Errorf("backup content = %q, want %q", data, "old content")
	}
}

func TestCleanupRestoresBackup(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "netrc"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, ".netrc")
	if err := os.WriteFile(linkPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Settings: config.Settings{
			Symlink: config.SymlinkConfig{Conflict: config.ConflictBackup, BackupExtension: ".bkp"},
		},
		Files: map[string]*config.FileConfig{
			"netrc": {
				Symlink: linkPath,
			},
		},
	}

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	mgr.Cleanup()

	// With the new design, Cleanup removes managed symlinks but does not
	// auto-restore backups. The symlink at linkPath should be gone.
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Error("symlink should be removed after Cleanup")
	}

	// The backup file should still exist.
	data, err := os.ReadFile(linkPath + ".bkp")
	if err != nil {
		t.Fatalf("backup file should still exist: %v", err)
	}
	if string(data) != "original" {
		t.Errorf("backup content = %q, want %q", data, "original")
	}
}

func TestSetupExistingSymlinkAlwaysReplaced(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "netrc"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, ".netrc")
	// Create an existing managed symlink pointing into the mount point
	// (simulating a previous daemon run that left a symlink behind).
	if err := os.Symlink(filepath.Join(mountPoint, "old-file"), linkPath); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig(mountPoint, map[string]*config.FileConfig{
		"netrc": {
			Symlink: linkPath,
		},
	})

	// Managed symlinks (pointing into mount) are always safe to replace.
	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink() error: %v", err)
	}
	expectedTarget := filepath.Join(mountPoint, "netrc")
	if target != expectedTarget {
		t.Errorf("symlink target = %q, want %q", target, expectedTarget)
	}
}

func TestSetupRefusesDirTarget(t *testing.T) {
	tmpDir := t.TempDir()

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, "netrc"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "adir")
	if err := os.MkdirAll(linkPath, 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig(mountPoint, map[string]*config.FileConfig{
		"netrc": {
			Symlink: linkPath,
		},
	})

	mgr := NewManager()
	if err := mgr.Setup(cfg, mountPoint); err == nil {
		t.Error("expected error when symlink target is a directory")
	}
}

func TestNoSymlinkConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := defaultConfig(tmpDir, map[string]*config.FileConfig{
		"netrc": {},
	})

	mgr := NewManager()
	if err := mgr.Setup(cfg, tmpDir); err != nil {
		t.Fatalf("Setup() should succeed with no symlink: %v", err)
	}
}
