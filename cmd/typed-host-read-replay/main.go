package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const (
	requestSchemaVersion = "pysolate.typed-host-read-replay.request.v1"
	outputSchemaVersion  = "pysolate.typed-host-read-replay.output.v1"
)

type sourceChunk struct {
	OffsetMilliseconds uint64 `json:"offset_ms"`
	Text               string `json:"text"`
}

type providerConfig struct {
	DelayMilliseconds uint64            `json:"delay_ms"`
	Values            map[string]string `json:"values"`
	Errors            map[string]string `json:"errors,omitempty"`
	RequiredCalls     uint32            `json:"required_calls"`
}

type replayRequest struct {
	SchemaVersion  string          `json:"schema_version"`
	RunID          string          `json:"run_id"`
	SourceChunks   []sourceChunk   `json:"source_chunks"`
	Inputs         json.RawMessage `json:"inputs"`
	ExpectedResult json.RawMessage `json:"expected_result"`
	Provider       providerConfig  `json:"provider"`
}

func (request replayRequest) Validate() error {
	if request.SchemaVersion != requestSchemaVersion {
		return fmt.Errorf("schema_version must be %q", requestSchemaVersion)
	}
	if strings.TrimSpace(request.RunID) == "" || len(request.RunID) > 128 {
		return errors.New("run_id must contain 1 to 128 characters")
	}
	if len(request.SourceChunks) == 0 || len(request.SourceChunks) > 1024 {
		return errors.New("source_chunks must contain 1 to 1024 chunks")
	}
	var sourceBytes int
	var prior uint64
	for index, chunk := range request.SourceChunks {
		if chunk.Text == "" {
			return fmt.Errorf("source_chunks[%d].text is empty", index)
		}
		if index > 0 && chunk.OffsetMilliseconds < prior {
			return errors.New("source chunk offsets must be nondecreasing")
		}
		if chunk.OffsetMilliseconds > uint64((10 * time.Minute).Milliseconds()) {
			return errors.New("source chunk offset exceeds 10 minutes")
		}
		prior = chunk.OffsetMilliseconds
		sourceBytes += len(chunk.Text)
	}
	if sourceBytes > 4<<20 {
		return errors.New("source exceeds 4 MiB")
	}
	var inputs map[string]any
	if len(request.Inputs) == 0 || json.Unmarshal(request.Inputs, &inputs) != nil || inputs == nil {
		return errors.New("inputs must be a JSON object")
	}
	if len(request.ExpectedResult) == 0 || !json.Valid(request.ExpectedResult) {
		return errors.New("expected_result must be valid JSON")
	}
	if request.Provider.DelayMilliseconds > uint64((30 * time.Second).Milliseconds()) {
		return errors.New("provider delay exceeds 30 seconds")
	}
	if len(request.Provider.Values) == 0 && len(request.Provider.Errors) == 0 {
		return errors.New("provider values or errors must not be empty")
	}
	for key, message := range request.Provider.Errors {
		if key == "" || message == "" {
			return errors.New("provider errors require non-empty keys and messages")
		}
		if _, exists := request.Provider.Values[key]; exists {
			return fmt.Errorf("provider key %q has both a value and an error", key)
		}
	}
	if request.Provider.RequiredCalls == 0 || request.Provider.RequiredCalls > 128 {
		return errors.New("provider required_calls must be between 1 and 128")
	}
	return nil
}

type providerTrace struct {
	Attempts     uint32   `json:"attempts"`
	Keys         []string `json:"keys"`
	ResultBytes  uint64   `json:"result_bytes"`
	ElapsedNanos uint64   `json:"elapsed_nanos"`
}

type fixedProvider struct {
	config providerConfig
	mu     sync.Mutex
	trace  providerTrace
}

func newFixedProvider(config providerConfig) *fixedProvider {
	values := make(map[string]string, len(config.Values))
	for key, value := range config.Values {
		values[key] = value
	}
	config.Values = values
	errorsByKey := make(map[string]string, len(config.Errors))
	for key, message := range config.Errors {
		errorsByKey[key] = message
	}
	config.Errors = errorsByKey
	return &fixedProvider{config: config}
}

func (provider *fixedProvider) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	return provider.Handle(ctx, arguments)
}

func (provider *fixedProvider) Handle(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	started := time.Now()
	var input struct {
		Path string `json:"path"`
	}
	decodeErr := json.Unmarshal(arguments, &input)
	provider.mu.Lock()
	provider.trace.Attempts++
	provider.trace.Keys = append(provider.trace.Keys, input.Path)
	provider.mu.Unlock()
	if decodeErr != nil || input.Path == "" {
		provider.addElapsed(started)
		return nil, errors.New("provider path is required")
	}
	value, hasValue := provider.config.Values[input.Path]
	errorMessage, hasError := provider.config.Errors[input.Path]
	if !hasValue && !hasError {
		provider.addElapsed(started)
		return nil, fmt.Errorf("unknown provider path %q", input.Path)
	}
	if delay := time.Duration(provider.config.DelayMilliseconds) * time.Millisecond; delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			provider.addElapsed(started)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if hasError {
		provider.addElapsed(started)
		return nil, errors.New(errorMessage)
	}
	response, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: value})
	provider.mu.Lock()
	provider.trace.ElapsedNanos += uint64(time.Since(started))
	if err == nil {
		provider.trace.ResultBytes += uint64(len(response))
	}
	provider.mu.Unlock()
	return response, err
}

func (provider *fixedProvider) addElapsed(started time.Time) {
	provider.mu.Lock()
	provider.trace.ElapsedNanos += uint64(time.Since(started))
	provider.mu.Unlock()
}

func (provider *fixedProvider) Trace() providerTrace {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	trace := provider.trace
	trace.Keys = append([]string(nil), trace.Keys...)
	return trace
}

type artifactManifest struct {
	ArtifactProfile string `json:"artifact_profile"`
	Artifact        struct {
		Filename string `json:"filename"`
	} `json:"artifact"`
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
	} `json:"build"`
}

type replayTimings struct {
	TotalNanos              uint64   `json:"total_nanos"`
	SetupNanos              uint64   `json:"setup_nanos"`
	BeginNanos              uint64   `json:"begin_nanos"`
	PostBeginNanos          uint64   `json:"post_begin_nanos"`
	FinalizeNanos           uint64   `json:"finalize_nanos"`
	SourceReleaseDriftNanos []uint64 `json:"source_release_drift_nanos"`
}

type replayOutcome struct {
	FinalProgramOutcome  string `json:"final_program_outcome"`
	ResultSHA256         string `json:"result_sha256"`
	LogicalCalls         uint32 `json:"logical_calls"`
	PhysicalAttempts     uint32 `json:"physical_attempts"`
	ReadyBeforeFinalize  uint32 `json:"ready_before_finalize"`
	WorkspaceDisposition string `json:"workspace_disposition"`
}

type replayOutput struct {
	SchemaVersion         string                                                 `json:"schema_version"`
	RunID                 string                                                 `json:"run_id"`
	RuntimeCommit         string                                                 `json:"runtime_commit"`
	ArtifactSHA256        string                                                 `json:"artifact_sha256"`
	ManifestSHA256        string                                                 `json:"manifest_sha256"`
	ImportInventorySHA256 string                                                 `json:"import_inventory_sha256"`
	ExpectedResultSHA256  string                                                 `json:"expected_result_sha256"`
	Timings               replayTimings                                          `json:"timings"`
	Outcome               replayOutcome                                          `json:"outcome"`
	Lifecycle             semanticspeculation.SemanticTreatmentLifecycleEvidence `json:"lifecycle"`
	Provider              providerTrace                                          `json:"provider"`
}

func main() {
	artifactRoot := flag.String("artifact-root", "", "directory containing the verified Guest artifact and metadata")
	flag.Parse()
	if *artifactRoot == "" {
		fatal(errors.New("-artifact-root is required"))
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	var request replayRequest
	if err := decoder.Decode(&request); err != nil {
		fatal(fmt.Errorf("decode request: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fatal(errors.New("request must contain exactly one JSON value"))
	}
	if err := request.Validate(); err != nil {
		fatal(err)
	}
	output, err := executeReplay(context.Background(), *artifactRoot, request)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatal(err)
	}
}

func executeReplay(ctx context.Context, artifactRoot string, request replayRequest) (replayOutput, error) {
	totalStarted := time.Now()
	manifestBytes, err := os.ReadFile(filepath.Join(artifactRoot, "manifest.json"))
	if err != nil {
		return replayOutput{}, err
	}
	inventoryBytes, err := os.ReadFile(filepath.Join(artifactRoot, "import-inventory.json"))
	if err != nil {
		return replayOutput{}, err
	}
	var manifest artifactManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return replayOutput{}, err
	}
	if manifest.ArtifactProfile == "" || manifest.Artifact.Filename == "" || filepath.Base(manifest.Artifact.Filename) != manifest.Artifact.Filename {
		return replayOutput{}, errors.New("invalid artifact manifest")
	}
	artifact, err := os.ReadFile(filepath.Join(artifactRoot, manifest.Artifact.Filename))
	if err != nil {
		return replayOutput{}, err
	}
	artifactSHA := digestBytes(artifact)
	manifestSHA := digestBytes(manifestBytes)
	inventorySHA := digestBytes(inventoryBytes)
	profile, err := runtimeconfig.NewExecutionProfile(manifest.ArtifactProfile, []string{"json"})
	if err != nil {
		return replayOutput{}, err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: manifest.ArtifactProfile, ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA,
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		return replayOutput{}, err
	}
	provider := newFixedProvider(request.Provider)
	plan, err := typedReadPlan(provider, request.Provider.RequiredCalls)
	if err != nil {
		return replayOutput{}, err
	}
	workspaceRoot, err := os.MkdirTemp("", "typed-host-read-replay-")
	if err != nil {
		return replayOutput{}, err
	}
	defer os.RemoveAll(workspaceRoot)
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Timeout = 2 * time.Minute
	runConfig.ExecutionProfile = &profile
	treatment, err := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
		Artifact: artifact, RunConfig: runConfig, Plan: plan, ProviderObservation: func() semanticspeculation.ProviderObservation {
			trace := provider.Trace()
			return semanticspeculation.ProviderObservation{
				Attempts: trace.Attempts, ResultBytes: trace.ResultBytes, CostUnits: uint64(trace.Attempts), ElapsedNanos: trace.ElapsedNanos,
			}
		},
		ImportClosureSHA256: inventorySHA, PhysicalReadBudget: request.Provider.RequiredCalls,
		RunID: request.RunID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: request.RunID,
	})
	if err != nil {
		return replayOutput{}, err
	}
	setupNanos := uint64(time.Since(totalStarted))
	beginStarted := time.Now()
	if err := treatment.Begin(ctx, request.Inputs); err != nil {
		_ = treatment.Cancel(context.Background())
		return replayOutput{}, err
	}
	beginNanos := uint64(time.Since(beginStarted))
	postBeginStarted := time.Now()
	drifts := make([]uint64, 0, len(request.SourceChunks))
	for _, chunk := range request.SourceChunks {
		due := postBeginStarted.Add(time.Duration(chunk.OffsetMilliseconds) * time.Millisecond)
		if delay := time.Until(due); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				_ = treatment.Cancel(context.Background())
				return replayOutput{}, ctx.Err()
			case <-timer.C:
			}
		}
		drift := time.Since(due)
		if drift < 0 {
			drift = 0
		}
		drifts = append(drifts, uint64(drift))
		if err := treatment.ObserveChunk(ctx, chunk.Text); err != nil {
			_ = treatment.Cancel(context.Background())
			return replayOutput{}, err
		}
	}
	finalizeStarted := time.Now()
	outcome, err := treatment.Finalize(ctx)
	finalizeNanos := uint64(time.Since(finalizeStarted))
	if err != nil {
		_ = treatment.Cancel(context.Background())
		return replayOutput{}, err
	}
	postBeginNanos := uint64(time.Since(postBeginStarted))
	trace := provider.Trace()
	expectedHash, err := playback.CanonicalSHA256(request.ExpectedResult)
	if err != nil {
		return replayOutput{}, err
	}
	if outcome.FinalProgramOutcome != "success" || outcome.ResultSHA256 != expectedHash {
		return replayOutput{}, fmt.Errorf("unexpected result: outcome=%s result=%s expected=%s", outcome.FinalProgramOutcome, outcome.ResultSHA256, expectedHash)
	}
	if outcome.LogicalCalls != request.Provider.RequiredCalls || outcome.PhysicalAttempts != request.Provider.RequiredCalls || trace.Attempts != request.Provider.RequiredCalls {
		return replayOutput{}, fmt.Errorf("call count mismatch: logical=%d physical=%d provider=%d expected=%d", outcome.LogicalCalls, outcome.PhysicalAttempts, trace.Attempts, request.Provider.RequiredCalls)
	}
	keys := append([]string(nil), trace.Keys...)
	sort.Strings(keys)
	for _, key := range keys {
		_, hasValue := request.Provider.Values[key]
		_, hasError := request.Provider.Errors[key]
		if !hasValue && !hasError {
			return replayOutput{}, fmt.Errorf("provider trace contains unknown key %q", key)
		}
	}
	return replayOutput{
		SchemaVersion: outputSchemaVersion, RunID: request.RunID, RuntimeCommit: manifest.Build.RepositoryCommit,
		ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA, ImportInventorySHA256: inventorySHA,
		ExpectedResultSHA256: expectedHash,
		Timings: replayTimings{
			TotalNanos: uint64(time.Since(totalStarted)), SetupNanos: setupNanos, BeginNanos: beginNanos,
			PostBeginNanos: postBeginNanos, FinalizeNanos: finalizeNanos, SourceReleaseDriftNanos: drifts,
		},
		Outcome: replayOutcome{
			FinalProgramOutcome: outcome.FinalProgramOutcome, ResultSHA256: outcome.ResultSHA256,
			LogicalCalls: outcome.LogicalCalls, PhysicalAttempts: outcome.PhysicalAttempts,
			ReadyBeforeFinalize: outcome.ReadyBeforeFinalize, WorkspaceDisposition: outcome.WorkspaceDisposition,
		},
		Lifecycle: treatment.LifecycleEvidence(), Provider: trace,
	}, nil
}

func typedReadPlan(handler capability.Handler, maxCalls uint32) (*capability.Plan, error) {
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"typed-host-read-replay"}`))
	if err != nil {
		return nil, err
	}
	registry := capability.NewRegistry()
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.typed-host-read-replay.v1", Description: "Read one deterministic replay value.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		ReadOnly: true, Idempotent: true, HandlerIdentity: "pysolate.typed-host-read-replay-handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}, ResultField: "body"},
		PreDispatch: &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "sources", Argument: "path"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
			Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
			MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
