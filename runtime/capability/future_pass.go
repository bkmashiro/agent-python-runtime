package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

var ErrInvalidFutureProjectionPass = errors.New("invalid capability Future projection pass")

type FutureProjectionPass struct {
	registration passregistration.Registration
}

func NewFutureProjectionPass() (FutureProjectionPass, error) {
	definition := passregistration.CapabilityFutureProjectionDefinition()
	registration, err := definition.Register("", futureProjectionConfigSHA256())
	if err != nil {
		return FutureProjectionPass{}, err
	}
	return FutureProjectionPass{registration: registration}, nil
}

func (pass FutureProjectionPass) Registration() passregistration.Registration {
	return pass.registration
}

// Project lowers one sealed capability Plan to its analyzer-free Future projection.
func (pass FutureProjectionPass) Project(plan *Plan) (string, error) {
	if pass.registration.IdentitySHA256() == "" || plan == nil {
		return "", ErrInvalidFutureProjectionPass
	}
	return plan.FuturePythonPrelude(), nil
}

func futureProjectionConfigSHA256() string {
	digest := sha256.Sum256([]byte(passregistration.CapabilityFutureProjectionVersion + "\x00live-nonapproval-plan-projection"))
	return "sha256:" + hex.EncodeToString(digest[:])
}
