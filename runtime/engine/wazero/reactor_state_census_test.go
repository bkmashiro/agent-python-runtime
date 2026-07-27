package wazero

import (
	"context"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazerort "github.com/tetratelabs/wazero"
)

func TestCensusCompiledReactorFixedMemoryStillFailsClosedOnOpaqueState(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, fixedMemoryModule())
	if err != nil {
		t.Fatal(err)
	}
	census := censusCompiledReactor(compiled)
	if census.Memory.Count != 1 || census.Memory.MinPages != 1 || census.Memory.MaxPages != 1 || !census.Memory.MaxDeclared || !census.Memory.Fixed {
		t.Fatalf("unexpected memory census %#v", census.Memory)
	}
	if census.RestoreDecision != ReactorRestoreSingleUseOnly {
		t.Fatalf("opaque state was treated as restorable: %#v", census)
	}
	if !containsStateClass(census.UnknownStateClasses, "mutable-globals") || !containsStateClass(census.UnknownStateClasses, "tables") {
		t.Fatalf("missing fail-closed unknown state classes: %#v", census.UnknownStateClasses)
	}
}

func TestCensusCompiledReactorRejectsGrowableOrMissingExportedMemory(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	for name, wasm := range map[string][]byte{
		"growable": growableMemoryModule(),
		"missing":  emptyModule(),
	} {
		t.Run(name, func(t *testing.T) {
			compiled, err := runtime.CompileModule(ctx, wasm)
			if err != nil {
				t.Fatal(err)
			}
			census := censusCompiledReactor(compiled)
			if census.Memory.COWEligible || census.RestoreDecision != ReactorRestoreSingleUseOnly || len(census.Reasons) == 0 {
				t.Fatalf("unsupported memory shape was accepted: %#v", census)
			}
		})
	}
}

func containsStateClass(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestEngineReturnsDefensiveStateCensus(t *testing.T) {
	ctx := context.Background()
	engine, err := New(ctx, fixedMemoryModule(), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(ctx)
	first := engine.StateCensus()
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if !first.Memory.COWEligible || len(first.Reasons) == 0 {
		t.Fatalf("unexpected engine census %#v", first)
	}
	invalid := first
	invalid.RestoreDecision = ReactorRestoreEligible
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted restore eligibility with unknown state classes")
	}
	invalid = first
	invalid.Memory.Fixed = false
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted COW eligibility for non-fixed memory")
	}
	first.Reasons[0] = "mutated"
	first.UnknownStateClasses[0] = "mutated"
	second := engine.StateCensus()
	if second.Reasons[0] == "mutated" || second.UnknownStateClasses[0] == "mutated" {
		t.Fatalf("engine exposed mutable census storage %#v", second)
	}
}

func emptyModule() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
}

func fixedMemoryModule() []byte {
	return append(emptyModule(),
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
		0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	)
}

func growableMemoryModule() []byte {
	return append(emptyModule(),
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	)
}
