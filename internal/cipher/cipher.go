// Package cipher defines the CacheCipher interface and implementations
// for encrypting/decrypting cached secret content.
package cipher

import (
	"fmt"
	"log/slog"
)

// CacheCipher encrypts and decrypts cached rendered template output.
type CacheCipher interface {
	// Encrypt encrypts plaintext and returns ciphertext.
	Encrypt(plaintext []byte) ([]byte, error)

	// Decrypt decrypts ciphertext and returns plaintext.
	Decrypt(ciphertext []byte) ([]byte, error)

	// Name returns the cipher backend name.
	Name() string
}

// New creates a CacheCipher for the given type string.
func New(cipherType string) (CacheCipher, error) {
	switch cipherType {
	case "auto":
		return newAutoCipher()
	case "keyring", "keychain":
		return NewAgeKeyring()
	case "ephemeral", "age-ephemeral":
		return NewAgeEphemeral()
	case "keyctl":
		return NewAgeKeyctl()
	default:
		return nil, fmt.Errorf("unsupported cipher type: %q", cipherType)
	}
}

// newAutoCipher tries keyring, then keyctl (Linux only), then ephemeral.
func newAutoCipher() (CacheCipher, error) {
	if c, err := NewAgeKeyring(); err == nil {
		return c, nil
	} else {
		slog.Debug("auto cipher: keyring unavailable, trying next", "error", err)
	}

	if c, err := NewAgeKeyctl(); err == nil {
		return c, nil
	} else {
		slog.Debug("auto cipher: keyctl unavailable, falling back to ephemeral", "error", err)
	}

	return NewAgeEphemeral()
}
