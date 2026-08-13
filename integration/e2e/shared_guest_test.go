package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestIdenticalLogicalInvocationsShareOneRealFreshGuest(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	code := "result = {'value': inputs['value'] + 1}"
	request, err := json.Marshal(map[string]any{
		"run_id": "shared-physical-guest", "code": code, "inputs": map[string]any{"value": 41},
	})
	if err != nil {
		t.Fatal(err)
	}

	flights := agentfunction.NewFlightGroup()
	functionEngine := agentfunction.Engine{Flights: flights}
	invocation, runConfig := sharedGuestInvocation(t, artifact, code, []string{"sys"}, []byte(`{"value":41}`))
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(ctx context.Context) (string, engine.Runner, error) {
			id := physical.Add(1)
			deadline := time.Now().Add(5 * time.Second)
			for flights.Stats().Waiters != 7 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
			return fmt.Sprintf("physical-%d", id), runner, err
		},
		Request: request, MaxResultBytes: 1024, DecodeResult: decodeSuccessfulGuestResult,
	}

	const logical = 8
	results := make(chan agentfunction.Result, logical)
	errorsChannel := make(chan error, logical)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(logical)
	for range logical {
		go func() {
			defer wait.Done()
			<-start
			result, err := functionEngine.ExecuteGuest(context.Background(), invocation, compute)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	leaders, waiters := 0, 0
	for result := range results {
		if string(result.Value) != `{"value":42}` || result.PhysicalExecutionID != "physical-1" {
			t.Fatalf("result=%+v", result)
		}
		switch result.Disposition {
		case agentfunction.Leader:
			leaders++
		case agentfunction.Waiter:
			waiters++
		}
	}
	if physical.Load() != 1 || leaders != 1 || waiters != logical-1 {
		t.Fatalf("physical=%d leaders=%d waiters=%d stats=%+v", physical.Load(), leaders, waiters, flights.Stats())
	}
	materializePrivateChildWorkspaces(t, []byte(`{"value":42}`), logical)
}

func materializePrivateChildWorkspaces(t *testing.T, value []byte, count int) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "children")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	refs := make(map[workspace.Ref]struct{}, count)
	hashes := make(map[string]struct{}, count)
	for index := range count {
		ref, err := manager.Create([]workspace.InitialFile{{Path: fmt.Sprintf("child-%d/result.json", index), Data: value}}, workspace.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		info, err := manager.Inspect(ref)
		if err != nil {
			t.Fatal(err)
		}
		refs[ref] = struct{}{}
		hashes[info.WorkspaceSHA256] = struct{}{}
	}
	if len(refs) != count || len(hashes) != count {
		t.Fatalf("private workspaces refs=%d hashes=%d want=%d", len(refs), len(hashes), count)
	}
}

func TestHostCallAttemptIsNotPublishedOrRetained(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	code := "import _agent_runtime_host\ntry:\n    _agent_runtime_host.call('{\"tool\":\"forbidden\",\"arguments\":{}}')\nexcept RuntimeError:\n    pass\nresult = {'caught': True}"
	request, err := json.Marshal(map[string]any{"run_id": "shared-host-call-negative", "code": code, "inputs": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	invocation, runConfig := sharedGuestInvocation(t, artifact, code, []string{"sys", "_agent_runtime_host"}, []byte(`{}`))
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(ctx context.Context) (string, engine.Runner, error) {
			id := physical.Add(1)
			runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
			return fmt.Sprintf("physical-negative-%d", id), runner, err
		},
		Request: request, MaxResultBytes: 1024, DecodeResult: decodeSuccessfulGuestResult,
	}
	functionEngine := agentfunction.Engine{Flights: agentfunction.NewFlightGroup()}
	for range 2 {
		result, err := functionEngine.ExecuteGuest(context.Background(), invocation, compute)
		if !errors.Is(err, agentfunction.ErrGuestNotShareable) || len(result.Value) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if physical.Load() != 2 {
		t.Fatalf("failed execution was retained: physical=%d", physical.Load())
	}
}

func decodeSuccessfulGuestResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	if response.Status != "ok" || len(response.Result) == 0 {
		return nil, errors.New("Guest result is not publishable")
	}
	return response.Result, nil
}

func sharedGuestInvocation(t *testing.T, artifact []byte, code string, allowedImports []string, inputs []byte) (agentfunction.Invocation, runtimeconfig.RunConfig) {
	t.Helper()
	artifactSHA := digestBytes(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: hashCharacter('9'),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		t.Fatal(err)
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(artifactSHA, "shared-compute")
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.DeterministicVerification = &deterministic
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: hashCharacter('1'), FunctionSourceSHA256: digestBytes([]byte(code)),
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: profileSHA,
		ImportClosureSHA256: digestBytes([]byte(strings.Join(allowedImports, "\x00"))), CanonicalInputs: inputs,
		ImmutableRootSHA256: []string{hashCharacter('6')}, DeterministicSettingsSHA256: deterministic.Identity(),
		OutputSchemaSHA256: hashCharacter('8'), PrivacyPartition: "shared-guest-test", PolicyEpochSHA256: hashCharacter('9'),
	}, config
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}
