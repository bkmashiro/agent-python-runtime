package pysolate

import (
	"bytes"
	"testing"
)

func TestTransformCOWDataImagePreservesSectionsAndSemantics(t *testing.T) {
	memory := []byte{1, 1, 1, 1} // one local, fixed one-page memory
	data := []byte{
		3,
		2, 0, 0x41, 0, 0x0b, 4, 'a', 'b', 'c', 'd', // active
		1, 2, 'p', 'q', // passive
		0, 0x41, 2, 0x0b, 2, 'x', 'y', // active overlap
	}
	custom := []byte{0, 3, 'x', 'y', 'z'}
	wasm := wasmModule(section(wasmMemorySection, memory), section(wasmDataSection, data), section(0, custom))

	image, err := transformCOWDataImage(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if image.memorySize != wasmPageSizeBytes || len(image.active) != 2 {
		t.Fatalf("image=%+v", image)
	}
	if image.active[0].offset != 0 || !bytes.Equal(image.active[0].data, []byte("abcd")) || image.active[1].offset != 2 || !bytes.Equal(image.active[1].data, []byte("xy")) {
		t.Fatalf("active segments=%+v", image.active)
	}

	originalNonData := nonDataSections(wasm)
	if got := nonDataSections(image.shell); !bytes.Equal(got, originalNonData) {
		t.Fatalf("non-Data sections changed\n got %x\nwant %x", got, originalNonData)
	}
	wantData := []byte{3, 2, 0, 0x41, 0, 0x0b, 0, 1, 2, 'p', 'q', 0, 0x41, 2, 0x0b, 0}
	if got := dataSectionPayload(image.shell); !bytes.Equal(got, wantData) {
		t.Fatalf("rewritten Data=%x want %x", got, wantData)
	}
}

func TestTransformCOWDataImageRefusesUnsupportedModules(t *testing.T) {
	baseData := []byte{1, 0, 0x41, 0, 0x0b, 1, 7}
	cases := []struct {
		name     string
		sections [][]byte
	}{
		{"start", [][]byte{section(wasmMemorySection, []byte{1, 1, 1, 1}), section(wasmStartSection, []byte{0}), section(wasmDataSection, baseData)}},
		{"unbounded", [][]byte{section(wasmMemorySection, []byte{1, 0, 1}), section(wasmDataSection, baseData)}},
		{"imported memory", [][]byte{section(wasmImportSection, []byte{1, 1, 'm', 1, 'x', 2, 1, 1, 1}), section(wasmMemorySection, []byte{1, 1, 1, 1}), section(wasmDataSection, baseData)}},
		{"nonconstant offset", [][]byte{section(wasmMemorySection, []byte{1, 1, 1, 1}), section(wasmDataSection, []byte{1, 0, 0x23, 0, 0x0b, 1, 7})}},
		{"out of bounds", [][]byte{section(wasmMemorySection, []byte{1, 1, 1, 1}), section(wasmDataSection, []byte{1, 0, 0x41, 0x80, 0x80, 0x04, 0x0b, 1, 7})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := transformCOWDataImage(wasmModule(tc.sections...)); err == nil {
				t.Fatal("accepted unsupported module")
			}
		})
	}
}

func wasmModule(sections ...[]byte) []byte {
	out := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for _, section := range sections {
		out = append(out, section...)
	}
	return out
}

func section(id byte, payload []byte) []byte {
	out := []byte{id}
	out = append(out, appendULEB(nil, uint32(len(payload)))...)
	return append(out, payload...)
}

func nonDataSections(wasm []byte) []byte {
	var out []byte
	for pos := 8; pos < len(wasm); {
		start, id := pos, wasm[pos]
		pos++
		length, next, err := readULEB(wasm, pos, 32)
		if err != nil {
			panic(err)
		}
		pos = next + int(length)
		if id != wasmDataSection {
			out = append(out, wasm[start:pos]...)
		}
	}
	return out
}

func dataSectionPayload(wasm []byte) []byte {
	for pos := 8; pos < len(wasm); {
		id := wasm[pos]
		pos++
		length, next, err := readULEB(wasm, pos, 32)
		if err != nil {
			panic(err)
		}
		if id == wasmDataSection {
			return wasm[next : next+int(length)]
		}
		pos = next + int(length)
	}
	return nil
}
