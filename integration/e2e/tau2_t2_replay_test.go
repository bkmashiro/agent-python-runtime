package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const tau2T2ReplaySchema = "pysolate.tau2-t2-runtime-replay-private.v1"

var tau2T2ReplayAttempt = regexp.MustCompile(`^[1-9][0-9]*$`)

func tau2T2ReplayMechanisms(variant string) (runtimeconfig.MechanismSet, error) {
	mechanisms := runtimeconfig.MechanismSet{ProgrammaticToolCalling: true}
	switch variant {
	case "fresh":
	case "prepared":
		mechanisms.PreparedRuntime = true
	case "cow":
		mechanisms.PreparedRuntime = true
		mechanisms.MemoryCOW = true
	default:
		return runtimeconfig.MechanismSet{}, fmt.Errorf("unsupported replay variant %q", variant)
	}
	if err := mechanisms.Validate(); err != nil {
		return runtimeconfig.MechanismSet{}, err
	}
	return mechanisms, nil
}

func tau2T2ReplayIdentity(corpusIdentity, taskID, turn, variant, attempt string) (runID, configSHA256 string, err error) {
	if len(tau2T2CohortIdentity.FindStringSubmatch(corpusIdentity)) != 2 {
		return "", "", fmt.Errorf("invalid frozen replay corpus identity")
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(taskID) || !regexp.MustCompile(`^[0-9]+$`).MatchString(turn) || !tau2T2ReplayAttempt.MatchString(attempt) {
		return "", "", fmt.Errorf("invalid replay task, turn, or attempt")
	}
	mechanisms, err := tau2T2ReplayMechanisms(variant)
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(mechanisms)
	if err != nil {
		return "", "", err
	}
	configDigest := sha256.Sum256(encoded)
	configSHA256 = fmt.Sprintf("sha256:%x", configDigest[:])
	identityBody, err := json.Marshal(struct {
		Cohort  string `json:"cohort"`
		Task    string `json:"task"`
		Turn    string `json:"turn"`
		Variant string `json:"variant"`
		Config  string `json:"config"`
		Attempt string `json:"attempt"`
	}{corpusIdentity, taskID, turn, variant, configSHA256, attempt})
	if err != nil {
		return "", "", err
	}
	identityDigest := sha256.Sum256(identityBody)
	return fmt.Sprintf("tau2-replay-%x", identityDigest[:]), configSHA256, nil
}

func tau2T2ReplayPrivatePath(root, path string, mustExist bool) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return fmt.Errorf("replay private paths must be absolute")
	}
	rootEntry, err := os.Lstat(root)
	if err != nil || rootEntry.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("replay private root must not be a symlink")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("replay private root must be a 0700-style directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	if resolvedParent != resolvedRoot {
		return fmt.Errorf("replay file must be directly contained by the private root")
	}
	if !mustExist {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("replay output must not already exist")
		}
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("replay input must be a private regular file")
	}
	return nil
}

func tau2T2ReplayVariantDisposition(variant string, prepared wazeroengine.PreparedState, cow wazeroengine.COWProbe) error {
	if prepared.FreshFallbackRuns != 0 {
		return fmt.Errorf("variant used a fresh fallback")
	}
	switch variant {
	case "fresh":
		if prepared.Selected || prepared.PreparedRuns != 0 || cow.COWSelected {
			return fmt.Errorf("fresh variant selected an optimization")
		}
	case "prepared":
		if !prepared.Selected || prepared.PreparedRuns != 1 || cow.COWSelected {
			return fmt.Errorf("prepared variant was not uniquely selected")
		}
	case "cow":
		if !prepared.Selected || prepared.PreparedRuns != 1 || !cow.COWSelected || cow.Fallback {
			return fmt.Errorf("COW variant was not uniquely selected")
		}
	default:
		return fmt.Errorf("unsupported replay variant %q", variant)
	}
	return nil
}

func TestTau2T2ReplayIdentityBindsVariantConfiguration(t *testing.T) {
	cohort := "sha256:" + regexp.MustCompile(`x`).ReplaceAllString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "a")
	fresh, freshConfig, err := tau2T2ReplayIdentity(cohort, "49", "2", "fresh", "1")
	if err != nil {
		t.Fatal(err)
	}
	prepared, preparedConfig, err := tau2T2ReplayIdentity(cohort, "49", "2", "prepared", "1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh == prepared || freshConfig == preparedConfig {
		t.Fatalf("variant identity collision fresh=%s prepared=%s", fresh, prepared)
	}
}

func TestTau2T2ReplayMechanismVariantsAreExact(t *testing.T) {
	fresh, err := tau2T2ReplayMechanisms("fresh")
	if err != nil || !fresh.ProgrammaticToolCalling || fresh.PreparedRuntime || fresh.MemoryCOW {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	prepared, err := tau2T2ReplayMechanisms("prepared")
	if err != nil || !prepared.PreparedRuntime || prepared.MemoryCOW {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	cow, err := tau2T2ReplayMechanisms("cow")
	if err != nil || !cow.PreparedRuntime || !cow.MemoryCOW {
		t.Fatalf("cow=%+v err=%v", cow, err)
	}
	if _, err := tau2T2ReplayMechanisms("unknown"); err == nil {
		t.Fatal("unknown replay variant was accepted")
	}
}

func TestTau2T2ReplayIdentityRejectsInvalidAttempt(t *testing.T) {
	cohort := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := tau2T2ReplayIdentity(cohort, "49", "2", "fresh", "0"); err == nil {
		t.Fatal("zero attempt was accepted")
	}
}

func TestTau2T2ReplayVariantDispositionFailsClosed(t *testing.T) {
	fresh := wazeroengine.PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1"}
	prepared := wazeroengine.PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1", Selected: true, PreparedRuns: 1}
	cow := wazeroengine.COWProbe{SchemaVersion: "pysolate.cow-probe.v1", COWSelected: true}
	if err := tau2T2ReplayVariantDisposition("fresh", fresh, wazeroengine.COWProbe{}); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayVariantDisposition("prepared", prepared, wazeroengine.COWProbe{}); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayVariantDisposition("cow", prepared, cow); err != nil {
		t.Fatal(err)
	}
	prepared.FreshFallbackRuns = 1
	if err := tau2T2ReplayVariantDisposition("prepared", prepared, wazeroengine.COWProbe{}); err == nil {
		t.Fatal("fresh fallback accepted")
	}
	if err := tau2T2ReplayVariantDisposition("cow", wazeroengine.PreparedState{Selected: true, PreparedRuns: 1}, wazeroengine.COWProbe{Fallback: true}); err == nil {
		t.Fatal("COW fallback accepted")
	}
}

func TestTau2T2ReplayPrivatePathRejectsEscapeSymlinkAndExistingOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.py")
	if err := os.WriteFile(source, []byte("result = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(root, source, true); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(root, filepath.Join(root, "new.json"), false); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(root, filepath.Join(filepath.Dir(root), "escape.json"), false); err == nil {
		t.Fatal("escape accepted")
	}
	link := filepath.Join(root, "linked.py")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(root, link, true); err == nil {
		t.Fatal("symlink accepted")
	}
	existing := filepath.Join(root, "existing.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(root, existing, false); err == nil {
		t.Fatal("existing output accepted")
	}
}

func TestTau2T2ReplayFrozenProgramThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	manifest := os.Getenv("PYSOLATE_TAU2_T2_PRIVATE_MANIFEST")
	corpusIdentity := os.Getenv("PYSOLATE_TAU2_REPLAY_CORPUS_IDENTITY")
	taskID := os.Getenv("PYSOLATE_TAU2_T2_TASK_ID")
	turn := os.Getenv("PYSOLATE_TAU2_REPLAY_TURN")
	variant := os.Getenv("PYSOLATE_TAU2_REPLAY_VARIANT")
	attempt := os.Getenv("PYSOLATE_TAU2_REPLAY_ATTEMPT")
	sourcePath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE")
	outputPath := os.Getenv("PYSOLATE_TAU2_REPLAY_OUTPUT_FILE")
	privateRoot := os.Getenv("PYSOLATE_TAU2_REPLAY_PRIVATE_ROOT")
	capabilityName := os.Getenv("PYSOLATE_TAU2_EXPECTED_CAPABILITY")
	argumentsText := os.Getenv("PYSOLATE_TAU2_EXPECTED_ARGUMENTS")
	argumentNamesText := os.Getenv("PYSOLATE_TAU2_EXPECTED_ARGUMENT_NAMES")
	expectedSourceSHA256 := os.Getenv("PYSOLATE_TAU2_EXPECTED_SOURCE_SHA256")
	expectedPlanSHA256 := os.Getenv("PYSOLATE_TAU2_EXPECTED_PLAN_SHA256")
	expectedContentSHA256 := os.Getenv("PYSOLATE_TAU2_EXPECTED_CONTENT_SHA256")
	if python == "" || sourceRoot == "" || manifest == "" || corpusIdentity == "" || taskID == "" || turn == "" || variant == "" || attempt == "" || sourcePath == "" || outputPath == "" || privateRoot == "" || capabilityName == "" || argumentsText == "" || argumentNamesText == "" || expectedSourceSHA256 == "" || expectedPlanSHA256 == "" || expectedContentSHA256 == "" {
		t.Skip("T2 runtime replay environment is required")
	}
	if variant == "cow" && runtime.GOOS != "linux" {
		t.Skip("T2 COW replay requires Linux")
	}
	wantName := fmt.Sprintf("task-%s-turn-%s-variant-%s-attempt-%s.json", taskID, turn, variant, attempt)
	if filepath.Base(outputPath) != wantName {
		t.Fatalf("replay output identity mismatch: got %s want %s", filepath.Base(outputPath), wantName)
	}
	if err := tau2T2ReplayPrivatePath(privateRoot, sourcePath, true); err != nil {
		t.Fatal(err)
	}
	if err := tau2T2ReplayPrivatePath(privateRoot, outputPath, false); err != nil {
		t.Fatal(err)
	}
	manifestInfo, err := os.Lstat(manifest)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Mode().Perm()&0o077 != 0 {
		t.Fatal("private parent manifest must be a private regular file")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil || len(source) == 0 || len(source) > 16*1024 {
		t.Fatal("replay source is outside bounded contract")
	}
	if tau2Digest(source) != expectedSourceSHA256 {
		t.Fatal("replay source does not match frozen corpus digest")
	}
	var arguments map[string]any
	var argumentNames []string
	if err := json.Unmarshal([]byte(argumentsText), &arguments); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(argumentNamesText), &argumentNames); err != nil || len(argumentNames) == 0 {
		t.Fatal("invalid replay argument names")
	}
	canonicalArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	runID, configSHA256, err := tau2T2ReplayIdentity(corpusIdentity, taskID, turn, variant, attempt)
	if err != nil {
		t.Fatal(err)
	}
	mechanisms, err := tau2T2ReplayMechanisms(variant)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2T2ReadPlan(t, python, sourceRoot, manifest, taskID, capabilityName, argumentNames, arguments)
	planDocument, err := plan.EvidenceDocument()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Identity() != expectedPlanSHA256 {
		t.Fatal("replay Plan does not match frozen corpus digest")
	}
	run := runTau2SourceBoundTurnWithOptions(t, wasm, tau2CanaryProfile(t, wasm), plan, runID, capabilityName, string(canonicalArguments), string(source), tau2SourceBoundOptions{Mechanisms: mechanisms, Observe: true})
	if err := tau2T2ReplayVariantDisposition(variant, run.PreparedState, run.COWProbe); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"execution.started", "capability.plan_bound", "capability.call.intent", "capability.call.started", "capability.call", "execution.completed"}
	if strings.Join(run.ObservationTypes, "\n") != strings.Join(wantEvents, "\n") {
		t.Fatalf("replay observation events=%v want=%v", run.ObservationTypes, wantEvents)
	}
	response := decodeRealGuestResponse(t, run.Request, run.Payload)
	if response.Metrics == nil || response.Metrics.GuestTimeMS == nil || response.Metrics.CapabilityCalls != 1 {
		t.Fatalf("replay response metrics=%+v", response.Metrics)
	}
	if tau2Digest([]byte(run.Content)) != expectedContentSHA256 {
		t.Fatal("replay content does not match frozen corpus oracle")
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version":  tau2T2ReplaySchema,
		"corpus_identity": corpusIdentity, "task_id": taskID, "turn": turn, "variant_id": variant, "attempt": attempt,
		"variant_config_sha256": configSHA256, "run_id": runID, "mechanisms": run.Mechanisms,
		"source_sha256": tau2Digest(source), "request_sha256": tau2Digest(run.Request), "response_sha256": tau2Digest(run.Payload),
		"content_sha256": tau2Digest([]byte(run.Content)), "capability_plan_sha256": plan.Identity(),
		"plan_document": json.RawMessage(planDocument), "grant_policy": tau2T2GrantPolicy(taskID, capabilityName, arguments),
		"receipt": run.Receipt, "prepared_state": run.PreparedState, "cow_probe": run.COWProbe,
		"observation_event_types": run.ObservationTypes, "observation_event_sha256": run.ObservationSHA256,
		"timings_ms":       map[string]float64{"analysis_engine_new": run.AnalysisEngineNewMS, "analysis": run.AnalysisMS, "execution_engine_new": run.ExecutionEngineNewMS, "run": run.RunMS, "total": run.TotalMS, "guest_exec": *response.Metrics.GuestTimeMS},
		"capability_calls": response.Metrics.CapabilityCalls, "result_bytes": response.Metrics.ResultBytes,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
