package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/evaluationv2"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestEvaluationV2EquivalentJSONIgnoresObjectKeyOrder(t *testing.T) {
	if !equivalentJSON([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`)) || equivalentJSON([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Fatal("JSON equivalence drift")
	}
}

func TestRealGuestEvaluationV2Pilots(t *testing.T)         { runRealGuestEvaluationV2(t, false) }
func TestRealGuestEvaluationV2ExpandedCohort(t *testing.T) { runRealGuestEvaluationV2(t, true) }

func runRealGuestEvaluationV2(t *testing.T, expanded bool) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	hostCommit := os.Getenv("AGENT_RUNTIME_HOST_COMMIT")
	if len(hostCommit) != 40 {
		t.Fatal("AGENT_RUNTIME_HOST_COMMIT must be exact")
	}
	definitions, err := evaluationv2.PilotDefinitions()
	if expanded {
		definitions, err = evaluationv2.ExpandedDefinitions()
	}
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evaluationv2.PilotCorpus()
	if expanded {
		corpus, err = evaluationv2.ExpandedCorpus()
	}
	if err != nil {
		t.Fatal(err)
	}
	corpusBytes, corpusID, err := evaluationv2.EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	profileID, err := evaluationv2.RuntimeProfileSHA256(runConfig)
	if err != nil {
		t.Fatal(err)
	}
	plan := evaluationv2.PilotPlan(hostCommit, shaID(artifact), shaID(manifestBytes), profileID, corpusID)
	if expanded {
		plan = evaluationv2.ExpandedPlan(hostCommit, shaID(artifact), shaID(manifestBytes), profileID, corpusID)
	}
	planBytes, planID, err := evaluationv2.EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := evaluationv2.ExpandRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	catalogServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":4,"title":"Beta"},{"id":"gamma","score":10,"title":"Gamma"}]}`))
	}))
	defer catalogServer.Close()
	manifestBody := canonicalFixture(t, "benchmark-manifest.v1.json")
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(manifestBody)
	}))
	defer manifestServer.Close()
	byID := map[string]evaluationv2.Definition{}
	for _, definition := range definitions {
		byID[definition.Workload.ID] = definition
	}
	rows := make([]evaluationv2.PilotRow, len(planned))
	results := map[string]json.RawMessage{}
	for i, row := range planned {
		rowContext, cancel := context.WithTimeout(context.Background(), time.Duration(plan.Ceilings.MaxWallMillisPerRow)*time.Millisecond)
		definition := byID[row.WorkloadID]
		capabilityPlan := evaluationV2CapabilityPlan(t, definition, catalogServer.URL, manifestServer.URL)
		broker, err := capability.NewBroker(capability.Config{RunIdentity: row.RowID, Plan: capabilityPlan})
		if err != nil {
			t.Fatal(err)
		}
		var result json.RawMessage
		var requestBytes, responseBytes uint64
		switch row.Condition {
		case evaluationv2.ConditionDirect:
			outcome, err := evaluationv2.RunDirect(rowContext, definition, broker)
			if err != nil {
				t.Fatal(err)
			}
			result = outcome.Result
			requestBytes = outcome.ControllerRequestBytes
			responseBytes = outcome.ControllerResponseBytes
		case evaluationv2.ConditionGuest:
			factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) { return broker, nil }}
			runner, err := factory.New(rowContext, artifact, runConfig)
			if err != nil {
				t.Fatal(err)
			}
			request, err := json.Marshal(runtimeconfig.RunRequest{RunID: row.RowID, Code: definition.Code, Inputs: definition.Inputs})
			if err != nil {
				t.Fatal(err)
			}
			payload, runErr := runner.Run(rowContext, request, capabilityPlan.PythonPrelude())
			closeErr := runner.Close(context.Background())
			if runErr != nil || closeErr != nil {
				t.Fatalf("run=%v close=%v", runErr, closeErr)
			}
			decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
			if err != nil {
				t.Fatal(err)
			}
			result = response.Result
			requestBytes, responseBytes = uint64(len(request)), uint64(len(payload))
		default:
			t.Fatal("unknown condition")
		}
		cancel()
		if err := broker.Finalize(true); err != nil {
			t.Fatal(err)
		}
		if err := evaluationv2.VerifyResult(definition, result, broker.CallCount()); err != nil {
			t.Fatal(err)
		}
		metrics, err := evaluationv2.DeriveMetrics(definition, row.Condition, requestBytes, responseBytes, broker)
		if err != nil || metrics.ControllerRequestBytes+metrics.ControllerResponseBytes > plan.Ceilings.MaxSerializedBytesRow {
			t.Fatalf("metrics=%+v err=%v", metrics, err)
		}
		key := row.WorkloadID
		if previous, ok := results[key]; ok && !equivalentJSON(previous, result) {
			t.Fatalf("condition oracle drift workload=%s direct=%s guest=%s", key, previous, result)
		}
		results[key] = append(json.RawMessage(nil), result...)
		rows[i] = evaluationv2.PilotRow{RowID: row.RowID, WorkloadID: row.WorkloadID, Condition: row.Condition, Repetition: row.Repetition, Status: evaluationv2.StatusCompleted, OracleStatus: evaluationv2.OraclePassed, EvidenceComplete: true, CapabilityPlanSHA256: capabilityPlan.Identity(), Metrics: metrics}
	}
	studySchema := evaluationv2.StudySchemaVersion
	wantRows, wantDirectBoundaries, wantGuestBoundaries, wantCalls := uint32(4), uint64(3), uint64(2), uint64(3)
	if expanded {
		studySchema, wantRows, wantDirectBoundaries, wantGuestBoundaries, wantCalls = evaluationv2.ExpandedStudySchemaVersion, 10, 6, 5, 6
	}
	study := evaluationv2.PilotStudy{SchemaVersion: studySchema, EvidenceClass: evaluationv2.EvidenceClass, CorpusSHA256: corpusID, PlanSHA256: planID, ProhibitedClaims: evaluationv2.RequiredProhibitedClaims(), Rows: rows}
	if err := evaluationv2.ValidateStudyAgainst(study, corpus, plan); err != nil {
		t.Fatal(err)
	}
	studyBytes, studyID, err := evaluationv2.EncodeStudy(study)
	if err != nil {
		t.Fatal(err)
	}
	summary, summaryBytes, summaryID, err := evaluationv2.DeriveSummary(study)
	if err != nil || evaluationv2.ValidateSummaryAgainst(summary, study) != nil || summary.Completed != wantRows || summary.OraclePassed != wantRows || summary.DirectControllerBoundaries != wantDirectBoundaries || summary.GuestControllerBoundaries != wantGuestBoundaries || summary.DirectCapabilityCalls != wantCalls || summary.GuestCapabilityCalls != wantCalls {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	t.Logf("v2-pilot offered=%d completed=%d oracle=%d direct_boundaries=%d guest_boundaries=%d direct_calls=%d guest_calls=%d", summary.Offered, summary.Completed, summary.OraclePassed, summary.DirectControllerBoundaries, summary.GuestControllerBoundaries, summary.DirectCapabilityCalls, summary.GuestCapabilityCalls)
	outputEnv := "PYSOLATE_EVALUATION_V2_OUTPUT"
	if expanded {
		outputEnv = "PYSOLATE_EVALUATION_V2_EXPANDED_OUTPUT"
	}
	if output := os.Getenv(outputEnv); output != "" {
		privateRoot := os.Getenv("PYSOLATE_PRIVATE_ROOT")
		validated, err := validatePrivateOutput(privateRoot, output)
		if err != nil {
			t.Fatal(err)
		}
		identities := []byte(fmt.Sprintf("{\"study\":%q,\"summary\":%q}\n", studyID, summaryID))
		if err := writePrivateStudy(privateRoot, validated, map[string][]byte{"corpus.json": corpusBytes, "plan.json": planBytes, "study.json": studyBytes, "summary.json": summaryBytes, "identities.json": identities}); err != nil {
			t.Fatal(err)
		}
	}
}

func equivalentJSON(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func evaluationV2CapabilityPlan(t *testing.T, definition evaluationv2.Definition, catalogURL, manifestURL string) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	for _, name := range definition.Workload.RequiredCapabilities {
		switch name {
		case "sources.demo_catalog":
			if err := capability.RegisterDemoCatalog(registry, capability.DemoCatalogPolicy{Endpoint: catalogURL, Timeout: time.Second, MaxResponseBytes: 4096}); err != nil {
				t.Fatal(err)
			}
		case "sources.benchmark_manifest":
			if err := capability.RegisterBenchmarkManifest(registry, capability.BenchmarkManifestPolicy{Endpoint: manifestURL, Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("unknown capability")
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: definition.Workload.ExpectedCapabilityCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
