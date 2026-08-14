package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
)

func main() {
	runsc := flag.String("runsc", "", "absolute runsc path")
	rootfs := flag.String("rootfs", "", "absolute immutable rootfs path")
	state := flag.String("state", "", "runtime-owned state root")
	image := flag.String("image-digest", "", "verified OCI image config digest")
	imageConfig := flag.String("image-config", "", "absolute OCI image config JSON path")
	rootfsDigest := flag.String("rootfs-digest", "", "verified deterministic rootfs digest")
	inspectRootFS := flag.Bool("inspect-rootfs", false, "print deterministic rootfs digest and exit")
	platform := flag.String("platform", "systrap", "runsc platform")
	scenario := flag.String("scenario", "success", "success, timeout, crash, oom, output-limit, or network-deny")
	timeout := flag.Duration("timeout", 20*time.Second, "execution timeout")
	memoryLimit := flag.Int64("memory-limit", 256<<20, "OCI cgroup memory limit in bytes")
	pidsLimit := flag.Int64("pids-limit", 64, "OCI cgroup pids limit")
	flag.Parse()
	if *inspectRootFS {
		if *rootfs == "" {
			flag.Usage()
			os.Exit(2)
		}
		identity, err := nativeengine.RootFSIdentity(*rootfs)
		if err != nil {
			fail(err)
		}
		fmt.Println(identity)
		return
	}
	if *runsc == "" || *rootfs == "" || *state == "" || *image == "" || *imageConfig == "" || *rootfsDigest == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := os.MkdirAll(*state, 0o700); err != nil {
		fail(err)
	}
	plan := capabilityPlan()
	code, maxOutput := scenarioCode(*scenario)
	request := runtimeconfig.RunRequest{
		RunID:  "gvisor-native-" + *scenario,
		Code:   code,
		Inputs: json.RawMessage(`{"value":21}`), Requirements: []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeatureShell, runtimeconfig.RequiredFeatureSubprocess},
	}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		fail(err)
	}
	shard, err := runtimeconfig.NewShardProfile(runtimeconfig.ShardProfileConfig{ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"json"}, ArtifactSHA256: digest("a"), ManifestSHA256: digest("b"), PreparedBaselineSHA256: digest("c"), IdlePolicy: runtimeconfig.ShardIdleRetireWhenIdle})
	if err != nil {
		fail(err)
	}
	decision, err := placement.Analyze(request, runtimeconfig.StatePortableValue, false, placement.Policy{AnalyzerVersion: placement.AnalyzerStaticV1, PysolateAvailable: true, NativeAvailable: true, PlainShard: shard})
	if err != nil {
		fail(err)
	}
	artifact := runtimeconfig.ExecutionArtifact{SchemaVersion: runtimeconfig.ExecutionArtifactSchemaVersion, Backend: runtimeconfig.BackendNativeSandbox, Kind: runtimeconfig.ArtifactOCIImage, ProfileID: "native-python", Target: "linux/arm64", ImageDigest: *image, RootFSSHA256: *rootfsDigest}
	backend := nativeengine.Backend{Config: nativeengine.Config{RunscPath: *runsc, RootFS: *rootfs, StateRoot: *state, Platform: *platform, HostUDS: "open", NetworkMode: "sandbox", ImageDigest: *image, ImageConfigPath: *imageConfig, Artifact: artifact, Plan: plan, TrustedPrepare: plan.PythonPrelude(), Timeout: *timeout, MaxOutputBytes: maxOutput, MemoryLimitBytes: *memoryLimit, PidsLimit: *pidsLimit}}
	placementPlan := placement.Plan{Decision: decision}
	output, evidence, err := backend.ExecuteWithEvidence(context.Background(), placementPlan, raw)
	evidenceVerified := false
	if evidence.ExecutionID != "" {
		if verifyErr := verification.VerifyNativeAttempt(placementPlan, artifact, evidence, err == nil); verifyErr != nil {
			err = errors.Join(err, verifyErr)
		} else {
			evidenceVerified = true
		}
	}
	result := struct {
		Response         json.RawMessage       `json:"response,omitempty"`
		ResponseBytes    int                   `json:"response_bytes"`
		ResponseSHA256   string                `json:"response_sha256,omitempty"`
		Evidence         nativeengine.Evidence `json:"evidence"`
		EvidenceVerified bool                  `json:"evidence_verified"`
		Lifecycle        any                   `json:"lifecycle,omitempty"`
		Error            string                `json:"error,omitempty"`
	}{ResponseBytes: len(output), Evidence: evidence, EvidenceVerified: evidenceVerified}
	if evidence.ExecutionID != "" {
		result.Lifecycle = evidence.Lifecycle()
	}
	if json.Valid(output) {
		result.Response = output
	} else if len(output) != 0 {
		digest := sha256.Sum256(output)
		result.ResponseSHA256 = fmt.Sprintf("sha256:%x", digest[:])
	}
	if err != nil {
		result.Error = err.Error()
	}
	encoded, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		fail(marshalErr)
	}
	fmt.Println(string(encoded))
	if err != nil {
		os.Exit(1)
	}
}

func scenarioCode(name string) (string, int) {
	switch name {
	case "success":
		return `import pathlib, subprocess
value = math_tools.double(inputs["value"])
path = pathlib.Path("/tmp/native-workspace-value.txt")
path.write_text(str(value))
result = {"value": value, "shell": subprocess.check_output(["/bin/sh", "-c", "printf native-shell-ok"]).decode(), "scratch": path.read_text()}`, 1 << 20
	case "timeout":
		return "import time\ntime.sleep(30)\nresult = {'unexpected': True}", 1 << 20
	case "crash":
		return "import os\nos._exit(17)", 1 << 20
	case "oom":
		return "payload = bytearray(512 * 1024 * 1024)\nfor offset in range(0, len(payload), 4096): payload[offset] = 1\nresult = {'unexpected': len(payload)}", 1 << 20
	case "output-limit":
		return "print('x' * 2000000)\nresult = {'unexpected': True}", 64 << 10
	case "network-deny":
		return `import socket
try:
    socket.create_connection(("1.1.1.1", 53), timeout=0.2)
    network = "unexpected-success"
except OSError as exc:
    network = type(exc).__name__
result = {"network": network}`, 1 << 20
	default:
		fail(fmt.Errorf("unknown scenario %q", name))
		return "", 0
	}
}

func capabilityPlan() *capability.Plan {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"probe"}`))
	if err != nil {
		fail(err)
	}
	err = registry.Register(capability.Spec{Name: "math.double", Version: "v1", Description: "double an integer", EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "probe.math.double.v1",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		Python: &capability.PythonProjection{Module: "math_tools", Method: "double", Arguments: []string{"value"}, ResultField: "value"}}, grant, capability.HandlerFunc(func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
		var value struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(input, &value); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]int{"value": value.Value * 2})
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
func digest(character string) string { return "sha256:" + strings.Repeat(character, 64) }
func fail(err error)                 { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
