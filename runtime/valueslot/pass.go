package valueslot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

var ErrInvalidPreparedValuePass = errors.New("invalid prepared value binding pass")

type PreparedValuePass struct {
	registration passregistration.Registration
}

func NewPreparedValuePass() (PreparedValuePass, error) {
	definition := passregistration.PreparedValueBindingDefinition()
	registration, err := definition.Register("", preparedValueConfigSHA256())
	if err != nil {
		return PreparedValuePass{}, err
	}
	return PreparedValuePass{registration: registration}, nil
}

func (pass PreparedValuePass) Registration() passregistration.Registration {
	return pass.registration
}

// Bind lowers one Host-owned ValueSlot declaration to the trusted Run binding.
func (pass PreparedValuePass) Bind(slotID string) (string, error) {
	if pass.registration.IdentitySHA256() == "" {
		return "", ErrInvalidPreparedValuePass
	}
	return PythonPrelude(slotID)
}

func preparedValueConfigSHA256() string {
	digest := sha256.Sum256([]byte(passregistration.PreparedValueBindingVersion + "\x00scalar-or-private-bytes"))
	return "sha256:" + hex.EncodeToString(digest[:])
}
