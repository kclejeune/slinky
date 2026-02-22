package cache

import (
	"testing"
	"time"

	"github.com/kclejeune/slinky/internal/cipher"
)

func newTestCache(t *testing.T) *SecretCache {
	t.Helper()
	c, err := cipher.NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	sc := New(c)
	t.Cleanup(sc.Stop)
	return sc
}

func TestPutAndGet(t *testing.T) {
	sc := newTestCache(t)

	if err := sc.Put("key1", []byte("hello"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	entry := sc.Get("key1")
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}

	plaintext, err := sc.Decrypt(entry)
	if err != nil {
		t.Fatal(err)
	}

	if string(plaintext) != "hello" {
		t.Errorf("decrypted = %q, want %q", plaintext, "hello")
	}
}

func TestGetMiss(t *testing.T) {
	sc := newTestCache(t)

	entry := sc.Get("nonexistent")
	if entry != nil {
		t.Error("expected nil for missing key")
	}
}

func TestEntryFreshStaleExpired(t *testing.T) {
	entry := &Entry{
		CreatedAt: time.Now(),
		TTL:       100 * time.Millisecond,
	}

	if !entry.Fresh() {
		t.Error("entry should be fresh immediately after creation")
	}

	time.Sleep(110 * time.Millisecond)
	if entry.Fresh() {
		t.Error("entry should not be fresh after TTL")
	}
	if !entry.Stale() {
		t.Error("entry should be stale after TTL but before 2x TTL")
	}

	time.Sleep(110 * time.Millisecond)
	if entry.Stale() {
		t.Error("entry should not be stale after 2x TTL")
	}
	if !entry.Expired() {
		t.Error("entry should be expired after 2x TTL")
	}
}

func TestClear(t *testing.T) {
	sc := newTestCache(t)

	if err := sc.Put("key1", []byte("val1"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := sc.Put("key2", []byte("val2"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	sc.Clear()

	if sc.Get("key1") != nil {
		t.Error("expected nil after Clear")
	}
	if sc.Get("key2") != nil {
		t.Error("expected nil after Clear")
	}
}

func TestClearKey(t *testing.T) {
	sc := newTestCache(t)

	if err := sc.Put("key1", []byte("val1"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := sc.Put("key2", []byte("val2"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	sc.ClearKey("key1")

	if sc.Get("key1") != nil {
		t.Error("expected nil after ClearKey")
	}
	if sc.Get("key2") == nil {
		t.Error("expected key2 to still exist")
	}
}

func TestStats(t *testing.T) {
	sc := newTestCache(t)

	if err := sc.Put("key1", []byte("val1"), 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	stats := sc.Stats()
	if _, ok := stats["key1"]; !ok {
		t.Error("expected key1 in stats")
	}
}
