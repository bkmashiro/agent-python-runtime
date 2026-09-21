// Package pysolate is a small, fresh-instance Wasm CPython runner.
package pysolate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/internal/cowmem"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

// Runner owns compiled code, tools and an optional clean image, never a live user Guest.
// Close must happen after all Run calls return. Tool implementations must honor context.
type Runner struct {
	artifactID     string
	runtime        wazero.Runtime
	code           wazero.CompiledModule
	manifest       Manifest
	guestManifest  []guestToolSpec
	preparedState  *preparedRecording
	image          []byte // Immutable full-copy baseline; COW owns its image separately.
	cow            cowmem.Runtime
	workspaceImage bool
}

type guestToolSpec struct {
	Name           string `json:"name"`
	PythonPath     string `json:"python_path"`
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
// WithCompilationCache supplies a caller-owned wazero cache to constructors.
// Close the cache only after all Runners using it have closed.
func WithCompilationCache(ctx context.Context, cache wazero.CompilationCache) context.Context {
	return context.WithValue(ctx, compilationCacheKey{}, cache)
}

type compilationCacheKey struct{}

func New(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	config := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(8192)
	if cache, ok := ctx.Value(compilationCacheKey{}).(wazero.CompilationCache); ok {
		config = config.WithCompilationCache(cache)
	}

	r := &Runner{
		artifactID:    fmt.Sprintf("sha256:%x", sha256.Sum256(wasm)),
		runtime:       wazero.NewRuntimeWithConfig(ctx, config),
		manifest:      make(Manifest, len(manifest)),
		guestManifest: make([]guestToolSpec, 0, len(manifest)),
	}
	var err error
	r.manifest, r.guestManifest, err = normalizeManifest(manifest)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}
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
		err = errors.Join(err, r.cow.Close())
		r.cow = nil
	}
	return err
}

// Run owns the instance, I/O and all Guest allocations until it returns.
func (r *Runner) Run(ctx context.Context, source string, inputs any) (Output, error) {
	if r.workspaceImage {
		return Output{}, errors.New("workspace-prepared runner requires RunWorkspace")
	}
	return r.run(ctx, source, inputs, false, nil, nil, nil)
}

// RunPLM enables the built-in Guest AST pass, only for explicitly allowed snapshot reads.
func (r *Runner) RunPLM(ctx context.Context, source string, inputs any) (Output, error) {
	if r.workspaceImage {
		return Output{}, errors.New("workspace-prepared runner requires RunWorkspace")
	}
	return r.run(ctx, source, inputs, true, nil, nil, nil)
}

// RunWorkspace grants one private bounded workspace at /workspace for this
// attempt. The lease remains caller-owned and may be reused after Run returns.
func (r *Runner) RunWorkspace(ctx context.Context, source string, inputs any, lease *workspacepkg.Lease) (Output, error) {
	if lease == nil {
		return Output{}, errors.New("nil workspace lease")
	}
	filesystem, _, err := lease.BeginRun()
	if err != nil {
		return Output{}, err
	}
	defer lease.EndRun()
	if (r.image != nil || r.cow != nil) && !r.workspaceImage {
		return Output{}, errors.New("prepared runner was not captured for workspace mounts")
	}
	fsConfig, err := workspaceFSConfig(filesystem)
	if err != nil {
		return Output{}, err
	}
	return r.run(ctx, source, inputs, false, nil, nil, fsConfig)
}

func workspaceFSConfig(filesystem experimentalsys.FS) (wazero.FSConfig, error) {
	base, ok := wazero.NewFSConfig().(experimentalsysfs.FSConfig)
	if !ok {
		return nil, errors.New("wazero does not support rooted workspace mounts")
	}
	return base.WithSysFSMount(filesystem, "workspace"), nil
}

func (r *Runner) run(ctx context.Context, source string, inputs any, plm bool, chunks <-chan string, recording *recording, fsConfig wazero.FSConfig) (Output, error) {
	state := newRun(ctx, r, plm)
	state.recording = recording
	state.fsConfig = fsConfig
	defer state.close()
	ctx = state.ctx
	request, err := r.marshalRunRequest(source, inputs, plm, fsConfig != nil)
	if err != nil {
		return Output{}, err
	}
	stdout, stderr := &boundedText{}, &boundedText{}
	// No host directories, environment, stdin, network or process capabilities.
	m, err := r.newGuest(ctx, stdout, stderr)
	if err != nil {
		return Output{}, err
	}
	defer m.Close(context.Background())
	response, err := executeGuest(ctx, m, request, chunks)
	if err != nil {
		return runFailure(state, stdout, stderr, err)
	}
	out := Output{Value: response.Value, Stdout: stdout.String(), Transformed: response.Transformed}
	if response.Error != "" {
		return out, &PythonError{Message: response.Error}
	}
	return out, nil
}

func (r *Runner) marshalRunRequest(source string, inputs any, plm, workspace bool) ([]byte, error) {
	request, err := json.Marshal(struct {
		Source    string          `json:"source"`
		Inputs    any             `json:"inputs"`
		PLM       bool            `json:"plm"`
		Workspace bool            `json:"workspace"`
		Manifest  []guestToolSpec `json:"manifest"`
	}{source, inputs, plm, workspace, r.guestManifest})
	if err != nil {
		return nil, err
	}
	if len(request) > maxMessage {
		return nil, errors.New("request exceeds 1 MiB")
	}
	return request, nil
}

func executeGuest(ctx context.Context, m api.Module, request []byte, chunks <-chan string) (guestExecutionResponse, error) {
	if chunks != nil {
		if err := receiveSource(ctx, m, request, chunks); err != nil {
			return guestExecutionResponse{}, err
		}
		// Input and source already belong to this Guest. Do not send a second copy.
		request = []byte(`{"prefix":true}`)
	}
	packed, err := callWithBytes(ctx, m, "execute", request)
	if err != nil {
		return guestExecutionResponse{}, err
	}
	return readGuestResponse(ctx, m, packed)
}

type guestExecutionResponse struct {
	Value       json.RawMessage `json:"value"`
	Error       string          `json:"error"`
	Transformed string          `json:"transformed"`
}

func readGuestResponse(ctx context.Context, m api.Module, packed []uint64) (guestExecutionResponse, error) {
	// execute returns (length << 32) | pointer. Go copies before release/Close.
	p, n := uint32(packed[0]), uint32(packed[0]>>32)
	if p == 0 {
		return guestExecutionResponse{}, errors.New("Guest execution bridge failed: ")
	}
	defer m.ExportedFunction("release").Call(ctx, uint64(p))
	if n > maxMessage {
		return guestExecutionResponse{}, errors.New("result exceeds 1 MiB")
	}
	data, ok := m.Memory().Read(p, n)
	if !ok {
		return guestExecutionResponse{}, errors.New("Guest result outside linear memory")
	}
	var response guestExecutionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return guestExecutionResponse{}, err
	}
	return response, nil
}

func runFailure(state *runState, stdout, stderr *boundedText, err error) (Output, error) {
	var exited *wazerosys.ExitError
	if state.controlErr != nil && errors.As(err, &exited) && exited.ExitCode() == journalExitCode {
		return Output{Stdout: stdout.String()}, state.controlErr
	}
	return Output{Stdout: stdout.String()}, fmt.Errorf("%w%s", err, stderr.String())
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
