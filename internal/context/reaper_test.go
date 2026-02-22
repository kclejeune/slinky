package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/config"
)

func setupReaperTest(t *testing.T) (*Manager, string) {
	t.Helper()
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	return mgr, projDir
}

func TestReaperSweepDeadPID(t *testing.T) {
	mgr, projDir := setupReaperTest(t)

	if _, err := mgr.Activate(projDir, nil, 99999); err != nil {
		t.Fatal(err)
	}

	reaper := &Reaper{
		mgr:     mgr,
		isAlive: func(int) bool { return false },
	}

	reaper.sweep()

	acts := mgr.Activations()
	if len(acts) != 0 {
		t.Errorf("expected 0 activations after sweeping dead PID, got %d", len(acts))
	}

	if len(mgr.TrackedPIDs()) != 0 {
		t.Error("expected no tracked PIDs after sweep")
	}
}

func TestReaperSweepAlivePID(t *testing.T) {
	mgr, projDir := setupReaperTest(t)

	if _, err := mgr.Activate(projDir, nil, 99999); err != nil {
		t.Fatal(err)
	}

	reaper := &Reaper{
		mgr:     mgr,
		isAlive: func(int) bool { return true },
	}

	reaper.sweep()

	acts := mgr.Activations()
	if len(acts) != 1 {
		t.Errorf("expected 1 activation (alive PID), got %d", len(acts))
	}

	pids := mgr.TrackedPIDs()
	if len(pids) != 1 || pids[0] != 99999 {
		t.Errorf("TrackedPIDs() = %v, want [99999]", pids)
	}
}

func TestReaperTriggersOnChange(t *testing.T) {
	tmpDir := t.TempDir()
	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	var onChangeCalled bool
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(_ map[string]*EffectiveFile) {
		onChangeCalled = true
	})

	if _, err := mgr.Activate(projDir, nil, 99999); err != nil {
		t.Fatal(err)
	}

	reaper := &Reaper{
		mgr:     mgr,
		isAlive: func(int) bool { return false },
	}

	reaper.sweep()

	if !onChangeCalled {
		t.Error("expected onChange to be called when reaper removes dead session")
	}

	acts := mgr.Activations()
	if len(acts) != 0 {
		t.Errorf("expected 0 activations after sweep, got %d", len(acts))
	}
}

func TestReaperContextCancellation(t *testing.T) {
	mgr, _ := setupReaperTest(t)

	reaper := &Reaper{
		mgr:      mgr,
		interval: 10 * time.Millisecond,
		isAlive:  func(int) bool { return true },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reaper.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("reaper did not exit after context cancellation")
	}
}
