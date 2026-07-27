//go:build !linux

package wazero

import (
	"context"
	"errors"
)

func cowReadyStrategySupported() bool { return false }

func newCOWPreparedRuntime(context.Context, *Engine) (cowPreparedRuntime, error) {
	return nil, errors.New("cow-ready-single-use is supported only on Linux")
}
