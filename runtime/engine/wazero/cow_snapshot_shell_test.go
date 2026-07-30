package wazero

import (
	"bytes"
	"testing"

	wabin "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

func snapshotShellFixture(t *testing.T) []byte {
	t.Helper()
	module := &wabinwasm.Module{
		MemorySection: &wabinwasm.Memory{Min: 1, Max: 1, IsMaxEncoded: true},
		DataSection: []*wabinwasm.DataSegment{
			{OffsetExpression: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{3}}, Init: []byte("active")},
			{OffsetExpression: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{5}}, Init: []byte("XY")},
			{Init: []byte("passive")},
		},
	}
	return wabin.EncodeModule(module)
}

func TestCOWSnapshotShellBuildsSeedAndPreservesSegmentIndexes(t *testing.T) {
	plan, err := buildCOWSnapshotShell(snapshotShellFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.seedSize != wasmPageSize {
		t.Fatalf("seed size = %d", plan.seedSize)
	}
	seed, err := plan.materializeSeed()
	if err != nil {
		t.Fatal(err)
	}
	if got := seed[3:9]; !bytes.Equal(got, []byte("acXYve")) {
		t.Fatalf("overlapping active data = %q", got)
	}
	shell, err := wabin.DecodeModule(plan.shell, wabinwasm.CoreFeaturesV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(shell.DataSection) != 3 || len(shell.DataSection[0].Init) != 0 || len(shell.DataSection[1].Init) != 0 ||
		!bytes.Equal(shell.DataSection[2].Init, []byte("passive")) {
		t.Fatal("snapshot shell changed segment indexes, classification, or passive payload")
	}
}

func TestCOWSnapshotShellRejectsUnsupportedMemoryAndOffsets(t *testing.T) {
	growable := &wabinwasm.Module{
		MemorySection: &wabinwasm.Memory{Min: 1},
		DataSection:   []*wabinwasm.DataSegment{{OffsetExpression: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{0}}, Init: []byte{1}}},
	}
	if _, err := buildCOWSnapshotShell(wabin.EncodeModule(growable)); err == nil {
		t.Fatal("growable memory was accepted")
	}
	nonConstant := &wabinwasm.Module{
		MemorySection: &wabinwasm.Memory{Min: 1, Max: 1, IsMaxEncoded: true},
		DataSection:   []*wabinwasm.DataSegment{{OffsetExpression: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeGlobalGet, Data: []byte{0}}, Init: []byte{1}}},
	}
	if _, err := buildCOWSnapshotShell(wabin.EncodeModule(nonConstant)); err == nil {
		t.Fatal("non-constant active offset was accepted")
	}
	noActive := &wabinwasm.Module{MemorySection: &wabinwasm.Memory{Min: 1, Max: 1, IsMaxEncoded: true}}
	if _, err := buildCOWSnapshotShell(wabin.EncodeModule(noActive)); err == nil {
		t.Fatal("module without active payload produced a snapshot shell")
	}
	start := uint32(0)
	withStart := &wabinwasm.Module{
		MemorySection: &wabinwasm.Memory{Min: 1, Max: 1, IsMaxEncoded: true},
		StartSection:  &start,
		DataSection:   []*wabinwasm.DataSegment{{OffsetExpression: &wabinwasm.ConstantExpression{Opcode: wabinwasm.OpcodeI32Const, Data: []byte{0}}, Init: []byte{1}}},
	}
	if _, err := buildCOWSnapshotShell(wabin.EncodeModule(withStart)); err == nil {
		t.Fatal("module with a start section produced a snapshot shell")
	}
}
