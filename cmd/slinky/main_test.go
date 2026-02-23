package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNewServiceConfig(t *testing.T) {
	cfg := newServiceConfig("")

	if cfg.Name != serviceName {
		t.Errorf("Name = %q, want %q", cfg.Name, serviceName)
	}
	if cfg.DisplayName != "slinky" {
		t.Errorf("DisplayName = %q, want %q", cfg.DisplayName, "slinky")
	}
	if len(cfg.Arguments) != 1 || cfg.Arguments[0] != "run" {
		t.Errorf("Arguments = %v, want [run]", cfg.Arguments)
	}
	if v, ok := cfg.Option["UserService"]; !ok || v != true {
		t.Errorf("Option[UserService] = %v, want true", v)
	}
}

func TestNewServiceConfigWithConfigPath(t *testing.T) {
	cfg := newServiceConfig("/etc/slinky/config.toml")

	want := []string{"run", "--config", "/etc/slinky/config.toml"}
	if len(cfg.Arguments) != len(want) {
		t.Fatalf("Arguments length = %d, want %d", len(cfg.Arguments), len(want))
	}
	for i, arg := range cfg.Arguments {
		if arg != want[i] {
			t.Errorf("Arguments[%d] = %q, want %q", i, arg, want[i])
		}
	}
}

func TestReadPID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	dir := filepath.Join(tmpDir, "slinky")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	wantPID := 12345
	if err := os.WriteFile(
		filepath.Join(dir, "pid"),
		[]byte(strconv.Itoa(wantPID)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	got, err := readPID()
	if err != nil {
		t.Fatalf("readPID() error = %v", err)
	}
	if got != wantPID {
		t.Errorf("readPID() = %d, want %d", got, wantPID)
	}
}

func TestReadPIDMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	_, err := readPID()
	if err == nil {
		t.Fatal("readPID() expected error for missing file, got nil")
	}
}

func TestDaemonizeAlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	dir := filepath.Join(tmpDir, "slinky")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write current process PID — signal 0 will succeed.
	myPID := os.Getpid()
	if err := os.WriteFile(
		filepath.Join(dir, "pid"),
		[]byte(strconv.Itoa(myPID)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	err := daemonizeStart("")
	if err == nil {
		t.Fatal("daemonizeStart() expected error for already running, got nil")
	}
	if got := err.Error(); got != "slinky is already running (pid "+strconv.Itoa(myPID)+")" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestStateDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	got := stateDir()
	want := filepath.Join(tmpDir, "slinky")
	if got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
}

func TestLogFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	got := logFilePath()
	want := filepath.Join(tmpDir, "slinky", "daemon.log")
	if got != want {
		t.Errorf("logFilePath() = %q, want %q", got, want)
	}
}
