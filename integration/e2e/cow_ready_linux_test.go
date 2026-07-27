//go:build linux

package e2e_test

import (
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestCOWReadySingleUseProductionArtifact(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 30 * time.Second
	instance := newEngineWithFactory(t, config, wazeroengine.Factory{
		PreparedCapacity: 2,
		Strategy:         enginecontract.StrategyCOWReadySingleUse,
	})
	properties := instance.Properties()
	if err := properties.Validate(); err != nil {
		t.Fatalf("invalid COW properties: %v", err)
	}
	if properties.ActiveStrategy != enginecontract.StrategyCOWReadySingleUse || properties.Fallback {
		t.Fatalf("unexpected COW properties: %+v", properties)
	}
	for index, value := range []int{41, 99} {
		response := run(
			t,
			instance,
			"cow-production-"+string(rune('a'+index)),
			"result = {'value': inputs['value'] + 1}",
			map[string]any{"value": value},
		)
		if response.Status != "ok" {
			t.Fatalf("run %d returned guest error: %#v", index, response.Error)
		}
		result, ok := response.Result.(map[string]any)
		if !ok || result["value"] != float64(value+1) {
			t.Fatalf("run %d returned unexpected result: %#v", index, response.Result)
		}
	}
}

func TestCOWReadySingleUseProductionArtifactSupportsWASITimerWait(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 30 * time.Second
	instance := newEngineWithFactory(t, config, wazeroengine.Factory{
		PreparedCapacity: 2,
		Strategy:         enginecontract.StrategyCOWReadySingleUse,
	})
	started := time.Now()
	response := run(t, instance, "cow-ready-timer", "import time\ntime.sleep(inputs['wait_seconds'])\nresult = 'awake'", map[string]any{"wait_seconds": 0.05})
	elapsed := time.Since(started)
	if response.Status != "ok" || response.Result != "awake" {
		t.Fatalf("WASI timer response=%#v", response)
	}
	if elapsed+time.Millisecond < 50*time.Millisecond {
		t.Fatalf("WASI timer returned early: elapsed=%s", elapsed)
	}
}
