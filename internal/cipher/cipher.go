// Package cipher defines the CacheCipher interface and implementations
// for encrypting/decrypting cached secret content.
package cipher

import "fmt"

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
	case "age-ephemeral":
		return NewAgeEphemeral()
	default:
		return nil, fmt.Errorf("unsupported cipher type: %q", cipherType)
	}
}
