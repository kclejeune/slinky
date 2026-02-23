package cipher

import (
	"bytes"
	"testing"
)

func assertRoundTrip(t *testing.T, c CacheCipher) {
	t.Helper()

	plaintext := []byte("round trip test value")
	ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("ciphertext should not equal plaintext")
	}
	decrypted, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}

	ciphertext, err = c.Encrypt([]byte{})
	if err != nil {
		t.Fatalf("Encrypt(empty) error: %v", err)
	}
	decrypted, err = c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt(empty) error: %v", err)
	}
	if len(decrypted) != 0 {
		t.Errorf("expected empty decrypted content, got %d bytes", len(decrypted))
	}
}

func TestAgeEphemeralRoundTrip(t *testing.T) {
	c, err := NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(t, c)
}

func TestAgeEphemeralCrossInstanceFails(t *testing.T) {
	c1, err := NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	c2, err := NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("cross instance test")
	ciphertext, err := c1.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c2.Decrypt(ciphertext)
	if err == nil {
		t.Error("expected error when decrypting with different cipher instance")
	}
}

func TestAgeEphemeralName(t *testing.T) {
	c, err := NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "ephemeral" {
		t.Errorf("Name() = %q, want %q", c.Name(), "ephemeral")
	}
}

func TestAgeKeyringRoundTrip(t *testing.T) {
	t.Cleanup(keyringDelete)

	c, err := NewAgeKeyring()
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(t, c)
}

func TestAgeKeyringPersistence(t *testing.T) {
	t.Cleanup(keyringDelete)

	c1, err := NewAgeKeyring()
	if err != nil {
		t.Fatalf("NewAgeKeyring() #1 error: %v", err)
	}

	plaintext := []byte("persistence test value")
	ciphertext, err := c1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	c2, err := NewAgeKeyring()
	if err != nil {
		t.Fatalf("NewAgeKeyring() #2 error: %v", err)
	}

	decrypted, err := c2.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() with second instance error: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestAgeKeyringName(t *testing.T) {
	t.Cleanup(keyringDelete)

	c, err := NewAgeKeyring()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "keyring" {
		t.Errorf("Name() = %q, want %q", c.Name(), "keyring")
	}
}
