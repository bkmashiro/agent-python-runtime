package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCheckpointHandoff(b *testing.B) {
	for _, fixture := range []struct {
		name     string
		files    int
		fileSize int
	}{
		{name: "agent-small", files: 32, fileSize: 4 << 10},
		{name: "agent-medium", files: 128, fileSize: 32 << 10},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			files := make([]InitialFile, fixture.files)
			for index := range files {
				files[index] = InitialFile{
					Path: fmt.Sprintf("src/file-%04d.txt", index),
					Data: make([]byte, fixture.fileSize),
				}
			}
			newFixture := func(b *testing.B) (*Manager, Ref) {
				b.Helper()
				base := filepath.Join(b.TempDir(), "workspaces")
				if err := os.Mkdir(base, 0o700); err != nil {
					b.Fatal(err)
				}
				manager, err := NewManager(base)
				if err != nil {
					b.Fatal(err)
				}
				ref, err := manager.Create(files, DefaultLimits())
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() {
					if err := manager.Close(); err != nil {
						b.Error(err)
					}
				})
				return manager, ref
			}
			b.ReportMetric(float64(fixture.files*fixture.fileSize), "workspace-bytes")

			b.Run("checkpoint", func(b *testing.B) {
				manager, ref := newFixture(b)
				lease, err := manager.Acquire(ref, "owner")
				if err != nil {
					b.Fatal(err)
				}
				defer lease.Release()
				b.ResetTimer()
				for range b.N {
					if _, err := lease.Checkpoint(); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("acquire", func(b *testing.B) {
				manager, ref := newFixture(b)
				b.ResetTimer()
				for range b.N {
					lease, err := manager.Acquire(ref, "owner")
					if err != nil {
						b.Fatal(err)
					}
					if err := lease.Release(); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("acquire-checkpoint", func(b *testing.B) {
				manager, ref := newFixture(b)
				lease, err := manager.Acquire(ref, "initial")
				if err != nil {
					b.Fatal(err)
				}
				checkpoint, err := lease.Checkpoint()
				if err != nil {
					b.Fatal(err)
				}
				if err := lease.Release(); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for range b.N {
					next, err := manager.AcquireCheckpoint(checkpoint, "owner")
					if err != nil {
						b.Fatal(err)
					}
					if err := next.Release(); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
