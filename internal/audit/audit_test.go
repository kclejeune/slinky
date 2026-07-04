package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("parsing audit line %q: %v", scanner.Text(), err)
		}
		events = append(events, ev)
	}
	return events
}

func TestRecorderWritesJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "audit.log")

	r, err := NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	now := time.Now().Truncate(time.Second)
	r.Record(Event{
		Time: now, Event: "read", File: "netrc", Backend: "fuse",
		PID: 42, UID: 1000, Process: "curl",
	})
	r.Record(Event{Time: now, Event: "serve", File: "npmrc", Backend: "fifo", PID: -1, UID: -1})

	events := readEvents(t, path)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Event != "read" || events[0].File != "netrc" || events[0].PID != 42 ||
		events[0].Process != "curl" {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	if events[1].Event != "serve" || events[1].PID != -1 || events[1].UID != -1 {
		t.Errorf("unexpected second event: %+v", events[1])
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("audit log mode = %o, want 600", perm)
	}
}

func TestGlobalRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")

	// Disabled: Record is a no-op.
	if prev := Set(nil); prev != nil {
		defer Set(prev)
	}
	Record(Event{Event: "read", File: "netrc"})
	if Enabled() {
		t.Error("Enabled() = true with nil recorder")
	}

	r, err := NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	Set(r)
	defer func() {
		Set(nil)
		r.Close()
	}()

	if !Enabled() {
		t.Error("Enabled() = false after Set")
	}

	Record(Event{Event: "read", File: "netrc", Backend: "fuse", PID: -1, UID: -1})

	events := readEvents(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Time.IsZero() {
		t.Error("zero Time was not stamped by Record")
	}
}

func TestProcessName(t *testing.T) {
	// Our own PID must resolve to something on Linux and macOS.
	name := ProcessName(os.Getpid())
	if name == "" {
		t.Skip("process name lookup unavailable on this platform")
	}

	if ProcessName(-1) != "" {
		t.Error("ProcessName(-1) should be empty")
	}
	if ProcessName(0) != "" {
		t.Error("ProcessName(0) should be empty")
	}
}
