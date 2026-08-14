package runtime_test

import (
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestColdIOPolicyIsHostOwnedBoundedAndExplicit(t *testing.T) {
	base := runtime.DefaultRunConfig()
	for name, mutate := range map[string]func(*runtime.RunConfig){
		"enabled without policy": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
		},
		"policy while disabled": func(config *runtime.RunConfig) {
			config.ColdIO = &runtime.ColdIOPolicy{ColdAfter: time.Millisecond}
		},
		"pageout before cold": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{ColdAfter: 10 * time.Millisecond, PageOutAfter: time.Millisecond}
		},
		"threshold beyond timeout": func(config *runtime.RunConfig) {
			config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
			config.ColdIO = &runtime.ColdIOPolicy{ColdAfter: config.Timeout}
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if config.Validate() == nil {
				t.Fatal("invalid cold-I/O policy accepted")
			}
		})
	}

	for _, policy := range []runtime.ColdIOPolicy{
		{ColdAfter: time.Millisecond},
		{ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond},
	} {
		config := base
		config.Mechanisms = runtime.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
		config.ColdIO = &policy
		if err := config.Validate(); err != nil {
			t.Fatalf("valid policy %+v rejected: %v", policy, err)
		}
	}
}
