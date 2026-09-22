package cowmem

import "testing"

func TestLinuxCOWCaptureSegmentsPreservesOrderAndSparseTail(t *testing.T) {
	resource, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cow := resource.(*linuxCOW)
	defer cow.Close()
	if err := cow.CaptureSegments(2*wasmPageSize, []Segment{
		{Offset: 0, Data: []byte("abcd")},
		{Offset: 2, Data: []byte("XY")},
	}); err != nil {
		t.Fatal(err)
	}
	memory := testCOWMemory(t, 2*wasmPageSize)
	defer memory.Free()
	data := memory.Reallocate(2 * wasmPageSize)
	if err := cow.attachBytes(data); err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "abXY" || data[wasmPageSize] != 0 {
		t.Fatalf("seed=%q tail=%d", data[:4], data[wasmPageSize])
	}
}
