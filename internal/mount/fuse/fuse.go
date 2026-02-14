// Package fuse implements the FUSE mount backend. Files exist only as
// in-memory responses to read() syscalls.
package fuse

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/resolver"
)

var (
	currentUID = uint32(os.Getuid())
	currentGID = uint32(os.Getgid())
)

type Backend struct {
	mountPoint string
	cfg        *config.Config
	resolver   *resolver.SecretResolver
	server     *fuse.Server
}

func New(cfg *config.Config, r *resolver.SecretResolver) *Backend {
	return &Backend{
		mountPoint: cfg.Settings.Mount.MountPoint,
		cfg:        cfg,
		resolver:   r,
	}
}

func (b *Backend) Mount(ctx context.Context) error {
	root := &fuseRoot{
		cfg:      b.cfg,
		resolver: b.resolver,
	}

	opts := &gofuse.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:  false,
			FsName:      "slinky",
			Name:        "slinky",
			DirectMount: true,
		},
	}

	server, err := gofuse.Mount(b.mountPoint, root, opts)
	if err != nil {
		return fmt.Errorf("mounting FUSE at %q: %w", b.mountPoint, err)
	}
	b.server = server

	slog.Info("FUSE mounted", "path", b.mountPoint)

	go func() {
		<-ctx.Done()
		slog.Info("context cancelled, unmounting FUSE")
		if err := b.Unmount(); err != nil {
			slog.Error("unmount error", "error", err)
		}
	}()

	server.Wait()
	return nil
}

func (b *Backend) Unmount() error {
	if b.server != nil {
		return b.server.Unmount()
	}
	return nil
}

func (b *Backend) Name() string {
	return string(config.BackendFUSE)
}

type fuseRoot struct {
	gofuse.Inode
	cfg      *config.Config
	resolver *resolver.SecretResolver
}

var _ gofuse.InodeEmbedder = (*fuseRoot)(nil)

func (r *fuseRoot) OnAdd(ctx context.Context) {
	for name, fc := range r.cfg.Files {
		parts := strings.Split(name, "/")
		parent := &r.Inode

		for _, dir := range parts[:len(parts)-1] {
			child := parent.GetChild(dir)
			if child == nil {
				child = parent.NewPersistentInode(ctx,
					&fuseDir{},
					gofuse.StableAttr{Mode: syscall.S_IFDIR},
				)
				parent.AddChild(dir, child, false)
			}
			parent = child
		}

		filename := parts[len(parts)-1]
		child := parent.NewPersistentInode(ctx,
			&fuseFile{
				name:     name,
				mode:     fc.Mode,
				resolver: r.resolver,
			},
			gofuse.StableAttr{Mode: syscall.S_IFREG},
		)
		parent.AddChild(filename, child, false)
	}
}

type fuseDir struct {
	gofuse.Inode
}

var _ gofuse.NodeGetattrer = (*fuseDir)(nil)

func (d *fuseDir) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o755 | syscall.S_IFDIR
	out.Uid = currentUID
	out.Gid = currentGID
	return 0
}

type fuseFile struct {
	gofuse.Inode
	name     string
	mode     uint32
	resolver *resolver.SecretResolver
}

var _ gofuse.NodeOpener = (*fuseFile)(nil)
var _ gofuse.NodeGetattrer = (*fuseFile)(nil)

func (f *fuseFile) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = f.mode
	out.Uid = currentUID
	out.Gid = currentGID
	// Size 0 because we use DIRECT_IO.
	return 0
}

func (f *fuseFile) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	content, err := f.resolver.Resolve(f.name)
	if err != nil {
		slog.Error("resolve failed", "file", f.name, "error", err)
		return nil, 0, syscall.EIO
	}

	return &secretHandle{content: content}, fuse.FOPEN_DIRECT_IO, 0
}

type secretHandle struct {
	content []byte
}

var _ gofuse.FileReader = (*secretHandle)(nil)
var _ gofuse.FileReleaser = (*secretHandle)(nil)

func (h *secretHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	end := min(int(off)+len(dest), len(h.content))
	if int(off) >= len(h.content) {
		return fuse.ReadResultData(nil), 0
	}
	return fuse.ReadResultData(h.content[off:end]), 0
}

func (h *secretHandle) Release(ctx context.Context) syscall.Errno {
	for i := range h.content {
		h.content[i] = 0
	}
	h.content = nil
	return 0
}
