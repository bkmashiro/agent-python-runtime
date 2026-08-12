package main

import (
	"os"
	"path/filepath"
	"testing"

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

func testPlaybackBundle(t *testing.T) playback.Bundle {
	t.Helper()
	digest := func(character byte) string { return "sha256:" + string(make([]byte, 0)) + repeatByte(character, 64) }
	result := []byte(`{"items":[]}`)
	arguments := []byte(`{}`)
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: digest('a'), RequestSHA256: digest('b'), ArtifactSHA256: digest('c'),
		ExecutionProfileSHA256: digest('d'), ExpectedResultSHA256: digest('e'),
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
