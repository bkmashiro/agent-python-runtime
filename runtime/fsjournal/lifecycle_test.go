package fsjournal

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestClosedStoreRejectsBufferedAppendAndMetadataCapture(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fs.db"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Begin("r", "v", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = x.Append(Event{Operation: "open", Path: "a"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = x.Append(Event{Operation: "read", Path: "a"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	if err = x.EnsureContent("a", 1, func() ([]byte, error) { t.Fatal("unexpected load"); return nil, nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("capture after close: %v", err)
	}
	if len(x.queued) != 0 || len(s.sessions) != 0 {
		t.Fatal("closed store retains queued sessions")
	}
}
func TestEmptyContentAndCommittedPrefixCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Begin("r", "v", Options{CaptureContents: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = x.EnsureContent("empty", 1, func() ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err = x.Append(Event{Operation: "read", Resource: "empty", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run, events, err := s.Events("r")
	if err != nil || run.Complete || run.EventCount != 1 || len(events) != 1 {
		t.Fatalf("%+v %v %v", run, events, err)
	}
	content, err := s.Content("empty", 1)
	if err != nil || len(content) != 0 {
		t.Fatalf("empty content: %q %v", content, err)
	}
}
