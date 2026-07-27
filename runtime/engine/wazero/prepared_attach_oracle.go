package wazero

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/tetratelabs/wazero/api"
)

type preparedGlobalSnapshot map[string]uint64

type initializationHostCallGuardKey struct{}

type initializationHostCallGuard struct {
	count atomic.Uint32
}

func guardInitializationHostCalls(ctx context.Context) (context.Context, *initializationHostCallGuard) {
	guard := &initializationHostCallGuard{}
	return context.WithValue(ctx, initializationHostCallGuardKey{}, guard), guard
}

func recordInitializationHostCall(ctx context.Context) bool {
	guard, ok := ctx.Value(initializationHostCallGuardKey{}).(*initializationHostCallGuard)
	if !ok || guard == nil {
		return false
	}
	guard.count.Add(1)
	return true
}

func verifyNoInitializationHostCalls(guard *initializationHostCallGuard) error {
	if guard == nil {
		return errors.New("prepared attach Host-call guard is missing")
	}
	if count := guard.count.Load(); count != 0 {
		return fmt.Errorf("prepared initialization attempted %d Host capability calls", count)
	}
	return nil
}

func snapshotPreparedMutableGlobals(module api.Module, names []string) (preparedGlobalSnapshot, error) {
	if module == nil {
		return nil, errors.New("prepared attach global snapshot requires a module")
	}
	snapshot := make(preparedGlobalSnapshot, len(names))
	for _, name := range names {
		global := module.ExportedGlobal(name)
		if global == nil {
			return nil, fmt.Errorf("prepared attach mutable global %q is not exported", name)
		}
		if _, mutable := global.(api.MutableGlobal); !mutable {
			return nil, fmt.Errorf("prepared attach global %q is not mutable", name)
		}
		snapshot[name] = global.Get()
	}
	return snapshot, nil
}

func verifyPreparedMutableGlobals(module api.Module, canonical preparedGlobalSnapshot) error {
	if module == nil {
		return errors.New("prepared attach global verification requires a module")
	}
	for name, want := range canonical {
		global := module.ExportedGlobal(name)
		if global == nil {
			return fmt.Errorf("prepared attach mutable global %q disappeared", name)
		}
		if _, mutable := global.(api.MutableGlobal); !mutable {
			return fmt.Errorf("prepared attach global %q lost mutability", name)
		}
		if got := global.Get(); got != want {
			return fmt.Errorf("prepared attach mutable global %q drifted: got=%#x want=%#x", name, got, want)
		}
	}
	return nil
}
