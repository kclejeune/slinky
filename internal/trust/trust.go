// Package trust implements a direnv-style trust system for project configs.
//
// Project .slinky.toml files can execute arbitrary commands via the exec
// template function. Before activating a project config for the first time,
// the user must explicitly approve it with "slinky allow". The SHA-256 hash
// of each config file is stored in a trust database; if the file changes,
// re-approval is required.
package trust

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ErrUntrusted is returned when a project config has not been allowed.
var ErrUntrusted = errors.New("untrusted project config")

// Store manages the set of trusted project config hashes.
type Store struct {
	mu   sync.Mutex
	path string
	db   map[string]string // canonical config path → hex SHA-256
}

// NewStore creates a Store backed by the given JSON file path.
// The file is created lazily on first write.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// DefaultStorePath returns the default trust database path under XDG_STATE_HOME.
func DefaultStorePath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "slinky", "trusted.json")
}

// IsTrusted reports whether the config file at path has been allowed.
// Returns true if the file's current hash matches the stored hash.
func (s *Store) IsTrusted(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.load(); err != nil {
		return false, err
	}

	canonical, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}

	storedHash, ok := s.db[canonical]
	if !ok {
		return false, nil
	}

	currentHash, err := hashFile(path)
	if err != nil {
		return false, err
	}

	return storedHash == currentHash, nil
}

// Allow trusts the config file at path by storing its current hash.
func (s *Store) Allow(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.load(); err != nil {
		return err
	}

	canonical, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	hash, err := hashFile(path)
	if err != nil {
		return err
	}

	s.db[canonical] = hash
	return s.save()
}

// Deny removes trust for the config file at path.
func (s *Store) Deny(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.load(); err != nil {
		return err
	}

	canonical, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	delete(s.db, canonical)
	return s.save()
}

// CheckPaths verifies that all given config file paths are trusted.
// Returns the first untrusted path found, or "" if all are trusted.
func (s *Store) CheckPaths(paths []string) (untrusted string, err error) {
	for _, p := range paths {
		trusted, err := s.IsTrusted(p)
		if err != nil {
			return p, fmt.Errorf("checking trust for %q: %w", p, err)
		}
		if !trusted {
			return p, nil
		}
	}
	return "", nil
}

// VerifiedFile holds the raw bytes and path of a trust-verified config file.
type VerifiedFile struct {
	Path string
	Data []byte
}

// ReadAndVerifyPaths reads each config file once, verifies its hash against
// the trust store, and returns the verified file contents. This avoids the
// TOCTOU window of checking trust then re-reading the file separately.
// Returns the first untrusted path if any file fails verification.
func (s *Store) ReadAndVerifyPaths(paths []string) (files []VerifiedFile, untrusted string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.load(); err != nil {
		return nil, "", err
	}

	files = make([]VerifiedFile, 0, len(paths))
	for _, p := range paths {
		canonical, err := filepath.Abs(p)
		if err != nil {
			return nil, p, fmt.Errorf("resolving path %q: %w", p, err)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return nil, p, fmt.Errorf("reading %q: %w", p, err)
		}

		storedHash, ok := s.db[canonical]
		if !ok {
			return nil, p, nil
		}

		h := sha256.Sum256(data)
		currentHash := fmt.Sprintf("%x", h)
		if storedHash != currentHash {
			return nil, p, nil
		}

		files = append(files, VerifiedFile{Path: p, Data: data})
	}

	return files, "", nil
}

func (s *Store) load() error {
	if s.db != nil {
		return nil
	}

	s.db = make(map[string]string)

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading trust store %q: %w", s.path, err)
	}

	if err := json.Unmarshal(data, &s.db); err != nil {
		return fmt.Errorf("parsing trust store %q: %w", s.path, err)
	}

	return nil
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating trust store directory: %w", err)
	}

	data, err := json.MarshalIndent(s.db, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return os.WriteFile(s.path, data, 0o600)
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}
