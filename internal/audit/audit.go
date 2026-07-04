// Package audit implements an append-only audit trail of secret file
// reads. When enabled, each read served by the daemon is recorded as one
// JSON object per line, including the caller's process identity when the
// mount backend can observe it (FUSE exposes the calling PID/UID; FIFO
// readers are anonymous).
//
// The active recorder is process-global, mirroring the log/slog pattern:
// backends call Record unconditionally and pay only an atomic load when
// auditing is disabled.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event is a single audit trail entry.
type Event struct {
	Time    time.Time `json:"time"`
	Event   string    `json:"event"`   // "read" (caller observed) or "serve" (anonymous reader)
	File    string    `json:"file"`    // logical file name, e.g. "netrc"
	Backend string    `json:"backend"` // mount backend that served the read
	PID     int       `json:"pid"`     // calling process ID, -1 if unknown
	UID     int       `json:"uid"`     // calling user ID, -1 if unknown
	Process string    `json:"process,omitempty"`
}

// Recorder appends events to an audit log file.
type Recorder struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// NewRecorder opens (or creates) the audit log at path for appending.
// The file is created with mode 0600; parent directories are created as
// needed.
func NewRecorder(path string) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	return &Recorder{f: f, path: path}, nil
}

// Path returns the audit log file path.
func (r *Recorder) Path() string { return r.path }

// Record appends one event to the log. Write errors are swallowed after
// the file is open — auditing must never break the read path.
func (r *Recorder) Record(ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	data = append(data, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.f.Write(data)
}

// Close closes the underlying log file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

var current atomic.Pointer[Recorder]

// Set installs r as the process-global recorder (nil disables auditing)
// and returns the previously installed recorder, if any, so the caller
// can close it.
func Set(r *Recorder) *Recorder {
	return current.Swap(r)
}

// Enabled reports whether a recorder is installed.
func Enabled() bool {
	return current.Load() != nil
}

// Record sends an event to the global recorder. A zero Time is stamped
// with the current time. No-op when auditing is disabled.
func Record(ev Event) {
	r := current.Load()
	if r == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	r.Record(ev)
}

// DefaultLogPath returns the default audit log path under XDG_STATE_HOME.
func DefaultLogPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "slinky", "audit.log")
}

// ProcessName returns a short name for the given PID, or "" if it cannot
// be determined. On Linux it reads /proc/<pid>/comm; elsewhere it falls
// back to ps(1).
func ProcessName(pid int) string {
	if pid <= 0 {
		return ""
	}

	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return strings.TrimSpace(string(data))
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}
