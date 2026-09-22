// Compare real durable attempts under explicit admission; the Host delay is synthetic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

type queueCaseSpec struct {
	name     string
	code     string
	wantPark bool
}

type phaseMeasurement struct {
	elapsed     time.Duration
	latencies   []int64
	completed   int
	errors      int
	resultCount int
	dispatches  int
}

type memorySnapshot struct {
	RSSKB          int64 `json:"rss_kib,omitempty"`
	PSSKB          int64 `json:"pss_kib,omitempty"`
	PrivateDirtyKB int64 `json:"private_dirty_kib,omitempty"`
}

type memorySampler struct {
	peakRSS          atomic.Int64
	peakPSS          atomic.Int64
	peakPrivateDirty atomic.Int64
	mu               sync.Mutex
	stop             chan struct{}
	done             chan struct{}
}

func newMemorySampler(interval time.Duration) *memorySampler {
	s := &memorySampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		s.sample()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.sample()
			case <-s.stop:
				return
			}
		}
	}()
	return s
}

func (s *memorySampler) sample() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := readMemory()
	updatePeak(&s.peakRSS, current.RSSKB)
	updatePeak(&s.peakPSS, current.PSSKB)
	updatePeak(&s.peakPrivateDirty, current.PrivateDirtyKB)
}

func (s *memorySampler) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peakRSS.Store(0)
	s.peakPSS.Store(0)
	s.peakPrivateDirty.Store(0)
}

func (s *memorySampler) close() {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}

func (s *memorySampler) snapshot() memorySnapshot {
	if s == nil {
		return memorySnapshot{}
	}
	return memorySnapshot{RSSKB: s.peakRSS.Load(), PSSKB: s.peakPSS.Load(), PrivateDirtyKB: s.peakPrivateDirty.Load()}
}

func updatePeak(peak *atomic.Int64, value int64) {
	for old := peak.Load(); value > old && !peak.CompareAndSwap(old, value); old = peak.Load() {
	}
}

func parseMemoryRollup(raw []byte) memorySnapshot {
	var snapshot memorySnapshot
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "Rss:":
			snapshot.RSSKB = value
		case "Pss:":
			snapshot.PSSKB = value
		case "Private_Dirty:":
			snapshot.PrivateDirtyKB = value
		}
	}
	return snapshot
}

func readMemory() memorySnapshot {
	raw, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return memorySnapshot{}
	}
	return parseMemoryRollup(raw)
}

func nullableMemory(snapshot memorySnapshot) any {
	if snapshot.RSSKB <= 0 && snapshot.PSSKB <= 0 && snapshot.PrivateDirtyKB <= 0 {
		return nil
	}
	return snapshot
}

func nullableMetric(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest")
	caseName := flag.String("case", "park-readmit", "park-readmit or read-finish")
	mode := flag.String("mode", "executor", "executor/semaphore/unbounded")
	tasks := flag.Int("tasks", 16, "logical Runs")
	active := flag.Int("active", 2, "running Guest bound")
	resident := flag.Int("resident", 0, "resident Guest bound; defaults to active")
	toolActive := flag.Int("tool-active", 0, "in-flight external Tool bound; defaults to resident")
	externalIO := flag.Bool("external-io", false, "let the opted-in probe yield its running slot")
	heap := flag.Int("heap", 8, "private allocation MiB per Guest")
	hold := flag.Duration("hold", 200*time.Millisecond, "synthetic Host wait")
	cow := flag.Bool("cow", true, "use seeded COW, false selects copy")
	warmup := flag.Int("warmup", 0, "completed unmeasured Runs before measured Run creation")
	profile := flag.String("cpuprofile", "", "diagnostic CPU profile, includes construction")
	measuredCPU := flag.String("measured-cpuprofile", "", "CPU profile scoped to measured phase(s) only")
	tracePath := flag.String("traceprofile", "", "runtime trace scoped to measured phase(s) only")
	allocPath := flag.String("allocprofile", "", "JSON allocation-counter delta scoped to measured phase(s)")
	phasePath := flag.String("phases", "", "JSON phase counts and elapsed nanoseconds for measured phase(s)")
	flag.Parse()
	if *resident == 0 {
		*resident = *active
	}
	if *toolActive == 0 {
		*toolActive = *resident
	}
	if *tasks < 1 || *tasks > 64 || *active < 1 || *resident < *active || *toolActive < 1 || *heap < 0 || *heap > 64 || *warmup < 0 || *warmup > 16 {
		return errors.New("invalid bounded fixture")
	}
	if *mode != "executor" && *mode != "semaphore" && *mode != "unbounded" {
		return errors.New("unknown mode")
	}
	if *profile != "" && *measuredCPU != "" {
		return errors.New("choose legacy -cpuprofile or -measured-cpuprofile, not both")
	}
	fullProfile, err := perfdiag.StartProfiles(*profile, "")
	if err != nil {
		return err
	}
	defer func() { _ = fullProfile.Stop() }()
	spec, err := selectQueueCase(*caseName, *heap)
	if err != nil {
		return err
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pysolate-queue-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	store, err := durable.Open(filepath.Join(dir, "runs.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	var held, peak atomic.Int32
	var toolDispatches atomic.Int32
	var peakRunning, peakResident, peakInflightTools atomic.Int64
	startupMemory := readMemory()
	memory := newMemorySampler(10 * time.Millisecond)
	defer memory.close()
	var collector *perfdiag.Collector
	if *measuredCPU != "" || *tracePath != "" || *allocPath != "" || *phasePath != "" {
		collector = perfdiag.NewCollector()
	}
	collecting := atomic.Bool{}
	toolObserver := durable.ToolObserverFunc(func(observation durable.ToolObservation) {
		if !collecting.Load() {
			return
		}
		collector.Add("tool.queue", observation.QueueDuration)
		collector.Add("tool.service", observation.ServiceDuration)
		collector.Add("tool.resume", observation.ResumeDuration)
	})
	scheduling := durable.Inline
	if *externalIO {
		scheduling = durable.ExternalIO
	}
	var executor *durable.Executor
	sample := func() {
		memory.sample()
		if executor != nil {
			stats := executor.Stats()
			updatePeak(&peakRunning, int64(stats.Running))
			updatePeak(&peakResident, int64(stats.Resident))
			updatePeak(&peakInflightTools, int64(stats.InflightTools))
		}
	}
	tools := []durable.Tool{
		{Name: "probe", Version: "v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Observer: toolObserver, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			toolDispatches.Add(1)
			n := held.Add(1)
			defer held.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			sample()
			timer := time.NewTimer(*hold)
			defer timer.Stop()
			select {
			case <-timer.C:
				return map[string]any{"value": 41}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}},
		{Name: "approve", Version: "v1", Recovery: durable.WaitMode, Wait: func(context.Context, json.RawMessage) (durable.WaitSpec, error) {
			return durable.WaitSpec{Kind: "approval", Request: json.RawMessage(`{}`)}, nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	runner, err := durable.NewRunner(ctx, store, wasm, "bench-v1", tools, durable.Preparation{Seed: "bench-seed", COW: *cow})
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	create := func(id string, inputID int) error {
		input, marshalErr := json.Marshal(map[string]int{"id": inputID})
		if marshalErr != nil {
			return marshalErr
		}
		_, createErr := runner.Create(ctx, durable.Definition{ID: id, Code: spec.code, Inputs: input, Seed: "bench-seed", ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "bench-v1"})
		return createErr
	}
	warmupStarted := time.Now()
	for i := 0; i < *warmup; i++ {
		id := fmt.Sprintf("warmup-%d", i)
		if err := create(id, i); err != nil {
			return err
		}
		out, resumeErr := runner.Resume(ctx, id)
		if spec.wantPark {
			if !errors.Is(resumeErr, durable.ErrParked) {
				return fmt.Errorf("warm-up park: %w", resumeErr)
			}
			if err := runner.Decide(ctx, id+"/wait/1", durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
				return err
			}
			out, err := runner.Resume(ctx, id)
			if err != nil || string(out.Value) != strconv.Itoa(i) {
				return fmt.Errorf("warm-up resume result=%s error=%v", out.Value, err)
			}
		} else if resumeErr != nil || string(out.Value) != strconv.Itoa(i) {
			return fmt.Errorf("warm-up result=%s error=%v", out.Value, resumeErr)
		}
	}
	warmupNS := time.Since(warmupStarted)
	setup := time.Since(started)
	ids := make([]string, *tasks)
	for i := range ids {
		ids[i] = fmt.Sprintf("task-%d", i)
		if err := create(ids[i], i); err != nil {
			return err
		}
	}
	if *mode == "executor" {
		executor, err = durable.NewExecutor(runner, durable.Limits{MaxRunning: *active, MaxResident: *resident, MaxInflightTools: *toolActive, MaxQueued: *tasks})
		if err != nil {
			return err
		}
		defer executor.Close(context.Background())
	}
	preMeasuredBatchMemory := readMemory()
	setupPeakMemory := memory.snapshot()
	memory.reset()
	toolDispatches.Store(0)
	if *allocPath != "" {
		if err := perfdiag.WriteAllocationSnapshot(*allocPath + ".before.pprof"); err != nil {
			return err
		}
	}
	measuredProfile, err := perfdiag.StartProfiles(*measuredCPU, *tracePath)
	if err != nil {
		return err
	}
	allocationStart := perfdiag.ReadAllocations()
	collecting.Store(collector != nil)
	stopMeasured := func() error {
		if measuredProfile == nil {
			return nil
		}
		collecting.Store(false)
		allocationEnd := perfdiag.ReadAllocations()
		profileErr := measuredProfile.Stop()
		if *allocPath != "" {
			profileErr = errors.Join(profileErr, perfdiag.WriteAllocationSnapshot(*allocPath+".after.pprof"))
		}
		measuredProfile = nil
		if err := perfdiag.WriteAllocationDelta(*allocPath, allocationStart, allocationEnd); err != nil {
			profileErr = errors.Join(profileErr, err)
		}
		if err := perfdiag.WritePhases(*phasePath, collector.Snapshot()); err != nil {
			profileErr = errors.Join(profileErr, err)
		}
		return profileErr
	}
	defer func() { _ = stopMeasured() }()
	var phaseLatencies [][]int64
	phase := func(wantPark bool) (phaseMeasurement, error) {
		begin := time.Now()
		latencies := make([]int64, len(ids))
		var wg sync.WaitGroup
		var completed, resultCount, phaseErrors atomic.Int64
		limit := *active
		if *mode == "unbounded" {
			limit = *tasks
		}
		phaseCtx := perfdiag.WithCollector(ctx, collector)
		slots := make(chan struct{}, limit)
		for i, id := range ids {
			submitted := time.Now()
			var attempt *durable.Attempt
			if executor != nil {
				attempt, err = executor.Submit(phaseCtx, id)
				if err != nil {
					return phaseMeasurement{}, err
				}
			}
			wg.Add(1)
			go func(i int, id string, attempt *durable.Attempt, submitted time.Time) {
				defer wg.Done()
				defer func() { latencies[i] = time.Since(submitted).Nanoseconds() }()
				var value []byte
				var e error
				if attempt != nil {
					out, x := attempt.Wait(phaseCtx)
					value, e = out.Value, x
				} else {
					select {
					case slots <- struct{}{}:
					case <-phaseCtx.Done():
						phaseErrors.Add(1)
						return
					}
					out, x := runner.Resume(phaseCtx, id)
					<-slots
					value, e = out.Value, x
				}
				resultCount.Add(1)
				valid := false
				if wantPark {
					valid = errors.Is(e, durable.ErrParked)
				} else {
					valid = e == nil && string(value) == strconv.Itoa(i)
				}
				if valid {
					completed.Add(1)
				} else {
					phaseErrors.Add(1)
				}
			}(i, id, attempt, submitted)
		}
		wg.Wait()
		sample()
		measurement := phaseMeasurement{elapsed: time.Since(begin), latencies: latencies, completed: int(completed.Load()), errors: int(phaseErrors.Load()), resultCount: int(resultCount.Load()), dispatches: int(toolDispatches.Load())}
		phaseLatencies = append(phaseLatencies, measurement.latencies)
		if measurement.completed+measurement.errors != *tasks || measurement.resultCount != *tasks {
			return measurement, fmt.Errorf("phase accounting completed=%d errors=%d results=%d tasks=%d", measurement.completed, measurement.errors, measurement.resultCount, *tasks)
		}
		if spec.name == "read-finish" && measurement.dispatches != *tasks {
			return measurement, fmt.Errorf("tool dispatch accounting dispatches=%d want=%d", measurement.dispatches, *tasks)
		}
		return measurement, nil
	}
	if !spec.wantPark {
		batch, err := phase(false)
		if err != nil {
			return err
		}
		sample()
		measuredPeakMemory := memory.snapshot()
		afterBatchMemory := readMemory()
		if *mode != "unbounded" {
			waitLimit := int64(*active)
			if *mode == "executor" && *externalIO {
				waitLimit = int64(*toolActive)
			}
			if int64(peak.Load()) > waitLimit || peakRunning.Load() > int64(*active) || peakResident.Load() > int64(*resident) || peakInflightTools.Load() > int64(*toolActive) {
				return errors.New("configured concurrency bound exceeded")
			}
		}
		if err := stopMeasured(); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"case": spec.name, "mode": *mode, "tasks": *tasks,
			"running_limit": *active, "resident_limit": *resident, "tool_limit": *toolActive,
			"external_io": *externalIO, "heap_mib": *heap, "synthetic_hold_ns": hold.Nanoseconds(),
			"cow": *cow, "preparation": map[bool]string{true: "cow", false: "copy"}[*cow],
			"gomaxprocs": runtime.GOMAXPROCS(0), "gomaxprocs_source": "runtime.GOMAXPROCS(0)", "num_cpu": runtime.NumCPU(),
			"warmup": *warmup, "warmup_ns": warmupNS.Nanoseconds(), "setup_ns": setup.Nanoseconds(), "batch_ns": batch.elapsed.Nanoseconds(), "request_ns": phaseLatencies[0],
			"startup_memory": nullableMemory(startupMemory), "sampled_setup_peak_memory": nullableMemory(setupPeakMemory), "pre_measured_batch_memory": nullableMemory(preMeasuredBatchMemory), "after_batch_memory": nullableMemory(afterBatchMemory),
			"sampled_peak_memory": nullableMemory(measuredPeakMemory), "sampled_peak_scope": "measured_batch", "memory_sample_interval_ns": int64(10 * time.Millisecond),
			"sampled_peak_rss_kib": nullableMetric(measuredPeakMemory.RSSKB), "sampled_peak_pss_kib": nullableMetric(measuredPeakMemory.PSSKB), "sampled_peak_private_dirty_kib": nullableMetric(measuredPeakMemory.PrivateDirtyKB),
			"peak_host_waits": peak.Load(), "peak_executor_running": peakRunning.Load(), "peak_executor_resident": peakResident.Load(), "peak_executor_inflight_tools": peakInflightTools.Load(),
			"completed": batch.completed, "errors": batch.errors, "result_count": batch.resultCount, "tool_dispatches": batch.dispatches,
		})
	}

	parked, err := phase(true)
	if err != nil {
		return err
	}
	afterPark := readMemory()
	sample()
	for _, id := range ids {
		decideCtx := perfdiag.WithCollector(ctx, collector)
		if err := runner.Decide(decideCtx, id+"/wait/1", durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
			return err
		}
	}
	resumed, err := phase(false)
	if err != nil {
		return err
	}
	peakLimit := *active
	if *mode == "executor" && *externalIO {
		peakLimit = *toolActive
	}
	if *mode != "unbounded" && (int(peak.Load()) > peakLimit || peakRunning.Load() > int64(*active) || peakResident.Load() > int64(*resident) || peakInflightTools.Load() > int64(*toolActive)) {
		return errors.New("configured concurrency bound exceeded")
	}
	if err := stopMeasured(); err != nil {
		return err
	}
	measuredPeakMemory := memory.snapshot()
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"case": spec.name, "mode": *mode, "tasks": *tasks, "running_limit": *active, "resident_limit": *resident, "tool_limit": *toolActive, "external_io": *externalIO, "heap_mib": *heap, "synthetic_hold_ns": hold.Nanoseconds(), "cow": *cow, "preparation": map[bool]string{true: "cow", false: "copy"}[*cow], "gomaxprocs": runtime.GOMAXPROCS(0), "gomaxprocs_source": "runtime.GOMAXPROCS(0)", "num_cpu": runtime.NumCPU(), "warmup": *warmup, "warmup_ns": warmupNS.Nanoseconds(), "setup_ns": setup.Nanoseconds(), "park_batch_ns": parked.elapsed.Nanoseconds(), "resume_batch_ns": resumed.elapsed.Nanoseconds(), "peak_host_waits": peak.Load(), "peak_executor_running": peakRunning.Load(), "peak_executor_resident": peakResident.Load(), "peak_executor_inflight_tools": peakInflightTools.Load(), "sampled_peak_rss_kib": nullableMetric(measuredPeakMemory.RSSKB), "sampled_peak_pss_kib": nullableMetric(measuredPeakMemory.PSSKB), "sampled_peak_private_dirty_kib": nullableMetric(measuredPeakMemory.PrivateDirtyKB), "sampled_peak_memory": nullableMemory(measuredPeakMemory), "sampled_peak_scope": "measured_batch_and_readmit", "memory_sample_interval_ns": int64(10 * time.Millisecond), "startup_memory": nullableMemory(startupMemory), "sampled_setup_peak_memory": nullableMemory(setupPeakMemory), "pre_measured_batch_memory": nullableMemory(preMeasuredBatchMemory), "after_park_memory": nullableMemory(afterPark), "after_park_rss_kib": nullableMetric(afterPark.RSSKB), "park_request_ns": phaseLatencies[0], "resume_request_ns": phaseLatencies[1], "park_completed": parked.completed, "resume_completed": resumed.completed, "completed": resumed.completed, "errors": parked.errors + resumed.errors, "result_count": resumed.resultCount, "tool_dispatches": resumed.dispatches, "park_tool_dispatches": parked.dispatches, "resume_tool_dispatches": resumed.dispatches - parked.dispatches})
}

func selectQueueCase(name string, heapMiB int) (queueCaseSpec, error) {
	prefix := fmt.Sprintf("scratch = bytearray(%d)\n", heapMiB<<20)
	switch name {
	case "park-readmit":
		return queueCaseSpec{name: name, wantPark: true, code: prefix + "probe()\napprove()\nresult = inputs['id']"}, nil
	case "read-finish":
		return queueCaseSpec{name: name, code: prefix + "record = probe()\nresult = inputs['id'] if record['value'] == 41 else -1"}, nil
	default:
		return queueCaseSpec{}, fmt.Errorf("unknown queue case %q", name)
	}
}

func nullableRSS(value int64) any {
	return nullableMetric(value)
}

func rss() int64 {
	return readMemory().RSSKB
}
