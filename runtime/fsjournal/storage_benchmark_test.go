package fsjournal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A group contains 1,000 reads of one 64-KiB immutable file version. Include DB
// open/schema/first capture, flush and close. No Guest execution is measured.
func BenchmarkRepeatedFileReads(b *testing.B) {
	for _, capture := range []bool{false, true} {
		b.Run(fmt.Sprintf("capture=%t", capture), func(b *testing.B) {
			root := b.TempDir()
			payload := bytes.Repeat([]byte("x"), 64<<10)
			var totalBytes int64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path := filepath.Join(root, fmt.Sprintf("%d.sqlite", i))
				s, err := Open(path)
				if err != nil {
					b.Fatal(err)
				}
				session, err := s.Begin("r", "v", Options{CaptureContents: capture})
				if err != nil {
					b.Fatal(err)
				}
				loads := 0
				for k := 0; k < 1000; k++ {
					if err = session.EnsureContent("file", 1, func() ([]byte, error) { loads++; return payload, nil }); err != nil {
						b.Fatal(err)
					}
					if err = session.Append(Event{Operation: "read", Path: "data.bin", Resource: "file", Version: 1, Count: 64 << 10}); err != nil {
						b.Fatal(err)
					}
				}
				if err = session.Finish(nil); err != nil {
					b.Fatal(err)
				}
				if err = s.Close(); err != nil {
					b.Fatal(err)
				}
				if (capture && loads != 1) || (!capture && loads != 0) {
					b.Fatalf("load count %d", loads)
				}
				info, err := os.Stat(path)
				if err != nil {
					b.Fatal(err)
				}
				totalBytes += info.Size()
			}
			b.StopTimer()
			b.ReportMetric(float64(totalBytes)/float64(b.N), "db-bytes/group")
			b.ReportMetric(float64(totalBytes)/float64(b.N)/1000, "db-bytes/read")
			// Reference bytes only: no serialization, DB headers or compression included.
			b.ReportMetric(1000*(64<<10), "repeated-payload-bytes/group")
		})
	}
}
