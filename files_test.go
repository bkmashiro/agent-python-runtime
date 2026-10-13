package pysolate

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/fsjournal"
	"github.com/bkmashiro/agent-python-runtime/runtime/versionfs"
)

func TestRunFilesRealGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "prepared"}[prepared], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			var r *Runner
			if prepared {
				r, err = NewPreparedWorkspace(ctx, wasm, nil)
			} else {
				r, err = New(ctx, wasm, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(context.Background())
			s, _ := versionfs.New(versionfs.DefaultLimits())
			s.Mkdir("data")
			s.WriteFile("data/a.txt", []byte("abcdef"))
			s.WriteFile("unread.txt", []byte("secret"))
			old := s.View()
			s.WriteFile("data/a.txt", []byte("NEW"))
			journal, err := fsjournal.Open(filepath.Join(t.TempDir(), "fs.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			rec, err := journal.Begin("old", old.Revision(), fsjournal.Options{CaptureContents: true})
			if err != nil {
				t.Fatal(err)
			}
			out, err := r.RunFiles(ctx, `import os
with open('/workspace/data/a.txt', 'rb') as f:
    prefix = f.read(2).decode()
    f.seek(4)
    suffix = f.read().decode()
blocked = False
try:
    open('/workspace/data/a.txt', 'w').write('bad')
except OSError:
    blocked = True
result = [prefix, suffix, sorted(os.listdir('/workspace')), os.stat('/workspace/data/a.txt').st_size, blocked]`, nil, old, FileRunOptions{Recorder: rec})
			if err != nil {
				_, trace, _ := journal.Events("old")
				t.Fatalf("%v; FS trace: %+v", err, trace)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, out.Value); err != nil {
				t.Fatal(err)
			}
			if compact.String() != `["ab","ef",["data","unread.txt"],6,true]` {
				t.Fatalf("output %s", out.Value)
			}
			run, events, err := journal.Events("old")
			if err != nil || !run.Complete || len(events) == 0 || run.EventCount != uint64(len(events)) {
				t.Fatalf("run %+v events %d err %v", run, len(events), err)
			}
			id, _ := old.Identity("data/a.txt")
			data, err := journal.Content(id.ResourceID, id.Version)
			if err != nil || string(data) != "abcdef" {
				t.Fatalf("content %q %v", data, err)
			}
			unread, _ := old.Identity("unread.txt")
			if _, err = journal.Content(unread.ResourceID, unread.Version); err == nil {
				t.Fatal("captured unread file")
			}
			refs, err := journal.Find(id.ResourceID, &id.Version)
			if err != nil || len(refs) == 0 {
				t.Fatalf("references %v %v", refs, err)
			}
			out, err = r.RunFiles(ctx, `result = open('/workspace/data/a.txt').read()`, nil, s.View())
			if err != nil || string(out.Value) != `"NEW"` {
				t.Fatalf("new output %s %v", out.Value, err)
			}
		})
	}
}
func TestRunFilesRecordingFailureCannotReturnSuccess(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	effects := 0
	r, err := New(ctx, wasm, Manifest{"effect": {Call: func(context.Context, json.RawMessage) (any, error) { effects++; return nil, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	s, _ := versionfs.New(versionfs.DefaultLimits())
	s.WriteFile("a", []byte("ok"))
	v := s.View()
	j, _ := fsjournal.Open(filepath.Join(t.TempDir(), "fs.db"))
	defer j.Close()
	rec, _ := j.Begin("failed", v.Revision(), fsjournal.Options{MaxEvents: 1, BatchSize: 1})
	out, err := r.RunFiles(ctx, `try:
    for _ in range(10):
        open('/workspace/a').read()
except BaseException:
    pass
effect()
result = 'must not succeed'`, nil, v, FileRunOptions{Recorder: rec})
	if err == nil || !strings.Contains(err.Error(), "event limit") {
		t.Fatalf("got %s %v", out.Value, err)
	}
	if effects != 0 || len(out.Value) != 0 {
		t.Fatalf("effects=%d value=%s", effects, out.Value)
	}
	run, _, e := j.Events("failed")
	if e != nil || run.Complete || run.RecordingError == "" {
		t.Fatalf("run %+v %v", run, e)
	}
}
func TestDispatchCancelledDoesNotInvoke(t *testing.T) {
	called := false
	r := &Runner{manifest: Manifest{"effect": {Call: func(context.Context, json.RawMessage) (any, error) { called = true; return nil, nil }}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := r.dispatch(ctx, callRequest{Tool: "effect"})
	if called {
		t.Fatal("invoked after cancellation")
	}
	if !strings.Contains(string(response), context.Canceled.Error()) {
		t.Fatalf("response %s", response)
	}
}
func TestRunFilesInvalidPreparedShape(t *testing.T) {
	r := &Runner{image: []byte{1}}
	s, _ := versionfs.New(versionfs.DefaultLimits())
	if _, err := r.RunFiles(context.Background(), "", nil, s.View()); err == nil {
		t.Fatal("accepted incompatible prepared runner")
	}
	if _, err := r.RunFiles(context.Background(), "", nil, nil); err == nil {
		t.Fatal("accepted nil view")
	}
}
