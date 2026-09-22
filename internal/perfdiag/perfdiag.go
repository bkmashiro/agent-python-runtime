// Package perfdiag contains opt-in, in-repository performance diagnostics.
// It is intentionally internal: runtime callers do not get a public observer API.
package perfdiag

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"sync"
	"time"
)

type contextKey struct{}

// Collector aggregates only phase counts and elapsed nanoseconds. It does not
// retain inputs, results, source, or other request data.
type Collector struct {
	mu     sync.Mutex
	phases map[string]Stats
}

// Stats is one phase aggregate.
type Stats struct {
	Count   uint64 `json:"count"`
	TotalNS int64  `json:"total_ns"`
}

// Entry is a deterministic, serialized phase aggregate.
type Entry struct {
	Phase string `json:"phase"`
	Stats
}

// NewCollector returns an enabled collector. A nil collector disables timing.
func NewCollector() *Collector {
	return &Collector{phases: make(map[string]Stats)}
}

// WithCollector scopes diagnostics to contexts used by the in-repository
// benchmark. It is not part of the root package's public runtime API.
func WithCollector(ctx context.Context, collector *Collector) context.Context {
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, collector)
}

func FromContext(ctx context.Context) *Collector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(contextKey{}).(*Collector)
	return collector
}

// Span is a zero-cost-to-end disabled span when its collector is nil.
type Span struct {
	collector *Collector
	phase     string
	started   time.Time
}

// Start begins a phase only when an opt-in collector is present. The disabled
// path does not read the clock or allocate a map entry.
func Start(ctx context.Context, phase string) Span {
	collector := FromContext(ctx)
	if collector == nil {
		return Span{}
	}
	return Span{collector: collector, phase: phase, started: time.Now()}
}

// End records one phase. Repeated End calls are harmless.
func (span *Span) End() {
	if span == nil || span.collector == nil {
		return
	}
	elapsed := time.Since(span.started).Nanoseconds()
	span.collector.AddNS(span.phase, elapsed)
	span.collector = nil
}

// Add records an already measured duration, useful for the durable tool
// observer whose existing API supplies queue/service/resume durations.
func (collector *Collector) Add(phase string, elapsed time.Duration) {
	if collector == nil || phase == "" {
		return
	}
	collector.AddNS(phase, elapsed.Nanoseconds())
}

// AddNS records an elapsed duration in nanoseconds.
func (collector *Collector) AddNS(phase string, elapsedNS int64) {
	if collector == nil || phase == "" {
		return
	}
	collector.mu.Lock()
	stats := collector.phases[phase]
	stats.Count++
	stats.TotalNS += elapsedNS
	collector.phases[phase] = stats
	collector.mu.Unlock()
}

// Snapshot returns sorted copies suitable for benchmark output.
func (collector *Collector) Snapshot() []Entry {
	if collector == nil {
		return nil
	}
	collector.mu.Lock()
	result := make([]Entry, 0, len(collector.phases))
	for phase, stats := range collector.phases {
		result = append(result, Entry{Phase: phase, Stats: stats})
	}
	collector.mu.Unlock()
	sort.Slice(result, func(left, right int) bool { return result[left].Phase < result[right].Phase })
	return result
}

// Profile controls a CPU profile and/or runtime trace for one explicitly
// bounded interval. The caller chooses whether that interval is setup or
// measured work.
type Profile struct {
	cpuPath   string
	tracePath string
	cpuFile   *os.File
	traceFile *os.File
	cpuOn     bool
	traceOn   bool
}

// Start starts profiles for the caller's current interval.
func StartProfiles(cpuPath, tracePath string) (*Profile, error) {
	profile := &Profile{cpuPath: cpuPath, tracePath: tracePath}
	if cpuPath != "" {
		file, err := os.Create(cpuPath)
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		profile.cpuFile, profile.cpuOn = file, true
	}
	if tracePath != "" {
		file, err := os.Create(tracePath)
		if err != nil {
			_ = profile.Stop()
			return nil, err
		}
		if err := trace.Start(file); err != nil {
			_ = file.Close()
			_ = profile.Stop()
			return nil, err
		}
		profile.traceFile, profile.traceOn = file, true
	}
	return profile, nil
}

// Stop ends the interval and closes its files.
func (profile *Profile) Stop() error {
	if profile == nil {
		return nil
	}
	var result error
	if profile.traceOn {
		trace.Stop()
		profile.traceOn = false
		result = errors.Join(result, profile.traceFile.Close())
	}
	if profile.cpuOn {
		pprof.StopCPUProfile()
		profile.cpuOn = false
		result = errors.Join(result, profile.cpuFile.Close())
	}
	return result
}

// AllocationStats are process counters sampled at the measured boundary. The
// delta is distinct from sampled retained heap (HeapAlloc/HeapInuse).
type AllocationStats struct {
	TotalAlloc uint64 `json:"total_alloc_bytes"`
	Mallocs    uint64 `json:"mallocs"`
	Frees      uint64 `json:"frees"`
	HeapAlloc  uint64 `json:"heap_alloc_bytes"`
	HeapInuse  uint64 `json:"heap_inuse_bytes"`
}

func ReadAllocations() AllocationStats {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return AllocationStats{
		TotalAlloc: mem.TotalAlloc,
		Mallocs:    mem.Mallocs,
		Frees:      mem.Frees,
		HeapAlloc:  mem.HeapAlloc,
		HeapInuse:  mem.HeapInuse,
	}
}

func (start AllocationStats) Delta(end AllocationStats) AllocationStats {
	return AllocationStats{
		TotalAlloc: end.TotalAlloc - start.TotalAlloc,
		Mallocs:    end.Mallocs - start.Mallocs,
		Frees:      end.Frees - start.Frees,
		HeapAlloc:  end.HeapAlloc,
		HeapInuse:  end.HeapInuse,
	}
}

// WriteAllocationDelta writes a small JSON artifact whose counters are scoped
// to the measured interval. It is deliberately not an allocs pprof, which
// would include startup allocations and be misleadingly labeled.
func WriteAllocationDelta(path string, start, end AllocationStats) error {
	if path == "" {
		return nil
	}
	payload := struct {
		Kind      string          `json:"kind"`
		Start     AllocationStats `json:"start"`
		End       AllocationStats `json:"end"`
		Measured  AllocationStats `json:"measured_delta"`
		HeapScope string          `json:"heap_scope"`
	}{
		Kind:      "measured_allocation_delta",
		Start:     start,
		End:       end,
		Measured:  start.Delta(end),
		HeapScope: "end_of_measured_interval_retained_heap",
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}

// WriteAllocationSnapshot writes a cumulative sampled allocation profile.
// Subtract the before profile from the after profile with pprof -base.
// GC flushes sampling records and must run outside the measured interval.
func WriteAllocationSnapshot(path string) error {
	if path == "" {
		return nil
	}
	runtime.GC()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = pprof.Lookup("allocs").WriteTo(f, 0)
	return errors.Join(err, f.Close())
}

// WritePhases serializes phase aggregates to a JSON artifact.
func WritePhases(path string, entries []Entry) error {
	if path == "" {
		return nil
	}
	encoded, err := json.MarshalIndent(struct {
		Kind   string  `json:"kind"`
		Phases []Entry `json:"phases"`
	}{Kind: "pysolate_phase_dissection", Phases: entries}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}
