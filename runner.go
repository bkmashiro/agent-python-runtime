// Package pysolate is a small, fresh-instance Wasm CPython runner.
package pysolate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

// Runner owns compiled code, tools and an optional clean image, never a live user Guest.
// Close must happen after all Run calls return. Tool implementations must honor context.
type Runner struct {
	artifactID    string
	runtime       wazero.Runtime
	code          wazero.CompiledModule
	manifest      Manifest
	guestManifest []guestToolSpec
	image         []byte // Immutable full-copy baseline; COW owns its image separately.
	cow           cowRuntime
}

type guestToolSpec struct {
	Name           string `json:"name"`
	AllowEarlyRead bool   `json:"allow_early_read"`
}

type Output struct {
	Value       json.RawMessage
	Stdout      string
	Transformed string // Optional actual Guest AST rendering for learning, not execution input.
}

var pythonIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true,
	"async": true, "await": true, "break": true, "class": true, "continue": true, "def": true,
	"del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

// New compiles the actual artifact once; every Run instantiates private memory.
func New(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	r := &Runner{
		artifactID:    fmt.Sprintf("sha256:%x", sha256.Sum256(wasm)),
		runtime:       wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(8192)),
		manifest:      make(Manifest, len(manifest)),
		guestManifest: make([]guestToolSpec, 0, len(manifest)),
	}
	for name, spec := range manifest {
		if !pythonIdentifier.MatchString(name) || pythonKeywords[name] || name == "inputs" || name == "__name__" || name == "__builtins__" || strings.HasPrefix(name, "_pysolate") {
			r.Close(ctx)
			return nil, fmt.Errorf("tool name cannot be injected into Python: %s", name)
		}
		if spec.Call == nil {
			r.Close(ctx)
			return nil, fmt.Errorf("tool has no Host implementation: %s", name)
		}
		r.manifest[name] = spec
		r.guestManifest = append(r.guestManifest, guestToolSpec{Name: name, AllowEarlyRead: spec.AllowEarlyRead})
	}
	sort.Slice(r.guestManifest, func(i, j int) bool { return r.guestManifest[i].Name < r.guestManifest[j].Name })
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r.runtime); err != nil {
		r.Close(ctx)
		return nil, err
	}
	if _, err := r.runtime.NewHostModuleBuilder("pysolate").NewFunctionBuilder().WithFunc(r.hostCall).Export("call").
		NewFunctionBuilder().WithFunc(r.hostPrepare).Export("prepare").
		NewFunctionBuilder().WithFunc(r.hostResolve).Export("resolve").Instantiate(ctx); err != nil {
		r.Close(ctx)
		return nil, err
	}
	var err error
	r.code, err = r.runtime.CompileModule(ctx, wasm)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}
	for _, name := range []string{"_initialize", "init", "alloc", "release", "execute"} {
		if r.code.ExportedFunctions()[name] == nil {
			r.Close(ctx)
			return nil, fmt.Errorf("missing Guest export: %s", name)
		}
	}
	if r.code.ExportedMemories()["memory"] == nil {
		r.Close(ctx)
		return nil, errors.New("missing Guest memory export")
	}
	return r, nil
}

// ArtifactID binds durable runs to the loaded Guest, computed once at construction.
func (r *Runner) ArtifactID() string { return r.artifactID }

// PythonError is a completed Python execution failure, unlike infrastructure errors.
type PythonError struct{ Message string }

func (e *PythonError) Error() string { return e.Message }

func (r *Runner) Close(ctx context.Context) error {
	r.image = nil
	err := r.runtime.Close(ctx)
	if r.cow != nil {
		err = errors.Join(err, r.cow.close())
		r.cow = nil
	}
	return err
}

// Run owns the instance, I/O and all Guest allocations until it returns.
func (r *Runner) Run(ctx context.Context, source string, inputs any) (Output, error) {
	return r.run(ctx, source, inputs, false, nil, nil)
}

// RunPLM enables the built-in Guest AST pass, only for explicitly allowed snapshot reads.
func (r *Runner) RunPLM(ctx context.Context, source string, inputs any) (Output, error) {
	return r.run(ctx, source, inputs, true, nil, nil)
}

func (r *Runner) run(ctx context.Context, source string, inputs any, plm bool, chunks <-chan string, recording *recording) (Output, error) {
	state := newRun(ctx, r, plm)
	state.recording = recording
	defer state.close()
	ctx = state.ctx
	request, err := json.Marshal(struct {
		Source   string          `json:"source"`
		Inputs   any             `json:"inputs"`
		PLM      bool            `json:"plm"`
		Manifest []guestToolSpec `json:"manifest"`
	}{source, inputs, plm, r.guestManifest})
	if err != nil {
		return Output{}, err
	}
	if len(request) > maxMessage {
		return Output{}, errors.New("request exceeds 1 MiB")
	}
	stdout, stderr := &boundedText{}, &boundedText{}
	// No host directories, environment, stdin, network or process capabilities.
	m, err := r.newGuest(ctx, stdout, stderr)
	if err != nil {
		return Output{}, err
	}
	defer m.Close(context.Background())
	fail := func(err error) (Output, error) {
		var exited *wazerosys.ExitError
		if state.controlErr != nil && errors.As(err, &exited) && exited.ExitCode() == journalExitCode {
			return Output{Stdout: stdout.String()}, state.controlErr
		}
		return Output{Stdout: stdout.String()}, fmt.Errorf("%w%s", err, stderr.String())
	}
	if chunks != nil {
		if err := receiveSource(ctx, m, request, chunks); err != nil {
			return fail(err)
		}
		// Input and source already belong to this Guest. Do not send a second copy.
		request = []byte(`{"prefix":true}`)
	}
	packed, err := callWithBytes(ctx, m, "execute", request)
	if err != nil {
		return fail(err)
	}
	// execute returns (length << 32) | pointer. Go copies before release/Close.
	p, n := uint32(packed[0]), uint32(packed[0]>>32)
	if p == 0 {
		return fail(errors.New("Guest execution bridge failed: "))
	}
	defer m.ExportedFunction("release").Call(ctx, uint64(p))
	if n > maxMessage {
		return fail(errors.New("result exceeds 1 MiB"))
	}
	data, ok := m.Memory().Read(p, n)
	if !ok {
		return fail(errors.New("Guest result outside linear memory"))
	}
	var response struct {
		Value       json.RawMessage `json:"value"`
		Error       string          `json:"error"`
		Transformed string          `json:"transformed"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fail(err)
	}
	out := Output{Value: response.Value, Stdout: stdout.String(), Transformed: response.Transformed}
	if response.Error != "" {
		return out, &PythonError{Message: response.Error}
	}
	return out, nil
}

// The 1 MiB message/output limit is a demo policy, not a Wasm limit.
const maxMessage = 1 << 20

type boundedText struct{ strings.Builder }

func (b *boundedText) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxMessage {
		return 0, errors.New("stdout/stderr exceeds 1 MiB")
	}
	return b.Builder.Write(p)
}

// writeResponse is the only Host → Guest memory write for tool results.
func writeResponse(m api.Module, ptr, capacity uint32, data []byte) uint32 {
	if len(data) > maxMessage || uint64(len(data)) > uint64(capacity) || !m.Memory().Write(ptr, data) {
		return ^uint32(0)
	}
	return uint32(len(data))
}
