package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func testSHA(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func writeFixture(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuildFixturePlanUsesReachGatedReadWithoutPreDispatch(t *testing.T) {
	plan, err := buildFixturePlan(&timedFixtureHandler{})
	if err != nil {
		t.Fatal(err)
	}
	specs := plan.Specs()
	if len(specs) != 1 || specs[0].PreDispatch != nil || specs[0].ReadOnly || specs[0].Idempotent || specs[0].EffectClass != "external_read" {
		t.Fatalf("unexpected fixture spec: %+v", specs)
	}
}

func TestProductionPreregistrationRejectsUnanchoredContractMutation(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "docs", "evidence")
	contractRaw, err := os.ReadFile(filepath.Join(root, "source-prefix-overlap-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract workflowbench.SourcePrefixExperimentContract
	if json.Unmarshal(contractRaw, &contract) != nil {
		t.Fatal("decode checked-in contract")
	}
	contract.ToolDelayMS++
	temporary := t.TempDir()
	writeFixture(t, filepath.Join(temporary, "contract.json"), contract)
	oracleRaw, _ := os.ReadFile(filepath.Join(root, "source-prefix-overlap-oracle-v1.json"))
	laneRaw, _ := os.ReadFile(filepath.Join(root, "source-prefix-overlap-lane-config-v1.json"))
	if err := os.WriteFile(filepath.Join(temporary, "oracle.json"), oracleRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "lane.json"), laneRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadPreregistration(filepath.Join(temporary, "contract.json"), filepath.Join(temporary, "oracle.json"), filepath.Join(temporary, "lane.json")); err == nil {
		t.Fatal("unanchored contract mutation accepted")
	}
}

func TestResolveVCSIdentityRequiresCleanEmbeddedRevision(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	if got, err := resolveVCSIdentity(map[string]string{"vcs.revision": commit, "vcs.modified": "false"}); err != nil || got != commit {
		t.Fatalf("identity=%q err=%v", got, err)
	}
	for _, settings := range []map[string]string{
		{"vcs.revision": commit, "vcs.modified": "true"},
		{"vcs.revision": "caller-chosen", "vcs.modified": "false"},
		{"vcs.modified": "false"},
	} {
		if _, err := resolveVCSIdentity(settings); err == nil {
			t.Fatalf("invalid settings accepted: %#v", settings)
		}
	}
}

func TestCheckedInEvidenceValidatesRemediationAttempt(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "docs", "evidence")
	contractRaw, err := os.ReadFile(filepath.Join(root, "source-prefix-overlap-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := workflowbench.DecodeSourcePrefixExperimentContract(contractRaw)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRaw, err := os.ReadFile(filepath.Join(root, "source-prefix-overlap-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workflowbench.DecodeSourcePrefixEvidence(evidenceRaw, contract)
	if err != nil || digestBytes(evidenceRaw) != "sha256:51e97f7604351aac6f1822b503e0c6425286f9cd44c6ebd21f0b6ea43b64da69" || evidence.MeasurementAttempt != workflowbench.SourcePrefixMeasurementAttempt || evidence.MedianSpeedupMilli != 1923 || evidence.ArtifactSHA256 != legacyGuestArtifactSHA256 || evidence.ArtifactSourceCommit != legacyGuestArtifactSourceCommit || evidence.HarnessSourceCommit != "ca25b1b767edd50dc25363df5347cb801c5c183a" {
		t.Fatalf("checked-in remediation evidence err=%v evidence=%+v", err, evidence)
	}
	plan, err := buildLegacyFixturePlan(&timedFixtureHandler{delay: 1})
	if err != nil || len(plan.Specs()) != 1 {
		t.Fatalf("rebuild fixture plan: plan=%+v err=%v", plan, err)
	}
	specJSON, err := json.Marshal(plan.Specs()[0])
	if err != nil {
		t.Fatal(err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, StagedObservation: true, PrivateWorkspace: true}
	profileJSON, err := json.Marshal(runConfig)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.CapabilityPlanSHA256 != plan.Identity() || evidence.CapabilitySpecSHA256 != digestBytes(specJSON) || evidence.ExecutionProfileSHA256 != digestBytes(profileJSON) || evidence.HandlerSHA256 != digestBytes([]byte(legacyFixtureHandlerContract)) {
		t.Fatalf("checked-in runtime identities do not match executable definitions: %+v", evidence)
	}
	for _, row := range evidence.Rows {
		if row.WorkspaceBeforeSHA256 == "" || row.WorkspaceBeforeSHA256 != row.WorkspaceAfterSHA256 {
			t.Fatalf("workspace identity drift: %+v", row)
		}
	}
}

func TestCheckedInReportBindsAcceptedEvidenceIdentities(t *testing.T) {
	reportPath := filepath.Join("..", "..", "..", "..", "docs", "research", "source-prefix-execution-overlap-v1.md")
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{
		"501daef99796c1af7cd7bab1e0ab712a199820b9",
		"sha256:a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b",
		"ca25b1b767edd50dc25363df5347cb801c5c183a",
		"sha256:51e97f7604351aac6f1822b503e0c6425286f9cd44c6ebd21f0b6ea43b64da69",
	} {
		if !strings.Contains(string(raw), identity) {
			t.Fatalf("report does not bind accepted identity %s", identity)
		}
	}
}

func TestCheckedInPreregistrationIsSelfConsistent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "docs", "evidence")
	contract, _, _, err := loadPreregistration(
		filepath.Join(root, "source-prefix-overlap-contract-v1.json"),
		filepath.Join(root, "source-prefix-overlap-oracle-v1.json"),
		filepath.Join(root, "source-prefix-overlap-lane-config-v1.json"),
	)
	if err != nil || contract.ExperimentID != "source-prefix-overlap-v1" {
		t.Fatalf("checked-in preregistration err=%v contract=%+v", err, contract)
	}
}

func TestCheckedInDayTripPreregistrationIsSelfConsistent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "docs", "evidence")
	contract, oracle, _, err := loadPreregistration(
		filepath.Join(root, "source-prefix-day-trip-contract-v2.json"),
		filepath.Join(root, "source-prefix-day-trip-oracle-v2.json"),
		filepath.Join(root, "source-prefix-day-trip-lane-config-v2.json"),
	)
	if err != nil || contract.ExperimentID != "source-prefix-day-trip-v2" || !strings.Contains(string(oracle.ExpectedResult), "oxford") {
		t.Fatalf("checked-in day-trip preregistration err=%v contract=%+v oracle=%s", err, contract, oracle.ExpectedResult)
	}
}

func TestLoadPreregistrationCrossChecksOracleAndLaneConfig(t *testing.T) {
	root := t.TempDir()
	oracle := sourcePrefixOracle{SchemaVersion: sourcePrefixOracleSchema, ExpectedResult: json.RawMessage(`{"label":"ALPHA"}`), LogicalCalls: 1, PhysicalDispatches: 1, WorkspaceDisposition: "published", ExternalWrites: 0}
	lane := sourcePrefixLaneConfig{SchemaVersion: sourcePrefixLaneConfigSchema, Mechanism: "reach_gated_source_prefix", QueueMaxChunks: 3, QueueMaxBytes: 65536, Clock: "monotonic_host", Baseline: "release_after_generation_complete", Treatment: "release_at_frozen_offsets"}
	oracleRaw := writeFixture(t, filepath.Join(root, "oracle.json"), oracle)
	laneRaw := writeFixture(t, filepath.Join(root, "lane.json"), lane)
	expected, err := canonicalResultSHA(oracle.ExpectedResult)
	if err != nil {
		t.Fatal(err)
	}
	contract := workflowbench.SourcePrefixExperimentContract{
		SchemaVersion: workflowbench.SourcePrefixExperimentContractSchema, ExperimentID: "source-prefix-overlap-v1",
		Schedule:    workflowbench.SourcePrefixSchedule{SchemaVersion: workflowbench.SourcePrefixScheduleSchema, CaseID: "source-prefix-overlap-v1", Chunks: []workflowbench.TimedSourceChunk{{OffsetMS: 0, Source: "record = slow.lookup('alpha')\n"}, {OffsetMS: 700, Source: "label = record['label'].upper()\n"}, {OffsetMS: 1400, Source: "result = {'label': label}\n"}}, MaxBufferedChunks: 3, MaxBufferedBytes: 65536},
		Repetitions: 3, ToolDelayMS: 1500, ExpectedResultSHA256: expected, OracleSHA256: testSHA(oracleRaw), LaneConfigSHA256: testSHA(laneRaw), ClaimBoundary: workflowbench.SourcePrefixClaimBoundary,
	}
	contractRaw := writeFixture(t, filepath.Join(root, "contract.json"), contract)
	anchors := preregistrationAnchors{contractSHA256: testSHA(contractRaw), oracleSHA256: testSHA(oracleRaw), laneSHA256: testSHA(laneRaw)}
	loaded, loadedOracle, loadedLane, err := loadPreregistrationWithAnchors(filepath.Join(root, "contract.json"), filepath.Join(root, "oracle.json"), filepath.Join(root, "lane.json"), anchors)
	if err != nil || loaded.ExperimentID != contract.ExperimentID || loadedOracle.LogicalCalls != 1 || loadedLane.QueueMaxChunks != 3 {
		t.Fatalf("contract=%+v oracle=%+v lane=%+v err=%v", loaded, loadedOracle, loadedLane, err)
	}
	if err := os.WriteFile(filepath.Join(root, "oracle.json"), append(oracleRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadPreregistrationWithAnchors(filepath.Join(root, "contract.json"), filepath.Join(root, "oracle.json"), filepath.Join(root, "lane.json"), anchors); err == nil {
		t.Fatal("oracle byte drift accepted")
	}
}

func TestStableStreamResultExtractsOnlyNestedCanonicalResult(t *testing.T) {
	payload := []byte(`{"status":"ok","result":{"result":{"label":"ALPHA"},"logs":[],"result_present":true,"result_source":"legacy_result","suites":[],"timeline":[],"eager":{"dispatched":0,"consumed":0,"orphaned":0}},"error":null}`)
	result, err := stableStreamResult(payload)
	if err != nil || string(result) != `{"label":"ALPHA"}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if _, err := stableStreamResult([]byte(`{"status":"ok","result":{"result":null,"result_present":false}}`)); err == nil {
		t.Fatal("missing stream result accepted")
	}
}

func TestAtomicPrivateWriteUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "evidence.json")
	if err := atomicPrivateWrite(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	directory, _ := os.Stat(filepath.Dir(path))
	file, _ := os.Stat(path)
	if directory.Mode().Perm() != 0o700 || file.Mode().Perm() != 0o600 {
		t.Fatalf("modes directory=%o file=%o", directory.Mode().Perm(), file.Mode().Perm())
	}
}

func TestTravelWeatherFixtureMatchesPublicDayTripStory(t *testing.T) {
	spec := fixtureCapabilitySpec()
	if spec.Name != "travel.weather" || spec.Python == nil || spec.Python.Module != "travel" || spec.Python.Method != "weather" {
		t.Fatalf("travel weather fixture=%+v", spec)
	}
	handler := &timedFixtureHandler{delay: time.Millisecond, origin: time.Now()}
	result, err := handler.Call(context.Background(), json.RawMessage(`{"destination":"oxford","date":"saturday"}`))
	if err != nil || string(result) != `{"condition":"light_rain","high_c":17}` {
		t.Fatalf("weather result=%s err=%v", result, err)
	}
}
