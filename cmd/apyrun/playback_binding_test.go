package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestPlaybackBundleStages0600AndPublishesAtomically(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run.playback.json")
	staged, err := stagePlaybackBundle(output, testPlaybackBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("output visible before publish: %v", err)
	}
	info, err := os.Stat(staged.temporaryPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stage info=%v err=%v", info, err)
	}
	if err := staged.publish(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := playback.Decode(data)
	if err != nil || decoded.Identity == "" {
		t.Fatalf("decode=%#v err=%v", decoded, err)
	}
	if err := staged.discard(); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackBundleStagePreservesExistingOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run.playback.json")
	prior := []byte("protected-prior-bundle")
	if err := os.WriteFile(output, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stagePlaybackBundle(output, testPlaybackBundle(t)); err == nil {
		t.Fatal("existing output was accepted")
	}
	actual, err := os.ReadFile(output)
	if err != nil || string(actual) != string(prior) {
		t.Fatalf("prior output=%q err=%v", actual, err)
	}
}

func TestPlaybackBundleDiscardLeavesNoOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run.playback.json")
	staged, err := stagePlaybackBundle(output, testPlaybackBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("output exists after discard: %v", err)
	}
}

func TestPlaybackAdmissionAndOutcomeBindHostContext(t *testing.T) {
	policy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	spec, grant, err := capability.DemoCatalogDefinition(policy)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	wasm := []byte("same-artifact")
	requestSHA256 := playback.SHA256([]byte("same-request"))
	profileSHA256, err := executionProfileSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	result := []byte(`{"ok":true}`)
	resultSHA256, _ := playback.CanonicalSHA256(result)
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: plan.Identity(), RequestSHA256: requestSHA256,
		ArtifactSHA256: playback.SHA256(wasm), ExecutionProfileSHA256: profileSHA256,
		ExpectedStatus: "ok", ExpectedResultSHA256: resultSHA256, Grants: plan.Grants(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlaybackAdmission(bundle, plan, config, wasm, requestSHA256, nil); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*playback.Bundle){
		func(value *playback.Bundle) { value.CapabilityPlanSHA256 = "sha256:" + repeatByte('1', 64) },
		func(value *playback.Bundle) { value.RequestSHA256 = "sha256:" + repeatByte('2', 64) },
		func(value *playback.Bundle) { value.ArtifactSHA256 = "sha256:" + repeatByte('3', 64) },
		func(value *playback.Bundle) { value.ExecutionProfileSHA256 = "sha256:" + repeatByte('4', 64) },
		func(value *playback.Bundle) { value.Grants[0].PolicySHA256 = "sha256:" + repeatByte('5', 64) },
		func(value *playback.Bundle) {
			value.InitialWorkspaceSHA256 = "sha256:" + repeatByte('6', 64)
			value.FinalWorkspaceSHA256 = "sha256:" + repeatByte('7', 64)
		},
	}
	for index, mutate := range mutations {
		changed := bundle
		changed.Grants = append([]capability.GrantBinding(nil), bundle.Grants...)
		mutate(&changed)
		if err := validatePlaybackAdmission(changed, plan, config, wasm, requestSHA256, nil); err == nil {
			t.Fatalf("admission mutation %d accepted", index)
		}
	}
	response := runtimeconfig.RunResponse{Status: runtimeconfig.RunResponseOK, Result: result}
	if err := validatePlaybackOutcome(bundle, response); err != nil {
		t.Fatal(err)
	}
	response.Status = runtimeconfig.RunResponseError
	if err := validatePlaybackOutcome(bundle, response); err == nil {
		t.Fatal("status mismatch accepted")
	}
	response.Status = runtimeconfig.RunResponseOK
	response.Result = []byte(`{"ok":false}`)
	if err := validatePlaybackOutcome(bundle, response); err == nil {
		t.Fatal("result mismatch accepted")
	}
}

func testPlaybackBundle(t *testing.T) playback.Bundle {
	t.Helper()
	digest := func(character byte) string { return "sha256:" + string(make([]byte, 0)) + repeatByte(character, 64) }
	result := []byte(`{"items":[]}`)
	arguments := []byte(`{}`)
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: digest('a'), RequestSHA256: digest('b'), ArtifactSHA256: digest('c'),
		ExecutionProfileSHA256: digest('d'), ExpectedStatus: "ok", ExpectedResultSHA256: digest('e'),
		Grants: []capability.GrantBinding{{Capability: "sources.demo_catalog", PolicySHA256: digest('f')}},
	}, []capability.TranscriptEntry{{
		OperationIndex: 0, Capability: "sources.demo_catalog", Arguments: arguments, ArgumentsSHA256: playback.SHA256(arguments),
		Result: result, ResultSHA256: playback.SHA256(result), Evidence: capability.TransportEvidence{
			Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: digest('1'),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func repeatByte(value byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
