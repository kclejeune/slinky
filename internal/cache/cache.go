// Package cache provides an encrypted in-memory cache with per-entry TTL,
// stale serving, and background reaping.
package cache

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kclejeune/slinky/internal/cipher"
)

type Entry struct {
	Ciphertext []byte
	CreatedAt  time.Time
	TTL        time.Duration
}

func (e *Entry) Fresh() bool {
	return time.Since(e.CreatedAt) < e.TTL
}

func (e *Entry) Stale() bool {
	age := time.Since(e.CreatedAt)
	return age >= e.TTL && age < 2*e.TTL
}

func (e *Entry) Expired() bool {
	return time.Since(e.CreatedAt) >= 2*e.TTL
}

type SecretCache struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	cipher  cipher.CacheCipher
	stopCh  chan struct{}
	stopFn  sync.Once
	wg      sync.WaitGroup
}

func New(c cipher.CacheCipher) *SecretCache {
	sc := &SecretCache{
		entries: make(map[string]*Entry),
		cipher:  c,
		stopCh:  make(chan struct{}),
	}
	sc.wg.Add(1)
	go sc.reaper()
	return sc
}

func (sc *SecretCache) Get(key string) *Entry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.entries[key]
}

func (sc *SecretCache) Put(key string, plaintext []byte, ttl time.Duration) error {
	ciphertext, err := sc.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypting cache entry: %w", err)
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.entries[key] = &Entry{
		Ciphertext: ciphertext,
		CreatedAt:  time.Now(),
		TTL:        ttl,
	}
	return nil
}

func (sc *SecretCache) Decrypt(entry *Entry) ([]byte, error) {
	return sc.cipher.Decrypt(entry.Ciphertext)
}

func (sc *SecretCache) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for _, e := range sc.entries {
		clear(e.Ciphertext)
	}
	sc.entries = make(map[string]*Entry)
}

// SwapCipher atomically replaces the cipher and clears all cached entries
// (existing ciphertext is undecryptable with the new key).
func (sc *SecretCache) SwapCipher(c cipher.CacheCipher) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for _, e := range sc.entries {
		clear(e.Ciphertext)
	}
	sc.entries = make(map[string]*Entry)
	sc.cipher = c
}

func (sc *SecretCache) ClearKey(key string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if e, ok := sc.entries[key]; ok {
		clear(e.Ciphertext)
	}
	delete(sc.entries, key)
}

// EntryInfo holds rich metadata about a cache entry.
type EntryInfo struct {
	Age   time.Duration
	TTL   time.Duration
	State string // "fresh", "stale", or "expired"
}

func (sc *SecretCache) Stats() map[string]EntryInfo {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	stats := make(map[string]EntryInfo, len(sc.entries))
	for k, e := range sc.entries {
		info := EntryInfo{
			Age: time.Since(e.CreatedAt),
			TTL: e.TTL,
		}
		switch {
		case e.Fresh():
			info.State = "fresh"
		case e.Stale():
			info.State = "stale"
		default:
			info.State = "expired"
		}
		stats[k] = info
	}
	return stats
}

// CipherName returns the name of the current cache cipher.
func (sc *SecretCache) CipherName() string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.cipher.Name()
}

func (sc *SecretCache) Stop() {
	sc.stopFn.Do(func() { close(sc.stopCh) })
	sc.wg.Wait()
}

func (sc *SecretCache) reaper() {
	defer sc.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sc.stopCh:
			return
		case <-ticker.C:
			sc.mu.Lock()
			for k, e := range sc.entries {
				if e.Expired() {
					slog.Debug("reaping expired cache entry", "key", k)
					clear(e.Ciphertext)
					delete(sc.entries, k)
				}
			}
			sc.mu.Unlock()
		}
	}
}
