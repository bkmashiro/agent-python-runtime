package versionmount

import (
	"errors"
	"io"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/fsrecord"
	"github.com/bkmashiro/agent-python-runtime/runtime/versionfs"
	sys "github.com/tetratelabs/wazero/experimental/sys"
)

type memorySink struct {
	revision string
	events   []fsrecord.Event
	contents int
	fail     bool
}

func (s *memorySink) ViewRevision() string { return s.revision }
func (s *memorySink) Append(e fsrecord.Event) error {
	if s.fail {
		return errors.New("recording failed")
	}
	s.events = append(s.events, e)
	return nil
}
func (s *memorySink) EnsureContent(_ string, _ uint64, load func() ([]byte, error)) error {
	_, err := load()
	s.contents++
	return err
}
func (s *memorySink) Finish(error) error { return nil }
func fixture(t *testing.T) (*versionfs.Store, *versionfs.View) {
	t.Helper()
	s, err := versionfs.New(versionfs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WriteFile("a.txt", []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	return s, s.View()
}
func TestMountReadOnlyAndRecording(t *testing.T) {
	_, v := fixture(t)
	sink := &memorySink{revision: v.Revision()}
	m, err := New(v, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range []sys.Oflag{sys.O_WRONLY, sys.O_RDWR, sys.O_CREAT, sys.O_TRUNC, sys.O_APPEND} {
		if f, e := m.OpenFile("a.txt", flags, 0600); e == 0 {
			f.Close()
			t.Fatalf("accepted write flags %v", flags)
		}
	}
	for _, path := range []string{"../a.txt", "/../a.txt", "x/../a.txt", "a\\b"} {
		if _, e := m.OpenFile(path, sys.O_RDONLY, 0); e == 0 {
			t.Fatalf("accepted %q", path)
		}
	}
	f, e := m.OpenFile("a.txt", sys.O_RDONLY, 0)
	if e != 0 {
		t.Fatal(e)
	}
	b := make([]byte, 2)
	if n, e := f.Read(b); n != 2 || e != 0 || string(b) != "ab" {
		t.Fatalf("read %d %v %q", n, e, b)
	}
	if n, e := f.Pread(b, 4); n != 2 || e != 0 || string(b) != "ef" {
		t.Fatalf("pread %d %v %q", n, e, b)
	}
	if n, e := f.Seek(1, io.SeekStart); n != 1 || e != 0 {
		t.Fatal(n, e)
	}
	f.Read(b)
	f.Close()
	if sink.contents != 1 {
		t.Fatalf("content loads %d", sink.contents)
	}
	var reads []fsrecord.Event
	for _, e := range sink.events {
		if e.Operation == "read" || e.Operation == "pread" {
			reads = append(reads, e)
		}
	}
	if len(reads) != 3 || reads[0].Offset != 0 || reads[1].Offset != 4 || reads[2].Offset != 1 {
		t.Fatalf("reads %+v", reads)
	}
	if m.Err() != nil {
		t.Fatal(m.Err())
	}
}
func TestMountRecordingFailureSticky(t *testing.T) {
	_, v := fixture(t)
	s := &memorySink{revision: v.Revision()}
	cancelled := false
	m, _ := New(v, s, func() { cancelled = true })
	f, e := m.OpenFile("a.txt", 0, 0)
	if e != 0 {
		t.Fatal(e)
	}
	s.fail = true
	// wazero maps a failed post-read Stat from EIO to EBADF.
	if n, e := f.Read(make([]byte, 2)); n != 0 || e != sys.EBADF {
		t.Fatal(n, e)
	}
	s.fail = false
	if _, e = m.OpenFile("a.txt", 0, 0); e != sys.EIO {
		t.Fatal(e)
	}
	_ = f.Close()
	if m.Err() == nil || !cancelled {
		t.Fatal("failure not sticky")
	}
}
func TestMountRejectsRecorderRevision(t *testing.T) {
	_, v := fixture(t)
	if _, err := New(v, &memorySink{revision: "other"}, nil); err == nil {
		t.Fatal("accepted another view")
	}
}
