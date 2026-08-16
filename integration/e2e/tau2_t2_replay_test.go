package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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
const tau2T2ReplayPreregistrationSchema = "pysolate.tau2-t2-runtime-replay-preregistration-private.v1"
const tau2T2ReplayAnchorSchema = "pysolate.tau2-t2-runtime-replay-anchor.v1"
const tau2T2ReplayAnchorPath = "../../docs/evidence/tau2-t2-runtime-replay-pilot-v1.json"
const tau2T2ReplayAnchorSHA256 = "sha256:c8db9b2841c32bdf67df6367e9581f66973f2bb09f00d9a33ed0bf5bc51e3866"

var tau2T2ReplayAttempt = regexp.MustCompile(`^[1-9][0-9]*$`)

type tau2T2ReplayAnchorCase struct {
	TaskID     string `json:"task_id"`
	Turn       string `json:"turn"`
	CaseSHA256 string `json:"case_sha256"`
}

type tau2T2ReplayAnchor struct {
	SchemaVersion                  string                   `json:"schema_version"`
	Classification                 string                   `json:"classification"`
	PerformanceComparisonSupported bool                     `json:"performance_comparison_supported"`
	PreregistrationIdentity        string                   `json:"preregistration_identity"`
	ParentRemediationIdentity      string                   `json:"parent_remediation_identity"`
	ArtifactSHA256                 string                   `json:"artifact_sha256"`
	Cases                          []tau2T2ReplayAnchorCase `json:"cases"`
}

type tau2T2ReplayCase struct {
	TaskID        string          `json:"task_id"`
	Turn          string          `json:"turn"`
	Capability    string          `json:"capability"`
	Arguments     json.RawMessage `json:"arguments"`
	ArgumentNames []string        `json:"argument_names"`
	SourceSHA256  string          `json:"source_sha256"`
	PlanSHA256    string          `json:"capability_plan_sha256"`
	ContentSHA256 string          `json:"content_sha256"`
}

type tau2T2ReplayPreregistration struct {
	SchemaVersion             string             `json:"schema_version"`
	Classification            string             `json:"classification"`
	ParentRemediationIdentity string             `json:"parent_remediation_identity"`
	ArtifactSHA256            string             `json:"artifact_sha256"`
	Cases                     []tau2T2ReplayCase `json:"cases"`
	Identity                  string             `json:"identity,omitempty"`
}

type tau2T2ReplayProfileIdentity struct {
	ID               string   `json:"id"`
	ArtifactSHA256   string   `json:"artifact_sha256"`
	ManifestSHA256   string   `json:"manifest_sha256"`
	AllowedImports   []string `json:"allowed_imports"`
	AvailableImports []string `json:"available_imports"`
	QualifiedImports []string `json:"qualified_imports"`
}

type tau2T2ReplayDeterministicIdentity struct {
	SchemaVersion      string `json:"schema_version"`
	Status             string `json:"status"`
	Identity           string `json:"identity"`
	ArtifactSHA256     string `json:"artifact_sha256"`
	RandomSeedSHA256   string `json:"random_seed_sha256"`
	WalltimeUnixNano   int64  `json:"walltime_unix_nano"`
	MonotonicStartNano int64  `json:"monotonic_start_nano"`
	ClockStepNano      int64  `json:"clock_step_nano"`
}

type tau2T2ReplayConfigIdentity struct {
	TimeoutNS        int64                                    `json:"timeout_ns"`
	MaxRequestBytes  uint32                                   `json:"max_request_bytes"`
	MaxResponseBytes uint32                                   `json:"max_response_bytes"`
	MemoryLimitPages uint32                                   `json:"memory_limit_pages"`
	ProgramSurface   runtimeconfig.ProgramSurfaceMode         `json:"program_surface"`
	Mechanisms       runtimeconfig.MechanismSet               `json:"mechanisms"`
	Profile          tau2T2ReplayProfileIdentity              `json:"execution_profile"`
	CapabilityGrants map[string]runtimeconfig.CapabilityGrant `json:"capability_grants"`
	ColdIO           *runtimeconfig.ColdIOPolicy              `json:"cold_io"`
	Deterministic    *tau2T2ReplayDeterministicIdentity       `json:"deterministic_verification"`
}

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

func tau2T2ReplayConfigDigest(profile runtimeconfig.ExecutionProfile, mechanisms runtimeconfig.MechanismSet) (string, error) {
	return tau2T2ReplayConfigDigestForConfigs(profile, tau2SourceBoundAnalysisConfig(profile), tau2SourceBoundExecutionConfig(profile, mechanisms))
}

func tau2T2ReplayConfigDigestForConfigs(profile runtimeconfig.ExecutionProfile, analysisConfig, executionConfig runtimeconfig.RunConfig) (string, error) {
	encode := func(config runtimeconfig.RunConfig) tau2T2ReplayConfigIdentity {
		var deterministic *tau2T2ReplayDeterministicIdentity
		if profile := config.DeterministicVerification; profile != nil {
			deterministic = &tau2T2ReplayDeterministicIdentity{
				SchemaVersion: profile.SchemaVersion(), Status: profile.Status(), Identity: profile.Identity(), ArtifactSHA256: profile.ArtifactSHA256(),
				RandomSeedSHA256: tau2Digest(profile.RandomSeed()), WalltimeUnixNano: profile.WalltimeUnixNano(), MonotonicStartNano: profile.MonotonicStartNano(), ClockStepNano: profile.ClockStepNano(),
			}
		}
		return tau2T2ReplayConfigIdentity{
			TimeoutNS: config.Timeout.Nanoseconds(), MaxRequestBytes: config.MaxRequestBytes, MaxResponseBytes: config.MaxResponseBytes,
			MemoryLimitPages: config.MemoryLimitPages, ProgramSurface: config.ProgramSurface, Mechanisms: config.Mechanisms,
			Profile: tau2T2ReplayProfileIdentity{
				ID: profile.ID(), ArtifactSHA256: profile.ArtifactSHA256(), ManifestSHA256: profile.ManifestSHA256(),
				AllowedImports: profile.AllowedImports(), AvailableImports: profile.AvailableImports(), QualifiedImports: profile.QualifiedImports(),
			},
			CapabilityGrants: config.CapabilityGrants, ColdIO: config.ColdIO, Deterministic: deterministic,
		}
	}
	encoded, err := json.Marshal(struct {
		Analysis  tau2T2ReplayConfigIdentity `json:"analysis"`
		Execution tau2T2ReplayConfigIdentity `json:"execution"`
	}{encode(analysisConfig), encode(executionConfig)})
	if err != nil {
		return "", err
	}
	return tau2Digest(encoded), nil
}

func tau2T2ReplayIdentity(corpusIdentity, caseSHA256, configSHA256, taskID, turn, variant, attempt string) (runID string, err error) {
	for _, identity := range []string{corpusIdentity, caseSHA256, configSHA256} {
		if len(tau2T2CohortIdentity.FindStringSubmatch(identity)) != 2 {
			return "", fmt.Errorf("invalid frozen replay identity component")
		}
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(taskID) || !regexp.MustCompile(`^[0-9]+$`).MatchString(turn) || !tau2T2ReplayAttempt.MatchString(attempt) {
		return "", fmt.Errorf("invalid replay task, turn, or attempt")
	}
	if _, err := tau2T2ReplayMechanisms(variant); err != nil {
		return "", err
	}
	identityBody, err := json.Marshal(struct {
		Corpus  string `json:"corpus"`
		Case    string `json:"case"`
		Config  string `json:"config"`
		Task    string `json:"task"`
		Turn    string `json:"turn"`
		Variant string `json:"variant"`
		Attempt string `json:"attempt"`
	}{corpusIdentity, caseSHA256, configSHA256, taskID, turn, variant, attempt})
	if err != nil {
		return "", err
	}
	identityDigest := sha256.Sum256(identityBody)
	return fmt.Sprintf("tau2-replay-%x", identityDigest[:]), nil
}

func tau2T2ReplayOpenRoot(rootPath string) (*os.Root, string, error) {
	return tau2T2ReplayOpenRootWithHook(rootPath, nil)
}

func tau2T2ReplayOpenRootWithHook(rootPath string, beforeOpen func()) (*os.Root, string, error) {
	if !filepath.IsAbs(rootPath) {
		return nil, "", fmt.Errorf("replay private root must be absolute")
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, "", err
	}
	before, err := os.Lstat(resolvedRoot)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() || before.Mode().Perm()&0o077 != 0 {
		return nil, "", fmt.Errorf("replay private root pre-open identity is invalid")
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	root, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, "", err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) || !after.IsDir() || after.Mode().Perm()&0o077 != 0 {
		_ = root.Close()
		return nil, "", fmt.Errorf("replay private root changed before descriptor binding")
	}
	return root, resolvedRoot, nil
}

func tau2T2ReplayRootName(resolvedRoot, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("replay file path must be absolute")
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	if resolvedParent != resolvedRoot || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("replay file must be directly contained by the private root")
	}
	return name, nil
}

func tau2T2ReplayReadPrivate(root *os.Root, name string, maxBytes int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("replay input must be a bounded private regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("read bounded replay input: %w", err)
	}
	return content, nil
}

func tau2T2ReplayLoadAnchor(taskID, turn string) (tau2T2ReplayAnchor, tau2T2ReplayAnchorCase, error) {
	content, err := os.ReadFile(tau2T2ReplayAnchorPath)
	if err != nil || len(content) == 0 || len(content) > 64*1024 || tau2Digest(content) != tau2T2ReplayAnchorSHA256 {
		return tau2T2ReplayAnchor{}, tau2T2ReplayAnchorCase{}, fmt.Errorf("trusted replay anchor digest mismatch")
	}
	var anchor tau2T2ReplayAnchor
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&anchor); err != nil {
		return anchor, tau2T2ReplayAnchorCase{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF || anchor.SchemaVersion != tau2T2ReplayAnchorSchema || anchor.Classification != "HARNESS_VALIDATION_ONLY_NOT_PERFORMANCE_EVIDENCE" || anchor.PerformanceComparisonSupported {
		return anchor, tau2T2ReplayAnchorCase{}, fmt.Errorf("trusted replay anchor contract mismatch")
	}
	var selected *tau2T2ReplayAnchorCase
	for index := range anchor.Cases {
		candidate := &anchor.Cases[index]
		if candidate.TaskID == taskID && candidate.Turn == turn {
			if selected != nil {
				return anchor, tau2T2ReplayAnchorCase{}, fmt.Errorf("duplicate trusted replay anchor case")
			}
			selected = candidate
		}
	}
	if selected == nil {
		return anchor, tau2T2ReplayAnchorCase{}, fmt.Errorf("case is absent from trusted replay anchor")
	}
	for _, digest := range []string{anchor.PreregistrationIdentity, anchor.ParentRemediationIdentity, anchor.ArtifactSHA256, selected.CaseSHA256} {
		if len(tau2T2CohortIdentity.FindStringSubmatch(digest)) != 2 {
			return anchor, tau2T2ReplayAnchorCase{}, fmt.Errorf("trusted replay anchor contains an invalid digest")
		}
	}
	return anchor, *selected, nil
}

func tau2T2ReplayLoadPreregistration(root *os.Root, name, expectedIdentity, taskID, turn string) (tau2T2ReplayPreregistration, tau2T2ReplayCase, string, error) {
	content, err := tau2T2ReplayReadPrivate(root, name, 1024*1024)
	if err != nil {
		return tau2T2ReplayPreregistration{}, tau2T2ReplayCase{}, "", err
	}
	var prereg tau2T2ReplayPreregistration
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prereg); err != nil {
		return prereg, tau2T2ReplayCase{}, "", err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return prereg, tau2T2ReplayCase{}, "", fmt.Errorf("replay preregistration has trailing content")
	}
	identity := prereg.Identity
	prereg.Identity = ""
	canonical, err := json.Marshal(prereg)
	if err != nil {
		return prereg, tau2T2ReplayCase{}, "", err
	}
	computedIdentity := tau2Digest(canonical)
	if prereg.SchemaVersion != tau2T2ReplayPreregistrationSchema || prereg.Classification != "HARNESS_VALIDATION_ONLY_NOT_PERFORMANCE_EVIDENCE" || identity != expectedIdentity || computedIdentity != expectedIdentity ||
		len(tau2T2CohortIdentity.FindStringSubmatch(prereg.ParentRemediationIdentity)) != 2 || len(tau2T2CohortIdentity.FindStringSubmatch(prereg.ArtifactSHA256)) != 2 || len(prereg.Cases) == 0 {
		return prereg, tau2T2ReplayCase{}, "", fmt.Errorf("replay preregistration identity or classification mismatch")
	}
	var selected *tau2T2ReplayCase
	seen := map[string]struct{}{}
	for index := range prereg.Cases {
		candidate := &prereg.Cases[index]
		key := candidate.TaskID + ":" + candidate.Turn
		if _, duplicate := seen[key]; duplicate {
			return prereg, tau2T2ReplayCase{}, "", fmt.Errorf("duplicate replay case identity")
		}
		seen[key] = struct{}{}
		if candidate.Capability == "" || len(candidate.ArgumentNames) == 0 ||
			len(tau2T2CohortIdentity.FindStringSubmatch(candidate.SourceSHA256)) != 2 || len(tau2T2CohortIdentity.FindStringSubmatch(candidate.PlanSHA256)) != 2 || len(tau2T2CohortIdentity.FindStringSubmatch(candidate.ContentSHA256)) != 2 {
			return prereg, tau2T2ReplayCase{}, "", fmt.Errorf("invalid frozen replay case")
		}
		if candidate.TaskID == taskID && candidate.Turn == turn {
			selected = candidate
		}
	}
	if selected == nil {
		return prereg, tau2T2ReplayCase{}, "", fmt.Errorf("replay case is absent from frozen preregistration")
	}
	caseCanonical, err := json.Marshal(selected)
	if err != nil {
		return prereg, tau2T2ReplayCase{}, "", err
	}
	return prereg, *selected, tau2Digest(caseCanonical), nil
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

func TestTau2T2ReplayTrustedAnchorIsFixed(t *testing.T) {
	anchor, replayCase, err := tau2T2ReplayLoadAnchor("1", "1")
	if err != nil {
		t.Fatal(err)
	}
	if anchor.PreregistrationIdentity == "" || replayCase.CaseSHA256 == "" {
		t.Fatal("trusted replay anchor is incomplete")
	}
}

func TestTau2T2ReplayConfigBindsDeterministicProfile(t *testing.T) {
	profile := tau2CanaryProfile(t, []byte("deterministic-config-artifact"))
	first, err := runtimeconfig.NewDeterministicVerificationProfile(profile.ArtifactSHA256(), "seed-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimeconfig.NewDeterministicVerificationProfile(profile.ArtifactSHA256(), "seed-b")
	if err != nil {
		t.Fatal(err)
	}
	analysisA := tau2SourceBoundAnalysisConfig(profile)
	analysisB := tau2SourceBoundAnalysisConfig(profile)
	analysisA.DeterministicVerification = &first
	analysisB.DeterministicVerification = &second
	execution := tau2SourceBoundExecutionConfig(profile, runtimeconfig.MechanismSet{ProgrammaticToolCalling: true})
	digestA, err := tau2T2ReplayConfigDigestForConfigs(profile, analysisA, execution)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := tau2T2ReplayConfigDigestForConfigs(profile, analysisB, execution)
	if err != nil {
		t.Fatal(err)
	}
	if digestA == digestB {
		t.Fatal("different deterministic profiles reused one config digest")
	}
}

func TestTau2T2ReplayIdentityBindsCaseAndFullConfiguration(t *testing.T) {
	corpus := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	caseA := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	caseB := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	configA := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	configB := "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	fresh, err := tau2T2ReplayIdentity(corpus, caseA, configA, "49", "2", "fresh", "1")
	if err != nil {
		t.Fatal(err)
	}
	changedCase, err := tau2T2ReplayIdentity(corpus, caseB, configA, "49", "2", "fresh", "1")
	if err != nil {
		t.Fatal(err)
	}
	changedConfig, err := tau2T2ReplayIdentity(corpus, caseA, configB, "49", "2", "fresh", "1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh == changedCase || fresh == changedConfig || changedCase == changedConfig {
		t.Fatal("replay identity did not bind case and full configuration")
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
	corpus := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	caseID := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	config := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := tau2T2ReplayIdentity(corpus, caseID, config, "49", "2", "fresh", "0"); err == nil {
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

func TestTau2T2ReplayPreregistrationIsIndependentFrozenOracle(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Chmod(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	preregistration := tau2T2ReplayPreregistration{
		SchemaVersion: tau2T2ReplayPreregistrationSchema, Classification: "HARNESS_VALIDATION_ONLY_NOT_PERFORMANCE_EVIDENCE",
		ParentRemediationIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactSHA256:            "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Cases: []tau2T2ReplayCase{{
			TaskID: "1", Turn: "1", Capability: "lookup", Arguments: json.RawMessage(`{"id":"x"}`), ArgumentNames: []string{"id"},
			SourceSHA256:  "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			PlanSHA256:    "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			ContentSHA256: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		}},
	}
	canonical, err := json.Marshal(preregistration)
	if err != nil {
		t.Fatal(err)
	}
	identity := tau2Digest(canonical)
	preregistration.Identity = identity
	encoded, err := json.Marshal(preregistration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "prereg.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	root, _, err := tau2T2ReplayOpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, replayCase, caseSHA256, err := tau2T2ReplayLoadPreregistration(root, "prereg.json", identity, "1", "1")
	if err != nil || replayCase.ContentSHA256 == "" || caseSHA256 == "" {
		t.Fatalf("load frozen preregistration case=%+v case_sha=%s err=%v", replayCase, caseSHA256, err)
	}
	preregistration.Cases[0].ContentSHA256 = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	tampered, err := json.Marshal(preregistration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "tampered.json"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := tau2T2ReplayLoadPreregistration(root, "tampered.json", identity, "1", "1"); err == nil {
		t.Fatal("tampered frozen preregistration was accepted")
	}
}

func TestTau2T2ReplayOpenRootRejectsPreOpenReplacement(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "private")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "original")
	root, _, err := tau2T2ReplayOpenRootWithHook(rootPath, func() {
		if renameErr := os.Rename(rootPath, moved); renameErr != nil {
			t.Fatal(renameErr)
		}
		if mkdirErr := os.Mkdir(rootPath, 0o700); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	})
	if root != nil {
		root.Close()
	}
	if err == nil {
		t.Fatal("pre-open private-root replacement was accepted")
	}
}

func TestTau2T2ReplayRootDescriptorPreventsPathReplacementEscape(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "private")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(rootPath, "source.py")
	if err := os.WriteFile(sourcePath, []byte("result = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, resolvedRoot, err := tau2T2ReplayOpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name, err := tau2T2ReplayRootName(resolvedRoot, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tau2T2ReplayReadPrivate(root, name, 1024); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "private-moved")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rootPath, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, rootPath); err != nil {
		t.Fatal(err)
	}
	file, err := root.OpenFile("result.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "result.json")); err != nil {
		t.Fatal("rooted write did not remain in the opened directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "result.json")); !os.IsNotExist(err) {
		t.Fatal("rooted write escaped after path replacement")
	}
}

func TestTau2T2ReplayFrozenProgramThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	manifest := os.Getenv("PYSOLATE_TAU2_T2_PRIVATE_MANIFEST")
	preregistrationPath := os.Getenv("PYSOLATE_TAU2_REPLAY_PREREGISTRATION_FILE")
	taskID := os.Getenv("PYSOLATE_TAU2_T2_TASK_ID")
	turn := os.Getenv("PYSOLATE_TAU2_REPLAY_TURN")
	variant := os.Getenv("PYSOLATE_TAU2_REPLAY_VARIANT")
	attempt := os.Getenv("PYSOLATE_TAU2_REPLAY_ATTEMPT")
	sourcePath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE")
	outputPath := os.Getenv("PYSOLATE_TAU2_REPLAY_OUTPUT_FILE")
	privateRoot := os.Getenv("PYSOLATE_TAU2_REPLAY_PRIVATE_ROOT")
	if python == "" || sourceRoot == "" || manifest == "" || preregistrationPath == "" || taskID == "" || turn == "" || variant == "" || attempt == "" || sourcePath == "" || outputPath == "" || privateRoot == "" {
		t.Skip("T2 runtime replay environment is required")
	}
	if variant == "cow" && runtime.GOOS != "linux" {
		t.Skip("T2 COW replay requires Linux")
	}
	wantName := fmt.Sprintf("task-%s-turn-%s-variant-%s-attempt-%s.json", taskID, turn, variant, attempt)
	if filepath.Base(outputPath) != wantName {
		t.Fatalf("replay output identity mismatch: got %s want %s", filepath.Base(outputPath), wantName)
	}
	anchor, anchorCase, err := tau2T2ReplayLoadAnchor(taskID, turn)
	if err != nil {
		t.Fatal(err)
	}
	root, resolvedRoot, err := tau2T2ReplayOpenRoot(privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	preregistrationName, err := tau2T2ReplayRootName(resolvedRoot, preregistrationPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceName, err := tau2T2ReplayRootName(resolvedRoot, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	outputName, err := tau2T2ReplayRootName(resolvedRoot, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	preregistration, replayCase, caseSHA256, err := tau2T2ReplayLoadPreregistration(root, preregistrationName, anchor.PreregistrationIdentity, taskID, turn)
	if err != nil {
		t.Fatal(err)
	}
	if preregistration.ParentRemediationIdentity != anchor.ParentRemediationIdentity || preregistration.ArtifactSHA256 != anchor.ArtifactSHA256 || caseSHA256 != anchorCase.CaseSHA256 {
		t.Fatal("private replay preregistration does not match trusted public anchor")
	}
	manifestInfo, err := os.Lstat(manifest)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Mode().Perm()&0o077 != 0 {
		t.Fatal("private parent manifest must be a private regular file")
	}
	source, err := tau2T2ReplayReadPrivate(root, sourceName, 16*1024)
	if err != nil {
		t.Fatal(err)
	}
	if tau2Digest(source) != replayCase.SourceSHA256 {
		t.Fatal("replay source does not match frozen preregistration")
	}
	var arguments map[string]any
	if err := json.Unmarshal(replayCase.Arguments, &arguments); err != nil || len(replayCase.ArgumentNames) == 0 || replayCase.Capability == "" {
		t.Fatal("invalid frozen replay capability or arguments")
	}
	canonicalArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonicalArguments) != string(replayCase.Arguments) {
		t.Fatal("frozen replay arguments are not canonical JSON")
	}
	mechanisms, err := tau2T2ReplayMechanisms(variant)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	if tau2Digest(wasm) != preregistration.ArtifactSHA256 {
		t.Fatal("Guest artifact does not match frozen preregistration")
	}
	profile := tau2CanaryProfile(t, wasm)
	configSHA256, err := tau2T2ReplayConfigDigest(profile, mechanisms)
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2T2ReadPlan(t, python, sourceRoot, manifest, taskID, replayCase.Capability, replayCase.ArgumentNames, arguments)
	planDocument, err := plan.EvidenceDocument()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Identity() != replayCase.PlanSHA256 {
		t.Fatal("replay Plan does not match frozen preregistration")
	}
	runID, err := tau2T2ReplayIdentity(anchor.PreregistrationIdentity, caseSHA256, configSHA256, taskID, turn, variant, attempt)
	if err != nil {
		t.Fatal(err)
	}
	run := runTau2SourceBoundTurnWithOptions(t, wasm, profile, plan, runID, replayCase.Capability, string(canonicalArguments), string(source), tau2SourceBoundOptions{Mechanisms: mechanisms, Observe: true})
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
	if tau2Digest([]byte(run.Content)) != replayCase.ContentSHA256 {
		t.Fatal("replay content does not match frozen preregistration oracle")
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version": tau2T2ReplaySchema, "corpus_identity": anchor.PreregistrationIdentity, "case_sha256": caseSHA256,
		"parent_remediation_identity": preregistration.ParentRemediationIdentity, "task_id": taskID, "turn": turn, "variant_id": variant, "attempt": attempt,
		"variant_config_sha256": configSHA256, "run_id": runID, "mechanisms": run.Mechanisms,
		"artifact_sha256": tau2Digest(wasm), "source_sha256": tau2Digest(source), "request_sha256": tau2Digest(run.Request), "response_sha256": tau2Digest(run.Payload),
		"content_sha256": tau2Digest([]byte(run.Content)), "expected_content_sha256": replayCase.ContentSHA256, "capability_plan_sha256": plan.Identity(),
		"plan_document": json.RawMessage(planDocument), "grant_policy": tau2T2GrantPolicy(taskID, replayCase.Capability, arguments),
		"receipt": run.Receipt, "prepared_state": run.PreparedState, "cow_probe": run.COWProbe,
		"observation_event_types": run.ObservationTypes, "observation_event_sha256": run.ObservationSHA256,
		"timings_ms":       map[string]float64{"analysis_engine_new": run.AnalysisEngineNewMS, "analysis": run.AnalysisMS, "execution_engine_new": run.ExecutionEngineNewMS, "run": run.RunMS, "total": run.TotalMS, "guest_exec": *response.Metrics.GuestTimeMS},
		"capability_calls": response.Metrics.CapabilityCalls, "result_bytes": response.Metrics.ResultBytes,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := root.OpenFile(outputName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
