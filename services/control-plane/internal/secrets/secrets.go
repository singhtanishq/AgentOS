// Package secrets provides authenticated encryption for application-managed
// secrets (provider API keys, webhook signing secrets).
//
// Envelope format: [keyVersion byte][12-byte nonce][ciphertext+GCM tag].
// The key-version byte supports rotation: multiple keys can be held
// simultaneously, with the newest used for encryption and the matching key
// selected for decryption.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

// MaxPlaintextSize bounds secret size (provider keys are short).
const MaxPlaintextSize = 16 * 1024

// ErrKeyVersionUnknown is returned when a ciphertext references a key
// version that is not loaded.
var ErrKeyVersionUnknown = errors.New("secrets: unknown key version")

// Manager encrypts and decrypts secrets with versioned AES-256-GCM keys.
type Manager struct {
	// activeKeyID is the version used for new ciphertexts.
	activeKeyID byte
	// keys maps key version → 32-byte AES key.
	keys map[byte][]byte
}

// NewManager builds a Manager from base64-encoded 32-byte keys, keyed by
// version (1-255). The highest version becomes the active key.
func NewManager(keysByVersion map[int]string) (*Manager, error) {
	if len(keysByVersion) == 0 {
		return nil, errors.New("secrets: at least one key is required")
	}
	m := &Manager{keys: make(map[byte][]byte, len(keysByVersion))}
	maxVersion := 0
	for version, encoded := range keysByVersion {
		if version < 1 || version > 255 {
			return nil, fmt.Errorf("secrets: key version %d out of range 1-255", version)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("secrets: key version %d is not valid base64: %w", version, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("secrets: key version %d must decode to 32 bytes, got %d", version, len(raw))
		}
		m.keys[byte(version)] = raw
		if version > maxVersion {
			maxVersion = version
		}
	}
	m.activeKeyID = byte(maxVersion)
	return m, nil
}

// Encrypt seals plaintext into the versioned envelope.
func (m *Manager) Encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("secrets: empty plaintext")
	}
	if len(plaintext) > MaxPlaintextSize {
		return nil, fmt.Errorf("secrets: plaintext exceeds %d bytes", MaxPlaintextSize)
	}
	key := m.keys[m.activeKeyID]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+len(nonce)+len(sealed))
	out = append(out, m.activeKeyID)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt opens a versioned envelope using the matching key.
func (m *Manager) Decrypt(envelope []byte) ([]byte, error) {
	if len(envelope) < 1+12+16 {
		return nil, errors.New("secrets: envelope too short")
	}
	version := envelope[0]
	key, ok := m.keys[version]
	if !ok {
		return nil, ErrKeyVersionUnknown
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	nonce := envelope[1 : 1+gcm.NonceSize()]
	sealed := envelope[1+gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: decrypt: %w", err)
	}
	return plaintext, nil
}

// DecryptParts opens ciphertext stored with the nonce kept in a separate
// column (the database schema stores them apart).
func (m *Manager) DecryptParts(version byte, nonce, sealed []byte) ([]byte, error) {
	key, ok := m.keys[version]
	if !ok {
		return nil, ErrKeyVersionUnknown
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: decrypt: %w", err)
	}
	return plaintext, nil
}

// ActiveKeyVersion reports the key version used for new envelopes.
func (m *Manager) ActiveKeyVersion() byte { return m.activeKeyID }

// Fingerprint returns a short, non-reversible identifier of a secret for
// display in UIs and logs (never the secret itself).
func Fingerprint(secret []byte) string {
	sum := sha256.Sum256(secret)
	return fmt.Sprintf("%x", sum[:4])
}

// HMACSign produces an HMAC-SHA256 signature (webhook signing).
func HMACSign(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// KeyVersion extracts the key-version byte from an envelope.
func KeyVersion(envelope []byte) byte {
	if len(envelope) == 0 {
		return 0
	}
	return envelope[0]
}

// EnvelopeSize returns the total size of a sealed envelope for validation
// helpers (version + nonce + GCM tag + plaintext).
func EnvelopeSize(plaintextLen int) int {
	return 1 + 12 + 16 + plaintextLen
}

// unused guard for binary keeps import symmetry with future headers
var _ = binary.LittleEndian
