package wazero

import (
	"context"
	"testing"

	wabinbinary "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func TestPreparedAttachMutableGlobalOracleDetectsDrift(t *testing.T) {
	wasm := wabinbinary.EncodeModule(&wabinwasm.Module{
		GlobalSection: []*wabinwasm.Global{{
			Type: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI32, Mutable: true},
			Init: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{7}},
		}},
		ExportSection: []*wabinwasm.Export{{Name: "state", Type: wabinwasm.ExternTypeGlobal, Index: 0}},
	})
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotPreparedMutableGlobals(module, []string{"state"})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPreparedMutableGlobals(module, snapshot); err != nil {
		t.Fatal(err)
	}
	module.ExportedGlobal("state").(api.MutableGlobal).Set(8)
	if err := verifyPreparedMutableGlobals(module, snapshot); err == nil {
		t.Fatal("mutable-global drift was accepted")
	}
}

func TestPreparedAttachMutableGlobalOracleRejectsMissingOrImmutable(t *testing.T) {
	wasm := wabinbinary.EncodeModule(&wabinwasm.Module{
		GlobalSection: []*wabinwasm.Global{{
			Type: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI32},
			Init: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{1}},
		}},
		ExportSection: []*wabinwasm.Export{{Name: "constant", Type: wabinwasm.ExternTypeGlobal, Index: 0}},
	})
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotPreparedMutableGlobals(module, []string{"missing"}); err == nil {
		t.Fatal("missing exported global was accepted")
	}
	if _, err := snapshotPreparedMutableGlobals(module, []string{"constant"}); err == nil {
		t.Fatal("immutable exported global was accepted as mutable")
	}
}
