package cipher

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"

	"filippo.io/age"
	"github.com/zalando/go-keyring"
)

const (
	keyringService = "slinky-cache-cipher"
	keyringAccount = "age-identity"
)

// ageCipher holds a loaded age identity and provides shared Encrypt/Decrypt.
type ageCipher struct {
	identity  *age.X25519Identity
	recipient *age.X25519Recipient
}

func (a *ageCipher) Encrypt(plaintext []byte) ([]byte, error) {
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

func (a *ageCipher) Decrypt(ciphertext []byte) ([]byte, error) {
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

// newPersistedAgeCipher loads an existing identity from a credential store,
// or generates and persists a new one. Corrupt stored identities are deleted
// and regenerated.
func newPersistedAgeCipher(
	load func() (string, error),
	store func(string) error,
	deleteFn func(),
) (ageCipher, error) {
	idStr, err := load()
	if err == nil {
		id, parseErr := age.ParseX25519Identity(idStr)
		if parseErr != nil {
			slog.Warn("corrupt identity in credential store, regenerating", "error", parseErr)
			deleteFn()
		} else {
			return ageCipher{identity: id, recipient: id.Recipient()}, nil
		}
	}

	id, err := age.GenerateX25519Identity()
	if err != nil {
		return ageCipher{}, fmt.Errorf("generating age identity: %w", err)
	}

	if err := store(id.String()); err != nil {
		return ageCipher{}, fmt.Errorf("storing identity: %w", err)
	}

	return ageCipher{identity: id, recipient: id.Recipient()}, nil
}

// AgeEphemeral implements CacheCipher using an in-memory age X25519 keypair.
// The private key exists only in process memory; when the daemon exits, the
// key is lost and all cached data becomes irrecoverable.
type AgeEphemeral struct{ ageCipher }

func NewAgeEphemeral() (*AgeEphemeral, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generating age identity: %w", err)
	}
	return &AgeEphemeral{ageCipher{identity: id, recipient: id.Recipient()}}, nil
}

func (a *AgeEphemeral) Name() string { return "ephemeral" }

// AgeKeyring implements CacheCipher using an age X25519 keypair persisted
// in the OS credential store (macOS Keychain, Linux Secret Service, Windows
// Credential Manager) via go-keyring.
type AgeKeyring struct{ ageCipher }

func NewAgeKeyring() (*AgeKeyring, error) {
	c, err := newPersistedAgeCipher(keyringLoad, keyringStore, keyringDelete)
	if err != nil {
		return nil, err
	}
	return &AgeKeyring{c}, nil
}

func (a *AgeKeyring) Name() string { return "keyring" }

// AgeKeyctl implements CacheCipher using an age X25519 keypair persisted
// in the Linux kernel user keyring via keyctl syscalls.
type AgeKeyctl struct{ ageCipher }

func (a *AgeKeyctl) Name() string { return "keyctl" }

func keyringLoad() (string, error) {
	secret, err := keyring.Get(keyringService, keyringAccount)
	if err != nil {
		return "", fmt.Errorf("keyring load: %w", err)
	}
	return secret, nil
}

func keyringStore(identity string) error {
	if err := keyring.Set(keyringService, keyringAccount, identity); err != nil {
		return fmt.Errorf("keyring store: %w", err)
	}
	return nil
}

func keyringDelete() {
	if err := keyring.Delete(keyringService, keyringAccount); err != nil {
		slog.Debug("keyring delete failed", "error", err)
	}
}
