//go:build linux

package cowmem

import (
	"bytes"
	"golang.org/x/sys/unix"
	"testing"
)

func TestPreparedImageSparseCapture(t *testing.T) {
	for _, mode := range []string{"zero", "mixed", "full"} {
		t.Run(mode, func(t *testing.T) {
			resource, err := New()
			if err != nil {
				t.Fatal(err)
			}
			c := resource.(*linuxCOW)
			defer c.Close()
			data := make([]byte, 2*wasmPageSize)
			pages := 0
			if mode == "mixed" {
				data[0] = 1
				data[3*cowPageSize-1] = 2
				data[len(data)-1] = 3
				pages = 3
			}
			if mode == "full" {
				for i := range data {
					data[i] = byte(i%255 + 1)
				}
				pages = len(data) / cowPageSize
			}
			if err := c.captureBytes(data); err != nil {
				t.Fatal(err)
			}
			var st unix.Stat_t
			if err := unix.Fstat(c.fd, &st); err != nil {
				t.Fatal(err)
			}
			if st.Blocks*512 != int64(pages*cowPageSize) {
				t.Fatalf("allocated=%d want=%d", st.Blocks*512, pages*cowPageSize)
			}
			first := testCOWMemory(t, uint64(len(data)))
			second := testCOWMemory(t, uint64(len(data)))
			a := first.Reallocate(uint64(len(data)))
			b := second.Reallocate(uint64(len(data)))
			if err := c.attachBytes(a); err != nil {
				t.Fatal(err)
			}
			if err := c.attachBytes(b); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, data) || !bytes.Equal(b, data) {
				t.Fatal("snapshot bytes differ")
			}
			a[cowPageSize] ^= 255
			if b[cowPageSize] != data[cowPageSize] {
				t.Fatal("sparse page write escaped private mapping")
			}
			if _, err := unix.Pwrite(c.fd, []byte{9}, 0); err == nil {
				t.Fatal("image not sealed")
			}
		})
	}
}
