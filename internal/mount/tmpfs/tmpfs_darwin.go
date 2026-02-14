//go:build darwin

package tmpfs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type darwinMounter struct {
	path      string
	device    string // whole-disk device, e.g. "/dev/disk4"
	partition string // partition device, e.g. "/dev/disk4s1"
}

func newPlatformMounter(path string) platformMounter {
	return &darwinMounter{path: path}
}

func (m *darwinMounter) Mount() error {
	if err := os.MkdirAll(m.path, 0o700); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	// 8192 sectors * 512 bytes = 4MB RAM disk.
	// hdiutil output has trailing whitespace; extract first field.
	out, err := exec.Command("hdiutil", "attach", "-nomount", "ram://8192").Output()
	if err != nil {
		return fmt.Errorf("hdiutil attach: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return fmt.Errorf("hdiutil attach: unexpected empty output")
	}
	m.device = fields[0]
	m.partition = m.device + "s1"

	// HFS+ instead of APFS: APFS requires ~16MB minimum, wasteful for small files.
	if err := exec.Command("diskutil", "eraseDisk", "HFS+", "Slinky", m.device).Run(); err != nil {
		_ = exec.Command("hdiutil", "detach", m.device).Run()
		return fmt.Errorf("diskutil eraseDisk: %w", err)
	}

	// diskutil auto-mounts at /Volumes/Slinky; unmount to remount at our path.
	_ = exec.Command("diskutil", "unmount", "/Volumes/Slinky").Run()

	if err := exec.Command("diskutil", "mount", "-mountPoint", m.path, m.partition).Run(); err != nil {
		_ = exec.Command("hdiutil", "detach", m.device).Run()
		return fmt.Errorf("diskutil mount at %q: %w", m.path, err)
	}

	_ = exec.Command("chflags", "hidden", m.path).Run()

	return nil
}

func (m *darwinMounter) Unmount() error {
	if m.device == "" {
		return nil
	}

	if err := exec.Command("hdiutil", "detach", m.device, "-force").Run(); err != nil {
		return fmt.Errorf("hdiutil detach %q: %w", m.device, err)
	}

	_ = os.Remove(m.path)
	return nil
}
