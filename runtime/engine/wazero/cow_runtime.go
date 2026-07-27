package wazero

import "context"

type cowPreparedRuntime interface {
	prepare(context.Context, *Engine, string) (*preparedInstance, error)
	close() error
}
