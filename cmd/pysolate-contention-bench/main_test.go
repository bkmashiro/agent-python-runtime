package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

func testTrace() *traceLog { return newTrace(io.Discard, func() {}) }

func TestSummaryKeepsFailuresAndMissingLatency(t *testing.T) {
	rows := []requestRow{{Work: "pair", Status: "ok", LatencyNS: 20, Calls: 2}, {Work: "pair", Status: "timeout", LatencyNS: 100, Calls: 1}, {Work: "short", Status: "rejected"}}
	s := summarize(rows)
	c := s["counts"].(map[string]int)
	if s["scheduled"] != 3 || c["ok"] != 1 || c["timeout"] != 1 || c["rejected"] != 1 || s["calls"] != int64(3) {
		t.Fatalf("%+v", s)
	}
	short := s["by_work"].(map[string]any)["short"].(map[string]any)
	raw, _ := json.Marshal(short)
	if !bytes.Contains(raw, []byte(`"ok_p95_ns":null`)) {
		t.Fatal(string(raw))
	}
	values := []int64{30, 10, 20}
	if *percentile(values, .95) != 30 || values[0] != 30 {
		t.Fatal("quantile mutated input or computed wrong rank")
	}
}

func TestAdmissionAndDeadlineDoNotExecute(t *testing.T) {
	plan, _ := workloads("pair")
	invoke := func(context.Context, string, any) (pysolate.Output, error) {
		t.Fatal("must not execute")
		return pysolate.Output{}, nil
	}
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	r := runRequest(context.Background(), 0, plan[0], time.Now(), time.Second, slots, testTrace(), invoke)
	if r.Status != "rejected" || r.Admitted {
		t.Fatalf("%+v", r)
	}
	<-slots
	r = runRequest(context.Background(), 1, plan[0], time.Now().Add(-time.Second), time.Millisecond, slots, testTrace(), invoke)
	if r.Status != "timeout" || r.Admitted || r.LatencyNS < time.Second.Nanoseconds() {
		t.Fatalf("%+v", r)
	}
}

func TestResultAndCallCountOracle(t *testing.T) {
	work := workload{Name: "pair", Expected: "42", Calls: 2}
	for _, tc := range []struct {
		value string
		calls int64
		want  string
	}{
		{"42", 2, "ok"}, {"41", 2, "incorrect"}, {"42", 1, "incorrect"},
	} {
		invoke := func(ctx context.Context, _ string, _ any) (pysolate.Output, error) {
			ctx.Value(requestKey{}).(*attribution).calls.Add(tc.calls)
			return pysolate.Output{Value: json.RawMessage(tc.value)}, nil
		}
		row := runRequest(context.Background(), 0, work, time.Now(), time.Second, make(chan struct{}, 1), testTrace(), invoke)
		if row.Status != tc.want {
			t.Fatalf("%+v: %+v", tc, row)
		}
	}
}

func TestCancelledLoadAccountsForEveryArrival(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan, _ := workloads("pair,short")
	c := config{Requests: 8, Interval: time.Second, Timeout: time.Second}
	invoke := func(context.Context, string, any) (pysolate.Output, error) {
		t.Fatal("must not execute")
		return pysolate.Output{}, nil
	}
	rows, _ := runLoad(ctx, c, plan, make(chan struct{}, 1), testTrace(), invoke)
	if len(rows) != 8 || countStatus(rows, "cancelled") != 8 {
		t.Fatalf("%+v", rows)
	}
	for i, r := range rows {
		if r.LatencyNS < 0 || r.ArrivalLagNS < 0 {
			t.Fatalf("future cancelled arrival has negative duration: %+v", r)
		}
		if r.ID != i || r.Work != plan[i%2].Name {
			t.Fatal("lost arrival identity")
		}
	}
}

func TestBackendBoundsAndPairsEveryCall(t *testing.T) {
	var raw bytes.Buffer
	trace := newTrace(&raw, func() {})
	b := newBackend(2, 5*time.Millisecond, trace)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, requestKey{}, &attribution{id: 0})
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			v, err := b.call(ctx, json.RawMessage(`{"value":42}`))
			if err != nil || v != 42 {
				t.Errorf("%v %v", v, err)
			}
		}()
	}
	group.Wait()
	s := b.snapshot()
	if s["attempts"] != 8 || s["acquired"] != 8 || s["peak_inflight"].(int) > 2 || s["end_inflight"] != 0 {
		t.Fatal(s)
	}
	counts := map[string]int{}
	dec := json.NewDecoder(&raw)
	for {
		var event map[string]any
		err := dec.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		counts[event["kind"].(string)]++
	}
	if counts["tool_queued"] != 8 || counts["tool_started"] != 8 || counts["tool_end"] != 8 {
		t.Fatal(counts)
	}
}

func TestBackendCancelledInQueueDoesNotAcquire(t *testing.T) {
	b := newBackend(1, time.Second, testTrace())
	b.slots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, requestKey{}, &attribution{id: 0})
	_, err := b.call(ctx, json.RawMessage(`{"value":42}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	s := b.snapshot()
	if s["acquired"] != 0 || s["attempts"] != 1 || s["errors"] != 1 || s["end_inflight"] != 0 {
		t.Fatal(s)
	}
	<-b.slots
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func TestTraceFailureCancelsCampaign(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := newTrace(brokenWriter{}, cancel)
	log.emit(map[string]string{"kind": "header"})
	if log.failure() == nil || ctx.Err() == nil {
		t.Fatal("recording failure was ignored")
	}
	log.emit("not written")
}

func TestSpareCapacityPolicyOnlySwitchesExistingAPIs(t *testing.T) {
	trace := testTrace()
	backend := newBackend(2, 0, trace)
	ctx := context.WithValue(context.Background(), requestKey{}, &attribution{id: 0})
	selected := ""
	makeInvoke := func(name string) invokeFunc {
		return func(_ context.Context, source string, inputs any) (pysolate.Output, error) {
			if source != "source" || inputs != 42 {
				t.Fatal("changed execution inputs")
			}
			selected = name
			return pysolate.Output{}, nil
		}
	}
	for occupied := 0; occupied <= 2; occupied++ {
		if occupied > 0 {
			backend.slots <- struct{}{}
		}
		for _, arm := range []string{"sequential", "early", "spare-capacity"} {
			invoke := chooseInvoke(arm, backend, trace, makeInvoke("sequential"), makeInvoke("early"))
			_, err := invoke(ctx, "source", 42)
			want := "sequential"
			if arm == "early" || (arm == "spare-capacity" && occupied < 2) {
				want = "early"
			}
			if err != nil || selected != want {
				t.Fatalf("%s occupied=%d: got %s %v", arm, occupied, selected, err)
			}
		}
	}
}

func TestConfigRejectsInvalidModes(t *testing.T) {
	c := config{Arm: "sequential", Requests: 1, Interval: 0, Timeout: time.Second, Inflight: 1, Capacity: 1, Workloads: "pair,short,dependent", Prepare: "copy"}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.DataImage = true
	if c.validate() == nil {
		t.Fatal("data image on copy accepted")
	}
	if _, err := workloads("missing"); err == nil {
		t.Fatal("unknown workload accepted")
	}
}

func TestRealGuestArmsAndCleanup(t *testing.T) {
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = "../../dist/pysolate.wasm"
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("real Guest unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	trace := testTrace()
	backend := newBackend(2, 10*time.Millisecond, trace)
	runner, err := pysolate.NewPrepared(ctx, wasm, pysolate.Manifest{"read": {Call: backend.call, AllowEarlyRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	plan, _ := workloads("pair,short,dependent")
	slots := make(chan struct{}, 2)
	for _, arm := range []string{"sequential", "early", "spare-capacity"} {
		t.Run(arm, func(t *testing.T) {
			invoke := chooseInvoke(arm, backend, trace, runner.Run, runner.RunWithEarlyReads)
			for id, w := range plan {
				row := runRequest(ctx, id, w, time.Now(), 10*time.Second, slots, trace, invoke)
				if row.Status != "ok" || row.Calls != int64(w.Calls) {
					t.Fatalf("%+v", row)
				}
				if arm == "early" && w.Name == "pair" && row.Output.Transformed == "" {
					t.Fatal("early path was not transformed")
				}
			}
		})
	}
	backend.delay = time.Second
	row := runRequest(ctx, 10, plan[0], time.Now(), 100*time.Millisecond, slots, trace, runner.RunWithEarlyReads)
	if row.Status != "timeout" || len(slots) != 0 || backend.snapshot()["end_inflight"] != 0 {
		t.Fatalf("%+v backend=%v", row, backend.snapshot())
	}
}
