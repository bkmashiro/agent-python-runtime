package capability_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestBranchBrokerStrictPrefixThenOverrideWithoutLiveHandler(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	prefix := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[{"id":"parent","score":1,"title":"Parent"}]}`))
	override := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[{"id":"child","score":2,"title":"Child"}]}`))
	override.OperationIndex = 1
	override.Evidence = overrideEvidence(override.Result)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "branch-child", Plan: plan, Branch: &capability.BranchConfig{
		ForkOperation: 1, PrefixEntries: []capability.TranscriptEntry{prefix}, Mode: capability.BranchOverride,
		SuffixEntries: []capability.TranscriptEntry{override},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for index, expected := range []string{"Parent", "Child"} {
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"call-`+string(rune('a'+index))+`","capability":"sources.demo_catalog","arguments":{}}`))
		if err != nil || !containsCodeOrStatus(response, "ok") || !containsJSONText(response, expected) {
			t.Fatalf("index=%d response=%s err=%v", index, response, err)
		}
	}
	if err := broker.Finalize(true); err != nil || handler.calls.Load() != 0 {
		t.Fatalf("finalize=%v live_calls=%d", err, handler.calls.Load())
	}
	transcript := broker.SnapshotTranscript()
	if len(transcript) != 2 || transcript[0].OperationIndex != 0 || transcript[1].OperationIndex != 1 || transcript[1].Evidence.Kind != "branch_override" {
		t.Fatalf("transcript=%+v", transcript)
	}
}

func TestBranchBrokerLiveSuffixUsesOnlySealedExternalReadHandler(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	prefix := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "branch-live-child", Plan: plan, Branch: &capability.BranchConfig{
		ForkOperation: 1, PrefixEntries: []capability.TranscriptEntry{prefix}, Mode: capability.BranchLiveSuffix,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, callID := range []string{"prefix", "live"} {
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"sources.demo_catalog","arguments":{}}`))
		if err != nil || !containsCodeOrStatus(response, "ok") {
			t.Fatalf("response=%s err=%v", response, err)
		}
	}
	if err := broker.Finalize(true); err != nil || handler.calls.Load() != 1 {
		t.Fatalf("finalize=%v live_calls=%d", err, handler.calls.Load())
	}
}

type forgedBranchEvidenceHandler struct{}

func (forgedBranchEvidenceHandler) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := (forgedBranchEvidenceHandler{}).CallWithEvidence(ctx, arguments)
	return result, err
}

func (forgedBranchEvidenceHandler) CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, capability.TransportEvidence, error) {
	result := json.RawMessage(`{"items":[]}`)
	return result, overrideEvidence(result), nil
}

func TestBranchBrokerRejectsLiveHandlerClaimingOverrideProvenance(t *testing.T) {
	plan := capturedPlan(t, forgedBranchEvidenceHandler{})
	prefix := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "branch-forged-provenance", Plan: plan, Branch: &capability.BranchConfig{
		ForkOperation: 1, PrefixEntries: []capability.TranscriptEntry{prefix}, Mode: capability.BranchLiveSuffix,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for index, callID := range []string{"prefix", "live"} {
		response, callErr := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"sources.demo_catalog","arguments":{}}`))
		if callErr != nil {
			t.Fatal(callErr)
		}
		if index == 1 && !containsCode(response, "invalid_result") {
			t.Fatalf("forged branch provenance accepted: %s", response)
		}
	}
	if err := broker.Finalize(false); err == nil {
		t.Fatal("poisoned live branch finalized")
	}
}

func TestBranchBrokerPoisonsMismatchAndRejectsUnusedSuffix(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	prefix := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	override := prefix
	override.OperationIndex = 1
	override.Evidence = overrideEvidence(override.Result)
	for name, calls := range map[string][]string{
		"mismatch": {`{"call_id":"bad","capability":"sources.demo_catalog","arguments":{"extra":true}}`},
		"unused":   {`{"call_id":"prefix","capability":"sources.demo_catalog","arguments":{}}`},
	} {
		t.Run(name, func(t *testing.T) {
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "branch-" + name, Plan: plan, Branch: &capability.BranchConfig{
				ForkOperation: 1, PrefixEntries: []capability.TranscriptEntry{prefix}, Mode: capability.BranchOverride,
				SuffixEntries: []capability.TranscriptEntry{override},
			}})
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range calls {
				_, _ = broker.Call(context.Background(), []byte(raw))
			}
			if err := broker.Finalize(false); err == nil {
				t.Fatal("incomplete/poisoned branch accepted")
			}
		})
	}
}

func TestConcurrentBranchChildrenHaveIsolatedConsumption(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	prefix := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	newChild := func(identity, title string) *capability.Broker {
		override := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[{"id":"child","score":2,"title":"`+title+`"}]}`))
		override.OperationIndex = 1
		override.Evidence = overrideEvidence(override.Result)
		broker, err := capability.NewBroker(capability.Config{RunIdentity: identity, Plan: plan, Branch: &capability.BranchConfig{
			ForkOperation: 1, PrefixEntries: []capability.TranscriptEntry{prefix}, Mode: capability.BranchOverride,
			SuffixEntries: []capability.TranscriptEntry{override},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return broker
	}
	children := []*capability.Broker{newChild("child-a", "A"), newChild("child-b", "B")}
	var wait sync.WaitGroup
	for index, broker := range children {
		wait.Add(1)
		go func(index int, broker *capability.Broker) {
			defer wait.Done()
			for operation := 0; operation < 2; operation++ {
				_, _ = broker.Call(context.Background(), []byte(`{"call_id":"child-`+string(rune('a'+index))+`-`+string(rune('0'+operation))+`","capability":"sources.demo_catalog","arguments":{}}`))
			}
		}(index, broker)
	}
	wait.Wait()
	for _, child := range children {
		if err := child.Finalize(true); err != nil || len(child.SnapshotTranscript()) != 2 {
			t.Fatalf("finalize=%v transcript=%+v", err, child.SnapshotTranscript())
		}
	}
}

func overrideEvidence(result json.RawMessage) capability.TransportEvidence {
	return capability.TransportEvidence{Kind: "branch_override", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: playback.SHA256(result)}
}

func containsJSONText(response []byte, expected string) bool {
	var decoded any
	if json.Unmarshal(response, &decoded) != nil {
		return false
	}
	encoded, _ := json.Marshal(decoded)
	return string(encoded) != "" && bytesContains(encoded, []byte(expected))
}

func bytesContains(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if string(value[index:index+len(fragment)]) == string(fragment) {
			return true
		}
	}
	return false
}
