// Package mount defines the Backend interface for presenting secret files.
package mount

import (
	"context"
	"fmt"

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
	Name() string
}

// NewBackend creates a mount backend based on the config.
func NewBackend(cfg *config.Config, r *resolver.SecretResolver, ctxMgr *slinkycontext.Manager) (Backend, error) {
	switch cfg.Settings.Mount.Backend {
	case config.BackendFUSE:
		return fuse.New(cfg, r, ctxMgr), nil
	case config.BackendTmpfs:
		return tmpfs.New(cfg, r, ctxMgr), nil
	case config.BackendFIFO:
		return fifo.New(cfg, r, ctxMgr), nil
	default:
		return nil, fmt.Errorf("unsupported mount backend: %q", cfg.Settings.Mount.Backend)
	}
}
