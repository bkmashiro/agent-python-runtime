package runtime_test

import (
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestColdIOPolicyIsHostOwnedBoundedAndExplicit(t *testing.T) {
	base := runtime.DefaultRunConfig()
	invalid := map[string]func(*runtime.RunConfig){
		"enabled without policy": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
		},
		"policy while disabled": func(config *runtime.RunConfig) {
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIOFixed, ColdAfter: time.Millisecond}
		},
		"implicit strategy": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{ColdAfter: time.Millisecond}
		},
		"natural with thresholds": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIONatural, ColdAfter: time.Millisecond}
		},
		"pageout before cold": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIOFixed, ColdAfter: 10 * time.Millisecond, PageOutAfter: time.Millisecond}
		},
		"fixed pressure threshold": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIOFixed, ColdAfter: time.Millisecond, PressureThreshold: .5}
		},
		"pressure without threshold": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIOPressure, ColdAfter: time.Millisecond}
		},
		"threshold beyond timeout": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{Strategy: runtime.ColdIOFixed, ColdAfter: config.Timeout}
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if config.Validate() == nil {
				t.Fatal("invalid cold-I/O policy accepted")
			}
		})
	}

	valid := []runtime.ColdIOPolicy{
		{Strategy: runtime.ColdIONatural},
		{Strategy: runtime.ColdIOFixed, ColdAfter: time.Millisecond},
		{Strategy: runtime.ColdIOFixed, ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond},
		{Strategy: runtime.ColdIOPressure, ColdAfter: time.Millisecond, PressureThreshold: .5},
		{Strategy: runtime.ColdIOPressure, ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond, PressureThreshold: 1},
	}
	for _, policy := range valid {
		config := base
		config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
		config.ColdIO = &policy
		if err := config.Validate(); err != nil {
			t.Fatalf("valid policy %+v rejected: %v", policy, err)
		}
	}
}
