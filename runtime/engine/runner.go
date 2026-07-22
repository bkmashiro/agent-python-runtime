// Package engine defines the runtime-neutral Host/guest execution boundary.
// Concrete WebAssembly runtimes live in child adapter packages.
package engine

import (
	"context"
	"errors"
	"fmt"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

// Runner executes requests against one compiled guest artifact.
type Runner interface {
	Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error)
	Close(ctx context.Context) error
	Properties() Properties
}

// Factory constructs a Runner without exposing backend-specific module, memory,
// linker, or store types to callers.
type Factory interface {
	Name() string
	New(ctx context.Context, wasm []byte, config runtime.RunConfig) (Runner, error)
}

// ResetMode identifies the isolation mechanism a Runner actually applies.
type ResetMode string

const (
	// ResetModeFreshInstance destroys the request instance and instantiates a new
	// one for the next Run. It is the portable fail-closed baseline.
	ResetModeFreshInstance ResetMode = "fresh-instance"
	// ResetModePreparedRestore is reserved for a backend that restores a proven
	// prepared boundary, including every artifact state class it can mutate.
	ResetModePreparedRestore ResetMode = "prepared-restore"
)

var ErrInvalidProperties = errors.New("invalid engine properties")

// Properties records observable backend behavior, not aspirational support.
type Properties struct {
	Backend   string
	ResetMode ResetMode
}

func (properties Properties) Validate() error {
	if properties.Backend == "" {
		return fmt.Errorf("%w: backend name is empty", ErrInvalidProperties)
	}
	switch properties.ResetMode {
	case ResetModeFreshInstance, ResetModePreparedRestore:
		return nil
	default:
		return fmt.Errorf("%w: unknown reset mode %q", ErrInvalidProperties, properties.ResetMode)
	}
}
