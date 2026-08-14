package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	nativeengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/native"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
	"github.com/bkmashiro/agent-python-runtime/runtime/verification"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func main() {
	runsc := flag.String("runsc", "", "runsc binary")
	rootfs := flag.String("rootfs", "", "verified rootfs")
	state := flag.String("state", "", "runtime-owned state root")
	workspaceParent := flag.String("workspace-parent", "", "runtime-owned workspace parent")
	image := flag.String("image-digest", "", "verified OCI image config digest")
	imageConfig := flag.String("image-config", "", "absolute OCI image config JSON path")
	rootfsDigest := flag.String("rootfs-digest", "", "rootfs tree digest")
	flag.Parse()
	if *runsc == "" || *rootfs == "" || *state == "" || *workspaceParent == "" || *image == "" || *imageConfig == "" || *rootfsDigest == "" {
		flag.Usage()
		os.Exit(2)
	}
	base, err := os.MkdirTemp(*workspaceParent, "workspace-probe-")
	if err != nil {
		fail(err)
	}
	if err := os.Chmod(base, 0o700); err != nil {
		fail(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		fail(err)
	}
	defer func() { _ = manager.Close(); _ = os.RemoveAll(base) }()
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		fail(err)
	}
	plan := capabilityPlan()
	artifact := runtimeconfig.ExecutionArtifact{SchemaVersion: runtimeconfig.ExecutionArtifactSchemaVersion, Backend: runtimeconfig.BackendNativeSandbox, Kind: runtimeconfig.ArtifactOCIImage, ProfileID: "native-python", Target: "linux/arm64", ImageDigest: *image, RootFSSHA256: *rootfsDigest}
	policy := plainPolicy()
	run := func(runID, code string, inputs any) (json.RawMessage, nativeengine.Evidence) {
		rawInputs, _ := json.Marshal(inputs)
		req := runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: rawInputs}
		raw, err := runtimeconfig.EncodeRunRequest(req)
		if err != nil {
			fail(err)
		}
		decision, err := placement.Analyze(req, runtimeconfig.StateWorkspaceRef, false, policy)
		if err != nil || decision.Backend != runtimeconfig.BackendNativeSandbox {
			fail(fmt.Errorf("workspace placement=%+v err=%v", decision, err))
		}
		backend := nativeengine.Backend{Config: nativeengine.Config{RunscPath: *runsc, RootFS: *rootfs, StateRoot: *state, Platform: "systrap", HostUDS: "open", NetworkMode: "sandbox", ImageDigest: *image, ImageConfigPath: *imageConfig, Artifact: artifact, Plan: plan, TrustedPrepare: plan.PythonPrelude(), Timeout: 10 * time.Second, MaxOutputBytes: 1 << 20, MemoryLimitBytes: 256 << 20, PidsLimit: 64, WorkspaceManager: manager, WorkspaceRef: ref}}
		placementPlan := placement.Plan{Decision: decision}
		output, evidence, err := backend.ExecuteWithEvidence(context.Background(), placementPlan, raw)
		if err != nil {
			fail(err)
		}
		if err := verification.VerifyNative(placementPlan, artifact, evidence); err != nil {
			fail(err)
		}
		return output, evidence
	}
	writeOutput, writeEvidence := run("workspace-write", `import json, pathlib
path = pathlib.Path("/workspace/value.json")
path.write_text(json.dumps({"value": inputs["value"]}), encoding="utf-8")
result = {"written": path.name, "value": inputs["value"]}`, map[string]any{"value": 73})
	readOutput, readEvidence := run("workspace-read", `import json, pathlib
path = pathlib.Path("/workspace/value.json")
result = {"value": json.loads(path.read_text(encoding="utf-8"))["value"], "exists": path.exists()}`, map[string]any{})
	portable := runtimeconfig.RunRequest{RunID: "portable-after-workspace", Code: "result = {'portable': True}", Inputs: json.RawMessage(`{}`)}
	portableDecision, err := placement.Analyze(portable, runtimeconfig.StatePortableValue, false, policy)
	if err != nil || portableDecision.Backend != runtimeconfig.BackendPysolateWASM {
		fail(fmt.Errorf("portable placement=%+v err=%v", portableDecision, err))
	}
	if writeEvidence.ExecutionID == readEvidence.ExecutionID || writeEvidence.WorkspaceTreeAfter == "" || writeEvidence.WorkspaceTreeAfter != readEvidence.WorkspaceTreeBefore {
		fail(fmt.Errorf("workspace lineage mismatch"))
	}
	result := struct {
		WorkspaceRef     string                `json:"workspace_ref"`
		Write            json.RawMessage       `json:"write"`
		Read             json.RawMessage       `json:"read"`
		WriteEvidence    nativeengine.Evidence `json:"write_evidence"`
		ReadEvidence     nativeengine.Evidence `json:"read_evidence"`
		PortableDecision placement.Decision    `json:"portable_decision"`
	}{string(ref), writeOutput, readOutput, writeEvidence, readEvidence, portableDecision}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

func plainPolicy() placement.Policy {
	d := func(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }
	shard, err := runtimeconfig.NewShardProfile(runtimeconfig.ShardProfileConfig{ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"json", "pathlib"}, ArtifactSHA256: d('a'), ManifestSHA256: d('b'), PreparedBaselineSHA256: d('c'), IdlePolicy: runtimeconfig.ShardIdleRetireWhenIdle})
	if err != nil {
		fail(err)
	}
	return placement.Policy{AnalyzerVersion: placement.AnalyzerStaticV1, PysolateAvailable: true, NativeAvailable: true, PlainShard: shard}
}

func capabilityPlan() *capability.Plan {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"workspace-probe"}`))
	if err != nil {
		fail(err)
	}
	err = registry.Register(capability.Spec{Name: "math.double", Version: "v1", Description: "double", EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "workspace-probe.math.double.v1", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), Python: &capability.PythonProjection{Module: "math_tools", Method: "double", Arguments: []string{"value"}, ResultField: "value"}}, grant, capability.HandlerFunc(func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":0}`), nil
	}))
	if err != nil {
		fail(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 4})
	if err != nil {
		fail(err)
	}
	return plan
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
