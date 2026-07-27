package wazero

import (
	"testing"

	wabinbinary "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

func TestCensusReactorArtifactClassifiesNonMemoryState(t *testing.T) {
	max := uint32(8)
	start := wabinwasm.Index(0)
	module := &wabinwasm.Module{
		TypeSection: []*wabinwasm.FunctionType{{}},
		ImportSection: []*wabinwasm.Import{
			{Module: "host", Name: "mutable", Type: wabinwasm.ExternTypeGlobal, DescGlobal: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI32, Mutable: true}},
			{Module: "host", Name: "table", Type: wabinwasm.ExternTypeTable, DescTable: &wabinwasm.Table{Min: 1, Max: &max, Type: wabinwasm.RefTypeFuncref}},
		},
		FunctionSection: []wabinwasm.Index{0},
		GlobalSection: []*wabinwasm.Global{
			{Type: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI64}, Init: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI64Const, Data: []byte{0}}},
			{Type: &wabinwasm.GlobalType{ValType: wabinwasm.ValueTypeI32, Mutable: true}, Init: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{0}}},
		},
		ExportSection: []*wabinwasm.Export{{Name: "state", Type: wabinwasm.ExternTypeGlobal, Index: 2}},
		TableSection:  []*wabinwasm.Table{{Min: 1, Max: &max, Type: wabinwasm.RefTypeFuncref}},
		StartSection:  &start,
		CodeSection:   []*wabinwasm.Code{{Body: []byte{wabinwasm.OpcodeEnd}}},
	}

	wasm := wabinbinary.EncodeModule(module)
	// wabin's decoder supports bulk-memory element/data modes, while its old
	// encoder intentionally doesn't. Append canonical minimal sections directly:
	// active/passive/declarative empty elements, then active/passive data.
	wasm = append(wasm,
		0x09, 0x0c, 0x03,
		0x00, 0x41, 0x00, 0x0b, 0x00,
		0x01, 0x00, 0x00,
		0x03, 0x00, 0x00,
		0x0b, 0x15, 0x02,
		0x00, 0x41, 0x00, 0x0b, 0x06, 'a', 'c', 't', 'i', 'v', 'e',
		0x01, 0x07, 'p', 'a', 's', 's', 'i', 'v', 'e',
	)
	census, err := censusReactorArtifact(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if !census.ParseComplete || census.ImportCount != 2 || len(census.ImportModules) != 1 || census.ImportModules[0] != "host" {
		t.Fatalf("unexpected import census: %#v", census)
	}
	if census.Globals.Count != 3 || census.Globals.ImportedCount != 1 || census.Globals.MutableCount != 2 {
		t.Fatalf("unexpected global census: %#v", census.Globals)
	}
	if len(census.Globals.ExportedMutableNames) != 1 || census.Globals.ExportedMutableNames[0] != "state" || census.Globals.UnexportedMutableCount != 1 {
		t.Fatalf("unexpected mutable-global visibility: %#v", census.Globals)
	}
	if census.Tables.Count != 2 || census.Tables.ImportedCount != 1 || census.Tables.DefinedCount != 1 {
		t.Fatalf("unexpected table census: %#v", census.Tables)
	}
	if census.Elements.ActiveCount != 1 || census.Elements.PassiveCount != 1 || census.Elements.DeclarativeCount != 1 {
		t.Fatalf("unexpected element census: %#v", census.Elements)
	}
	if census.Data.ActiveCount != 1 || census.Data.PassiveCount != 1 || !census.HasStartFunction {
		t.Fatalf("unexpected initialization census: %#v", census)
	}
	if err := census.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCensusReactorArtifactFailsClosedOnInvalidBinary(t *testing.T) {
	census, err := censusReactorArtifact([]byte("not-wasm"))
	if err == nil || census.ParseComplete {
		t.Fatalf("invalid binary was accepted: census=%#v err=%v", census, err)
	}
}
