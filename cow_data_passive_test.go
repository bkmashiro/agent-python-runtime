package pysolate

import (
	"context"
	"github.com/tetratelabs/wazero"
	"testing"
)

func TestCOWDataImagePassivePrefixExecution(t *testing.T) {
	// Two passive entries, then an active suffix, like the actual CPython image.
	// The function initializes one byte from passive entry 1, drops it and reads
	// the byte back. Segment dropping must remain private to each instance.
	body := []byte{0, 0x41, 8, 0x41, 0, 0x41, 1, 0xfc, 8, 1, 0, 0xfc, 9, 1, 0x41, 8, 0x2d, 0, 0, 0x0b}
	code := append([]byte{1, byte(len(body))}, body...)
	original := wasmModule(
		section(1, []byte{1, 0x60, 0, 1, 0x7f}),
		section(3, []byte{1, 0}),
		section(5, []byte{1, 1, 1, 1}),
		section(7, []byte{2, 6, 'm', 'e', 'm', 'o', 'r', 'y', 2, 0, 3, 'r', 'u', 'n', 0, 0}),
		section(12, []byte{3}), section(10, code),
		section(11, []byte{3, 1, 0, 1, 1, 42, 0, 0x41, 0, 0x0b, 1, 7}),
	)
	image, err := transformCOWDataImage(original)
	if err != nil {
		t.Fatal(err)
	}
	if got := dataSectionPayload(image.shell); len(got) != 6 || got[0] != 2 {
		t.Fatalf("passive prefix %x", got)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	compiled, err := r.CompileModule(ctx, image.shell)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		m, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range image.active {
			if !m.Memory().Write(uint32(s.offset), s.data) {
				t.Fatal("seed write")
			}
		}
		got, err := m.ExportedFunction("run").Call(ctx)
		if err != nil || len(got) != 1 || got[0] != 42 {
			t.Fatalf("instance %d: %v %v", i, got, err)
		}
		if _, err = m.ExportedFunction("run").Call(ctx); err == nil {
			t.Fatal("data.drop did not affect this instance")
		}
		if b, _ := m.Memory().ReadByte(0); b != 7 {
			t.Fatal("seed changed")
		}
		if err = m.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
