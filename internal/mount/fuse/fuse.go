// Package fuse implements the FUSE mount backend for slinky.
// Files exist only as in-memory responses to read() syscalls.
// The file tree is dynamic: Lookup/Readdir consult the ContextManager's
// effective file set at each call, so context switches are reflected
// immediately without remounting.
package fuse

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/resolver"
)

// uid and gid of the current process, captured at init time so every
// inode reports the same owner.
var (
	currentUID = uint32(os.Getuid())
	currentGID = uint32(os.Getgid())
)

// Backend implements mount.Backend using FUSE.
type Backend struct {
	mountPoint string
	cfg        *config.Config
	resolver   *resolver.SecretResolver
	ctxMgr     *slinkycontext.Manager
	server     *fuse.Server
}

// New creates a new FUSE backend. ctxMgr may be nil.
func New(cfg *config.Config, r *resolver.SecretResolver, ctxMgr *slinkycontext.Manager) *Backend {
	return &Backend{
		mountPoint: cfg.Settings.Mount.MountPoint,
		cfg:        cfg,
		resolver:   r,
		ctxMgr:     ctxMgr,
	}
}

// Mount mounts the FUSE filesystem and blocks until the context is cancelled.
func (b *Backend) Mount(ctx context.Context) error {
	// Clean up any stale FUSE mount left by a previous daemon instance
	// (e.g. crash, or launchd KeepAlive restarting before unmount completes).
	unmountStale(b.mountPoint)

	root := &fuseRoot{
		cfg:      b.cfg,
		resolver: b.resolver,
		ctxMgr:   b.ctxMgr,
	}

	opts := &gofuse.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:  false,
			FsName:      "slinky",
			Name:        "slinky",
			DirectMount: true,
		},
		// Zero timeouts so the kernel doesn't cache dentries/attrs.
		// This ensures context switches are reflected immediately.
		EntryTimeout: durationPtr(0),
		AttrTimeout:  durationPtr(0),
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

// Unmount unmounts the FUSE filesystem.
func (b *Backend) Unmount() error {
	if b.server != nil {
		return b.server.Unmount()
	}
	return nil
}

// Reconfigure is a no-op for FUSE since Lookup/Readdir are already dynamic.
func (b *Backend) Reconfigure() error {
	return nil
}

// Name returns the backend name.
func (b *Backend) Name() string {
	return "fuse"
}

// effectiveFiles returns the current file set, preferring ContextManager
// if available, falling back to cfg.Files.
func effectiveFiles(cfg *config.Config, ctxMgr *slinkycontext.Manager) map[string]*effectiveEntry {
	result := make(map[string]*effectiveEntry)

	if ctxMgr != nil {
		for name, ef := range ctxMgr.Effective() {
			result[name] = &effectiveEntry{fc: ef.FileConfig}
		}
		return result
	}

	for name, fc := range cfg.Files {
		result[name] = &effectiveEntry{fc: fc}
	}
	return result
}

type effectiveEntry struct {
	fc *config.FileConfig
}

// fuseRoot is the root inode of the FUSE filesystem.
// It uses dynamic Lookup and Readdir instead of static OnAdd.
type fuseRoot struct {
	gofuse.Inode
	cfg      *config.Config
	resolver *resolver.SecretResolver
	ctxMgr   *slinkycontext.Manager
}

var _ gofuse.InodeEmbedder = (*fuseRoot)(nil)
var _ gofuse.NodeLookuper = (*fuseRoot)(nil)
var _ gofuse.NodeReaddirer = (*fuseRoot)(nil)
var _ gofuse.NodeGetattrer = (*fuseRoot)(nil)

func (r *fuseRoot) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o755 | syscall.S_IFDIR
	out.Uid = currentUID
	out.Gid = currentGID
	return 0
}

// Lookup handles path resolution at the root level.
func (r *fuseRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	files := effectiveFiles(r.cfg, r.ctxMgr)

	if entry, ok := files[name]; ok {
		child := r.NewInode(ctx,
			&fuseFile{
				name:     name,
				mode:     entry.fc.Mode,
				resolver: r.resolver,
			},
			gofuse.StableAttr{Mode: syscall.S_IFREG},
		)
		out.Uid = currentUID
		out.Gid = currentGID
		out.Mode = entry.fc.Mode
		return child, 0
	}

	prefix := name + "/"
	for fname := range files {
		if strings.HasPrefix(fname, prefix) {
			child := r.NewInode(ctx,
				&fuseDir{
					prefix:   prefix,
					cfg:      r.cfg,
					ctxMgr:   r.ctxMgr,
					resolver: r.resolver,
				},
				gofuse.StableAttr{Mode: syscall.S_IFDIR},
			)
			out.Uid = currentUID
			out.Gid = currentGID
			out.Mode = 0o755 | syscall.S_IFDIR
			return child, 0
		}
	}

	return nil, syscall.ENOENT
}

// Readdir lists the contents of the root directory.
func (r *fuseRoot) Readdir(ctx context.Context) (gofuse.DirStream, syscall.Errno) {
	files := effectiveFiles(r.cfg, r.ctxMgr)
	seen := make(map[string]bool)
	var entries []fuse.DirEntry

	for name, entry := range files {
		parts := strings.SplitN(name, "/", 2)
		top := parts[0]

		if seen[top] {
			continue
		}
		seen[top] = true

		if len(parts) == 1 {
			entries = append(entries, fuse.DirEntry{
				Name: top,
				Mode: entry.fc.Mode,
			})
		} else {
			entries = append(entries, fuse.DirEntry{
				Name: top,
				Mode: syscall.S_IFDIR | 0o755,
			})
		}
	}

	return gofuse.NewListDirStream(entries), 0
}

// fuseDir represents an intermediate directory in the FUSE filesystem.
// It dynamically resolves its children from the effective file set.
type fuseDir struct {
	gofuse.Inode
	prefix   string // e.g. "docker/" — the path prefix this dir represents
	cfg      *config.Config
	ctxMgr   *slinkycontext.Manager
	resolver *resolver.SecretResolver
}

var _ gofuse.NodeGetattrer = (*fuseDir)(nil)
var _ gofuse.NodeLookuper = (*fuseDir)(nil)
var _ gofuse.NodeReaddirer = (*fuseDir)(nil)

func (d *fuseDir) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o755 | syscall.S_IFDIR
	out.Uid = currentUID
	out.Gid = currentGID
	return 0
}

func (d *fuseDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	files := effectiveFiles(d.cfg, d.ctxMgr)
	fullPath := d.prefix + name

	if entry, ok := files[fullPath]; ok {
		child := d.NewInode(ctx,
			&fuseFile{
				name:     fullPath,
				mode:     entry.fc.Mode,
				resolver: d.resolver,
			},
			gofuse.StableAttr{Mode: syscall.S_IFREG},
		)
		out.Uid = currentUID
		out.Gid = currentGID
		out.Mode = entry.fc.Mode
		return child, 0
	}

	subPrefix := fullPath + "/"
	for fname := range files {
		if strings.HasPrefix(fname, subPrefix) {
			child := d.NewInode(ctx,
				&fuseDir{
					prefix:   subPrefix,
					cfg:      d.cfg,
					ctxMgr:   d.ctxMgr,
					resolver: d.resolver,
				},
				gofuse.StableAttr{Mode: syscall.S_IFDIR},
			)
			out.Uid = currentUID
			out.Gid = currentGID
			out.Mode = 0o755 | syscall.S_IFDIR
			return child, 0
		}
	}

	return nil, syscall.ENOENT
}

func (d *fuseDir) Readdir(ctx context.Context) (gofuse.DirStream, syscall.Errno) {
	files := effectiveFiles(d.cfg, d.ctxMgr)
	seen := make(map[string]bool)
	var entries []fuse.DirEntry

	for name, entry := range files {
		if !strings.HasPrefix(name, d.prefix) {
			continue
		}
		rest := name[len(d.prefix):]
		parts := strings.SplitN(rest, "/", 2)
		top := parts[0]

		if seen[top] {
			continue
		}
		seen[top] = true

		if len(parts) == 1 {
			entries = append(entries, fuse.DirEntry{
				Name: top,
				Mode: entry.fc.Mode,
			})
		} else {
			entries = append(entries, fuse.DirEntry{
				Name: top,
				Mode: syscall.S_IFDIR | 0o755,
			})
		}
	}

	return gofuse.NewListDirStream(entries), 0
}

// fuseFile represents a single secret file in the FUSE filesystem.
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
	// Size 0 since we use DIRECT_IO.
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

// secretHandle holds the resolved content for an open file handle.
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

func durationPtr(d time.Duration) *time.Duration {
	return &d
}

// unmountStale detects and cleans up a stale FUSE mount at the given path.
// A mount is considered stale if stat fails with an error other than ENOENT
// (e.g. "Device not configured" on macOS), or if the path is a live mount
// point (different device ID than its parent) left by a previous daemon.
func unmountStale(mountPoint string) {
	if !isMountedOrStale(mountPoint) {
		return
	}
	slog.Info("cleaning stale FUSE mount", "path", mountPoint)
	if err := exec.Command("umount", mountPoint).Run(); err != nil {
		if runtime.GOOS == "darwin" {
			slog.Warn("umount failed, trying diskutil", "path", mountPoint, "error", err)
			if err := exec.Command("diskutil", "unmount", "force", mountPoint).Run(); err != nil {
				slog.Error("failed to clean stale mount", "path", mountPoint, "error", err)
			}
		} else {
			slog.Error("failed to clean stale mount", "path", mountPoint, "error", err)
		}
	}
}

// isMountedOrStale reports whether the path is either a stale FUSE mount
// (stat fails with something other than ENOENT) or an active mount point
// (device ID differs from parent directory).
func isMountedOrStale(path string) bool {
	var st syscall.Stat_t
	err := syscall.Stat(path, &st)
	if err != nil {
		// ENOENT means the path doesn't exist — not mounted.
		// Any other error (ENXIO, EIO, etc.) indicates a stale mount.
		return err != syscall.ENOENT
	}

	// Stat succeeded — check if it's a mount point by comparing device IDs
	// with the parent directory.
	var parentSt syscall.Stat_t
	if err := syscall.Stat(filepath.Dir(path), &parentSt); err != nil {
		return false
	}
	return st.Dev != parentSt.Dev
}
