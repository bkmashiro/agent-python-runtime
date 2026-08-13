package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestRealGuestSparkScenarioCoreTreatments(t *testing.T) {
	corpusPath := os.Getenv("PYSOLATE_SPARK_CORPUS")
	outputPath := os.Getenv("PYSOLATE_ACCEPTANCE_CORE_REPORT")
	if corpusPath == "" || outputPath == "" {
		t.Skip("PYSOLATE_SPARK_CORPUS and PYSOLATE_ACCEPTANCE_CORE_REPORT are required")
	}
	corpusRaw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	corpus, corpusSHA, err := composableacceptance.DecodeCorpus(corpusRaw)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        os.Getenv("PYSOLATE_HOST_SOURCE_COMMIT"),
		GuestArtifactSHA256: hashBytes(artifact), CorpusSHA256: corpusSHA, Model: corpus.Model,
	}
	coreRows := make(map[string]struct {
		scenarioSHA string
		oracleSHA   string
	}, len(corpus.Scenarios))
	for _, scenario := range corpus.Scenarios {
		scenarioSHA, err := composableacceptance.ScenarioIdentity(scenario)
		if err != nil {
			t.Fatal(err)
		}
		oracleSHA := composableacceptance.ArtifactIdentity(scenario.ExpectedArtifact)
		coreRows[scenario.ID] = struct {
			scenarioSHA string
			oracleSHA   string
		}{scenarioSHA: scenarioSHA, oracleSHA: oracleSHA}
		for _, treatment := range []composableacceptance.Treatment{
			composableacceptance.TreatmentFresh,
			composableacceptance.TreatmentPrepared,
			composableacceptance.TreatmentCOW,
		} {
			row := runScenarioCoreTreatment(t, artifact, scenario, scenarioSHA, oracleSHA, treatment)
			report.Rows = append(report.Rows, row)
		}
	}
	composableacceptance.SortRows(report.Rows)
	encoded, _, err := composableacceptance.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".core-report-*")
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		t.Fatal(err)
	}
}

func runScenarioCoreTreatment(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, treatment composableacceptance.Treatment) composableacceptance.Row {
	t.Helper()
	row := composableacceptance.Row{
		ScenarioID: scenario.ID, ScenarioSHA256: scenarioSHA, Treatment: treatment,
		Status: "passed", OracleSHA256: oracleSHA, GuestCreated: 1, GuestDestroyed: 1,
		EvidenceScope: "direct_replay", ConformanceSHA256: composableacceptance.ArtifactIdentity("TestRealGuestSparkScenarioCoreTreatments@" + os.Getenv("PYSOLATE_HOST_SOURCE_COMMIT")),
		TerminalDisposition: "closed", EvidenceComplete: true,
	}
	config := runtimeconfig.DefaultRunConfig()
	switch treatment {
	case composableacceptance.TreatmentFresh:
	case composableacceptance.TreatmentPrepared:
		config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true}
		row.TerminalDisposition = "consumed_single_use"
	case composableacceptance.TreatmentCOW:
		config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true, MemoryCOW: true}
		row.TerminalDisposition = "discarded_after_single_use"
	default:
		t.Fatalf("unsupported core treatment %q", treatment)
	}
	manager, base := newComposableWorkspace(t)
	defer manager.Close()
	factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: base, WorkspaceOwner: "spark-" + scenario.ID}
	started := time.Now()
	runner, err := factory.New(context.Background(), artifact, config)
	if err != nil {
		if treatment == composableacceptance.TreatmentCOW && runtime.GOOS != "linux" {
			row.Status = "skipped"
			row.GuestCreated = 0
			row.GuestDestroyed = 0
			row.TerminalDisposition = "platform_unavailable"
			row.EvidenceComplete = true
			row.RelativeElapsedMillis = float64(time.Since(started).Microseconds()) / 1000
			return row
		}
		t.Fatal(err)
	}
	if treatment == composableacceptance.TreatmentCOW {
		probe := runner.(*wazeroengine.Engine).COWProbe()
		if !probe.COWSelected || probe.Fallback || len(probe.Blockers) != 0 {
			t.Fatalf("scenario=%s COW probe=%+v", scenario.ID, probe)
		}
	}
	request := plainRequest(t, "result = "+pythonStringLiteral(t, scenario.ExpectedArtifact))
	response, err := runner.Run(context.Background(), request, "")
	if err != nil {
		_ = runner.Close(context.Background())
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := responseStringResult(t, response); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s outcome mismatch", scenario.ID, treatment)
	}
	row.RelativeElapsedMillis = float64(time.Since(started).Microseconds()) / 1000
	return row
}

func pythonStringLiteral(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func responseStringResult(t *testing.T, response []byte) string {
	t.Helper()
	var envelope struct {
		Status string `json:"status"`
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
		t.Fatalf("invalid response err=%v envelope=%+v", err, envelope)
	}
	return envelope.Result
}
