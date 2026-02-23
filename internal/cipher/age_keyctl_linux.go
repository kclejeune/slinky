//go:build linux

package cipher

import (
	"fmt"
	"log/slog"

	"golang.org/x/sys/unix"
)

const keyctlDescription = "slinky-cache-cipher"

func NewAgeKeyctl() (*AgeKeyctl, error) {
	c, err := newPersistedAgeCipher(keyctlLoad, keyctlStore, keyctlDelete)
	if err != nil {
		return nil, err
	}
	return &AgeKeyctl{c}, nil
}

func keyctlLoad() (string, error) {
	keyID, err := unix.KeyctlSearch(unix.KEY_SPEC_USER_KEYRING, "user", keyctlDescription, 0)
	if err != nil {
		return "", fmt.Errorf("keyctl search: %w", err)
	}

	buf := make([]byte, 128)
	n, err := unix.KeyctlBuffer(unix.KEYCTL_READ, keyID, buf, 0)
	if err != nil {
		return "", fmt.Errorf("keyctl read: %w", err)
	}

	return string(buf[:n]), nil
}

func keyctlStore(identity string) error {
	keyID, err := unix.AddKey(
		"user",
		keyctlDescription,
		[]byte(identity),
		unix.KEY_SPEC_USER_KEYRING,
	)
	if err != nil {
		return fmt.Errorf("keyctl add: %w", err)
	}

	// Possessor-only permissions: read/write/search/link/setattr/view.
	if err := unix.KeyctlSetperm(keyID, 0x3f000000); err != nil {
		return fmt.Errorf("keyctl setperm: %w", err)
	}

	return nil
}

func keyctlDelete() {
	keyID, err := unix.KeyctlSearch(unix.KEY_SPEC_USER_KEYRING, "user", keyctlDescription, 0)
	if err != nil {
		return
	}
	if _, err := unix.KeyctlInt(unix.KEYCTL_INVALIDATE, keyID, 0, 0, 0); err != nil {
		slog.Debug("keyctl delete failed", "error", err)
	}
}
