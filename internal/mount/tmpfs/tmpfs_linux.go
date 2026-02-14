//go:build linux

package tmpfs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type linuxMounter struct {
	path string
}

func newPlatformMounter(path string) platformMounter {
	return &linuxMounter{path: path}
}

func (m *linuxMounter) Mount() error {
	if err := os.MkdirAll(m.path, 0o700); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	if err := unix.Mount("tmpfs", m.path, "tmpfs", 0, "size=4m,mode=0700"); err != nil {
		return fmt.Errorf("mounting tmpfs: %w", err)
	}

	return nil
}

func (m *linuxMounter) Unmount() error {
	if err := unix.Unmount(m.path, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmounting tmpfs: %w", err)
	}
	return os.Remove(m.path)
}
