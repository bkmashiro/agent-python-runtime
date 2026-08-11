package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrInvalidGrant = errors.New("invalid Host capability grant")

const capabilityGrantSchemaVersion = "pysolate.capability-grant.v1"

// Grant is an opaque Host-owned identity for the exact per-Run policy granted
// to one capability. It intentionally does not expose or retain policy bytes.
type Grant struct {
	policySHA256 string
}

// GrantBinding is the public immutable projection included in a sealed Plan.
type GrantBinding struct {
	Capability   string `json:"capability"`
	PolicySHA256 string `json:"policy_sha256"`
}

func NewGrant(policy json.RawMessage) (Grant, error) {
	if len(policy) == 0 || len(policy) > maxCapabilitySchemaBytes {
		return Grant{}, ErrInvalidGrant
	}
	document, canonical, err := canonicalJSON(policy)
	if err != nil {
		return Grant{}, ErrInvalidGrant
	}
	if _, ok := document.(map[string]any); !ok {
		return Grant{}, ErrInvalidGrant
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion string          `json:"schema_version"`
		Policy        json.RawMessage `json:"policy"`
	}{SchemaVersion: capabilityGrantSchemaVersion, Policy: canonical})
	if err != nil {
		return Grant{}, ErrInvalidGrant
	}
	digest := sha256.Sum256(encoded)
	return Grant{policySHA256: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (grant Grant) Identity() string {
	return grant.policySHA256
}

func validGrant(grant Grant) bool {
	return validSHA256Identity(grant.policySHA256)
}

func validSHA256Identity(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
