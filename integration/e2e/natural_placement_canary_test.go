package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
)

type naturalPlacementRequest struct {
	SchemaVersion string `json:"schema_version"`
	Source        struct {
		Dataset      string `json:"dataset"`
		Revision     string `json:"revision"`
		LicenseID    string `json:"license_id"`
		SourceSHA256 string `json:"source_sha256"`
	} `json:"source"`
	Task struct {
		RecordSHA256       string         `json:"record_sha256"`
		RecordBodySHA256   string         `json:"record_body_sha256"`
		TrajectorySHA256   string         `json:"trajectory_sha256"`
		Language           string         `json:"language"`
		Resolved           int            `json:"resolved"`
		TrajectoryMessages int            `json:"trajectory_messages"`
		ToolNameCounts     map[string]int `json:"tool_name_counts"`
	} `json:"task"`
	PlacementContract struct {
		RequiredFeatures         []runtimeconfig.RequiredFeature `json:"required_features"`
		MutableWorkspaceObserved bool                            `json:"mutable_workspace_observed"`
		ExpectedBackend          string                          `json:"expected_backend"`
		ExpectedReason           string                          `json:"expected_reason"`
		PysolateGuestCalls       int                             `json:"pysolate_guest_calls"`
	} `json:"placement_contract"`
	PrivateBodiesIncluded bool `json:"private_bodies_included"`
}

type placementCountingBackend struct {
	calls int
	last  placement.Plan
}

func (backend *placementCountingBackend) Execute(_ context.Context, plan placement.Plan, _ []byte) ([]byte, error) {
	backend.calls++
	backend.last = plan
	return []byte(`{"status":"placement-control-accepted"}`), nil
}

func naturalPlacementPolicy(t *testing.T, native bool) placement.Policy {
	t.Helper()
	shard, err := runtimeconfig.NewShardProfile(runtimeconfig.ShardProfileConfig{
		ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"agent_runtime", "json", "math", "sys"},
		ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IdlePolicy:     runtimeconfig.ShardIdleRetireWhenIdle,
	})
	if err != nil {
		t.Fatal(err)
	}
	return placement.Policy{AnalyzerVersion: placement.AnalyzerStaticV1, PysolateAvailable: true, NativeAvailable: native, PlainShard: shard}
}

func TestNaturalOpenSWEPlacementControlBeforeGuest(t *testing.T) {
	requestPath := os.Getenv("PYSOLATE_NATURAL_PLACEMENT_REQUEST")
	evidenceDir := os.Getenv("PYSOLATE_NATURAL_PLACEMENT_EVIDENCE_DIR")
	if requestPath == "" || evidenceDir == "" {
		t.Skip("natural placement private evidence paths are not configured")
	}
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture naturalPlacementRequest
	if err := json.Unmarshal(requestBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != "pysolate.natural-placement-request.v1" || fixture.Source.Dataset != "nvidia/Open-SWE-Traces" || fixture.Task.Language != "python" || fixture.Task.Resolved != 1 || fixture.PrivateBodiesIncluded {
		t.Fatalf("invalid placement fixture: %+v", fixture)
	}
	if fmt.Sprint(fixture.PlacementContract.RequiredFeatures) != "[shell subprocess]" || !fixture.PlacementContract.MutableWorkspaceObserved {
		t.Fatalf("invalid placement requirements: %+v", fixture.PlacementContract)
	}
	inputs, err := json.Marshal(map[string]any{"task_record_sha256": fixture.Task.RecordSHA256, "trajectory_sha256": fixture.Task.TrajectorySHA256})
	if err != nil {
		t.Fatal(err)
	}
	runRequest := runtimeconfig.RunRequest{
		RunID: "natural-placement-open-swe-v1", Code: "result = inputs['task_record_sha256']", Inputs: inputs,
		Requirements: append([]runtimeconfig.RequiredFeature(nil), fixture.PlacementContract.RequiredFeatures...),
	}
	raw, err := json.Marshal(runRequest)
	if err != nil {
		t.Fatal(err)
	}
	wasm := &placementCountingBackend{}
	native := &placementCountingBackend{}
	selectedOrchestrator := placement.Orchestrator{Policy: naturalPlacementPolicy(t, true), Pysolate: wasm, Native: native}
	selected, err := selectedOrchestrator.Execute(context.Background(), raw, runtimeconfig.StatePortableValue, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Decision.Status != placement.StatusSelected || selected.Decision.Backend != runtimeconfig.BackendNativeSandbox || selected.Decision.Reason != placement.ReasonRequiredNativeFeature || wasm.calls != 0 || native.calls != 1 || selected.Promotion != nil {
		t.Fatalf("unexpected selected lane: decision=%+v wasm=%d native=%d promotion=%+v", selected.Decision, wasm.calls, native.calls, selected.Promotion)
	}
	unavailableWASM := &placementCountingBackend{}
	unavailableNative := &placementCountingBackend{}
	unavailableOrchestrator := placement.Orchestrator{Policy: naturalPlacementPolicy(t, false), Pysolate: unavailableWASM, Native: unavailableNative}
	unavailable, unavailableErr := unavailableOrchestrator.Execute(context.Background(), raw, runtimeconfig.StatePortableValue, false)
	if !errors.Is(unavailableErr, placement.ErrBackendUnavailable) || unavailable.Decision.Status != placement.StatusUnavailable || unavailable.Decision.Backend != "" || unavailable.Decision.Reason != placement.ReasonNativeUnavailable || unavailableWASM.calls != 0 || unavailableNative.calls != 0 {
		t.Fatalf("unexpected unavailable lane: err=%v decision=%+v wasm=%d native=%d", unavailableErr, unavailable.Decision, unavailableWASM.calls, unavailableNative.calls)
	}
	requestDigest := sha256.Sum256(raw)
	manifest := map[string]any{
		"schema_version":        "pysolate.natural-placement-evidence.v1",
		"source_request_sha256": fmt.Sprintf("sha256:%x", sha256.Sum256(requestBytes)),
		"run_request_sha256":    fmt.Sprintf("sha256:%x", requestDigest),
		"source":                fixture.Source,
		"task":                  fixture.Task,
		"requirements":          fixture.PlacementContract.RequiredFeatures,
		"lanes": map[string]any{
			"selected_native":    map[string]any{"decision": selected.Decision, "pysolate_backend_calls": wasm.calls, "native_backend_calls": native.calls, "promotion": false, "workspace_started": false, "effects_started": false},
			"native_unavailable": map[string]any{"decision": unavailable.Decision, "pysolate_backend_calls": unavailableWASM.calls, "native_backend_calls": unavailableNative.calls, "error": "backend_unavailable", "workspace_started": false, "effects_started": false},
		},
		"model_calls":             0,
		"private_bodies_included": false,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(evidenceDir, "placement-evidence.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
