package pysolate

import (
	"bytes"
	"testing"
)

func TestCOWDataImageElidesUnreferencedActiveDescriptors(t *testing.T) {
	data := []byte{2, 0, 0x41, 0, 0x0b, 2, 7, 8, 0, 0x41, 1, 0x0b, 1, 9}
	original := wasmModule(section(wasmMemorySection, []byte{1, 1, 1, 1}), section(wasmDataSection, data), section(0, []byte{1, 'x', 42}))
	image, err := transformCOWDataImage(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dataSectionPayload(image.shell), []byte{0}) {
		t.Fatal("unreferenced active descriptors retained")
	}
	if !bytes.Equal(nonDataSections(original), nonDataSections(image.shell)) {
		t.Fatal("unrelated section changed")
	}
	if len(image.active) != 2 || image.active[1].offset != 1 || !bytes.Equal(image.active[0].data, []byte{7, 8}) || !bytes.Equal(image.active[1].data, []byte{9}) {
		t.Fatal("seed segments/order changed")
	}
}

func TestCOWDataImageCompactsDataCount(t *testing.T) {
	data := []byte{1, 0, 0x41, 0, 0x0b, 1, 7}
	memory := section(wasmMemorySection, []byte{1, 1, 1, 1})
	original := wasmModule(memory, section(12, []byte{1}), section(wasmDataSection, data))
	image, err := transformCOWDataImage(original)
	if err != nil {
		t.Fatal(err)
	}
	want := wasmModule(memory, section(12, []byte{0}), section(wasmDataSection, []byte{0}))
	if !bytes.Equal(image.shell, want) {
		t.Fatalf("shell=%x want=%x", image.shell, want)
	}
	for _, count := range [][]byte{{0}, {2}, {1, 0}, {0x80}} {
		if _, err := transformCOWDataImage(wasmModule(memory, section(12, count), section(wasmDataSection, data))); err == nil {
			t.Fatalf("accepted invalid count %x", count)
		}
	}
}
