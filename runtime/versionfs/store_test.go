package versionfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestImmutableViewsIdentityAndAliases(t *testing.T) {
	s, err := New(Limits{MaxFiles: 16, MaxBytes: 128, MaxFileBytes: 64, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir("dir"); err != nil {
		t.Fatal(err)
	}
	input := []byte("old")
	if err := s.WriteFile("dir/file", input); err != nil {
		t.Fatal(err)
	}
	old := s.View()
	oldID, _ := old.Identity("dir/file")
	input[0] = 'X'
	got, err := old.ReadFile("dir/file")
	if err != nil || string(got) != "old" {
		t.Fatalf("published alias: %q, %v", got, err)
	}
	got[0] = 'Y'
	got, _ = old.ReadFile("dir/file")
	if string(got) != "old" {
		t.Fatalf("ReadFile alias: %q", got)
	}
	if err := s.WriteFile("dir/file", []byte("new")); err != nil {
		t.Fatal(err)
	}
	newView := s.View()
	newID, _ := newView.Identity("dir/file")
	if oldID.ResourceID != newID.ResourceID || newID.Version != oldID.Version+1 {
		t.Fatalf("replacement identity: %#v -> %#v", oldID, newID)
	}
	if b, _ := old.ReadFile("dir/file"); string(b) != "old" {
		t.Fatalf("old view changed: %q", b)
	}
	f, err := old.Open("dir/file")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile("dir/file", []byte("latest")); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "old" {
		t.Fatalf("opened handle changed: %q %v", b, err)
	}
	_ = f.Close()
	if err := s.Remove("dir/file"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile("dir/file", []byte("again")); err != nil {
		t.Fatal(err)
	}
	recreated, _ := s.View().Identity("dir/file")
	if recreated.ResourceID == newID.ResourceID {
		t.Fatal("delete/recreate reused resource identity")
	}
	s2, _ := New(DefaultLimits())
	_ = s2.WriteFile("dir/file", []byte("again"))
	other, _ := s2.View().Identity("dir/file")
	if recreated.ResourceID == other.ResourceID {
		t.Fatal("different stores reused resource identity")
	}
}

func TestPathsQuotasAndAtomicFailure(t *testing.T) {
	s, err := New(Limits{MaxFiles: 2, MaxBytes: 4, MaxFileBytes: 3, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "../x", "a/../x", "/x", `a\\b`, "a//b", string([]byte{0xff})} {
		if err := s.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("accepted path %q", p)
		}
	}
	if err := s.WriteFile("missing/file", []byte("x")); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := s.Mkdir("a"); err != nil {
		t.Fatal(err)
	}
	before := s.View()
	if err := s.WriteFile("a/x", []byte("1234")); err == nil {
		t.Fatal("per-file quota ignored")
	}
	if err := s.WriteFile("a/x", []byte("123")); err != nil {
		t.Fatal(err)
	}
	published := s.View()
	if err := s.WriteFile("a/y", []byte("12")); err == nil {
		t.Fatal("tree byte quota ignored")
	}
	if s.View().Revision() != published.Revision() {
		t.Fatal("failed write published a revision")
	}
	if _, err := before.Open("a/x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("prior view changed: %v", err)
	}
	if err := s.Mkdir("a/x"); err == nil {
		t.Fatal("file replaced by directory")
	}
	if err := s.WriteFile("a/x/y", []byte("z")); err == nil {
		t.Fatal("file used as parent")
	}
	if err := s.Remove("a"); err == nil {
		t.Fatal("nonempty directory removed")
	}
	if err := s.Remove("."); err == nil {
		t.Fatal("root removed")
	}
	if err := s.WriteFile(".", []byte("x")); err == nil {
		t.Fatal("root replaced")
	}
}

func TestReadSeekDirectoryAndFSTest(t *testing.T) {
	s, err := New(Limits{MaxFiles: 20, MaxBytes: 100, MaxFileBytes: 100, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"z", "a", "d/k"} {
		if p == "d/k" {
			_ = s.Mkdir("d")
		}
		if err := s.WriteFile(p, []byte("012345")); err != nil {
			t.Fatal(err)
		}
	}
	v := s.View()
	if err := fstest.TestFS(v, "z", "a", "d/k"); err != nil {
		t.Fatal(err)
	}
	f, _ := v.Open("z")
	if _, err := f.(io.ReaderAt).ReadAt(make([]byte, 1), 6); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt EOF: %v", err)
	}
	if n, err := f.(io.ReaderAt).ReadAt(nil, 99); n != 0 || err != nil {
		t.Fatalf("empty ReadAt: %d %v", n, err)
	}
	if _, err := f.(io.Seeker).Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
	if n, err := f.(io.Seeker).Seek(-1, io.SeekEnd); err != nil || n != 5 {
		t.Fatalf("seek end: %d %v", n, err)
	}
	_ = f.Close()
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("read closed file: %v", err)
	}
	d, _ := v.Open(".")
	entries, err := d.(fs.ReadDirFile).ReadDir(1)
	if err != nil || len(entries) != 1 || entries[0].Name() != "a" {
		t.Fatalf("first ReadDir: %v %v", entries, err)
	}
	entries, err = d.(fs.ReadDirFile).ReadDir(0)
	if err != nil || len(entries) != 2 || entries[0].Name() != "d" || entries[1].Name() != "z" {
		t.Fatalf("remaining ReadDir: %v %v", entries, err)
	}
	if entries, err = d.(fs.ReadDirFile).ReadDir(1); len(entries) != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadDir EOF: %v %v", entries, err)
	}
	_ = d.Close()
	d, _ = v.Open(".")
	entries, err = d.(fs.ReadDirFile).ReadDir(-1)
	if err != nil || len(entries) != 3 {
		t.Fatalf("ReadDir all: %v %v", entries, err)
	}
}

func TestConcurrentViewsAndWrites(t *testing.T) {
	s, _ := New(DefaultLimits())
	_ = s.WriteFile("f", []byte("x"))
	start := make(chan struct{})
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			for n := 0; n < 100; n++ {
				v := s.View()
				b, err := v.ReadFile("f")
				if err != nil || !bytes.Contains([]byte("x0123456789"), b) {
					done <- errors.New("invalid concurrent snapshot")
					return
				}
			}
			done <- nil
		}()
	}
	close(start)
	for i := 0; i < 100; i++ {
		if err := s.WriteFile("f", []byte("0123456789")); err != nil {
			t.Fatal(err)
		}
		if err := s.WriteFile("f", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestParentDirectoryVersionTracksVisibleChildren(t *testing.T) {
	s, _ := New(DefaultLimits())
	_ = s.Mkdir("d")
	v1 := s.View()
	d1, _ := v1.Identity("d")
	_ = s.WriteFile("d/f", []byte("a"))
	v2 := s.View()
	d2, _ := v2.Identity("d")
	if d2.ResourceID != d1.ResourceID || d2.Version != d1.Version+1 {
		t.Fatalf("directory create version: %#v -> %#v", d1, d2)
	}
	f2, _ := v2.Identity("d/f")
	_ = s.WriteFile("d/f", []byte("b"))
	v3 := s.View()
	d3, _ := v3.Identity("d")
	f3, _ := v3.Identity("d/f")
	if d3.Version != d2.Version+1 {
		t.Fatalf("file replacement did not version parent entry metadata: %#v -> %#v", d2, d3)
	}
	if f3.Version != f2.Version+1 {
		t.Fatalf("file replacement version: %#v -> %#v", f2, f3)
	}
	_ = s.Remove("d/f")
	d4, _ := s.View().Identity("d")
	if d4.Version != d3.Version+1 {
		t.Fatalf("directory remove version: %#v -> %#v", d3, d4)
	}
}

func BenchmarkView(b *testing.B) {
	s, _ := New(DefaultLimits())
	_ = s.WriteFile("f", []byte("data"))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.View()
	}
}

func BenchmarkWriteFanout(b *testing.B) {
	for _, fanout := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprint(fanout), func(b *testing.B) {
			s, _ := New(Limits{MaxFiles: 2048, MaxBytes: 1 << 20, MaxFileBytes: 1024, MaxDepth: 4})
			for i := 0; i < fanout; i++ {
				_ = s.WriteFile(fmt.Sprintf("f%04d", i), []byte("x"))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.WriteFile("target", []byte("x")); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
