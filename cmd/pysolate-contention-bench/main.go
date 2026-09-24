// Command pysolate-contention-bench compares real Guest execution under open-loop
// arrivals and a bounded synthetic tool backend. It never calls a model.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type config struct {
	Arm       string        `json:"arm"`
	Requests  int           `json:"requests"`
	Interval  time.Duration `json:"interval_ns"`
	Timeout   time.Duration `json:"timeout_ns"`
	Inflight  int           `json:"max_inflight_requests"`
	Capacity  int           `json:"tool_capacity"`
	Delay     time.Duration `json:"synthetic_tool_delay_ns"`
	Workloads string        `json:"workloads"`
	Prepare   string        `json:"prepare"`
	DataImage bool          `json:"cow_data_image"`
}

type workload struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Expected string `json:"expected_json"`
	Calls    int    `json:"expected_calls_on_success"`
}

func workloads(names string) ([]workload, error) {
	all := map[string]workload{
		"pair":      {"pair", "a=read(value=20)\nb=read(value=22)\nresult=a+b", "42", 2},
		"short":     {"short", "result=read(value=42)", "42", 1},
		"dependent": {"dependent", "a=read(value=20)\nb=read(value=a+2)\nresult=a+b", "42", 2},
	}
	var out []workload
	for _, name := range strings.Split(names, ",") {
		w, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("unknown workload %q", name)
		}
		out = append(out, w) // Repetition is an explicit, deterministic workload weight.
	}
	return out, nil
}

func (c config) validate() error {
	if c.Arm != "sequential" && c.Arm != "early" && c.Arm != "spare-capacity" {
		return errors.New("arm must be sequential, early or spare-capacity")
	}
	// These are harness safety limits, not runtime or WASM limits.
	if c.Requests < 1 || c.Requests > 10000 || c.Inflight < 1 || c.Inflight > 64 || c.Capacity < 1 || c.Capacity > 128 || c.Interval < 0 || c.Interval > time.Minute || c.Timeout <= 0 || c.Timeout > time.Minute || c.Delay < 0 || c.Delay > time.Minute {
		return errors.New("invalid workload bounds")
	}
	if c.Prepare != "copy" && c.Prepare != "cow" {
		return errors.New("prepare must be copy or cow")
	}
	if c.DataImage && c.Prepare != "cow" {
		return errors.New("cow-data-image requires cow preparation")
	}
	_, err := workloads(c.Workloads)
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	var c config
	flag.StringVar(&c.Arm, "arm", "sequential", "sequential, early or spare-capacity (identical Python sources)")
	flag.IntVar(&c.Requests, "requests", 60, "number of scheduled requests")
	flag.DurationVar(&c.Interval, "interval", 50*time.Millisecond, "fixed arrival interval; zero means a burst")
	flag.DurationVar(&c.Timeout, "timeout", 500*time.Millisecond, "deadline from scheduled arrival, including dispatch lag")
	flag.IntVar(&c.Inflight, "inflight", 8, "request admission cap; excess arrivals are rejected")
	flag.IntVar(&c.Capacity, "tool-capacity", 2, "shared synthetic backend service slots")
	flag.DurationVar(&c.Delay, "tool-delay", 50*time.Millisecond, "synthetic service time after acquiring a tool slot")
	flag.StringVar(&c.Workloads, "workloads", "pair,short,dependent", "cyclic workload names; repetitions set weights")
	flag.StringVar(&c.Prepare, "prepare", "copy", "copy or Linux cow")
	flag.BoolVar(&c.DataImage, "cow-data-image", false, "opt-in data image, requires cow")
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	out := flag.String("out", "", "required private JSONL path; refuses overwrite")
	flag.Parse()
	if err := c.validate(); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Sync(), file.Close()) }()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	trace := newTrace(file, cancel)
	plan, _ := workloads(c.Workloads)
	build, _ := debug.ReadBuildInfo()
	trace.emit(map[string]any{"kind": "header", "schema": 1, "config": c, "programs": plan, "inputs": nil,
		"guest_sha256": fmt.Sprintf("%x", sha256.Sum256(wasm)), "go": runtime.Version(), "os": runtime.GOOS,
		"arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "build": build, "decision_events": true,
		"trace":         "synchronous JSONL; fsync on close; no footer means partial; tracing included in timings",
		"tool_contract": "read echoes integer value after bounded synthetic wait; stable readonly data; honors cancellation"})
	backend := newBackend(c.Capacity, c.Delay, trace)
	manifest := pysolate.Manifest{"read": {Call: backend.call, AllowEarlyRead: true}}
	setup := time.Now()
	setupCtx, setupCancel := context.WithTimeout(ctx, 3*time.Minute)
	var runner *pysolate.Runner
	if c.Prepare == "cow" {
		runner, err = pysolate.NewPreparedCOW(setupCtx, wasm, manifest, pysolate.COWOptions{DataImage: c.DataImage})
	} else {
		runner, err = pysolate.NewPrepared(setupCtx, wasm, manifest)
	}
	setupCancel()
	if err != nil {
		return errors.Join(err, trace.failure())
	}
	defer func() { err = errors.Join(err, runner.Close(context.Background())) }()
	trace.emit(map[string]any{"kind": "prepared", "setup_ns": time.Since(setup).Nanoseconds()})
	invoke := chooseInvoke(c.Arm, backend, trace, runner.Run, runner.RunWithEarlyReads)
	// Every workload gets an excluded warm-up with a generous bounded deadline.
	slots := make(chan struct{}, c.Inflight)
	for i, w := range plan {
		warm := runRequest(ctx, -1-i, w, time.Now(), time.Minute, slots, trace, invoke)
		if warm.Status != "ok" {
			return fmt.Errorf("warm-up %s: %s: %s", w.Name, warm.Status, warm.Error)
		}
	}
	backend.reset()
	rows, elapsed := runLoad(ctx, c, plan, slots, trace, invoke)
	summary := summarize(rows)
	summary["kind"] = "summary"
	summary["config"] = c
	summary["elapsed_ns"] = elapsed.Nanoseconds()
	summary["on_time_per_second"] = float64(countStatus(rows, "ok")) / elapsed.Seconds()
	summary["backend"] = backend.snapshot()
	summary["complete"] = ctx.Err() == nil && trace.failure() == nil
	trace.emit(summary)
	if err := trace.failure(); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if countStatus(rows, "failed")+countStatus(rows, "incorrect") > 0 {
		return errors.New("execution or correctness failure; inspect private trace")
	}
	return nil // Deadlines and admission rejections are measured outcomes, not hidden failures.
}
