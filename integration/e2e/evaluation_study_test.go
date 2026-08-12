package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/evaluationworkloads"
	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/operator"
	"github.com/bkmashiro/agent-python-runtime/research/workloads"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

type studyBaseline struct {
	result    json.RawMessage
	workspace string
	tape      []capability.TranscriptEntry
}

func TestRealGuestEvaluationStudy(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = qualifiedPlanningConfig(t, artifactPath, artifact)
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evaluationworkloads.Corpus(definitions)
	if err != nil {
		t.Fatal(err)
	}
	corpusBytes, corpusID, err := evaluation.EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	hostCommit := os.Getenv("AGENT_RUNTIME_HOST_COMMIT")
	if len(hostCommit) != 40 {
		t.Fatal("AGENT_RUNTIME_HOST_COMMIT must be the exact 40-character commit identity")
	}
	plan := evaluation.Plan{SchemaVersion: evaluation.PlanSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, HostCommit: hostCommit, GuestArtifactSHA256: shaID(artifact), GuestManifestSHA256: shaID(manifestBytes), CorpusSHA256: corpusID, RuntimeProfileSHA256: shaID([]byte("wazero/default-plus-qualified-deterministic/v1")), TreatmentOrder: []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch, evaluation.TreatmentDeterministicVerify}, Repetitions: 1, Ceilings: evaluation.Ceilings{MaxRows: 12, MaxWallMillisPerRow: 180000, MaxEvidenceBytesPerRow: 1 << 20}, ProhibitedClaims: evaluation.RequiredProhibitedClaims()}
	planBytes, planID, err := evaluation.EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := evaluation.ExpandPlanRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	store, err := labstore.Open(filepath.Join(t.TempDir(), "labstore"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baselines := map[string]studyBaseline{}
	evidenceFiles := map[string][]byte{}
	raw := evaluation.RawStudy{SchemaVersion: evaluation.RawSchemaVersion, CorpusSHA256: corpusID, PlanSHA256: planID, Rows: make([]evaluation.RawRow, len(planned))}
	byID := map[string]workloads.Workload{}
	for _, d := range definitions {
		byID[d.ID] = d
	}
	for i, item := range planned {
		recorder, err := evaluation.NewRowRecorder(item)
		if err != nil {
			t.Fatal(err)
		}
		if !item.Supported {
			row, err := recorder.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			raw.Rows[i] = row
			continue
		}
		setupStart := time.Now()
		definition := byID[item.WorkloadID]
		binding := workloadWorkspace(t, definition)
		if err := recorder.RecordSetup(ms(setupStart)); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Start(); err != nil {
			t.Fatal(err)
		}
		execStart := time.Now()
		response, workspaceID, tape, replayChecked, replayEquivalent, branchChecked, branchDiverged := executeStudyTreatment(t, artifactPath, artifact, definition, item.Treatment, binding, baselines)
		executionMillis := ms(execStart)
		if executionMillis > plan.Ceilings.MaxWallMillisPerRow {
			t.Fatalf("row %s exceeded execution ceiling: %dms", item.RowID, executionMillis)
		}
		if err := recorder.RecordExecution(executionMillis); err != nil {
			t.Fatal(err)
		}
		oracleStart := time.Now()
		snapshot, err := binding.manager.Snapshot(binding.ref)
		if err != nil {
			t.Fatal(err)
		}
		entries := make([]workloads.WorkspaceEntry, len(snapshot.Entries))
		for j, e := range snapshot.Entries {
			entries[j] = workloads.WorkspaceEntry{Path: e.Path, Kind: e.Kind, Executable: e.Executable, Size: e.Size, SHA256: e.SHA256}
		}
		if definition.ID == "bounded-planning-v1" {
			entries = []workloads.WorkspaceEntry{}
		}
		oracle := evaluation.OraclePassed
		if item.Treatment == evaluation.TreatmentCounterfactualBranch {
			if !branchDiverged {
				oracle = evaluation.OracleFailed
			}
		} else if err := definition.Verify(response, entries, uint32(len(tape))); err != nil {
			oracle = evaluation.OracleFailed
		}
		if err := recorder.RecordOracle(oracle, ms(oracleStart)); err != nil {
			t.Fatal(err)
		}
		if item.Treatment == evaluation.TreatmentLiveCapture {
			baselines[definition.ID] = studyBaseline{result: append(json.RawMessage(nil), response...), workspace: workspaceID, tape: append([]capability.TranscriptEntry(nil), tape...)}
		}
		evidenceStart := time.Now()
		before, err := store.Stats()
		if err != nil {
			t.Fatal(err)
		}
		puts := uint32(0)
		reused := uint32(0)
		logical := uint64(0)
		for _, body := range [][]byte{corpusBytes, planBytes} {
			_, published, err := store.PutJSON(labstore.KindSemanticDocument, body, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
			if err != nil {
				t.Fatal(err)
			}
			puts++
			if !published {
				reused++
			}
			logical += uint64(len(body))
		}
		after, err := store.Stats()
		if err != nil {
			t.Fatal(err)
		}
		metrics := evaluation.RowMetrics{ReplayChecked: replayChecked, ReplayEquivalent: replayEquivalent, BranchChecked: branchChecked, BranchDiverged: branchDiverged, LogicalBytes: logical, StoredBytes: after.StoredBytes - before.StoredBytes, ObjectCount: puts, ReusedObjectCount: reused}
		complete := oracle == evaluation.OraclePassed
		problem := ""
		if !complete {
			problem = "oracle_mismatch"
		}
		if err := recorder.RecordEvidence(complete, ms(evidenceStart), metrics, problem); err != nil {
			t.Fatal(err)
		}
		row, err := recorder.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		if row.Status != evaluation.RowUnsupported {
			evidenceBody, evidenceID, err := row.BodyFreeEvidence(corpusID, planID)
			if err != nil || uint64(len(evidenceBody)) > plan.Ceilings.MaxEvidenceBytesPerRow {
				t.Fatalf("row %s evidence exceeds ceiling or is invalid: bytes=%d err=%v", item.RowID, len(evidenceBody), err)
			}
			evidenceFiles["row-"+strings.TrimPrefix(evidenceID, "sha256:")+".json"] = evidenceBody
		}
		raw.Rows[i] = row
	}
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	rawBytes, rawID, err := evaluation.EncodeRawStudy(raw)
	if err != nil {
		t.Fatal(err)
	}
	report, refs, err := evaluation.RebuildReport(raw, planned)
	if err != nil {
		t.Fatal(err)
	}
	reportBytes, reportID, err := evaluation.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	_, summaryBytes, summaryID, err := evaluation.DeriveMeasurementSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, decodedID, err := evaluation.DecodeRawStudy(rawBytes)
	if err != nil || decodedID != rawID {
		t.Fatal(err)
	}
	report2, refs2, err := evaluation.RebuildReport(rebuilt, planned)
	if err != nil {
		t.Fatal(err)
	}
	_, reportID2, err := evaluation.EncodeReport(report2)
	if err != nil || reportID2 != reportID || fmt.Sprint(refs) != fmt.Sprint(refs2) {
		t.Fatal("independent report rebuild drift")
	}
	root := os.Getenv("PYSOLATE_EVALUATION_OUTPUT")
	privateRoot := os.Getenv("PYSOLATE_PRIVATE_ROOT")
	root, err = validatePrivateOutput(privateRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"corpus.json": corpusBytes, "plan.json": planBytes, "raw.json": rawBytes, "report.json": reportBytes, "measurements.json": summaryBytes, "identities.json": []byte(fmt.Sprintf("{\"measurements\":%q,\"raw\":%q,\"report\":%q}\n", summaryID, rawID, reportID))}
	for name, body := range evidenceFiles {
		files[name] = body
	}
	if err := writePrivateStudy(privateRoot, root, files); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluationStudyOutputRequiresAbsoluteDeclaredPrivateRoot(t *testing.T) {
	privateRoot := filepath.Join(t.TempDir(), ".artifacts-private")
	if err := os.MkdirAll(privateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(privateRoot, "study")
	if got, err := validatePrivateOutput(privateRoot, child); err != nil || got != filepath.Clean(child) {
		t.Fatalf("child=%q err=%v", got, err)
	}
	for _, invalid := range [][2]string{{".artifacts-private", "study"}, {privateRoot, privateRoot}, {privateRoot, filepath.Dir(privateRoot)}, {privateRoot, filepath.Join(privateRoot, "..", "public")}} {
		if _, err := validatePrivateOutput(invalid[0], invalid[1]); err == nil {
			t.Fatalf("accepted root=%q output=%q", invalid[0], invalid[1])
		}
	}
	if err := writePrivateStudy(privateRoot, child, map[string][]byte{"report.json": []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(child); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("child mode=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Join(child, "report.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode=%v err=%v", info, err)
	}
	if err := writePrivateStudy(privateRoot, child, map[string][]byte{"report.json": []byte("replacement")}); err == nil {
		t.Fatal("existing study directory was overwritten")
	}
}

func TestPrivateStudyWriterRejectsSymlinkEscape(t *testing.T) {
	privateRoot := filepath.Join(t.TempDir(), ".artifacts-private")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(privateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(privateRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateStudy(privateRoot, filepath.Join(privateRoot, "escape"), map[string][]byte{"report.json": []byte("{}")}); err == nil {
		t.Fatal("symlink escape accepted")
	}
	linkedRoot := filepath.Join(t.TempDir(), ".artifacts-private")
	if err := os.Symlink(outside, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateStudy(linkedRoot, filepath.Join(linkedRoot, "study"), map[string][]byte{"report.json": []byte("{}")}); err == nil {
		t.Fatal("symlink private root accepted")
	}
}

func validatePrivateOutput(privateRoot, output string) (string, error) {
	if output == "" || privateRoot == "" || !filepath.IsAbs(output) || !filepath.IsAbs(privateRoot) {
		return "", fmt.Errorf("absolute private output paths required")
	}
	privateRoot, output = filepath.Clean(privateRoot), filepath.Clean(output)
	relative, err := filepath.Rel(privateRoot, output)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evaluation output must be a child of the declared private root")
	}
	return output, nil
}

func writePrivateStudy(privateRoot, output string, files map[string][]byte) error {
	output, err := validatePrivateOutput(privateRoot, output)
	if err != nil {
		return err
	}
	info, err := os.Lstat(privateRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private root must be a real directory")
	}
	root, err := os.OpenRoot(privateRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(privateRoot, output)
	if err != nil {
		return err
	}
	if relative == "." || strings.ContainsRune(relative, filepath.Separator) {
		return fmt.Errorf("evaluation output must be one new private-root child")
	}
	if err := root.Mkdir(relative, 0700); err != nil {
		return err
	}
	studyRoot, err := root.OpenRoot(relative)
	if err != nil {
		return err
	}
	defer studyRoot.Close()
	if err := studyRoot.Chmod(".", 0700); err != nil {
		return err
	}
	for name, body := range files {
		if filepath.Base(name) != name || name == "." {
			return fmt.Errorf("invalid private study filename")
		}
		if err := studyRoot.WriteFile(name, body, 0600); err != nil {
			return err
		}
		if err := studyRoot.Chmod(name, 0600); err != nil {
			return err
		}
	}
	return nil
}

func executeStudyTreatment(t *testing.T, artifactPath string, artifact []byte, definition workloads.Workload, treatment evaluation.Treatment, binding branchWorkspaceBinding, baselines map[string]studyBaseline) (json.RawMessage, string, []capability.TranscriptEntry, bool, bool, bool, bool) {
	t.Helper()
	switch treatment {
	case evaluation.TreatmentLiveCapture:
		if definition.ID == "stateful-local-v1" {
			r, tape := runCanonicalWorkload(t, artifact, definition, nil, nil, binding)
			info, _ := binding.manager.Inspect(binding.ref)
			return r.Result, info.WorkspaceSHA256, tape, false, false, false, false
		}
		plan, cleanup := studyLivePlan(t, definition)
		defer cleanup()
		r, tape := runLiveWorkload(t, artifact, definition, plan, binding)
		info, _ := binding.manager.Inspect(binding.ref)
		return r.Result, info.WorkspaceSHA256, tape, false, false, false, false
	case evaluation.TreatmentOfflineReplay:
		base := baselines[definition.ID]
		var plan *capability.Plan
		if definition.ID != "stateful-local-v1" {
			plan, _ = workloadPlayback(t, definition.ID)
		}
		r, tape := runCanonicalWorkload(t, artifact, definition, plan, base.tape, binding)
		info, _ := binding.manager.Inspect(binding.ref)
		equiv := workloads.EqualResult(base.result, r.Result) && base.workspace == info.WorkspaceSHA256
		return r.Result, info.WorkspaceSHA256, tape, true, equiv, false, false
	case evaluation.TreatmentCounterfactualBranch:
		plan, entries := workloadPlayback(t, definition.ID)
		request := workloadRequest(t, definition)
		parent, parentResponse := runParentPlayback(t, artifact, plan, request, entries, binding)
		operation := uint32(0)
		name := "sources.demo_catalog"
		alternate := json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":4,"title":"Beta"},{"id":"gamma","score":10,"title":"Gamma"}]}`)
		if definition.ID == "structured-source-v1" {
			operation = 1
			name = "sources.benchmark_manifest"
			alternate = alternateManifest(t, "evaluation-study-counterfactual")
		}
		override := branchTranscriptEntry(operation, name, alternate, "branch_override")
		manifest, err := playback.NewBranchManifest(playback.BranchMetadata{ParentBundleSHA256: parent.Identity, ForkOperation: operation, RequestSHA256: parent.RequestSHA256, ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256, InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: plan.Identity(), ChildGrants: plan.Grants(), SuffixMode: playback.BranchOverride}, parent, []capability.TranscriptEntry{override})
		if err != nil {
			t.Fatal(err)
		}
		child := workloadWorkspace(t, definition)
		outcome, err := operator.RunBranch(context.Background(), operator.BranchRunConfig{WASM: artifact, Runtime: runtimeconfig.DefaultRunConfig(), Plan: plan, Parent: parent, Manifest: manifest, Request: request, TrustedPrepare: plan.PythonPrelude(), Invocation: runtimeconfig.InvocationRef{AgentRunID: "evaluation-study", InvocationID: definition.ID + "-branch", InvocationAttempt: 1, ExecutionID: definition.ID + "-branch"}, WorkspaceManager: child.manager, WorkspaceRef: child.ref, WorkspaceOwner: definition.ID + "-branch"})
		if err != nil {
			t.Fatal(err)
		}
		diverged := !workloads.EqualResult(parentResponse.Result, outcome.Response.Result) && parent.ExpectedResultSHA256 != outcome.ChildBundle.ExpectedResultSHA256
		return outcome.Response.Result, outcome.ChildBundle.FinalWorkspaceSHA256, outcome.ChildBundle.Entries, false, false, true, diverged
	case evaluation.TreatmentDeterministicVerify:
		results := runStudyDeterministic(t, artifactPath, artifact, definition)
		_, entries := workloadPlayback(t, definition.ID)
		return results[0], "", entries, true, workloads.EqualResult(results[0], results[1]), false, false
	default:
		t.Fatal("unexpected treatment")
	}
	return nil, "", nil, false, false, false, false
}

func studyLivePlan(t *testing.T, definition workloads.Workload) (*capability.Plan, func()) {
	t.Helper()
	demoBody := `{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`
	if definition.ID == "bounded-planning-v1" {
		demoBody = `{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":9,"title":"Beta"},{"id":"gamma","score":6,"title":"Gamma"}]}`
	}
	demo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(demoBody))
	}))
	registry := capability.NewRegistry()
	if err := capability.RegisterDemoCatalog(registry, capability.DemoCatalogPolicy{Endpoint: demo.URL, Timeout: time.Second, MaxResponseBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	var benchmark *httptest.Server
	if definition.ID == "structured-source-v1" {
		body := canonicalFixture(t, "benchmark-manifest.v1.json")
		benchmark = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		if err := capability.RegisterBenchmarkManifest(registry, capability.BenchmarkManifestPolicy{Endpoint: benchmark.URL, Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: definition.ExpectedCapabilityCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan, func() {
		demo.Close()
		if benchmark != nil {
			benchmark.Close()
		}
	}
}

func runStudyDeterministic(t *testing.T, artifactPath string, artifact []byte, definition workloads.Workload) [2]json.RawMessage {
	t.Helper()
	config := qualifiedPlanningConfig(t, artifactPath, artifact)
	plan, entries := workloadPlayback(t, definition.ID)
	var out [2]json.RawMessage
	for i := 0; i < 2; i++ {
		var broker *capability.Broker
		factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
			created, err := capability.NewBroker(capability.Config{RunIdentity: fmt.Sprintf("study-deterministic-%d", i), Plan: plan, Playback: &capability.PlaybackConfig{Entries: entries}})
			broker = created
			return created, err
		}}
		runner, err := factory.New(context.Background(), artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		request := workloadRequestWithCompatibility(t, definition, &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}})
		payload, err := runner.Run(context.Background(), request, plan.PythonPrelude())
		closeErr := runner.Close(context.Background())
		if err != nil || closeErr != nil || broker == nil || broker.Finalize(true) != nil {
			t.Fatalf("deterministic run=%v close=%v", err, closeErr)
		}
		decoded, _ := runtimeconfig.DecodeRunRequest(request)
		response, err := runtimeconfig.DecodeAndValidateRunResponse(decoded, payload)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = response.Result
	}
	return out
}

func qualifiedPlanningConfig(t *testing.T, artifactPath string, artifact []byte) runtimeconfig.RunConfig {
	t.Helper()
	root := filepath.Dir(artifactPath)
	manifest, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	inventory, _ := os.ReadFile(filepath.Join(root, "import-inventory.json"))
	qualification, _ := os.ReadFile(filepath.Join(root, "import-qualification.json"))
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), artifact, manifest, inventory, qualification)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(identity.ArtifactSHA256, "evaluation-study-bounded-planning")
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	config.DeterministicVerification = &deterministic
	return config
}

func shaID(body []byte) string { sum := sha256.Sum256(body); return fmt.Sprintf("sha256:%x", sum[:]) }
func ms(start time.Time) uint64 {
	value := time.Since(start).Milliseconds()
	if value < 0 {
		return 0
	}
	return uint64(value)
}
