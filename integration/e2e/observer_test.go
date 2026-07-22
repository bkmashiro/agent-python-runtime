package e2e_test

import (
	"sync"
	"testing"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestWazeroObserverRecordsLifecyclePhases(t *testing.T) {
	var mutex sync.Mutex
	var phases []string
	factory := wazeroengine.Factory{
		Observer: func(observation wazeroengine.Observation) {
			mutex.Lock()
			defer mutex.Unlock()
			if observation.Duration <= 0 || !observation.Success {
				t.Errorf("invalid phase observation: %#v", observation)
			}
			phases = append(phases, observation.Phase)
		},
	}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	response := runWithPrepare(t, instance, "observer-run", "result = prepared + 1", nil, "prepared = 41")
	if response.Status != "ok" || response.Result != float64(42) {
		t.Fatalf("observed Run failed: %#v", response)
	}
	mutex.Lock()
	defer mutex.Unlock()
	want := []string{"instantiate_host", "compile", "instantiate_guest", "_initialize", "runtime_init", "prepare", "execute"}
	if len(phases) != len(want) {
		t.Fatalf("phases=%v, want %v", phases, want)
	}
	for index := range want {
		if phases[index] != want[index] {
			t.Fatalf("phases=%v, want %v", phases, want)
		}
	}
}
