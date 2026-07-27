package rest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newRequestID produces the value for the NTNX-Request-Id header. The v4 APIs
// use it as an idempotence token, so every logical request needs its own.
// A random version 4 UUID is generated locally to avoid pulling in a dependency
// for one header.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A non-unique identifier is still better than an absent one: the header
		// only affects retry de-duplication.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}
