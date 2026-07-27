package wazero

import (
	"context"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wabinbinary "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
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
	census := censusCompiledReactor(compiled, fixedMemoryModule())
	if census.Memory.Count != 1 || census.Memory.MinPages != 1 || census.Memory.MaxPages != 1 || !census.Memory.MaxDeclared || !census.Memory.Fixed {
		t.Fatalf("unexpected memory census %#v", census.Memory)
	}
	if census.RestoreDecision != ReactorRestoreSingleUseOnly {
		t.Fatalf("opaque state was treated as restorable: %#v", census)
	}
	if !containsStateClass(census.UnknownStateClasses, "module-instance-state") || !containsStateClass(census.UnknownStateClasses, "wasi-host-state") {
		t.Fatalf("missing fail-closed unknown state classes: %#v", census.UnknownStateClasses)
	}
	if census.Artifact.Globals.Count != 0 || census.Artifact.Tables.Count != 0 || !census.Artifact.ParseComplete {
		t.Fatalf("unexpected static artifact census: %#v", census.Artifact)
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
			census := censusCompiledReactor(compiled, wasm)
			if census.Memory.COWEligible || census.RestoreDecision != ReactorRestoreSingleUseOnly || len(census.Reasons) == 0 {
				t.Fatalf("unsupported memory shape was accepted: %#v", census)
			}
		})
	}
}

func TestCensusCompiledReactorKeepsObservedMutableStateFailClosed(t *testing.T) {
	maximum := uint32(1)
	wasm := wabinbinary.EncodeModule(&wabinwasm.Module{
		MemorySection: &wabinwasm.Memory{Min: 1, Max: 1, IsMaxEncoded: true},
		GlobalSection: []*wabinwasm.Global{{
			Type: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI32, Mutable: true},
			Init: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{0}},
		}},
		TableSection:  []*wabinwasm.Table{{Min: 1, Max: &maximum, Type: wabinwasm.RefTypeFuncref}},
		ExportSection: []*wabinwasm.Export{{Name: guestMemoryExport, Type: wabinwasm.ExternTypeMemory, Index: 0}},
	})
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	census := censusCompiledReactor(compiled, wasm)
	if census.Artifact.Globals.MutableCount != 1 || census.Artifact.Tables.Count != 1 {
		t.Fatalf("static mutable state is missing: %#v", census.Artifact)
	}
	if !containsStateClass(census.UnknownStateClasses, "mutable-globals") || !containsStateClass(census.UnknownStateClasses, "tables") || census.RestoreDecision != ReactorRestoreSingleUseOnly {
		t.Fatalf("observed mutable state was not kept fail closed: %#v", census)
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
