// Package main is a benchmark-only bridge: Python executes in Pysolate, while
// AppWorld API implementations and grading stay in a trusted child process.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

type toolBinding struct {
	Name       string `json:"name"`
	PythonPath string `json:"python_path"`
}
type envelope struct {
	Kind  string          `json:"kind"`
	ID    uint64          `json:"id"`
	Tools []toolBinding   `json:"tools"`
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
}
type wireRead struct {
	frame envelope
	err   error
}
type peer struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	frames chan wireRead
	mu     sync.Mutex
	next   uint64
}

func startPeer(ctx context.Context, python, host, task, experiment, trace string, stderr io.Writer) (*peer, error) {
	cmd := exec.CommandContext(ctx, python, "-u", host, "--task", task, "--experiment", experiment, "--trace", trace)
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		return nil, err
	}
	p := &peer{cmd: cmd, input: input, frames: make(chan wireRead, 1)}
	go func() {
		defer close(p.frames)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			var frame envelope
			err := json.Unmarshal(scanner.Bytes(), &frame)
			select {
			case p.frames <- wireRead{frame, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case p.frames <- wireRead{err: err}:
		case <-ctx.Done():
		}
	}()
	return p, nil
}
func (p *peer) receive(ctx context.Context) (envelope, error) {
	select {
	case r, ok := <-p.frames:
		if !ok {
			return envelope{}, io.EOF
		}
		return r.frame, r.err
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		return envelope{}, ctx.Err()
	}
}
func (p *peer) call(ctx context.Context, op, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A trusted worker can still stall before reading stdin. Bound the write too.
	stop := context.AfterFunc(ctx, func() { _ = p.cmd.Process.Kill(); _ = p.input.Close() })
	defer stop()
	p.next++
	req := struct {
		ID        uint64          `json:"id"`
		Op        string          `json:"op"`
		Tool      string          `json:"tool,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}{p.next, op, tool, args}
	if err := json.NewEncoder(p.input).Encode(req); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	response, err := p.receive(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if response.ID != p.next {
		return nil, errors.New("host response identity mismatch")
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if len(response.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return response.Value, nil
}
func (p *peer) close() error { _ = p.input.Close(); return p.cmd.Wait() }

func makeManifest(bindings []toolBinding, p *peer) (pysolate.Manifest, error) {
	manifest := pysolate.Manifest{}
	for _, binding := range bindings {
		if binding.Name == "" || binding.PythonPath == "" {
			return nil, errors.New("invalid host binding")
		}
		if _, ok := manifest[binding.Name]; ok {
			return nil, errors.New("duplicate host binding")
		}
		name := binding.Name
		manifest[name] = pysolate.ToolSpec{PythonPath: binding.PythonPath, Call: func(ctx context.Context, args json.RawMessage) (any, error) { return p.call(ctx, "call", name, args) }}
	}
	return manifest, nil // No AppWorld tool is implicitly approved for early reads.
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "appworld bridge failed; inspect private records")
		os.Exit(1)
	}
}
func run() (err error) {
	python := flag.String("python", "python3", "trusted AppWorld environment Python")
	host := flag.String("host", "examples/appworld-bridge/host.py", "trusted host worker")
	task := flag.String("task", "", "explicit dev task ID")
	experiment := flag.String("experiment", "", "new AppWorld experiment name")
	sourcePath := flag.String("source", "", "Python source to execute in fresh Guest")
	inputsPath := flag.String("inputs", "", "optional JSON inputs")
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	record := flag.String("record", "", "new private JSONL file")
	hostTrace := flag.String("host-trace", "", "new private host JSONL file")
	timeout := flag.Duration("timeout", 30*time.Second, "Guest execution deadline")
	flag.Parse()
	if *task == "" || *experiment == "" || *sourcePath == "" || *record == "" || *hostTrace == "" || *record == *hostTrace || *timeout <= 0 || *timeout > 2*time.Minute {
		return errors.New("invalid bridge flags")
	}
	source, err := os.ReadFile(*sourcePath)
	if err != nil {
		return err
	}
	if len(source) > 1<<20 {
		return errors.New("source exceeds benchmark limit")
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	var inputs any
	if *inputsPath != "" {
		raw, e := os.ReadFile(*inputsPath)
		if e != nil {
			return e
		}
		if len(raw) > 1<<20 {
			return errors.New("inputs exceed benchmark limit")
		}
		if !json.Valid(raw) {
			return errors.New("inputs must be one valid JSON value")
		}
		inputs = json.RawMessage(raw) // Preserve integers and JSON types without a float64 round trip.
	}
	file, err := os.OpenFile(*record, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Sync(), file.Close()) }()
	encode := func(v any) error {
		if e := json.NewEncoder(file).Encode(v); e != nil {
			return e
		}
		return file.Sync()
	}
	if err = encode(map[string]any{"kind": "header", "source": string(source), "inputs": inputs, "guest_sha256": fmt.Sprintf("%x", sha256.Sum256(wasm)), "task": *task, "experiment": *experiment, "timeout_ns": timeout.Nanoseconds(), "scope": "one fresh Guest, serial native AppWorld API bridge; no persistent Python globals; no model"}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stderr, err := os.OpenFile(*record+".host-stderr", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	p, err := startPeer(ctx, *python, *host, *task, *experiment, *hostTrace, stderr)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.close()); cancel() }()
	ready, err := p.receive(ctx)
	if err != nil {
		return err
	}
	if ready.Kind != "ready" {
		return errors.New("host not ready")
	}
	if err = encode(map[string]any{"kind": "ready", "bindings": ready.Tools}); err != nil {
		return err
	}
	manifest, err := makeManifest(ready.Tools, p)
	if err != nil {
		return err
	}
	runner, err := pysolate.NewPrepared(ctx, wasm, manifest)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runner.Close(context.Background())) }()
	callCtx, stop := context.WithTimeout(ctx, *timeout)
	defer stop()
	callCtx, rawIO := perfdiag.WithGuestIO(callCtx)
	if err = encode(map[string]any{"kind": "execution_start"}); err != nil {
		return err
	}
	began := time.Now()
	output, runErr := runner.Run(callCtx, string(source), inputs)
	elapsed := time.Since(began)
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	if err = encode(map[string]any{"kind": "execution_end", "output": output, "raw_io": rawIO, "error": errorText, "elapsed_ns": elapsed.Nanoseconds()}); err != nil {
		return err
	}
	grade, gradeErr := p.call(ctx, "finish", "", nil)
	gradeError := ""
	if gradeErr != nil {
		gradeError = gradeErr.Error()
	}
	if err = encode(map[string]any{"kind": "finish", "grade": grade, "error": gradeError, "complete": gradeErr == nil}); err != nil {
		return err
	}
	// A run failing the task oracle is data, not an adapter execution error.
	if runErr != nil || gradeErr != nil {
		return errors.New("execution or evaluation failed")
	}
	fmt.Println("appworld bridge completed; grading and full trace retained privately")
	return nil
}
