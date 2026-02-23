// Package mount defines the Backend interface for presenting secret files.
package mount

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/mount/fifo"
	"github.com/kclejeune/slinky/internal/mount/fuse"
	"github.com/kclejeune/slinky/internal/mount/tmpfs"
	"github.com/kclejeune/slinky/internal/resolver"
)

type Backend interface {
	Mount(ctx context.Context) error
	Unmount() error
	Reconfigure() error
	UpdateConfig(cfg *config.Config)
	Name() string
}

// NewBackend creates a mount backend based on the config.
// When the backend is "auto", it tries FUSE first, then tmpfs, then falls back to FIFO.
func NewBackend(cfg *config.Config, r *resolver.SecretResolver, ctxMgr *slinkycontext.Manager) (Backend, error) {
	backend := cfg.Settings.Mount.Backend
	if backend == config.BackendAuto {
		backend = resolveAutoBackend()
	}

	switch backend {
	case config.BackendFUSE:
		return fuse.New(cfg, r, ctxMgr), nil
	case config.BackendTmpfs:
		return tmpfs.New(cfg, r, ctxMgr), nil
	case config.BackendFIFO:
		return fifo.New(cfg, r, ctxMgr), nil
	default:
		return nil, fmt.Errorf("unsupported mount backend: %q", backend)
	}
}

// resolveAutoBackend probes for available backends and returns the best one.
// Preference order: FUSE > tmpfs > FIFO.
func resolveAutoBackend() config.BackendType {
	if FUSEAvailable() {
		slog.Info("auto backend: FUSE available, using fuse")
		return config.BackendFUSE
	}
	if TmpfsAvailable() {
		slog.Info("auto backend: tmpfs available, using tmpfs")
		return config.BackendTmpfs
	}
	slog.Warn("auto backend: FUSE and tmpfs not available, falling back to fifo")
	return config.BackendFIFO
}

// FUSEAvailable reports whether the system supports FUSE.
func FUSEAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		// macOS: only macFUSE is supported. FUSE-T (kext-free alternative)
		// uses a stream-based NFS translation protocol that requires cgo
		// support in the go-fuse library, which is not available.
		for _, path := range []string{"/dev/macfuse0", "/dev/osxfuse0",
			"/Library/Filesystems/macfuse.fs", "/Library/Filesystems/osxfuse.fs"} {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
		return false
	case "linux":
		// Linux: check for /dev/fuse device and fusermount binary.
		if _, err := os.Stat("/dev/fuse"); err != nil {
			return false
		}
		if _, err := exec.LookPath("fusermount3"); err == nil {
			return true
		}
		if _, err := exec.LookPath("fusermount"); err == nil {
			return true
		}
		return false
	default:
		return false
	}
}

// TmpfsAvailable reports whether the system can create RAM-backed mounts.
func TmpfsAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		// macOS: needs hdiutil and diskutil (present on all standard installs).
		if _, err := exec.LookPath("hdiutil"); err != nil {
			return false
		}
		_, err := exec.LookPath("diskutil")
		return err == nil
	case "linux":
		// Linux: tmpfs mount requires root or CAP_SYS_ADMIN.
		return os.Geteuid() == 0
	default:
		return false
	}
}
