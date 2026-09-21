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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest")
	mode := flag.String("mode", "executor", "executor/semaphore/unbounded")
	tasks := flag.Int("tasks", 16, "logical Runs")
	active := flag.Int("active", 2, "running Guest bound")
	resident := flag.Int("resident", 0, "resident Guest bound; defaults to active")
	toolActive := flag.Int("tool-active", 0, "in-flight external Tool bound; defaults to resident")
	externalIO := flag.Bool("external-io", false, "let the opted-in probe yield its running slot")
	heap := flag.Int("heap", 8, "private allocation MiB per Guest")
	hold := flag.Duration("hold", 200*time.Millisecond, "synthetic Host wait")
	cow := flag.Bool("cow", true, "use seeded COW, false selects copy")
	flag.Parse()
	if *resident == 0 {
		*resident = *active
	}
	if *toolActive == 0 {
		*toolActive = *resident
	}
	if *tasks < 1 || *tasks > 64 || *active < 1 || *resident < *active || *toolActive < 1 || *heap < 0 || *heap > 64 {
		return errors.New("invalid bounded fixture")
	}
	if *mode != "executor" && *mode != "semaphore" && *mode != "unbounded" {
		return errors.New("unknown mode")
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
	var peakRSS atomic.Int64
	scheduling := durable.Inline
	if *externalIO {
		scheduling = durable.ExternalIO
	}
	sample := func() {
		v := rss()
		for old := peakRSS.Load(); v > old && !peakRSS.CompareAndSwap(old, v); old = peakRSS.Load() {
		}
	}
	tools := []durable.Tool{
		{Name: "probe", Version: "v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			n := held.Add(1)
			defer held.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			sample()
			timer := time.NewTimer(*hold)
			defer timer.Stop()
			select {
			case <-timer.C:
				return true, nil
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
	setup := time.Since(started)
	ids := make([]string, *tasks)
	for i := range ids {
		ids[i] = fmt.Sprintf("task-%d", i)
		input, _ := json.Marshal(map[string]int{"id": i})
		_, err = runner.Create(ctx, durable.Definition{ID: ids[i], Code: fmt.Sprintf("scratch = bytearray(%d)\nprobe()\napprove()\nresult = inputs['id']", *heap<<20), Inputs: input, Seed: "bench-seed", ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "bench-v1"})
		if err != nil {
			return err
		}
	}
	var executor *durable.Executor
	if *mode == "executor" {
		executor, err = durable.NewExecutor(runner, durable.Limits{MaxRunning: *active, MaxResident: *resident, MaxInflightTools: *toolActive, MaxQueued: *tasks})
		if err != nil {
			return err
		}
		defer executor.Close(context.Background())
	}
	var phaseLatencies [][]int64
	phase := func(wantPark bool) (time.Duration, error) {
		begin := time.Now()
		latencies := make([]int64, len(ids))
		errs := make(chan error, len(ids))
		var wg sync.WaitGroup
		limit := *active
		if *mode == "unbounded" {
			limit = *tasks
		}
		slots := make(chan struct{}, limit)
		for i, id := range ids {
			submitted := time.Now()
			var attempt *durable.Attempt
			if executor != nil {
				attempt, err = executor.Submit(ctx, id)
				if err != nil {
					return 0, err
				}
			}
			wg.Add(1)
			go func(i int, id string, attempt *durable.Attempt) {
				defer wg.Done()
				defer func() { latencies[i] = time.Since(submitted).Nanoseconds() }()
				var value []byte
				var e error
				if attempt != nil {
					out, x := attempt.Wait(ctx)
					value, e = out.Value, x
				} else {
					select {
					case slots <- struct{}{}:
					case <-ctx.Done():
						errs <- ctx.Err()
						return
					}
					out, x := runner.Resume(ctx, id)
					<-slots
					value, e = out.Value, x
				}
				if wantPark {
					if !errors.Is(e, durable.ErrParked) {
						errs <- fmt.Errorf("expected park, got %v", e)
					}
				} else if e != nil || string(value) != strconv.Itoa(i) {
					errs <- fmt.Errorf("result=%s error=%v", value, e)
				}
			}(i, id, attempt)
		}
		wg.Wait()
		elapsed := time.Since(begin)
		phaseLatencies = append(phaseLatencies, latencies)
		close(errs)
		for e := range errs {
			return elapsed, e
		}
		return elapsed, nil
	}
	parked, err := phase(true)
	if err != nil {
		return err
	}
	afterPark := rss()
	sample()
	for _, id := range ids {
		if err := runner.Decide(ctx, id+"/wait/1", durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
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
	if *mode != "unbounded" && int(peak.Load()) > peakLimit {
		return errors.New("configured concurrency bound exceeded")
	}
	var peakMem, parkedMem any
	if peakRSS.Load() > 0 {
		peakMem = peakRSS.Load()
		parkedMem = afterPark
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "tasks": *tasks, "running_limit": *active, "resident_limit": *resident, "tool_limit": *toolActive, "external_io": *externalIO, "heap_mib": *heap, "synthetic_hold_ns": hold.Nanoseconds(), "setup_ns": setup.Nanoseconds(), "park_batch_ns": parked.Nanoseconds(), "resume_batch_ns": resumed.Nanoseconds(), "peak_host_waits": peak.Load(), "sampled_peak_rss_kib": peakMem, "after_park_rss_kib": parkedMem, "park_request_ns": phaseLatencies[0], "resume_request_ns": phaseLatencies[1], "completed": *tasks})
}
func rss() int64 {
	raw, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "VmRSS:" {
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			return v
		}
	}
	return 0
}
