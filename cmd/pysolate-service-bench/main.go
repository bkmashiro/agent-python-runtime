package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	"github.com/bkmashiro/agent-python-runtime/service"
)

type metadata struct {
	Type           string `json:"type"`
	Artifact       string `json:"artifact"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactBytes  int    `json:"artifact_bytes"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GoVersion      string `json:"go_version"`
	MaxActive      int    `json:"max_active"`
	Iterations     int    `json:"iterations_per_worker"`
	ServiceSetupNS int64  `json:"service_setup_ns"`
	HTTPSetupNS    int64  `json:"http_setup_ns"`
}

type sample struct {
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	Concurrency int    `json:"concurrency"`
	Worker      int    `json:"worker"`
	Iteration   int    `json:"iteration"`
	Status      int    `json:"status"`
	E2ENS       int64  `json:"e2e_ns"`
	RunNS       int64  `json:"run_ns"`
	TotalNS     int64  `json:"server_total_ns"`
}

type summary struct {
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	Concurrency int    `json:"concurrency"`
	Samples     int    `json:"samples"`
	E2EP50NS    int64  `json:"e2e_p50_ns"`
	E2EP95NS    int64  `json:"e2e_p95_ns"`
	RunP50NS    int64  `json:"run_p50_ns"`
	RunP95NS    int64  `json:"run_p95_ns"`
}

type runReply struct {
	RunNS   int64  `json:"run_ns"`
	TotalNS int64  `json:"total_ns"`
	Error   string `json:"error"`
}

type requestResult struct {
	status int
	e2eNS  int64
	body   []byte
	reply  runReply
	err    error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "agent-core Guest artifact")
	iterations := flag.Int("iterations", 20, "measured requests per worker after one excluded warmup")
	concurrencyFlag := flag.String("concurrency", "1,2,4", "comma-separated concurrent worker counts")
	maxActive := flag.Int("max-active", 4, "service execution slots")
	mode := flag.String("mode", "all", "plain, workspace, or all")
	flag.Parse()
	if *iterations < 1 || *iterations > 10000 || *maxActive < 1 || *maxActive > 64 {
		return errors.New("invalid iteration or max-active value")
	}
	concurrencies, err := parseConcurrencies(*concurrencyFlag, *maxActive)
	if err != nil {
		return err
	}
	modes := []string{*mode}
	if *mode == "all" {
		modes = []string{"plain", "workspace"}
	}
	for _, value := range modes {
		if value != "plain" && value != "workspace" {
			return errors.New("mode must be plain, workspace, or all")
		}
	}

	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(wasm)
	root, err := os.MkdirTemp("", "pysolate-service-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	manager, err := workspacepkg.NewManager(root)
	if err != nil {
		return err
	}
	started := make(chan struct{}, *maxActive)
	release := make(chan struct{})
	manifest := pysolate.Manifest{"benchmark/wait": {
		PythonPath: "benchmark.wait",
		Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			select {
			case <-release:
				return true, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	setupStarted := time.Now()
	handler, err := service.New(ctx, wasm, manifest, manager, *maxActive)
	serviceSetupNS := time.Since(setupStarted).Nanoseconds()
	if err != nil {
		_ = manager.Close()
		return err
	}
	defer handler.Close(context.Background())
	listenStarted := time.Now()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 2 * time.Minute}
	if err := waitReady(ctx, client, baseURL); err != nil {
		return err
	}
	httpSetupNS := time.Since(listenStarted).Nanoseconds()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(metadata{
		Type: "metadata", Artifact: *guest, ArtifactSHA256: hex.EncodeToString(digest[:]), ArtifactBytes: len(wasm),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), MaxActive: *maxActive,
		Iterations: *iterations, ServiceSetupNS: serviceSetupNS, HTTPSetupNS: httpSetupNS,
	}); err != nil {
		return err
	}
	for _, benchmarkMode := range modes {
		for _, concurrency := range concurrencies {
			rows, err := runCase(ctx, client, baseURL, benchmarkMode, concurrency, *iterations)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := encoder.Encode(row); err != nil {
					return err
				}
			}
			if err := encoder.Encode(summarize(benchmarkMode, concurrency, rows)); err != nil {
				return err
			}
		}
	}

	overloadStarted := time.Now()
	blockingBody, _ := json.Marshal(map[string]any{"source": "result=benchmark.wait()", "inputs": map[string]any{}})
	blockingDone := make(chan requestResult, *maxActive)
	for range *maxActive {
		go func() { blockingDone <- postJSON(ctx, client, baseURL+"/v1/run", blockingBody) }()
	}
	for range *maxActive {
		select {
		case <-started:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	overloaded := postJSON(ctx, client, baseURL+"/v1/run", []byte(`{"source":"result=1","inputs":{}}`))
	close(release)
	for range *maxActive {
		result := <-blockingDone
		if result.err != nil || result.status != http.StatusOK {
			return fmt.Errorf("blocking overload request: status=%d err=%v", result.status, result.err)
		}
	}
	if overloaded.err != nil || overloaded.status != http.StatusTooManyRequests {
		return fmt.Errorf("overload probe: status=%d err=%v", overloaded.status, overloaded.err)
	}
	if err := encoder.Encode(map[string]any{"type": "overload", "status": overloaded.status, "e2e_ns": overloaded.e2eNS, "elapsed_ns": time.Since(overloadStarted).Nanoseconds()}); err != nil {
		return err
	}
	select {
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	default:
	}
	return nil
}

func parseConcurrencies(raw string, maxActive int) ([]int, error) {
	seen := map[int]bool{}
	var values []int
	for _, item := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || value < 1 || value > maxActive {
			return nil, fmt.Errorf("invalid concurrency %q; each value must be between 1 and max-active", item)
		}
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, errors.New("at least one concurrency is required")
	}
	sort.Ints(values)
	return values, nil
}

func waitReady(ctx context.Context, client *http.Client, baseURL string) error {
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func runCase(ctx context.Context, client *http.Client, baseURL, mode string, concurrency, iterations int) ([]sample, error) {
	paths := make([]string, concurrency)
	if mode == "plain" {
		for index := range paths {
			paths[index] = "/v1/run"
		}
	} else {
		for index := range paths {
			body, _ := json.Marshal(map[string]any{"files": []map[string]any{{"path": "value.txt", "data": []byte("0")}}})
			result := postJSON(ctx, client, baseURL+"/v1/workspaces", body)
			if result.err != nil || result.status != http.StatusCreated {
				return nil, fmt.Errorf("create benchmark workspace: status=%d err=%v", result.status, result.err)
			}
			var reply struct {
				Workspace string `json:"workspace"`
			}
			if err := decodeResponse(result, &reply); err != nil {
				return nil, err
			}
			paths[index] = "/v1/workspaces/" + reply.Workspace + "/run"
		}
		defer func() {
			for _, runPath := range paths {
				workspacePath := strings.TrimSuffix(runPath, "/run")
				_, _, _, _ = request(ctx, client, http.MethodDelete, baseURL+workspacePath, nil)
			}
		}()
	}
	source := `result=inputs["value"]+1`
	if mode == "workspace" {
		source = `open("/workspace/value.txt","w").write(str(inputs["value"])); result=inputs["value"]+1`
	}
	bodies := make([][]byte, concurrency)
	for worker := range concurrency {
		bodies[worker], _ = json.Marshal(map[string]any{"source": source, "inputs": map[string]any{"value": worker}})
		warmup := postJSON(ctx, client, baseURL+paths[worker], bodies[worker])
		if warmup.err != nil || warmup.status != http.StatusOK {
			return nil, fmt.Errorf("%s warmup worker %d: status=%d err=%v", mode, worker, warmup.status, warmup.err)
		}
	}
	rows := make([]sample, 0, concurrency*iterations)
	results := make(chan sample, concurrency*iterations)
	errs := make(chan error, concurrency)
	start := make(chan struct{})
	var group sync.WaitGroup
	for worker := range concurrency {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			<-start
			for iteration := range iterations {
				result := postJSON(ctx, client, baseURL+paths[worker], bodies[worker])
				if result.err != nil || result.status != http.StatusOK {
					errs <- fmt.Errorf("%s worker %d iteration %d: status=%d err=%v response=%s", mode, worker, iteration, result.status, result.err, result.reply.Error)
					return
				}
				results <- sample{Type: "sample", Mode: mode, Concurrency: concurrency, Worker: worker, Iteration: iteration, Status: result.status, E2ENS: result.e2eNS, RunNS: result.reply.RunNS, TotalNS: result.reply.TotalNS}
			}
		}(worker)
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	if err := <-errs; err != nil {
		return nil, err
	}
	for row := range results {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Worker == rows[j].Worker {
			return rows[i].Iteration < rows[j].Iteration
		}
		return rows[i].Worker < rows[j].Worker
	})
	return rows, nil
}

func postJSON(ctx context.Context, client *http.Client, url string, body []byte) requestResult {
	status, responseBody, elapsed, err := request(ctx, client, http.MethodPost, url, body)
	result := requestResult{status: status, e2eNS: elapsed, body: responseBody, err: err}
	if err == nil {
		result.err = json.Unmarshal(responseBody, &result.reply)
	}
	return result
}

func request(ctx context.Context, client *http.Client, method, url string, body []byte) (int, []byte, int64, error) {
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	elapsed := time.Since(started).Nanoseconds()
	if err != nil {
		return 0, nil, elapsed, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	return response.StatusCode, responseBody, elapsed, err
}

func decodeResponse(result requestResult, target any) error {
	if result.err != nil {
		return result.err
	}
	return json.Unmarshal(result.body, target)
}

func summarize(mode string, concurrency int, rows []sample) summary {
	e2e := make([]int64, len(rows))
	run := make([]int64, len(rows))
	for index, row := range rows {
		e2e[index] = row.E2ENS
		run[index] = row.RunNS
	}
	return summary{Type: "summary", Mode: mode, Concurrency: concurrency, Samples: len(rows), E2EP50NS: percentile(e2e, 0.50), E2EP95NS: percentile(e2e, 0.95), RunP50NS: percentile(run, 0.50), RunP95NS: percentile(run, 0.95)}
}

func percentile(values []int64, fraction float64) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(float64(len(ordered))*fraction)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}
