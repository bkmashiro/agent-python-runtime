package wazero

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestNonCOWPreparedWarmupRunsForEveryReadySlotAndBindsGeneration(t *testing.T) {
	wasm := e1DirtyLoopReactor(0, 4096, 1)
	var phasesMu sync.Mutex
	var phases []string
	runner, err := (Factory{
		PreparedCapacity:      3,
		Strategy:              enginecontract.StrategySingleUsePrepared,
		PreparedWarmupProfile: COWWarmupNumPyReadyV1,
		Observer: func(observation Observation) {
			if observation.Success {
				phasesMu.Lock()
				phases = append(phases, observation.Phase)
				phasesMu.Unlock()
			}
		},
	}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := runner.(*Engine)
	if ready := engine.PreparedReady(); ready != 3 {
		t.Fatalf("ready=%d, want 3", ready)
	}
	phasesMu.Lock()
	observedPhases := append([]string(nil), phases...)
	phasesMu.Unlock()
	warmups := 0
	for _, phase := range observedPhases {
		if phase == "pool_prepare_warmup" {
			warmups++
		}
	}
	if warmups != 3 {
		t.Fatalf("prepared warmup observations=%d, want 3; phases=%v", warmups, observedPhases)
	}
	state := engine.PreparedWarmupState()
	digest := sha256.Sum256(wasm)
	generation := sha256.Sum256(append(digest[:], []byte("\x00"+COWWarmupNumPyReadyV1)...))
	if state.Profile != COWWarmupNumPyReadyV1 || state.GenerationSHA256 != fmt.Sprintf("%x", generation[:]) {
		t.Fatalf("prepared warmup state=%+v", state)
	}
	if image := engine.PreparedImageState(); image.Available {
		t.Fatalf("non-COW warmup reported a COW image: %+v", image)
	}
}

func TestPreparedWarmupRejectsStrategyDriftAndConflictingProfiles(t *testing.T) {
	wasm := e1DirtyLoopReactor(0, 4096, 1)
	for name, factory := range map[string]Factory{
		"fresh": {
			PreparedWarmupProfile: COWWarmupNumPyReadyV1,
		},
		"COW": {
			PreparedCapacity: 1, Strategy: enginecontract.StrategyCOWReadySingleUse,
			PreparedWarmupProfile: COWWarmupNumPyReadyV1,
		},
		"conflicting": {
			PreparedCapacity: 1, Strategy: enginecontract.StrategySingleUsePrepared,
			PreparedWarmupProfile: COWWarmupNumPyReadyV1, COWWarmupProfile: COWWarmupRequestShellV1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if runner, err := factory.New(context.Background(), wasm, runtimeconfig.DefaultRunConfig()); err == nil {
				_ = runner.Close(context.Background())
				t.Fatal("invalid prepared warmup configuration was accepted")
			} else if !strings.Contains(err.Error(), "warmup") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
