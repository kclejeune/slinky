package cipher

import (
	"bytes"
	"testing"
)

func TestAgeEphemeralRoundTrip(t *testing.T) {
	c, err := NewAgeEphemeral()
	if err != nil {
		t.Fatalf("NewAgeEphemeral() error: %v", err)
	}

	plaintext := []byte("super secret token value")

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
	if c.Name() != "age-ephemeral" {
		t.Errorf("Name() = %q, want %q", c.Name(), "age-ephemeral")
	}
}

func TestAgeEphemeralEmptyPlaintext(t *testing.T) {
	c, err := NewAgeEphemeral()
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := c.Encrypt([]byte{})
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	if len(decrypted) != 0 {
		t.Errorf("expected empty decrypted content, got %d bytes", len(decrypted))
	}
}
