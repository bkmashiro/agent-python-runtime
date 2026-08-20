package capability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	sourcebindingtrusted "github.com/bkmashiro/agent-python-runtime/runtime/internal/sourcebinding"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestBrokerUsesHostRegistryAndBoundedCalls(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"hello"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`))
	if err != nil || !json.Valid(response) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if broker.Calls() != 1 || len(broker.SnapshotReceipts()) != 1 || broker.SnapshotReceipts()[0].Outcome != "ok" {
		t.Fatalf("unexpected broker evidence: calls=%d receipts=%#v", broker.Calls(), broker.SnapshotReceipts())
	}
	response, err = broker.Call(context.Background(), []byte(`{"call_id":"two","capability":"workspace.read_text","arguments":{}}`))
	if err != nil || !containsCode(response, "call_budget_exceeded") {
		t.Fatalf("budget response=%s err=%v", response, err)
	}
}

func TestBrokerEmitsBodyFreeIntentAndStartedAroundLiveHandler(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"hello"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &callLifecycleRecorder{}
	if err := broker.AttachCallLifecycleObserver(recorder); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`)); err != nil {
		t.Fatal(err)
	}
	if len(recorder.observations) != 2 || recorder.observations[0].Phase != capability.CallLifecycleIntent ||
		recorder.observations[1].Phase != capability.CallLifecycleStarted || recorder.observations[0].CallID != "one" ||
		recorder.observations[0].ArgumentsSHA256 == "" || recorder.observations[0].CapabilityPlanSHA256 != plan.Identity() {
		t.Fatalf("lifecycle observations=%+v", recorder.observations)
	}
	if err := broker.AttachCallLifecycleObserver(&callLifecycleRecorder{}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("late observer attach err=%v", err)
	}
}

type callLifecycleRecorder struct {
	observations []capability.CallLifecycleObservation
}

func (recorder *callLifecycleRecorder) ObserveCallLifecycle(_ context.Context, observed capability.CallLifecycleObservation) {
	recorder.observations = append(recorder.observations, observed)
}

func TestBrokerDeniesUnregisteredTool(t *testing.T) {
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"network.fetch","arguments":{}}`))
	if err != nil || !containsCode(response, "capability_denied") {
		t.Fatalf("denial response=%s err=%v", response, err)
	}
	if receipts := broker.SnapshotReceipts(); len(receipts) != 1 || receipts[0].Outcome != "denied" {
		t.Fatalf("denial receipt=%#v", receipts)
	}
}

func TestStreamingBrokerDeniesWriteEvenThroughRawBridge(t *testing.T) {
	var calls atomic.Uint32
	registry := capability.NewRegistry()
	spec := basicSpec("workspace.write_text", "test.workspace.write-text.v1")
	spec.EffectClass = capability.EffectWorkspaceWrite
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"stream-write","capability":"workspace.write_text","arguments":{}}`)
	response, err := broker.CallStreaming(context.Background(), request)
	if err != nil || !containsCode(response, "streaming_write_denied") || calls.Load() != 0 {
		t.Fatalf("response=%s calls=%d err=%v", response, calls.Load(), err)
	}
	response, err = broker.Call(context.Background(), []byte(`{"call_id":"sealed-write","capability":"workspace.write_text","arguments":{}}`))
	if err != nil || containsCode(response, "streaming_write_denied") || calls.Load() != 1 {
		t.Fatalf("sealed response=%s calls=%d err=%v", response, calls.Load(), err)
	}
}

func TestBrokerClaimsExactStagedObservationWithoutCallingLiveHandler(t *testing.T) {
	var liveCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		liveCalls.Add(1)
		return json.RawMessage(`{"text":"live"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimer := &stagedClaimer{capability: "workspace.read_text", arguments: json.RawMessage(`{"path":"note.txt"}`), result: json.RawMessage(`{"text":"staged"}`)}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan, StagedClaimer: claimer, SemanticPreDispatch: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`))
	if err != nil || liveCalls.Load() != 0 || !json.Valid(response) || !containsResult(response, "staged") {
		t.Fatalf("response=%s live=%d err=%v", response, liveCalls.Load(), err)
	}
	if receipts := broker.SnapshotReceipts(); len(receipts) != 1 || receipts[0].Outcome != "ok" {
		t.Fatalf("receipts=%#v", receipts)
	}
}

func TestBrokerFailsClosedWhenConfiguredStageRejectsDynamicClaim(t *testing.T) {
	var liveCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		liveCalls.Add(1)
		return json.RawMessage(`{"text":"live"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	claimer := &stagedClaimer{claimErr: errors.New("exact claim mismatch")}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan, StagedClaimer: claimer, SemanticPreDispatch: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"other.txt"}}`))
	if err != nil || liveCalls.Load() != 0 || !containsCode(response, "staged_observation_mismatch") {
		t.Fatalf("response=%s live=%d err=%v", response, liveCalls.Load(), err)
	}
}

func TestBothPresentationSeparatesDirectAndProgrammaticSequences(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"ok"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan, ProgrammaticParentCallID: "parent", AllowDirectCalls: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, callID := range []string{"direct-1", "parent:program:1"} {
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"workspace.read_text","arguments":{"path":"x"}}`))
		if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
			t.Fatalf("call=%s response=%s err=%v", callID, response, err)
		}
	}
	nearMatch, err := broker.Call(context.Background(), []byte(`{"call_id":"other:program:1","capability":"workspace.read_text","arguments":{"path":"x"}}`))
	if err != nil || !strings.Contains(string(nearMatch), "programmatic_call_identity_mismatch") {
		t.Fatalf("near-match response=%s err=%v", nearMatch, err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"parent:program:2","capability":"workspace.read_text","arguments":{"path":"x"}}`))
	if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
		t.Fatalf("second child response=%s err=%v", response, err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 3 || receipts[0].ParentCallID != "" || receipts[1].ParentCallID != "parent" || receipts[2].ParentCallID != "parent" {
		t.Fatalf("receipts=%#v", receipts)
	}
}

func TestBrokerBindsSourceOnlyToExactProgrammaticCalls(t *testing.T) {
	registry := capability.NewRegistry()
	var handlerCalls atomic.Uint32
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"text":"ok"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	binding := receipt.SourceBinding{
		SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound,
		DocumentID: "sha256:" + strings.Repeat("1", 64), SourceSHA256: "sha256:" + strings.Repeat("2", 64),
		OccurrenceID: "sha256:" + strings.Repeat("3", 64), Capability: "workspace.read_text", DynamicOccurrence: 1,
		StartLine: 2, StartColumn: 9, EndLine: 2, EndColumn: 38,
	}
	resolver, recording := newRecordingSourceResolver(t, binding)
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "host-run", Plan: plan, ProgrammaticParentCallID: "parent", AllowDirectCalls: true, SourceResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sourceBoundResponse []byte
	for _, callID := range []string{"direct-1", "parent:program:1"} {
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"workspace.read_text","arguments":{"path":"x"}}`))
		if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
			t.Fatalf("call=%s response=%s err=%v", callID, response, err)
		}
		if callID == "parent:program:1" {
			sourceBoundResponse = append([]byte(nil), response...)
		}
	}
	receipts := broker.SnapshotReceipts()
	if handlerCalls.Load() != 2 || len(receipts) != 2 || receipts[0].Source != nil || receipts[1].Source == nil || !receipt.ValidIdentity(receipts[1]) {
		t.Fatalf("calls=%d receipts=%#v", handlerCalls.Load(), receipts)
	}
	receipts[1].Source.StartLine = 999
	freshReceipts := broker.SnapshotReceipts()
	if freshReceipts[1].Source == nil || freshReceipts[1].Source.StartLine != binding.StartLine || !receipt.ValidIdentity(freshReceipts[1]) {
		t.Fatalf("source receipt snapshot was not defensive: %#v", freshReceipts)
	}
	receipts = freshReceipts
	if len(recording.requests) != 1 || !recording.requests[0].Programmatic || string(recording.requests[0].Arguments) != `{"path":"x"}` {
		t.Fatalf("resolver requests=%#v", recording.requests)
	}
	baseline, err := capability.NewBroker(capability.Config{RunIdentity: "baseline-run", Plan: plan, ProgrammaticParentCallID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	baselineResponse, err := baseline.Call(context.Background(), []byte(`{"call_id":"parent:program:1","capability":"workspace.read_text","arguments":{"path":"x"}}`))
	if err != nil || !bytes.Equal(sourceBoundResponse, baselineResponse) || handlerCalls.Load() != 3 {
		t.Fatalf("source-bound response drift: bound=%s baseline=%s calls=%d err=%v", sourceBoundResponse, baselineResponse, handlerCalls.Load(), err)
	}
	baselineReceipts := baseline.SnapshotReceipts()
	if len(baselineReceipts) != 1 || baselineReceipts[0].Source != nil || baselineReceipts[0].Outcome != receipts[1].Outcome {
		t.Fatalf("baseline receipts=%#v source-bound=%#v", baselineReceipts, receipts)
	}
}

func TestBrokerRejectsInvalidResolvedSourceBeforeDispatch(t *testing.T) {
	registry := capability.NewRegistry()
	var handlerCalls atomic.Uint32
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"text":"unexpected"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	resolver, _ := newRecordingSourceResolver(t, receipt.SourceBinding{})
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "host-run", Plan: plan, ProgrammaticParentCallID: "parent", SourceResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"parent:program:1","capability":"workspace.read_text","arguments":{"path":"x"}}`))
	if err != nil || !containsCode(response, "source_binding_invalid") || handlerCalls.Load() != 0 {
		t.Fatalf("response=%s calls=%d err=%v", response, handlerCalls.Load(), err)
	}
}

type recordingSourceResolver struct {
	binding  receipt.SourceBinding
	requests []capability.SourceBindingRequest
}

func newRecordingSourceResolver(t *testing.T, binding receipt.SourceBinding) (*capability.SourceBindingResolver, *recordingSourceResolver) {
	t.Helper()
	recording := &recordingSourceResolver{binding: binding}
	authority := sourcebindingtrusted.New(func(request sourcebindingtrusted.Request) (receipt.SourceBinding, bool) {
		recording.requests = append(recording.requests, request)
		return recording.binding, true
	})
	resolver, err := capability.NewSourceBindingResolver(authority)
	if err != nil {
		t.Fatal(err)
	}
	return resolver, recording
}

func TestProgrammaticBrokerRequiresExactParentBoundChildSequence(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"hello"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan, ProgrammaticParentCallID: "parent-call"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"call_id":"parent-call:program:2","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`,
		`{"call_id":"near-parent:program:1","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`,
	} {
		response, err := broker.Call(context.Background(), []byte(raw))
		if err != nil || !containsCode(response, "programmatic_call_identity_mismatch") {
			t.Fatalf("response=%s err=%v", response, err)
		}
	}
	valid, err := broker.Call(context.Background(), []byte(`{"call_id":"parent-call:program:1","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`))
	if err != nil || !containsResult(valid, "hello") {
		t.Fatalf("response=%s err=%v", valid, err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 1 || receipts[0].CallID != "parent-call:program:1" || receipts[0].ParentCallID != "parent-call" {
		t.Fatalf("receipts=%#v", receipts)
	}
}

type stagedClaimer struct {
	capability string
	arguments  json.RawMessage
	result     json.RawMessage
	claimErr   error
}

func (claimer *stagedClaimer) Finalize(bool) error { return nil }

func (claimer *stagedClaimer) Claim(_ context.Context, capabilityName string, arguments json.RawMessage) (capability.StagedCapabilityOutcome, error) {
	if claimer.claimErr != nil {
		return capability.StagedCapabilityOutcome{}, claimer.claimErr
	}
	if capabilityName != claimer.capability || string(arguments) != string(claimer.arguments) {
		return capability.StagedCapabilityOutcome{}, errors.New("exact staged claim mismatch")
	}
	return capability.StagedCapabilityOutcome{Result: append(json.RawMessage(nil), claimer.result...)}, nil
}

func TestBrokerRequiresExplicitSemanticPreDispatchEnablement(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"live"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	claimer := &stagedClaimer{capability: "workspace.read_text", arguments: json.RawMessage(`{"path":"a"}`), result: json.RawMessage(`{"text":"staged"}`)}
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "disabled-stage", Plan: plan, StagedClaimer: claimer}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("disabled staged broker error=%v", err)
	}
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "missing-claimer", Plan: plan, SemanticPreDispatch: true}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("enabled broker without claimer error=%v", err)
	}
}

func TestBrokerRejectsStagedClaimerForUnqualifiedCapability(t *testing.T) {
	var liveCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("plain.read", "test.plain.read.v1"), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		liveCalls.Add(1)
		return json.RawMessage(`{}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	claimer := &stagedClaimer{capability: "plain.read", arguments: json.RawMessage(`{}`), result: json.RawMessage(`{}`)}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "unqualified-stage", Plan: plan, StagedClaimer: claimer, SemanticPreDispatch: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"plain.read","arguments":{}}`))
	if err != nil || liveCalls.Load() != 0 || !containsCode(response, "staged_observation_unqualified") {
		t.Fatalf("response=%s live=%d err=%v", response, liveCalls.Load(), err)
	}
}

func TestPreparedPreDispatchEnforcesHostResultByteLimit(t *testing.T) {
	registry := capability.NewRegistry()
	spec := stagedTestSpec()
	spec.PreDispatch.MaxResultBytes = 16
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"this result is too large"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.PreparePreDispatch(spec.Name, json.RawMessage(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := prepared.Call(context.Background())
	if err != nil || outcome.ErrorCode != "invalid_result" || len(outcome.Result) != 0 || outcome.PhysicalResultBytes <= 16 {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestPreparedPreDispatchExecutesEligibleHandlerExactlyOnce(t *testing.T) {
	var calls atomic.Uint32
	registry := capability.NewRegistry()
	spec := basicSpec("sources.read", "test.sources.read.v1")
	spec.EffectClass = capability.EffectExternalRead
	spec.InputSchema = json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`)
	spec.OutputSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
	spec.Python = &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}}
	spec.ReadOnly, spec.Idempotent = true, true
	spec.PreDispatch = &capability.PreDispatchContract{
		Resource:  capability.ResourceReference{Namespace: "source", Argument: "key"},
		Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
		MaxResultBytes: 1 << 20, CostUnits: 1,
	}
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"value":"ready"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	prepared, err := plan.PreparePreDispatch("sources.read", json.RawMessage(`{"key":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Call(context.Background())
	if err != nil || string(result.Result) != `{"value":"ready"}` || calls.Load() != 1 {
		t.Fatalf("result=%s calls=%d err=%v", result.Result, calls.Load(), err)
	}
	if _, err := prepared.Call(context.Background()); !errors.Is(err, capability.ErrPreDispatchAlreadyStarted) || calls.Load() != 1 {
		t.Fatalf("second call err=%v calls=%d", err, calls.Load())
	}
}

func TestPreparedPreDispatchRejectsCapturedPlaybackUntilTranscriptBindingExists(t *testing.T) {
	registry := capability.NewRegistry()
	spec := stagedTestSpec()
	spec.Playback = capability.PlaybackCaptured
	if err := registry.Register(spec, basicGrant(t), &countingEvidenceHandler{}); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if _, err := plan.PreparePreDispatch("workspace.read_text", json.RawMessage(`{"path":"a.txt"}`)); !errors.Is(err, capability.ErrPreDispatchUnavailable) {
		t.Fatalf("prepare error=%v", err)
	}
}

func stagedTestSpec() capability.Spec {
	spec := basicSpec("workspace.read_text", "test.workspace.read-text.v1")
	spec.EffectClass = capability.EffectWorkspaceRead
	spec.InputSchema = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	spec.OutputSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
	spec.Python = &capability.PythonProjection{Module: "workspace", Method: "read_text", Arguments: []string{"path"}}
	spec.ReadOnly, spec.Idempotent = true, true
	spec.PreDispatch = &capability.PreDispatchContract{
		Resource:  capability.ResourceReference{Namespace: "workspace", Argument: "path"},
		Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
		MaxResultBytes: 1 << 20, CostUnits: 1,
	}
	return spec
}

func containsResult(response []byte, text string) bool {
	var decoded struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	return json.Unmarshal(response, &decoded) == nil && decoded.Result.Text == text
}

func containsCode(response []byte, code string) bool {
	var decoded struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(response, &decoded) == nil && decoded.Error != nil && decoded.Error.Code == code
}
