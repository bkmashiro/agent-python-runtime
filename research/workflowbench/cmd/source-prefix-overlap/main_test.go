package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
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
	writeFixture(t, filepath.Join(root, "contract.json"), contract)
	loaded, loadedOracle, loadedLane, err := loadPreregistration(filepath.Join(root, "contract.json"), filepath.Join(root, "oracle.json"), filepath.Join(root, "lane.json"))
	if err != nil || loaded.ExperimentID != contract.ExperimentID || loadedOracle.LogicalCalls != 1 || loadedLane.QueueMaxChunks != 3 {
		t.Fatalf("contract=%+v oracle=%+v lane=%+v err=%v", loaded, loadedOracle, loadedLane, err)
	}
	if err := os.WriteFile(filepath.Join(root, "oracle.json"), append(oracleRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadPreregistration(filepath.Join(root, "contract.json"), filepath.Join(root, "oracle.json"), filepath.Join(root, "lane.json")); err == nil {
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
