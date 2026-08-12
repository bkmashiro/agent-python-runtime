package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/evaluationlab"
	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
	"github.com/bkmashiro/agent-python-runtime/research/operator"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestInspectAndCompareUseSemanticHumanAndBoundedJSON(t *testing.T) {
	parent := cliTestBundle(t, `{"answer":"parent"}`)
	child := cliTestBundle(t, `{"answer":"child"}`)
	parentPath := writeBundle(t, "parent.playback.json", parent)
	childPath := writeBundle(t, "child.playback.json", child)

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"inspect", "-bundle", parentPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("inspect code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: ok") || !strings.Contains(stdout.String(), "sources.demo_catalog") || strings.Contains(stdout.String(), parent.Identity) {
		t.Fatalf("human inspect is not semantic/bounded: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"inspect", "-bundle", parentPath, "-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("inspect JSON code=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() > maxOutputBytes {
		t.Fatalf("JSON output is unbounded: %d", stdout.Len())
	}
	var summary operator.BundleSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil || summary.BundleSHA256 != parent.Identity || summary.SourceCalls != 1 {
		t.Fatalf("summary=%+v err=%v output=%s", summary, err, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"compare", "-left", parentPath, "-right", childPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compare code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "result: different") || !strings.Contains(stdout.String(), "plan: same") || strings.Contains(stdout.String(), parent.Identity) {
		t.Fatalf("human compare is not semantic: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"compare", "-left", parentPath, "-right", childPath, "-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("compare JSON code=%d stderr=%s", code, stderr.String())
	}
	var comparison operator.BundleComparison
	if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil || comparison.SameResult || !comparison.SamePlan || comparison.CallDifferences != 1 {
		t.Fatalf("comparison=%+v err=%v output=%s", comparison, err, stdout.String())
	}
}

func TestBranchPlanPublishesProtectedNoOverwriteAndExportsDAG(t *testing.T) {
	parent := cliTestBundle(t, `{"answer":"parent"}`)
	parentPath := writeBundle(t, "parent.playback.json", parent)
	overridePath := writeProtected(t, "override.json", []byte("{\n  \"answer\": \"child\"\n}"))
	manifestRoot := t.TempDir()
	if err := os.Chmod(manifestRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manifestRoot, "child.branch.json")

	var stdout, stderr bytes.Buffer
	arguments := []string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "override",
		"-override-result", overridePath, "-output", manifestPath,
	}
	if code := execute(arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("branch plan code=%d stderr=%s", code, stderr.String())
	}
	info, err := os.Stat(manifestPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest info=%v err=%v", info, err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := playback.DecodeBranchManifest(manifestBytes)
	if err != nil || manifest.ValidateParent(parent) != nil || manifest.SuffixMode != playback.BranchOverride || len(manifest.SuffixEntries) != 1 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	entry := manifest.SuffixEntries[0]
	if string(entry.Result) != `{"answer":"child"}` || entry.Capability != "sources.demo_catalog" || entry.Evidence.Kind != "branch_override" {
		t.Fatalf("override entry=%+v", entry)
	}
	child, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: manifest.ChildCapabilityPlanSHA256, RequestSHA256: manifest.RequestSHA256,
		ArtifactSHA256: manifest.ArtifactSHA256, ExecutionProfileSHA256: manifest.ExecutionProfileSHA256,
		ExpectedStatus: "ok", ExpectedResultSHA256: playback.SHA256([]byte(`{"answer":"child"}`)),
		Grants: manifest.ChildGrants,
	}, manifest.SuffixEntries)
	if err != nil {
		t.Fatal(err)
	}
	childPath := writeBundle(t, "child.playback.json", child)
	before := append([]byte(nil), manifestBytes...)
	stdout.Reset()
	stderr.Reset()
	if code := execute(arguments, &stdout, &stderr); code == 0 {
		t.Fatal("branch plan overwrote an existing manifest")
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("existing manifest changed err=%v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"branch", "dag", "-parent", parentPath, "-manifest", manifestPath, "-child", childPath, "-max-nodes", "8", "-json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("branch DAG code=%d stderr=%s", code, stderr.String())
	}
	var dag operator.BranchDAG
	if err := json.Unmarshal(stdout.Bytes(), &dag); err != nil || len(dag.Nodes) != 2 || len(dag.Edges) != 1 || dag.Edges[0].BranchSHA256 != manifest.Identity {
		t.Fatalf("dag=%+v err=%v output=%s", dag, err, stdout.String())
	}
}

func TestStoreStatsIsReadOnlyAndBenchmarkIsBounded(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(storeRoot, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(labstore.KindPrompt, []byte("shared prompt"), labstore.PutOptions{
		Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := cliTreeSnapshot(t, storeRoot)

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"store", "stats", "-root", storeRoot}, &stdout, &stderr); code != 0 {
		t.Fatalf("store stats code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "objects: 1") || strings.Contains(stdout.String(), storeRoot) {
		t.Fatalf("human stats is not semantic: %q", stdout.String())
	}
	after := cliTreeSnapshot(t, storeRoot)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only stats changed store\nbefore=%#v\nafter=%#v", before, after)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"store", "stats", "-root", storeRoot, "-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("store stats JSON code=%d stderr=%s", code, stderr.String())
	}
	var stats labstore.StoreStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil || stats.SchemaVersion != labstore.StatsSchemaVersion || stats.ObjectCount != 1 {
		t.Fatalf("stats=%+v err=%v output=%s", stats, err, stdout.String())
	}
	if !reflect.DeepEqual(before, cliTreeSnapshot(t, storeRoot)) {
		t.Fatal("JSON read-only stats changed store")
	}

	benchmarkRoot := filepath.Join(t.TempDir(), "benchmark")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"store", "benchmark", "-root", benchmarkRoot, "-long-steps", "2", "-branch-children", "2",
		"-swarm-agents", "1", "-swarm-steps", "2", "-low-reuse-items", "2", "-payload-bytes", "64", "-json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("store benchmark code=%d stderr=%s", code, stderr.String())
	}
	var report labstore.BenchmarkReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.SchemaVersion != labstore.BenchmarkSchemaVersion || len(report.Shapes) != 4 {
		t.Fatalf("report=%+v err=%v output=%s", report, err, stdout.String())
	}
	if info, err := os.Stat(benchmarkRoot); err != nil || !info.IsDir() {
		t.Fatalf("benchmark root info=%v err=%v", info, err)
	}
}

func TestBranchPlanSupportsRecordedAndExplicitLiveSuffix(t *testing.T) {
	parent := cliTestBundle(t, `{"answer":"parent"}`)
	alternate := cliTestBundle(t, `{"answer":"alternate"}`)
	parentPath := writeBundle(t, "parent.playback.json", parent)
	alternatePath := writeBundle(t, "alternate.playback.json", alternate)
	root := protectedDir(t)

	var stdout, stderr bytes.Buffer
	recordedPath := filepath.Join(root, "recorded.branch.json")
	if code := execute([]string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "recorded_suffix",
		"-suffix-bundle", alternatePath, "-output", recordedPath,
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("recorded plan code=%d stderr=%s", code, stderr.String())
	}
	recorded := readManifestForTest(t, recordedPath)
	if recorded.SuffixMode != playback.BranchRecordedSuffix || len(recorded.SuffixEntries) != 1 || string(recorded.SuffixEntries[0].Result) != `{"answer":"alternate"}` {
		t.Fatalf("recorded=%+v", recorded)
	}

	stdout.Reset()
	stderr.Reset()
	livePath := filepath.Join(root, "live.branch.json")
	if code := execute([]string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "live_suffix",
		"-child-binding-bundle", alternatePath, "-output", livePath, "-json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("live plan code=%d stderr=%s", code, stderr.String())
	}
	live := readManifestForTest(t, livePath)
	if live.SuffixMode != playback.BranchLiveSuffix || len(live.SuffixEntries) != 0 {
		t.Fatalf("live=%+v", live)
	}
	var report branchPlanReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.SchemaVersion != branchPlanSchemaVersion || report.SuffixMode != "live_suffix" || report.SuffixOperations != 0 {
		t.Fatalf("report=%+v err=%v output=%s", report, err, stdout.String())
	}
}

func TestCLIRejectsUnsafeUnboundedAndAmbiguousInputs(t *testing.T) {
	parent := cliTestBundle(t, `{"answer":"parent"}`)
	parentPath := writeBundle(t, "parent.playback.json", parent)
	validOverride := writeProtected(t, "override.json", []byte(`{"answer":"child"}`))

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "research/operator.RunBranch") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"branch", "run"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "RunBranch") {
		t.Fatalf("branch run boundary code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"inspect", "-bundle", "relative.json"}, &stdout, &stderr); code == 0 {
		t.Fatal("relative input path accepted")
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"inspect", "-bundle", parentPath, "-max-calls", "257"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unbounded max-calls code=%d stderr=%q", code, stderr.String())
	}

	link := filepath.Join(t.TempDir(), "bundle-link.json")
	if err := os.Symlink(parentPath, link); err == nil {
		stdout.Reset()
		stderr.Reset()
		if code := execute([]string{"inspect", "-bundle", link}, &stdout, &stderr); code == 0 {
			t.Fatal("symbolic-link input accepted")
		}
	}

	duplicate := writeProtected(t, "duplicate.json", []byte(`{"answer":1,"answer":2}`))
	outputRoot := protectedDir(t)
	duplicateOutput := filepath.Join(outputRoot, "duplicate.branch.json")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "override",
		"-override-result", duplicate, "-output", duplicateOutput,
	}, &stdout, &stderr); code == 0 {
		t.Fatal("duplicate-key override accepted")
	}
	if _, err := os.Lstat(duplicateOutput); !os.IsNotExist(err) {
		t.Fatalf("failed plan published output: %v", err)
	}

	permissiveRoot := t.TempDir()
	if err := os.Chmod(permissiveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "override",
		"-override-result", validOverride, "-output", filepath.Join(permissiveRoot, "unsafe.branch.json"),
	}, &stdout, &stderr); code == 0 {
		t.Fatal("manifest published into permissive parent")
	}

	missingStore := filepath.Join(t.TempDir(), "missing-store")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"store", "stats", "-root", missingStore}, &stdout, &stderr); code == 0 {
		t.Fatal("missing read-only store accepted")
	}
	if _, err := os.Lstat(missingStore); !os.IsNotExist(err) {
		t.Fatalf("read-only stats created a store: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	hugeCommand := strings.Repeat("x", maxOutputBytes+1024)
	if code := execute([]string{hugeCommand}, &stdout, &stderr); code != 2 || stderr.Len() > maxOutputBytes {
		t.Fatalf("unbounded error output code=%d bytes=%d", code, stderr.Len())
	}
}

func TestConcurrentBranchPublicationIsAtomicNoOverwrite(t *testing.T) {
	parent := cliTestBundle(t, `{"answer":"parent"}`)
	parentPath := writeBundle(t, "parent.playback.json", parent)
	overridePath := writeProtected(t, "override.json", []byte(`{"answer":"child"}`))
	outputPath := filepath.Join(protectedDir(t), "winner.branch.json")
	arguments := []string{
		"branch", "plan", "-parent", parentPath, "-fork", "0", "-mode", "override",
		"-override-result", overridePath, "-output", outputPath,
	}
	const contenders = 8
	codes := make(chan int, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var stdout, stderr bytes.Buffer
			codes <- execute(arguments, &stdout, &stderr)
		}()
	}
	wait.Wait()
	close(codes)
	winners := 0
	for code := range codes {
		if code == 0 {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("publication winners=%d", winners)
	}
	manifest := readManifestForTest(t, outputPath)
	if manifest.ParentBundleSHA256 != parent.Identity || manifest.SuffixMode != playback.BranchOverride {
		t.Fatalf("published manifest=%+v", manifest)
	}
}

func cliTestBundle(t *testing.T, result string) playback.Bundle {
	t.Helper()
	raw := json.RawMessage(result)
	entry := capability.TranscriptEntry{
		OperationIndex:  0,
		Capability:      "sources.demo_catalog",
		Arguments:       json.RawMessage(`{}`),
		ArgumentsSHA256: playback.SHA256([]byte(`{}`)),
		Result:          raw,
		ResultSHA256:    playback.SHA256(raw),
		Evidence: capability.TransportEvidence{
			Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(raw)), BodySHA256: playback.SHA256(raw),
		},
	}
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: cliDigest('a'), RequestSHA256: cliDigest('b'), ArtifactSHA256: cliDigest('c'),
		ExecutionProfileSHA256: cliDigest('d'), ExpectedStatus: "ok", ExpectedResultSHA256: playback.SHA256(raw),
		Grants: []capability.GrantBinding{{Capability: "sources.demo_catalog", PolicySHA256: cliDigest('e')}},
	}, []capability.TranscriptEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func writeBundle(t *testing.T, name string, bundle playback.Bundle) string {
	t.Helper()
	encoded, err := playback.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeProtected(t *testing.T, name string, value []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func protectedDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func readManifestForTest(t *testing.T, path string) playback.BranchManifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := playback.DecodeBranchManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestLabProjectEmitsCanonicalGoDocumentsReadOnly(t *testing.T) {
	reportPath, rowID := writeEvaluationReport(t)
	before, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := evaluationlab.Project(before, rowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range labview.AllKinds() {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"lab", "project", "-report", reportPath, "-row", rowID, "-kind", string(kind)}, &stdout, &stderr); code != 0 {
			t.Fatalf("kind=%s code=%d stderr=%s", kind, code, stderr.String())
		}
		if stdout.Len() > maxOutputBytes {
			t.Fatalf("kind=%s bytes=%d", kind, stdout.Len())
		}
		if _, _, err := labview.Decode(kind, stdout.Bytes()); err != nil {
			t.Fatalf("kind=%s: %v", kind, err)
		}
		var value any
		switch kind {
		case labview.KindIndex:
			value = direct.Index
		case labview.KindStudySummary:
			value = direct.Study
		case labview.KindRunDetail:
			value = direct.Run
		case labview.KindTimelinePage:
			value = direct.Timeline
		case labview.KindBranchDAG:
			value = direct.DAG
		case labview.KindWorkspaceDiff:
			value = direct.Workspace
		case labview.KindRunComparison:
			value = direct.Comparison
		case labview.KindObjectRef:
			value = direct.Refs
		case labview.KindProblem:
			value = direct.Problem
		}
		expected, _, err := labview.Encode(kind, value)
		if err != nil || !bytes.Equal(stdout.Bytes(), expected) {
			t.Fatalf("kind=%s producer drift err=%v", kind, err)
		}
	}
	after, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("lab project mutated report")
	}
}

func writeEvaluationReport(t *testing.T) (string, string) {
	t.Helper()
	row := evaluation.Row{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: cliDigest('a'), PlanSHA256: cliDigest('b'), EvidenceRefs: []string{cliDigest('c')}}
	other := evaluation.Row{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentOfflineReplay, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentOfflineReplay, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: cliDigest('a'), PlanSHA256: cliDigest('b'), EvidenceRefs: []string{cliDigest('d')}}
	report := evaluation.Report{SchemaVersion: evaluation.ReportSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, CorpusSHA256: cliDigest('a'), PlanSHA256: cliDigest('b'), ProhibitedClaims: evaluation.RequiredProhibitedClaims(), Summary: evaluation.Summary{Offered: 2, Completed: 2}, Rows: []evaluation.Row{row, other}}
	body, _, err := evaluation.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "report.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path, row.RowID
}

func cliDigest(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}

type cliSnapshotEntry struct {
	Mode    os.FileMode
	Size    int64
	ModTime int64
	Body    string
}

func cliTreeSnapshot(t *testing.T, root string) map[string]cliSnapshotEntry {
	t.Helper()
	result := make(map[string]cliSnapshotEntry)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := cliSnapshotEntry{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value.Body = string(body)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
