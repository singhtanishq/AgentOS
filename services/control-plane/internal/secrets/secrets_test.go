package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T, seed byte) string {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	m, err := NewManager(map[int]string{1: testKey(t, 1)})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	secret := []byte("sk-provider-api-key-1234567890")
	envelope, err := m.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(envelope, secret) {
		t.Fatal("envelope contains the plaintext secret")
	}
	if KeyVersion(envelope) != 1 {
		t.Fatalf("key version = %d, want 1", KeyVersion(envelope))
	}
	got, err := m.Decrypt(envelope)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestKeyRotation(t *testing.T) {
	m, err := NewManager(map[int]string{
		1: testKey(t, 1),
		2: testKey(t, 2),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// A secret sealed under v1 remains decryptable after v2 becomes active.
	oldEnvelope, err := m.Encrypt([]byte("rotate-me"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if KeyVersion(oldEnvelope) != 2 {
		t.Fatalf("active key = %d, want 2 (highest)", KeyVersion(oldEnvelope))
	}

	// Simulate an envelope created when v1 was active.
	m1, err := NewManager(map[int]string{1: testKey(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	v1Envelope, err := m1.Encrypt([]byte("legacy secret"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Decrypt(v1Envelope)
	if err != nil {
		t.Fatalf("Decrypt v1 envelope with rotated keys: %v", err)
	}
	if string(got) != "legacy secret" {
		t.Fatalf("decrypted = %q", got)
	}
}

func TestUnknownKeyVersionRejected(t *testing.T) {
	m, err := NewManager(map[int]string{1: testKey(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := NewManager(map[int]string{9: testKey(t, 9)})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := foreign.Encrypt([]byte("other key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Decrypt(envelope); err == nil || !strings.Contains(err.Error(), "unknown key version") {
		t.Fatalf("expected unknown key version error, got %v", err)
	}
}

func TestTamperedCiphertextRejected(t *testing.T) {
	m, err := NewManager(map[int]string{1: testKey(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := m.Encrypt([]byte("do not tamper"))
	if err != nil {
		t.Fatal(err)
	}
	envelope[len(envelope)-1] ^= 0xFF
	if _, err := m.Decrypt(envelope); err == nil {
		t.Fatal("tampered envelope decrypted successfully")
	}
}

func TestInputValidation(t *testing.T) {
	m, err := NewManager(map[int]string{1: testKey(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Encrypt(nil); err == nil {
		t.Error("Encrypt accepted empty plaintext")
	}
	if _, err := m.Encrypt(make([]byte, MaxPlaintextSize+1)); err == nil {
		t.Error("Encrypt accepted oversized plaintext")
	}
	if _, err := NewManager(nil); err == nil {
		t.Error("NewManager accepted zero keys")
	}
	if _, err := NewManager(map[int]string{1: "not-base64!!"}); err == nil {
		t.Error("NewManager accepted invalid base64")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := NewManager(map[int]string{1: short}); err == nil {
		t.Error("NewManager accepted a short key")
	}
}

func TestFingerprintStableAndShort(t *testing.T) {
	a := Fingerprint([]byte("same secret"))
	b := Fingerprint([]byte("same secret"))
	c := Fingerprint([]byte("different secret"))
	if a != b || a == c {
		t.Fatalf("fingerprint mismatch: %q %q %q", a, b, c)
	}
	if len(a) != 8 {
		t.Fatalf("fingerprint length = %d, want 8 hex chars", len(a))
	}
}
