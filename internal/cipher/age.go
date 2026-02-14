package cipher

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// AgeEphemeral implements CacheCipher using an in-memory age X25519 keypair.
// The private key exists only in process memory; when the daemon exits, the
// key is lost and all cached data becomes irrecoverable.
type AgeEphemeral struct {
	identity  *age.X25519Identity
	recipient *age.X25519Recipient
}

func NewAgeEphemeral() (*AgeEphemeral, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generating age identity: %w", err)
	}
	return &AgeEphemeral{
		identity:  id,
		recipient: id.Recipient(),
	}, nil
}

func (a *AgeEphemeral) Encrypt(plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, a.recipient)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("age encrypt write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("age encrypt close: %w", err)
	}
	return buf.Bytes(), nil
}

func (a *AgeEphemeral) Decrypt(ciphertext []byte) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), a.identity)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("age decrypt read: %w", err)
	}
	return plaintext, nil
}

func (a *AgeEphemeral) Name() string {
	return "age-ephemeral"
}
