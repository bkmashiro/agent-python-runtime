package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

type traceLog struct {
	mu     sync.Mutex
	enc    *json.Encoder
	err    error
	origin time.Time
	cancel context.CancelFunc
}

func newTrace(w io.Writer, cancel context.CancelFunc) *traceLog {
	return &traceLog{enc: json.NewEncoder(w), origin: time.Now(), cancel: cancel}
}
func (t *traceLog) emit(v any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return
	}
	if err := t.enc.Encode(v); err != nil {
		t.err = err
		t.cancel()
	}
}
func (t *traceLog) failure() error { t.mu.Lock(); defer t.mu.Unlock(); return t.err }

type requestKey struct{}
type attribution struct {
	id    int
	calls atomic.Int64
}
type invokeFunc func(context.Context, string, any) (pysolate.Output, error)

// This host-side pilot samples occupancy; it neither reserves capacity nor
// changes the runtime. Every arm records the same decision event.
func chooseInvoke(arm string, backend *toolBackend, trace *traceLog, sequential, early invokeFunc) invokeFunc {
	return func(ctx context.Context, source string, inputs any) (pysolate.Output, error) {
		owner, ok := ctx.Value(requestKey{}).(*attribution)
		if !ok {
			return pysolate.Output{}, errors.New("missing request attribution")
		}
		occupied := len(backend.slots)
		useEarly := arm == "early" || (arm == "spare-capacity" && occupied < cap(backend.slots))
		trace.emit(map[string]any{"kind": "mode_decision", "request_id": owner.id, "early_reads": useEarly, "occupied_tool_slots": occupied})
		if useEarly {
			return early(ctx, source, inputs)
		}
		return sequential(ctx, source, inputs)
	}
}

type requestRow struct {
	Kind         string            `json:"kind"`
	ID           int               `json:"id"`
	Work         string            `json:"work"`
	Status       string            `json:"status"`
	ScheduledNS  int64             `json:"scheduled_ns"`
	DeadlineNS   int64             `json:"deadline_ns"`
	ArrivalLagNS int64             `json:"arrival_lag_ns"`
	LatencyNS    int64             `json:"latency_ns"`
	Admitted     bool              `json:"admitted"`
	Calls        int64             `json:"calls"`
	Output       pysolate.Output   `json:"output"`
	RawIO        *perfdiag.GuestIO `json:"raw_guest_io"`
	Error        string            `json:"error,omitempty"`
}

func runRequest(parent context.Context, id int, work workload, scheduled time.Time, timeout time.Duration, slots chan struct{}, trace *traceLog, invoke invokeFunc) (row requestRow) {
	row = requestRow{Kind: "request_end", ID: id, Work: work.Name, ScheduledNS: scheduled.Sub(trace.origin).Nanoseconds(), ArrivalLagNS: max(0, time.Since(scheduled).Nanoseconds())}
	row.DeadlineNS = row.ScheduledNS + timeout.Nanoseconds()
	ctx, cancel := context.WithDeadline(parent, scheduled.Add(timeout))
	defer cancel()
	owner := &attribution{id: id}
	ctx = context.WithValue(ctx, requestKey{}, owner)
	ctx, row.RawIO = perfdiag.WithGuestIO(ctx)
	trace.emit(map[string]any{"kind": "request_start", "id": id, "work": work.Name, "scheduled_ns": row.ScheduledNS, "arrival_lag_ns": row.ArrivalLagNS})
	defer func() {
		row.Calls = owner.calls.Load()
		// A campaign cancellation can classify an arrival before it was due.
		row.LatencyNS = max(0, time.Since(scheduled).Nanoseconds())
		if row.Status == "ok" && row.LatencyNS > timeout.Nanoseconds() {
			row.Status = "timeout"
		}
		trace.emit(row)
	}()
	if ctx.Err() != nil {
		row.Status = contextStatus(ctx.Err())
		return
	}
	select {
	case slots <- struct{}{}:
		row.Admitted = true
		defer func() { <-slots }()
	default:
		row.Status = "rejected"
		return
	}
	var err error
	row.Output, err = invoke(ctx, work.Source, nil)
	if err != nil {
		row.Error = err.Error()
	}
	switch {
	case ctx.Err() != nil:
		row.Status = contextStatus(ctx.Err())
	case time.Now().After(scheduled.Add(timeout)):
		row.Status = "timeout"
	case err != nil:
		row.Status = "failed"
	case string(row.Output.Value) != work.Expected || owner.calls.Load() != int64(work.Calls):
		row.Status = "incorrect"
	default:
		row.Status = "ok"
	}
	return
}

func contextStatus(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "cancelled"
}

func waitUntil(ctx context.Context, at time.Time) {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func runLoad(ctx context.Context, c config, plan []workload, slots chan struct{}, trace *traceLog, invoke invokeFunc) ([]requestRow, time.Duration) {
	epoch := time.Now()
	trace.emit(map[string]any{"kind": "measured_start", "at_ns": epoch.Sub(trace.origin).Nanoseconds()})
	rows := make([]requestRow, c.Requests)
	var group sync.WaitGroup
	for i := range rows {
		scheduled := epoch.Add(time.Duration(i) * c.Interval)
		waitUntil(ctx, scheduled)
		group.Add(1)
		go func(i int) {
			defer group.Done()
			rows[i] = runRequest(ctx, i, plan[i%len(plan)], scheduled, c.Timeout, slots, trace, invoke)
		}(i)
	}
	group.Wait()
	return rows, time.Since(epoch)
}

func countStatus(rows []requestRow, status string) int {
	n := 0
	for _, r := range rows {
		if r.Status == status {
			n++
		}
	}
	return n
}

// Nearest-rank quantile. Missing observations remain null, never zero.
func percentile(values []int64, fraction float64) *int64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	v := sorted[int(math.Ceil(fraction*float64(len(sorted))))-1]
	return &v
}

func summarize(rows []requestRow) map[string]any {
	counts := map[string]int{"ok": 0, "rejected": 0, "timeout": 0, "cancelled": 0, "failed": 0, "incorrect": 0}
	var completed, lag []int64
	byWork := map[string][]requestRow{}
	calls := int64(0)
	for _, r := range rows {
		counts[r.Status]++
		calls += r.Calls
		lag = append(lag, r.ArrivalLagNS)
		if r.Status == "ok" {
			completed = append(completed, r.LatencyNS)
		}
		byWork[r.Work] = append(byWork[r.Work], r)
	}
	workStats := map[string]any{}
	for name, group := range byWork {
		var latencies []int64
		status := map[string]int{}
		for _, r := range group {
			status[r.Status]++
			if r.Status == "ok" {
				latencies = append(latencies, r.LatencyNS)
			}
		}
		workStats[name] = map[string]any{"scheduled": len(group), "counts": status, "ok_p50_ns": percentile(latencies, .5), "ok_p95_ns": percentile(latencies, .95)}
	}
	return map[string]any{"scheduled": len(rows), "counts": counts, "calls": calls, "ok_p50_ns": percentile(completed, .5), "ok_p95_ns": percentile(completed, .95), "arrival_lag_p95_ns": percentile(lag, .95), "by_work": workStats}
}

type toolRow struct {
	Kind      string          `json:"kind"`
	RequestID int             `json:"request_id"`
	CallID    int64           `json:"call_id"`
	Args      json.RawMessage `json:"args"`
	Acquired  bool            `json:"acquired"`
	QueueNS   int64           `json:"queue_ns"`
	ServiceNS int64           `json:"service_ns"`
	Value     any             `json:"value"`
	Error     string          `json:"error,omitempty"`
}

type toolBackend struct {
	slots  chan struct{}
	delay  time.Duration
	trace  *traceLog
	mu     sync.Mutex
	active int
	peak   int
	rows   []toolRow
}

func newBackend(capacity int, delay time.Duration, trace *traceLog) *toolBackend {
	return &toolBackend{slots: make(chan struct{}, capacity), delay: delay, trace: trace}
}
func (b *toolBackend) call(ctx context.Context, args json.RawMessage) (value any, err error) {
	owner, ok := ctx.Value(requestKey{}).(*attribution)
	if !ok {
		return nil, errors.New("missing request attribution")
	}
	row := toolRow{Kind: "tool_end", RequestID: owner.id, CallID: owner.calls.Add(1), Args: append(json.RawMessage(nil), args...)}
	queued := time.Now()
	b.trace.emit(map[string]any{"kind": "tool_queued", "request_id": row.RequestID, "call_id": row.CallID, "args": row.Args, "at_ns": queued.Sub(b.trace.origin).Nanoseconds()})
	defer func() {
		row.Value = value
		if err != nil {
			row.Error = err.Error()
		}
		b.trace.emit(row)
		if owner.id >= 0 {
			b.mu.Lock()
			b.rows = append(b.rows, row)
			b.mu.Unlock()
		}
	}()
	var input struct {
		Value int `json:"value"`
	}
	if err = json.Unmarshal(args, &input); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		row.QueueNS = time.Since(queued).Nanoseconds()
		return
	}
	select {
	case b.slots <- struct{}{}:
	case <-ctx.Done():
		row.QueueNS = time.Since(queued).Nanoseconds()
		return nil, ctx.Err()
	}
	start := time.Now()
	row.Acquired = true
	row.QueueNS = start.Sub(queued).Nanoseconds()
	b.mu.Lock()
	b.active++
	if b.active > b.peak {
		b.peak = b.active
	}
	b.mu.Unlock()
	defer func() {
		row.ServiceNS = time.Since(start).Nanoseconds()
		b.mu.Lock()
		b.active--
		b.mu.Unlock()
		<-b.slots
	}()
	b.trace.emit(map[string]any{"kind": "tool_started", "request_id": row.RequestID, "call_id": row.CallID, "at_ns": start.Sub(b.trace.origin).Nanoseconds()})
	timer := time.NewTimer(b.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return input.Value, nil
	}
}
func (b *toolBackend) reset() { b.mu.Lock(); defer b.mu.Unlock(); b.rows = nil; b.peak = 0 }
func (b *toolBackend) snapshot() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var waits []int64
	acquired, failures := 0, 0
	for _, r := range b.rows {
		waits = append(waits, r.QueueNS)
		if r.Acquired {
			acquired++
		}
		if r.Error != "" {
			failures++
		}
	}
	return map[string]any{"attempts": len(b.rows), "acquired": acquired, "errors": failures, "queue_p95_ns": percentile(waits, .95), "peak_inflight": b.peak, "end_inflight": b.active}
}
