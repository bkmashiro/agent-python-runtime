//go:build !linux

package wazero

import (
	"context"
	"errors"
)

func newCOWPreparedRuntime(context.Context, *Engine) (cowPreparedRuntime, error) {
	return nil, errors.New("memory COW is only available on Linux")
}
