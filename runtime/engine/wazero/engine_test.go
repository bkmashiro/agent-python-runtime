package wazero_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func unifiedCatalog(t *testing.T) *passplugin.Registry {
	t.Helper()
	catalog, err := passplugin.NewUnifiedCatalog(passplugin.UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: "sha256:" + strings.Repeat("a", 64),
		PreparedNumpyLoadConfigSHA256:   "sha256:" + strings.Repeat("b", 64),
		PreparedPureRegionConfigSHA256:  "sha256:" + strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestFactoryRequiresBrokerForProgrammaticToolsAndApproval(t *testing.T) {
	for _, configure := range []func(*runtimeconfig.RunConfig){
		func(config *runtimeconfig.RunConfig) {
			config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
			config.Mechanisms.ProgrammaticToolCalling = true
		},
		func(config *runtimeconfig.RunConfig) { config.Mechanisms.ApprovalSuspension = true },
	} {
		config := runtimeconfig.DefaultRunConfig()
		configure(&config)
		_, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config)
		if err == nil || !strings.Contains(err.Error(), "require a capability Broker factory") {
			t.Fatalf("factory error = %v", err)
		}
	}
}

func TestFactoryQuarantinesLegacyEarlyExecutionBehindResearchGate(t *testing.T) {
	for name, mechanisms := range map[string]runtimeconfig.MechanismSet{
		"semantic pre-dispatch": {SemanticAnalysis: true, SemanticPreDispatch: true, StagedObservation: true},
		"retained-prefix Guest": {Streaming: true, PrivateWorkspace: true},
	} {
		t.Run(name, func(t *testing.T) {
			config := runtimeconfig.DefaultRunConfig()
			config.Mechanisms = mechanisms
			if _, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
				t.Fatalf("default factory error=%v", err)
			}
		})
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, SemanticPreDispatch: true, StagedObservation: true}
	_, err := (wazeroengine.Factory{LegacyResearchExecution: true}).New(context.Background(), []byte("not wasm"), config)
	if err == nil || !strings.Contains(err.Error(), "requires a capability Broker factory") {
		t.Fatalf("research-gated factory error=%v", err)
	}
}

func TestFactoryRequiresBrokerForSplitPhaseCalls(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, SplitPhaseCalls: true, StagedObservation: true}
	_, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config)
	if err == nil || !strings.Contains(err.Error(), "requires a capability Broker factory") {
		t.Fatalf("factory error = %v", err)
	}
}

func TestFactoryLowersEnabledPassesBeforeArtifactParsing(t *testing.T) {
	catalog := unifiedCatalog(t)
	enabled, err := catalog.Enable(sourcepatch.SplitPhaseCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (wazeroengine.Factory{Passes: enabled}).New(context.Background(), []byte("not wasm"), runtimeconfig.DefaultRunConfig())
	if err == nil || !strings.Contains(err.Error(), "requires a capability Broker factory") {
		t.Fatalf("pass lowering did not select Future runtime: %v", err)
	}
}

func TestFactoryRejectsDirectOptimizationSelectionWhenPassCatalogIsBound(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SplitPhaseCalls = true
	_, err := (wazeroengine.Factory{Passes: unifiedCatalog(t)}).New(context.Background(), []byte("not wasm"), config)
	if !errors.Is(err, passplugin.ErrDirectOptimizationSelection) {
		t.Fatalf("direct optimization bypass error=%v", err)
	}
}

func TestFactoryRequiresValueSlotTableExactlyWhenSelected(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
	if _, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config); err == nil || !strings.Contains(err.Error(), "value slots require") {
		t.Fatalf("missing table error=%v", err)
	}
	object, _ := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("1"), "producer-v1", "input-v1", "run-one")
	table, _ := valueslot.NewTable([]valueslot.Entry{{
		Spec:   valueslot.SlotSpec{ID: "slot-one", SourceOccurrence: "line-1", ProducerIdentity: "producer-v1", InputIdentity: "input-v1", Kind: valueslot.KindJSONScalar, MaxBytes: 16, PrivacyPartition: "run-one", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1},
		Object: object, Strategy: valueslot.StrategyInlineJSON,
	}})
	if _, err := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), []byte("not wasm"), runtimeconfig.DefaultRunConfig()); err == nil || !strings.Contains(err.Error(), "require the value-slot mechanism") {
		t.Fatalf("disabled mechanism error=%v", err)
	}
}

func TestFactoryFailureClosesTransferredValueSlotTable(t *testing.T) {
	object, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("1"), "producer-v1", "input-v1", "run-one")
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{
		Spec:   valueslot.SlotSpec{ID: "slot-one", SourceOccurrence: "line-1", ProducerIdentity: "producer-v1", InputIdentity: "input-v1", Kind: valueslot.KindJSONScalar, MaxBytes: 16, PrivacyPartition: "run-one", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1},
		Object: object, Strategy: valueslot.StrategyInlineJSON,
	}})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
	if _, err := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), []byte("short"), config); err == nil {
		t.Fatal("expected short artifact failure")
	}
	if evidence := table.Evidence(); !evidence.Closed || !object.CanEvict() || object.ConsumerCount() != 0 {
		t.Fatalf("evidence=%+v consumers=%d can_evict=%t", evidence, object.ConsumerCount(), object.CanEvict())
	}
}

func TestFactoryRejectsInvalidMechanismsBeforeArtifactParsing(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{Streaming: true}
	_, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config)
	if !errors.Is(err, runtimeconfig.ErrInvalidMechanismSet) {
		t.Fatalf("factory error = %v", err)
	}
}

func TestRunStreamRequiresSelectedMechanism(t *testing.T) {
	runner, err := (wazeroengine.Factory{}).New(context.Background(), []byte{0, 97, 115, 109, 1, 0, 0, 0}, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	prepares := make(chan string)
	close(prepares)
	streamRunner, ok := runner.(interface {
		RunStream(context.Context, []byte, <-chan string) ([]byte, error)
	})
	if !ok {
		t.Fatal("runner lacks stream seam")
	}
	if _, err := streamRunner.RunStream(context.Background(), []byte(`{}`), prepares); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
		t.Fatalf("RunStream() error = %v", err)
	}
}

func TestLowLevelConstructorsCannotBypassLegacyResearchGate(t *testing.T) {
	for name, mechanisms := range map[string]runtimeconfig.MechanismSet{
		"semantic pre-dispatch": {SemanticAnalysis: true, SemanticPreDispatch: true, StagedObservation: true},
		"retained-prefix Guest": {Streaming: true, PrivateWorkspace: true},
	} {
		t.Run(name, func(t *testing.T) {
			config := runtimeconfig.DefaultRunConfig()
			config.Mechanisms = mechanisms
			if _, err := wazeroengine.New(context.Background(), []byte("not wasm"), config); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
				t.Fatalf("New error=%v", err)
			}
			factory := func(context.Context) (*capability.Broker, error) { return nil, nil }
			if _, err := wazeroengine.NewWithBrokerFactory(context.Background(), []byte("not wasm"), config, factory); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
				t.Fatalf("NewWithBrokerFactory error=%v", err)
			}
		})
	}
}

func TestFactoryIsFreshOnly(t *testing.T) {
	runner, err := (wazeroengine.Factory{}).New(context.Background(), []byte{0, 97, 115, 109, 1, 0, 0, 0}, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	if properties := runner.Properties(); properties.Backend != "wazero" || properties.ExecutionProfileID != "" {
		t.Fatalf("unexpected properties: %#v", properties)
	}
}

func TestFactoryDoesNotAcquireWorkspaceBeforeRequestAdmission(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	runner, err := (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "runner"}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	probe, err := manager.Acquire(ref, "preflight-probe")
	if err != nil {
		t.Fatalf("workspace acquired before request admission: %v", err)
	}
	if err := probe.Release(); err != nil {
		t.Fatal(err)
	}
	_, runErr := runner.Run(context.Background(), []byte(`{"run_id":"native","code":"result=1","inputs":{},"requirements":["shell"]}`), "")
	var unsupported *runtimeconfig.UnsupportedRunError
	if !errors.As(runErr, &unsupported) {
		t.Fatalf("run error=%v", runErr)
	}
	probe, err = manager.Acquire(ref, "post-rejection-probe")
	if err != nil {
		t.Fatalf("rejected request acquired workspace: %v", err)
	}
	if err := probe.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicProfileRejectsArtifactSubstitutionBeforeCompile(t *testing.T) {
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	actual := sha256.Sum256(wasm)
	expectedArtifact := "sha256:" + strings.Repeat("a", 64)
	if expectedArtifact == fmt.Sprintf("sha256:%x", actual[:]) {
		t.Fatal("test artifact unexpectedly matches substitution identity")
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(expectedArtifact, "artifact-substitution")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: expectedArtifact, ManifestSHA256: "sha256:" + strings.Repeat("b", 64),
		ImportRoots: []string{"sys"}, QualifiedImportRoots: []string{"sys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	config.DeterministicVerification = &deterministic
	if _, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config); err == nil {
		t.Fatal("substituted artifact admitted")
	}
}

func TestExecutionProfileRejectsArtifactSubstitutionWithoutDeterministicProfile(t *testing.T) {
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: "sha256:" + strings.Repeat("a", 64),
		ManifestSHA256: "sha256:" + strings.Repeat("b", 64), ImportRoots: []string{"sys"},
		QualifiedImportRoots: []string{"sys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	if _, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config); !errors.Is(err, runtimeconfig.ErrExecutionProfileArtifactMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestLegacyGuestFailsSourceValidationBeforeBroker(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	brokerCalled := false
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		brokerCalled = true
		return nil, nil
	}}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	_, err = runner.Run(context.Background(), []byte(`{"run_id":"r","code":"result = 1","inputs":{}}`), "")
	if err == nil || brokerCalled {
		t.Fatalf("legacy guest did not fail closed: err=%v broker_called=%v", err, brokerCalled)
	}
}

func TestSplitPhaseSetupFailureFinalizesSourceTimeCandidate(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	plan := splitPhaseCleanupPlan(t, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "setup-failure", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"setup-failure-read","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	if err := table.IssueOrReuse(context.Background(), "slot-setup-failure", request); err != nil {
		t.Fatal(err)
	}
	<-started
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SplitPhaseCalls = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return broker, nil
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	_, err = runner.Run(context.Background(), []byte(`{"run_id":"setup-failure","code":"result = 1","inputs":{}}`), "")
	if err == nil {
		t.Fatal("legacy Guest unexpectedly passed source validation")
	}
	snapshot := table.Snapshot()
	if snapshot.Cancelled != 1 || snapshot.Discarded != 1 || snapshot.PhysicalFinishes != 1 {
		t.Fatalf("source-time candidate leaked after setup failure: %+v", snapshot)
	}
}

func splitPhaseCleanupPlan(t *testing.T, handler capability.HandlerFunc) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"setup-failure"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "workspace.read_text", Version: "test.workspace.read-text.v1", Description: "setup cleanup read",
		EffectClass: capability.EffectWorkspaceRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "test.workspace.read-text.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "workspace", Method: "read_text", Arguments: []string{"path"}},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "workspace", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
			Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
