package mount

import (
	"context"
	"fmt"

	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/mount/fuse"
	"github.com/kclejeune/slinky/internal/mount/tmpfs"
	"github.com/kclejeune/slinky/internal/resolver"
)

type Backend interface {
	// Mount blocks until ctx is cancelled or an error occurs.
	Mount(ctx context.Context) error
	Unmount() error
	Name() string
}

func NewBackend(cfg *config.Config, r *resolver.SecretResolver) (Backend, error) {
	switch cfg.Settings.Mount.Backend {
	case config.BackendFUSE:
		return fuse.New(cfg, r), nil
	case config.BackendTmpfs:
		return tmpfs.New(cfg, r), nil
	default:
		return nil, fmt.Errorf("unsupported mount backend: %q", cfg.Settings.Mount.Backend)
	}
}
