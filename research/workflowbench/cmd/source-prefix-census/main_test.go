package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func testPlanDocument(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(planDocument{
		SchemaVersion: "pysolate.capability-plan.v6", MaxCalls: 1,
		Capabilities: []capability.Spec{{
			Name: "fixture.read", Version: "v1", Description: "fixture", EffectClass: capability.EffectExternalRead,
			Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-handler",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
			Python:       &capability.PythonProjection{Module: "fixture", Method: "read", Arguments: []string{"key"}},
		}},
		Grants: []capability.GrantBinding{{Capability: "fixture.read", PolicySHA256: digestBytes([]byte("grant"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPlanProjectionsBindsDocumentAndEffect(t *testing.T) {
	raw := testPlanDocument(t)
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	projections, effects, err := planProjections(indented.Bytes(), digestBytes(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 1 || projections[0].Name != "fixture.read" || effects["fixture.read"] != capability.EffectExternalRead {
		t.Fatalf("projections=%+v effects=%+v", projections, effects)
	}
	if _, _, err := planProjections(raw, digestBytes([]byte("other"))); err == nil {
		t.Fatal("tampered plan identity accepted")
	}
}

func joinedReceipt(t *testing.T, runID, planSHA, capabilityName, sourceSHA, occurrenceID string, span semantic.SourceSpan) receipt.Receipt {
	t.Helper()
	base := receipt.NewBound(runID, planSHA, "call-1", "", capabilityName, 1, digestBytes([]byte("request")), "success", []byte(`{}`))
	bound, err := receipt.BindSource(base, receipt.SourceBinding{
		SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound,
		DocumentID: semantic.SourceDocumentIdentity(sourceSHA), SourceSHA256: sourceSHA, OccurrenceID: occurrenceID,
		Capability: capabilityName, DynamicOccurrence: 1, StartLine: span.StartLine, StartColumn: span.StartColumn, EndLine: span.EndLine, EndColumn: span.EndColumn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func TestVerifyReceiptJoinBindsPlanRunDocumentOccurrenceAndSpan(t *testing.T) {
	runID, planSHA, capabilityName := "run-1", digestBytes([]byte("plan")), "tool.read"
	sourceSHA, occurrenceID := digestBytes([]byte("source")), digestBytes([]byte("occurrence"))
	span := semantic.SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 12}
	analysis := semantic.Analysis{SourceSHA256: sourceSHA, CallSites: []semantic.CallSite{{ID: occurrenceID, Span: span, Capability: capabilityName, DynamicOccurrence: 1}}}
	request := guestRequest{RunID: runID, Code: "source"}
	cell := cellProjection{CapabilityPlanSHA256: planSHA, Receipt: joinedReceipt(t, runID, planSHA, capabilityName, sourceSHA, occurrenceID, span)}
	if err := verifyReceiptJoin(cell, request, analysis, sourceSHA); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name     string
		cell     cellProjection
		req      guestRequest
		analysis semantic.Analysis
	}{
		{name: "self-consistent wrong plan", cell: cellProjection{CapabilityPlanSHA256: planSHA, Receipt: joinedReceipt(t, runID, digestBytes([]byte("other-plan")), capabilityName, sourceSHA, occurrenceID, span)}, req: request, analysis: analysis},
		{name: "wrong run", cell: cell, req: guestRequest{RunID: "other-run"}, analysis: analysis},
		{name: "wrong document", cell: cell, req: request, analysis: semantic.Analysis{SourceSHA256: digestBytes([]byte("other-source")), CallSites: analysis.CallSites}},
		{name: "wrong occurrence", cell: cell, req: request, analysis: semantic.Analysis{SourceSHA256: sourceSHA, CallSites: []semantic.CallSite{{ID: digestBytes([]byte("other-occurrence")), Span: span, Capability: capabilityName, DynamicOccurrence: 1}}}},
		{name: "wrong span", cell: cell, req: request, analysis: semantic.Analysis{SourceSHA256: sourceSHA, CallSites: []semantic.CallSite{{ID: occurrenceID, Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 13}, Capability: capabilityName, DynamicOccurrence: 1}}}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if err := verifyReceiptJoin(mutation.cell, mutation.req, mutation.analysis, sourceSHA); err == nil {
				t.Fatal("expected receipt join rejection")
			}
		})
	}
}

func TestEventIdentityBindsTurnAndPlan(t *testing.T) {
	parent := digestBytes([]byte("parent"))
	a := eventIdentity(parent, "1", "2", digestBytes([]byte("source")), digestBytes([]byte("plan")))
	b := eventIdentity(parent, "1", "3", digestBytes([]byte("source")), digestBytes([]byte("plan")))
	if a == b || a == "" || b == "" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	var request guestRequest
	if err := strictDecode([]byte(`{"run_id":"r","code":"result=1","inputs":{},"unknown":true}`), &request); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestCheckedInCensusEvidenceAndReportAreBound(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "docs")
	evidenceRaw, err := os.ReadFile(filepath.Join(root, "evidence", "source-prefix-opportunity-census-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if digestBytes(evidenceRaw) != "sha256:cfedf4adfe63051d9e7b233ef8b36031fb4fda360a7d32e0e634cdce31da5604" {
		t.Fatal("public census evidence raw SHA drift")
	}
	evidence, err := workflowbench.DecodeSourcePrefixCensusEvidence(evidenceRaw)
	if err != nil || evidence.Denominator.Events != 36 || evidence.Denominator.UniqueSources != 30 || evidence.Counts.StructurallyEligible != 0 || evidence.Counts.StructurallyIneligible != 36 || evidence.Counts.TimingNotRecorded != 36 {
		t.Fatalf("invalid checked-in census evidence: %+v err=%v", evidence, err)
	}
	preregistration, err := os.ReadFile(filepath.Join(root, "evidence", "source-prefix-opportunity-census-preregistration-v1.json"))
	if err != nil || digestBytes(preregistration) != fixedPreregistrationSHA256 {
		t.Fatal("checked-in census preregistration drift")
	}
	report, err := os.ReadFile(filepath.Join(root, "research", "source-prefix-opportunity-census-v1.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{
		fixedParentReportSHA256, fixedPreregistrationSHA256, fixedArtifactSourceCommit, fixedArtifactSHA256,
		fixedArtifactManifestSHA256, fixedAcceptedHarnessCommit, evidence.Identity,
		"sha256:cfedf4adfe63051d9e7b233ef8b36031fb4fda360a7d32e0e634cdce31da5604",
		"Do **not** run trace-derived timing replay for this cohort",
	} {
		if !strings.Contains(string(report), identity) {
			t.Fatalf("census report missing accepted identity/boundary %s", identity)
		}
	}
	if strings.Contains(string(report), "1.923") {
		t.Fatal("census report must not repeat authored speedup values")
	}
}

func TestCurrentHarnessCommitRejectsNonAcceptedBuild(t *testing.T) {
	if commit, err := currentHarnessCommit(); err == nil || commit != "" {
		t.Fatalf("post-measurement build must not masquerade as accepted harness: commit=%q err=%v", commit, err)
	}
}

func TestWritePrivateUsesRestrictedMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(root, "evidence.json")
	if err := writePrivate(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Mode().Perm() != 0o700 || file.Mode().Perm() != 0o600 {
		t.Fatalf("directory=%#o file=%#o", directory.Mode().Perm(), file.Mode().Perm())
	}
}
