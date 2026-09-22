// Command pysolate-bench measures existing execution APIs; delays are synthetic.
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
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
	"github.com/tetratelabs/wazero"
)

type callKey struct{}
type row struct {
	Kind   string `json:"kind"`
	Mode   string `json:"mode"`
	Work   string `json:"work"`
	Round  int    `json:"round"`
	Worker int    `json:"worker"`
	Nanos  int64  `json:"ns"`
	Calls  int32  `json:"physical_calls"`
	RSSKB  int64  `json:"rss_kib,omitempty"`
	PSSKB  int64  `json:"pss_kib,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	prepared := flag.String("prepare", "fresh", "recorded/durable image: fresh/copy/cow")
	mode := flag.String("mode", "fresh", "fresh/copy/cow/recorded/durable-live/durable-replay")
	work := flag.String("work", "python", "python/numpy/tools/early-reads")
	n := flag.Int("n", 5, "measured rounds")
	concurrency := flag.Int("concurrency", 1, "requests per round")
	calls := flag.Int("calls", 8, "tool calls per request")
	payload := flag.Int("payload", 0, "synthetic response body bytes")
	delay := flag.Duration("delay", 0, "synthetic Host delay per call")
	cacheDir := flag.String("cache", "", "optional private native compilation cache")
	profile := flag.String("cpuprofile", "", "diagnostic CPU profile, includes construction")
	flag.Parse()
	if *n < 1 || *concurrency < 1 || *calls < 0 || *calls > 512 || *payload < 0 || *payload > 900000 {
		return errors.New("invalid workload size")
	}
	if *profile != "" {
		f, err := os.Create(*profile)
		if err != nil {
			return err
		}
		defer f.Close()
		if err = pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	source, expected, err := program(*work, *calls)
	if err != nil {
		return err
	}
	body := strings.Repeat("x", *payload)
	tool := func(ctx context.Context, args json.RawMessage) (any, error) {
		if counter, ok := ctx.Value(callKey{}).(*atomic.Int32); ok {
			counter.Add(1)
		}
		if *delay > 0 {
			timer := time.NewTimer(*delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var a struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"value": a.Value, "body": body}, nil
	}
	manifest := pysolate.Manifest{"read": {Call: tool, AllowEarlyRead: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if *cacheDir != "" {
		cache, e := wazero.NewCompilationCacheWithDir(*cacheDir)
		if e != nil {
			return e
		}
		defer cache.Close(context.Background())
		ctx = pysolate.WithCompilationCache(ctx, cache)
	}

	var invoke func(context.Context, int, int) (pysolate.Output, error)
	var closeRunner func() error
	historySeedNS := int64(0)
	setup := time.Now()
	switch *mode {
	case "fresh", "copy", "cow", "recorded":
		var core *pysolate.Runner
		switch *mode {
		case "copy":
			core, err = pysolate.NewPrepared(ctx, wasm, manifest)
		case "cow":
			core, err = pysolate.NewPreparedCOW(ctx, wasm, manifest)
		default:
			if *mode == "recorded" && *prepared != "fresh" {
				if *prepared == "copy" {
					core, err = pysolate.NewPreparedRecorded(ctx, wasm, manifest, "bench-seed")
				} else if *prepared == "cow" {
					core, err = pysolate.NewPreparedRecordedCOW(ctx, wasm, manifest, "bench-seed")
				} else {
					return errors.New("unknown preparation")
				}
			} else {
				core, err = pysolate.New(ctx, wasm, manifest)
			}
		}
		if err != nil {
			return err
		}
		closeRunner = func() error { return core.Close(context.Background()) }
		invoke = func(ctx context.Context, _, _ int) (pysolate.Output, error) {
			if *mode == "recorded" {
				if *work == "early-reads" {
					return pysolate.Output{}, errors.New("recorded execution excludes early-reads")
				}
				return core.RunRecorded(ctx, source, nil, "bench-seed", passJournal{})
			}
			switch *work {
			case "early-reads":
				return core.RunWithEarlyReads(ctx, source, nil)
			default:
				return core.Run(ctx, source, nil)
			}
		}
	case "durable-live", "durable-replay":
		if *work != "tools" {
			return errors.New("durable measurements use -work tools")
		}
		dir, e := os.MkdirTemp("", "pysolate-bench-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(dir)
		store, e := durable.Open(filepath.Join(dir, "runs.db"))
		if e != nil {
			return e
		}
		defer store.Close()
		var preparation []durable.Preparation
		if *prepared != "fresh" {
			if *prepared != "copy" && *prepared != "cow" {
				return errors.New("unknown preparation")
			}
			preparation = []durable.Preparation{{Seed: "bench-seed", COW: *prepared == "cow"}}
		}
		dr, e := durable.NewRunner(ctx, store, wasm, "bench-v1", []durable.Tool{
			{Name: "read", Version: "v1", Recovery: durable.RetrySafe, Call: tool},
			{Name: "wait", Version: "v1", Recovery: durable.WaitMode, Wait: func(context.Context, json.RawMessage) (durable.WaitSpec, error) {
				return durable.WaitSpec{Kind: "approval", Request: json.RawMessage(`{}`)}, nil
			}},
		}, preparation...)
		if e != nil {
			return e
		}
		closeRunner = func() error { return dr.Close(context.Background()) }
		code := source + "\nassert result == " + expected + "\nwait()\n"
		create := func(ctx context.Context, id string) error {
			_, e := dr.Create(ctx, durable.Definition{ID: id, Code: code, Seed: "bench-seed", Inputs: json.RawMessage(`null`), ArtifactSHA256: dr.ArtifactID(), EnvironmentVersion: "bench-v1"})
			return e
		}
		if *mode == "durable-replay" {
			seedStart := time.Now()
			for worker := 0; worker < *concurrency; worker++ {
				id := fmt.Sprintf("replay-%d", worker)
				if e = create(ctx, id); e != nil {
					return e
				}
				if _, e = dr.Resume(ctx, id); !errors.Is(e, durable.ErrParked) {
					return fmt.Errorf("seed history: %w", e)
				}
			}
			historySeedNS = time.Since(seedStart).Nanoseconds()
		}
		invoke = func(ctx context.Context, round, worker int) (pysolate.Output, error) {
			id := fmt.Sprintf("replay-%d", worker)
			if *mode == "durable-live" {
				id = fmt.Sprintf("live-%d-%d", round, worker)
				if e := create(ctx, id); e != nil {
					return pysolate.Output{}, e
				}
			}
			out, e := dr.Resume(ctx, id)
			if errors.Is(e, durable.ErrParked) {
				return out, nil
			}
			return out, fmt.Errorf("expected parked attempt: %w", e)
		}
	default:
		return errors.New("unknown mode")
	}
	setupNS := time.Since(setup).Nanoseconds()
	defer func() { _ = closeRunner() }()
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(map[string]any{"kind": "environment", "mode": *mode, "work": *work, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "concurrency": *concurrency, "rounds": *n, "calls": *calls, "payload": *payload, "synthetic_delay_ns": delay.Nanoseconds(), "prepare": *prepared, "setup_ns": setupNS - historySeedNS, "history_seed_ns": historySeedNS, "profiled": *profile != ""}); err != nil {
		return err
	}
	// One excluded warm-up per worker. Durable replay histories were seeded above.
	for worker := 0; worker < *concurrency; worker++ {
		out, e := invoke(ctx, -1, worker)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(*mode, "durable-") && string(out.Value) != expected {
			return fmt.Errorf("warm-up result=%s want=%s", out.Value, expected)
		}
	}
	failed := false
	for round := 0; round < *n; round++ {
		rows := make([]row, *concurrency)
		var group sync.WaitGroup
		started := time.Now()
		for worker := range rows {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				var counter atomic.Int32
				requestCtx := context.WithValue(ctx, callKey{}, &counter)
				start := time.Now()
				out, e := invoke(requestCtx, round, worker)
				elapsed := time.Since(start).Nanoseconds()
				if e == nil && !strings.HasPrefix(*mode, "durable-") && string(out.Value) != expected {
					e = fmt.Errorf("result=%s want=%s", out.Value, expected)
				}
				want := int32(0)
				if *work == "tools" || *work == "early-reads" {
					want = int32(*calls)
				}
				if *mode == "durable-replay" {
					want = 0
				}
				if e == nil && counter.Load() != want {
					e = fmt.Errorf("dispatches=%d want=%d", counter.Load(), want)
				}
				rows[worker] = row{Kind: "run", Mode: *mode, Work: *work, Round: round, Worker: worker, Nanos: elapsed, Calls: counter.Load()}
				if e != nil {
					rows[worker].Error = e.Error()
				}
			}(worker)
		}
		group.Wait()
		batchNS := time.Since(started).Nanoseconds()
		for _, value := range rows {
			if value.Error != "" {
				failed = true
			}
			if err := enc.Encode(value); err != nil {
				return err
			}
		}
		rss, pss := memory()
		if err := enc.Encode(row{Kind: "batch", Mode: *mode, Work: *work, Round: round, Nanos: batchNS, RSSKB: rss, PSSKB: pss}); err != nil {
			return err
		}
	}
	start := time.Now()
	err = closeRunner()
	closeRunner = func() error { return nil }
	closeNS := time.Since(start).Nanoseconds()
	rss, pss := memory()
	_ = enc.Encode(row{Kind: "close", Mode: *mode, Work: *work, Nanos: closeNS, RSSKB: rss, PSSKB: pss})
	if err != nil {
		return err
	}
	if failed {
		return errors.New("one or more scheduled requests failed")
	}
	return nil
}

type passJournal struct{}

func (passJournal) Call(ctx context.Context, _ string, _ json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
	return next(ctx), nil
}
func program(work string, calls int) (string, string, error) {
	switch work {
	case "python":
		return "result = sum(i*i for i in range(1000))", "332833500", nil
	case "numpy":
		return "import numpy as np\na=np.arange(10000,dtype=np.int64)\nresult=int(a.sum())", "49995000", nil
	case "tools", "early-reads":
		var b strings.Builder
		for i := 0; i < calls; i++ {
			fmt.Fprintf(&b, "x%d = read(value=%d)\n", i, i)
		}
		b.WriteString("result = 0")
		for i := 0; i < calls; i++ {
			fmt.Fprintf(&b, " + x%d['value']", i)
		}
		b.WriteString("\n")
		return b.String(), strconv.Itoa(calls * (calls - 1) / 2), nil
	}
	return "", "", errors.New("unknown workload")
}
func memory() (rss, pss int64) {
	data, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "Rss:":
			rss = v
		case "Pss:":
			pss = v
		}
	}
	return
}
