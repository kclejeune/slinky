package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
)

func TestServerClientActivate(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Name: "netrc", Render: "native", Template: "tpl"},
		},
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()

	client := NewClient(socketPath)
	resp, err := client.Activate(tmpDir, map[string]string{"TOKEN": "abc"}, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}
	if !resp.OK {
		t.Errorf("Activate() OK = false, error: %s", resp.Error)
	}
	if len(resp.Files) != 1 {
		t.Errorf("Activate() returned %d files, want 1", len(resp.Files))
	}

	cancel()
}

func TestServerClientStatus(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Name: "netrc", Render: "native", Template: "tpl"},
			"npmrc": {Name: "npmrc", Render: "native", Template: "tpl2"},
		},
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = server.Serve(ctx) }()

	client := NewClient(socketPath)

	_, _ = client.Activate("/some/dir", nil, 0)

	resp, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if !resp.Running {
		t.Error("Status() Running = false")
	}
	if len(resp.ActiveDirs) != 1 || resp.ActiveDirs[0] != "/some/dir" {
		t.Errorf("Status() ActiveDirs = %v, want [/some/dir]", resp.ActiveDirs)
	}
	if len(resp.Files) != 2 {
		t.Errorf("Status() returned %d files, want 2", len(resp.Files))
	}

	cancel()
}

func TestServerMultiActivateStatus(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	go func() { _ = server.Serve(ctx) }()

	client := NewClient(socketPath)
	_, _ = client.Activate(projA, nil, 0)
	_, _ = client.Activate(projB, nil, 0)

	resp, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}

	slices.Sort(resp.ActiveDirs)
	if len(resp.ActiveDirs) != 2 {
		t.Errorf("expected 2 active dirs, got %d: %v", len(resp.ActiveDirs), resp.ActiveDirs)
	}

	if len(resp.Files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(resp.Files), resp.Files)
	}

	if len(resp.Layers) != 2 {
		t.Errorf("expected 2 layer entries, got %d", len(resp.Layers))
	}

	cancel()
}

func TestServerDeactivate(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	go func() { _ = server.Serve(ctx) }()

	client := NewClient(socketPath)
	_, _ = client.Activate(projA, nil, 0)
	_, _ = client.Activate(projB, nil, 0)

	deactResp, err := client.Deactivate(projA, 0)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}
	if !deactResp.OK {
		t.Errorf("Deactivate() OK = false, error: %s", deactResp.Error)
	}

	resp, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(resp.ActiveDirs) != 1 || resp.ActiveDirs[0] != projB {
		t.Errorf("expected ActiveDirs = [%s], got %v", projB, resp.ActiveDirs)
	}
	if len(resp.Files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(resp.Files), resp.Files)
	}

	cancel()
}

func TestServerActivateConflictError(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	go func() { _ = server.Serve(ctx) }()

	client := NewClient(socketPath)

	resp, err := client.Activate(projA, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projA) error: %v", err)
	}
	if !resp.OK {
		t.Fatalf("Activate(projA) OK = false: %s", resp.Error)
	}

	resp, err = client.Activate(projB, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projB) transport error: %v", err)
	}
	if resp.OK {
		t.Error("expected Activate(projB) OK = false due to conflict")
	}
	if !strings.Contains(resp.Error, "conflict") {
		t.Errorf("expected conflict in error, got: %s", resp.Error)
	}

	cancel()
}

func TestDefaultSocketPath(t *testing.T) {
	path := DefaultSocketPath()
	if path == "" {
		t.Error("DefaultSocketPath() returned empty string")
	}
}

func TestClientNoServer(t *testing.T) {
	client := NewClient("/nonexistent/socket")
	_, err := client.Activate("/tmp", nil, 0)
	if err == nil {
		t.Error("expected error when no server is running")
	}

	_, err = client.Status()
	if err == nil {
		t.Error("expected error when no server is running")
	}

	_, err = client.Deactivate("/tmp", 0)
	if err == nil {
		t.Error("expected error when no server is running")
	}
}

func TestServerSessionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ctl")

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tpl, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, slinkycontext.DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxMgr := slinkycontext.NewManager(globalCfg, slinkycontext.DefaultProjectConfigNames, nil)
	server := NewServer(socketPath, ctxMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	go func() { _ = server.Serve(ctx) }()

	client := NewClient(socketPath)

	_, _ = client.Activate(projDir, nil, 1001)
	_, _ = client.Activate(projDir, nil, 1002)

	resp, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if pids, ok := resp.Sessions[projDir]; !ok || len(pids) != 2 {
		t.Errorf("Sessions[%s] = %v, want 2 PIDs", projDir, pids)
	}

	_, _ = client.Deactivate(projDir, 1001)

	resp, err = client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(resp.ActiveDirs) != 1 {
		t.Errorf("expected 1 active dir, got %d", len(resp.ActiveDirs))
	}
	if pids := resp.Sessions[projDir]; len(pids) != 1 || pids[0] != 1002 {
		t.Errorf("Sessions = %v, want [1002]", pids)
	}

	_, _ = client.Deactivate(projDir, 1002)

	resp, err = client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(resp.ActiveDirs) != 0 {
		t.Errorf("expected 0 active dirs, got %d: %v", len(resp.ActiveDirs), resp.ActiveDirs)
	}

	cancel()
}
