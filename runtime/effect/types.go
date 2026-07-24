package effect

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

var (
	ErrConflict         = errors.New("idempotency conflict")
	ErrVersionConflict  = errors.New("resource version conflict")
	ErrNotFound         = errors.New("effect record not found")
	ErrRecipientDenied  = errors.New("recipient denied")
	ErrAuthorityDenied  = errors.New("Host authority denied")
	ErrManifestMismatch = errors.New("manifest digest mismatch")
)

func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
