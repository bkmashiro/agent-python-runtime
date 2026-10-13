package fsjournal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "journal.sqlite")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	return s, p
}
func TestPersistenceDuplicateAndOrdering(t *testing.T) {
	s, p := openTest(t)
	x, e := s.Begin("run", "rev", Options{BatchSize: 3})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 7; i++ {
		if e = x.Append(Event{Operation: "read", Resource: "r", Version: 2, Offset: int64(i)}); e != nil {
			t.Fatal(e)
		}
	}
	if e = x.Finish(errors.New("python failed")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Begin("run", "rev", Options{}); e == nil {
		t.Fatal("duplicate accepted")
	}
	s.Close()
	s, e = Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, events, e := s.Events("run")
	if e != nil {
		t.Fatal(e)
	}
	if !r.Complete || r.RunError != "python failed" || len(events) != 7 || r.EventCount != 7 {
		t.Fatalf("run=%+v len=%d", r, len(events))
	}
	for i, v := range events {
		if v.Seq != uint64(i+1) {
			t.Fatalf("seq=%d", v.Seq)
		}
	}
	if _, _, e = s.Events("missing"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing error=%v", e)
	}
}
func TestInterruptedPrefixStaysIncomplete(t *testing.T) {
	s, p := openTest(t)
	x, _ := s.Begin("interrupted", "v", Options{BatchSize: 1})
	_ = x.Append(Event{Operation: "open"})
	_ = s.Close()
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, ev, e := s.Events("interrupted")
	if e != nil || r.Complete || len(ev) != 1 {
		t.Fatalf("%+v %d %v", r, len(ev), e)
	}
}
func TestStickyLimitsAndContent(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	x, _ := s.Begin("bounded", "v", Options{MaxEvents: 1, BatchSize: 8, CaptureContents: true})
	var calls atomic.Int32
	load := func() ([]byte, error) { calls.Add(1); return []byte("contents"), nil }
	if e := x.EnsureContent("r", 1, load); e != nil {
		t.Fatal(e)
	}
	if e := x.EnsureContent("r", 1, load); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
	_ = x.Append(Event{Operation: "read", Resource: "r", Version: 1, Detail: json.RawMessage(`{"n":1}`)})
	if e := x.Append(Event{Operation: "read"}); e == nil {
		t.Fatal("limit accepted")
	}
	if e := x.Finish(nil); e == nil {
		t.Fatal("sticky error not returned")
	}
	r, ev, e := s.Events("bounded")
	if e != nil || r.Complete || len(ev) != 0 {
		t.Fatalf("run=%+v events=%d err=%v", r, len(ev), e)
	}
	y, _ := s.Begin("metadata", "v", Options{})
	if e = y.EnsureContent("x", 1, func() ([]byte, error) { t.Fatal("loader called with capture disabled"); return nil, nil }); e != nil {
		t.Fatal(e)
	}
	_ = y.Finish(nil)
}
func TestCapturedVersionAndIndex(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	x, _ := s.Begin("lookup", "v", Options{CaptureContents: true, BatchSize: 1})
	for _, v := range []uint64{1, 2} {
		if e := x.EnsureContent("same", v, func() ([]byte, error) { return []byte(fmt.Sprint(v)), nil }); e != nil {
			t.Fatal(e)
		}
		if e := x.Append(Event{Operation: "read", Resource: "same", Version: v}); e != nil {
			t.Fatal(e)
		}
	}
	_ = x.Finish(nil)
	v := uint64(2)
	refs, e := s.Find("same", &v)
	if e != nil || len(refs) != 1 || refs[0].Version != 2 {
		t.Fatalf("%+v %v", refs, e)
	}
	content, e := s.Content("same", 2)
	if e != nil || string(content) != "2" {
		t.Fatalf("content=%q err=%v", content, e)
	}
	content[0] = 'X'
	again, e := s.Content("same", 2)
	if e != nil || string(again) != "2" {
		t.Fatalf("content alias leaked: %q err=%v", again, e)
	}
	plan, e := s.QueryPlanForFind()
	if e != nil || !strings.Contains(plan, "events_resource_version_run_seq") {
		t.Fatalf("plan=%s err=%v", plan, e)
	}
}
func TestConcurrentSessions(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	const n = 8
	const m = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			x, e := s.Begin(fmt.Sprint(i), "v", Options{BatchSize: 5})
			if e != nil {
				t.Error(e)
				return
			}
			for j := 0; j < m; j++ {
				if e = x.Append(Event{Operation: "stat", Resource: "r", Version: 1}); e != nil {
					t.Error(e)
					return
				}
			}
			if e = x.Finish(nil); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	refs, e := s.Find("r", nil)
	if e != nil || len(refs) != n*m {
		t.Fatalf("refs=%d err=%v", len(refs), e)
	}
}
func TestAdditionalLimitsAreSticky(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	queued, _ := s.Begin("queue-limit", "v", Options{MaxQueuedBytes: 1})
	if err := queued.Append(Event{Operation: "read"}); err == nil {
		t.Fatal("queued metadata limit accepted")
	}
	if err := queued.Finish(nil); err == nil {
		t.Fatal("queue limit not sticky")
	}
	captured, _ := s.Begin("capture-limit", "v", Options{CaptureContents: true, MaxCapturedBytes: 3})
	if err := captured.EnsureContent("r", 1, func() ([]byte, error) { return []byte("four"), nil }); err == nil {
		t.Fatal("capture limit accepted")
	}
	if err := captured.Finish(nil); err == nil {
		t.Fatal("capture limit not sticky")
	}
}

func TestOptionsDetailAndClose(t *testing.T) {
	s, _ := openTest(t)
	x, _ := s.Begin("d", "", Options{MaxDetailBytes: 2})
	if e := x.Append(Event{Operation: "read", Detail: json.RawMessage(`{"long":1}`)}); e == nil {
		t.Fatal("oversized detail accepted")
	}
	if e := x.Finish(nil); e == nil {
		t.Fatal("sticky failure expected")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Begin("x", "", Options{}); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func BenchmarkBatchAppend(b *testing.B) {
	p := filepath.Join(b.TempDir(), "bench.db")
	s, e := Open(p)
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	payload := json.RawMessage(`{"payload":"` + strings.Repeat("x", 1024) + `"}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x, e := s.Begin(fmt.Sprintf("run-%d", i), "v", Options{BatchSize: 64})
		if e != nil {
			b.Fatal(e)
		}
		for j := 0; j < 100; j++ {
			if e = x.Append(Event{Operation: "read", Path: "/file", Resource: "r", Version: 1, Detail: payload}); e != nil {
				b.Fatal(e)
			}
		}
		if e = x.Finish(nil); e != nil {
			b.Fatal(e)
		}
	}
	b.StopTimer()
	_ = s.Close()
	info, e := os.Stat(p)
	if e != nil {
		b.Fatal(e)
	}
	b.ReportMetric(float64(info.Size())/float64(b.N*100), "sqlite-bytes/event")
	b.ReportMetric(float64(b.N*100*len(payload))/float64(info.Size()), "naive-payload/db-ratio")
}
