package cipher

import "fmt"

// CacheCipher encrypts and decrypts cached rendered template output.
type CacheCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
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
