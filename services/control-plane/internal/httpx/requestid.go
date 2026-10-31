package httpx

import (
	"crypto/rand"
	"encoding/hex"
)

// NewRequestID returns a 128-bit cryptographically random request ID.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A broken CSPRNG is unrecoverable for request correlation.
		panic("httpx: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
