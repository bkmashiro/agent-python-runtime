package transaction

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

type RandomIDSource struct{}

func (RandomIDSource) New(prefix string) (string, error) {
	if !validIdentifier(prefix) || len(prefix) > 32 {
		return "", ErrInvalidInput
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("generate Host transaction identity")
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}

var _ IDSource = RandomIDSource{}
