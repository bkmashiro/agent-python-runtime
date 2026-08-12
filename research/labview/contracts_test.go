package labview_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCanonicalFixtureMatrixAndStrictRoundTrip(t *testing.T) {
	fixtures, err := labview.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 54 {
		t.Fatalf("fixtures=%d", len(fixtures))
	}
	for name, fixture := range fixtures {
		decoded, identity, err := labview.Decode(fixture.Kind, fixture.Bytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		reencoded, second, err := labview.Encode(fixture.Kind, decoded)
		if err != nil || identity != second || !bytes.Equal(fixture.Bytes, reencoded) {
			t.Fatalf("%s round trip", name)
		}
		hash := sha256.Sum256(fixture.Bytes)
		if fixture.SHA256 != fmt.Sprintf("sha256:%x", hash[:]) {
			t.Fatalf("%s hash", name)
		}
		actual, err := os.ReadFile(filepath.Join("testdata", "canonical", name))
		if err != nil || !bytes.Equal(actual, fixture.Bytes) {
			t.Fatalf("%s checked-in drift: %v", name, err)
		}
	}
	manifest, err := labview.CanonicalFixtureManifest()
	if err != nil {
		t.Fatal(err)
	}
	checkedManifest, err := os.ReadFile(filepath.Join("testdata", "canonical", "manifest.sha256"))
	if err != nil || !bytes.Equal(manifest, checkedManifest) {
		t.Fatalf("manifest drift: %v", err)
	}
}

func TestCanonicalCasesCrossValidate(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty", "ordinary", "branched", "incomplete", "truncated", "private"} {
		set, ok := sets[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if err := labview.ValidateSet(set); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestStrictAndCrossContractFailures(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	ordinary := sets["branched"]
	encoded, _, err := labview.Encode(labview.KindRunDetail, ordinary.Run)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"unknown":      append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"unknown":true}`)...),
		"trailing":     append(append([]byte(nil), encoded...), []byte(`{}`)...),
		"noncanonical": append([]byte(" "), encoded...),
	} {
		if _, _, err := labview.Decode(labview.KindRunDetail, raw); !errors.Is(err, labview.ErrInvalid) {
			t.Fatalf("%s err=%v", name, err)
		}
	}

	mutate := func(name string, fn func(*labview.Set)) {
		value := ordinary.Clone()
		fn(&value)
		refreshIndexDigests(t, &value)
		if err := labview.ValidateSet(value); !errors.Is(err, labview.ErrInvalid) {
			t.Fatalf("%s err=%v", name, err)
		}
	}
	mutate("private available", func(s *labview.Set) { s.Run.Refs[0].Privacy = labview.PrivacyPrivate })
	mutate("absolute path", func(s *labview.Set) { s.Workspace.Changes[0].Path = "/private/result.json" })
	mutate("traversal path", func(s *labview.Set) { s.Workspace.Changes[0].Path = "../result.json" })
	mutate("windows host path", func(s *labview.Set) { s.Workspace.Changes[0].Path = "C:/Users/secret.txt" })
	mutate("pagination", func(s *labview.Set) { s.Timeline.Page.Total = 0 })
	mutate("unsafe cursor", func(s *labview.Set) { s.Timeline.Page.Cursor = "../private/path" })
	mutate("index order", func(s *labview.Set) { s.Index.Links[0], s.Index.Links[1] = s.Index.Links[1], s.Index.Links[0] })
	mutate("changed delta same digest", func(s *labview.Set) { s.Comparison.CallDeltas[0].RightSHA256 = s.Comparison.CallDeltas[0].LeftSHA256 })
	mutate("left-only has right", func(s *labview.Set) { s.Comparison.CallDeltas[0].Kind = "left_only" })
	mutate("dangling edge", func(s *labview.Set) { s.DAG.Edges[0].ChildRunID = "run-missing" })
	mutate("timeline completeness drift", func(s *labview.Set) { s.Timeline.EvidenceCompleteness = labview.Incomplete })
	mutate("DAG status drift", func(s *labview.Set) {
		for i := range s.DAG.Nodes {
			if s.DAG.Nodes[i].RunID == s.Run.RunID {
				s.DAG.Nodes[i].Status = "failed"
			}
		}
	})
	mutate("cycle", func(s *labview.Set) {
		s.DAG.Edges = append(s.DAG.Edges, labview.DAGEdge{ParentRunID: s.DAG.Edges[0].ChildRunID, ChildRunID: s.DAG.Edges[0].ParentRunID, ForkOperation: 0, SuffixMode: labview.SuffixOverride})
	})
	mutate("comparison link", func(s *labview.Set) { s.Comparison.RightRunID = "run-missing" })
	mutate("weaken claims", func(s *labview.Set) { s.Study.ProhibitedClaims = s.Study.ProhibitedClaims[:1] })
	mutate("missing status total", func(s *labview.Set) { s.Study.StatusTotals = s.Study.StatusTotals[:3] })
	mutate("status total reorder", func(s *labview.Set) {
		s.Study.StatusTotals[0], s.Study.StatusTotals[1] = s.Study.StatusTotals[1], s.Study.StatusTotals[0]
	})
	mutate("private case body availability", func(s *labview.Set) {
		s.Refs.Ref.Availability = labview.AvailabilityAvailable
		s.Refs.Ref.Privacy = labview.PrivacyPrivate
	})
	mutate("authority capability", func(s *labview.Set) { s.Index.Capabilities[0] = "execute" })
	mutate("link relation drift", func(s *labview.Set) { s.Index.Links[0].Rel = "execution" })
	mutate("missing index projection", func(s *labview.Set) { s.Index.Links = s.Index.Links[1:]; s.Index.Page.Returned--; s.Index.Page.Total-- })
	mutate("terminal outcome drift", func(s *labview.Set) { s.Timeline.Events[len(s.Timeline.Events)-1].Outcome = "denied" })
	mutate("denied call result", func(s *labview.Set) { s.Timeline.Events[1].Outcome = "denied" })
	mutate("unknown ref kind", func(s *labview.Set) { s.Run.Refs[0].Kind = "authority_grant" })
	mutate("completed failed oracle", func(s *labview.Set) { s.Run.Status = "completed"; s.Run.OracleStatus = "failed" })
	mutate("failed passed oracle", func(s *labview.Set) { s.Run.Status = "failed"; s.Run.OracleStatus = "passed" })
	mutate("authority problem code", func(s *labview.Set) { s.Run.ProblemCodes = []string{"authority_granted"} })
	mutate("missing required ref", func(s *labview.Set) { s.Run.Refs = s.Run.Refs[1:] })
	mutate("duplicate ref kind", func(s *labview.Set) { s.Run.Refs[1].Kind = s.Run.Refs[0].Kind })
	mutate("problem run drift", func(s *labview.Set) { s.Problem.RunID = "run-missing" })
	mutate("problem ref drift", func(s *labview.Set) {
		s.Problem.RefSHA256 = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	})
	mutate("run problem missing run", func(s *labview.Set) { s.Problem.Scope = "run"; s.Problem.RunID = "" })
	mutate("reference problem missing ref", func(s *labview.Set) { s.Problem.Scope = "reference"; s.Problem.RefSHA256 = "" })
	mutate("index problem with linkage", func(s *labview.Set) { s.Problem.Scope = "index"; s.Problem.RunID = s.Run.RunID })
}

func refreshIndexDigests(t *testing.T, set *labview.Set) {
	t.Helper()
	documents := map[labview.Kind]any{
		labview.KindStudySummary:  set.Study,
		labview.KindRunDetail:     set.Run,
		labview.KindTimelinePage:  set.Timeline,
		labview.KindBranchDAG:     set.DAG,
		labview.KindWorkspaceDiff: set.Workspace,
		labview.KindRunComparison: set.Comparison,
		labview.KindObjectRef:     set.Refs,
		labview.KindProblem:       set.Problem,
	}
	for index := range set.Index.Links {
		link := &set.Index.Links[index]
		_, identity, err := labview.Encode(link.Kind, documents[link.Kind])
		if err == nil {
			link.SHA256 = identity
		}
	}
}

func TestCapabilityTimeoutProjection(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	set := sets["ordinary"]
	set.Timeline.Events[1].Outcome = "timeout"
	set.Timeline.Events[1].ResultSHA256 = ""
	refreshIndexDigests(t, &set)
	if err := labview.ValidateSet(set); err != nil {
		t.Fatalf("timeout projection rejected: %v", err)
	}
	raw, _, err := labview.Encode(labview.KindTimelinePage, set.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := compileSchema(t, labview.KindTimelinePage).Validate(instance); err != nil {
		t.Fatalf("timeout schema rejected: %v", err)
	}
}

func TestTimelineContinuationSequence(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	timeline := sets["ordinary"].Timeline
	timeline.Events = timeline.Events[:2]
	timeline.Events[0].Sequence, timeline.Events[0].ParentSequence = 5, 4
	timeline.Events[1].Sequence, timeline.Events[1].ParentSequence = 6, 4
	timeline.Page = labview.Page{Cursor: "page_2", NextCursor: "page_3", Returned: 2, Total: 8, Truncated: true}
	if _, _, err := labview.Encode(labview.KindTimelinePage, timeline); err != nil {
		t.Fatalf("continuation rejected: %v", err)
	}
}

func TestIndexRequiresAllEightReadProjections(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	index := sets["ordinary"].Index
	index.Links = index.Links[1:]
	index.Page.Returned--
	index.Page.Total--
	if _, _, err := labview.Encode(labview.KindIndex, index); !errors.Is(err, labview.ErrInvalid) {
		t.Fatalf("sparse index err=%v", err)
	}
}

func TestSchemasRejectAdversarialDocuments(t *testing.T) {
	sets, err := labview.CanonicalSets()
	if err != nil {
		t.Fatal(err)
	}
	base := sets["branched"]
	cases := []struct {
		name   string
		kind   labview.Kind
		value  any
		mutate func(map[string]any)
	}{
		{"unknown field", labview.KindRunDetail, base.Run, func(v map[string]any) { v["authority_token"] = "forbidden" }},
		{"invalid status", labview.KindRunDetail, base.Run, func(v map[string]any) { v["status"] = "running" }},
		{"private available", labview.KindObjectRef, base.Refs, func(v map[string]any) {
			r := v["ref"].(map[string]any)
			r["privacy"] = "private"
			r["availability"] = "available"
		}},
		{"workspace traversal", labview.KindWorkspaceDiff, base.Workspace, func(v map[string]any) { v["changes"].([]any)[0].(map[string]any)["path"] = "../escape" }},
		{"windows host path", labview.KindWorkspaceDiff, base.Workspace, func(v map[string]any) { v["changes"].([]any)[0].(map[string]any)["path"] = "C:/Users/secret.txt" }},
		{"timeline outcome", labview.KindTimelinePage, base.Timeline, func(v map[string]any) { v["events"].([]any)[2].(map[string]any)["outcome"] = "denied" }},
		{"authority ref", labview.KindRunDetail, base.Run, func(v map[string]any) { v["refs"].([]any)[0].(map[string]any)["kind"] = "authority_grant" }},
		{"duplicate ref kind", labview.KindRunDetail, base.Run, func(v map[string]any) { v["refs"].([]any)[1].(map[string]any)["kind"] = "artifact" }},
		{"unknown comparison dimension", labview.KindRunComparison, base.Comparison, func(v map[string]any) { v["same_dimensions"] = []any{"authority"} }},
		{"unknown reason code", labview.KindRunComparison, base.Comparison, func(v map[string]any) { v["reason_codes"] = []any{"authority_changed"} }},
		{"relation drift", labview.KindIndex, base.Index, func(v map[string]any) { v["links"].([]any)[0].(map[string]any)["rel"] = "execution" }},
	}
	for _, tc := range cases {
		raw, _, err := labview.Encode(tc.kind, tc.value)
		if err != nil {
			t.Fatal(err)
		}
		var instance map[string]any
		if err := json.Unmarshal(raw, &instance); err != nil {
			t.Fatal(err)
		}
		tc.mutate(instance)
		if err := compileSchema(t, tc.kind).Validate(instance); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
}

func TestClosedV1CompatibilityKeepsDeclaredOptionalFieldsButRequiresV2ForNewWireFields(t *testing.T) {
	fixtures, err := labview.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	for _, fixture := range fixtures {
		if fixture.Kind == labview.KindProblem {
			if err := json.Unmarshal(fixture.Bytes, &base); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if base == nil {
		t.Fatal("missing problem fixture")
	}
	schema := compileSchema(t, labview.KindProblem)
	withoutDeclaredOptional := make(map[string]any, len(base))
	for key, value := range base {
		withoutDeclaredOptional[key] = value
	}
	delete(withoutDeclaredOptional, "run_id")
	delete(withoutDeclaredOptional, "ref_sha256")
	withoutDeclaredOptional["scope"] = "study"
	if err := schema.Validate(withoutDeclaredOptional); err != nil {
		t.Fatalf("declared optional omission requires no v2: %v", err)
	}
	withDeclaredOptional := make(map[string]any, len(base)+2)
	for key, value := range base {
		withDeclaredOptional[key] = value
	}
	withDeclaredOptional["scope"] = "run"
	withDeclaredOptional["code"] = "evidence_incomplete"
	withDeclaredOptional["run_id"] = "run-compatible"
	delete(withDeclaredOptional, "ref_sha256")
	if err := schema.Validate(withDeclaredOptional); err != nil {
		t.Fatalf("declared optional addition requires no v2: %v", err)
	}
	withUndeclaredOptional := make(map[string]any, len(base)+1)
	for key, value := range base {
		withUndeclaredOptional[key] = value
	}
	withUndeclaredOptional["future_optional"] = nil
	if err := schema.Validate(withUndeclaredOptional); err == nil {
		t.Fatal("new optional wire field was treated as v1-compatible")
	}
}

func compileSchema(t *testing.T, kind labview.Kind) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "lab", "v1", string(kind)+".schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	url := "https://agent-runtime.dev/lab/v1/" + string(kind) + ".schema.json"
	if err := compiler.AddResource(url, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestSchemasExistAndDeclareClosedDraft202012Objects(t *testing.T) {
	fixtures, err := labview.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range labview.AllKinds() {
		path := filepath.Join("..", "..", "schemas", "lab", "v1", string(kind)+".schema.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !bytes.Contains(raw, []byte(`"https://json-schema.org/draft/2020-12/schema"`)) || !bytes.Contains(raw, []byte(`"additionalProperties": false`)) {
			t.Fatalf("%s not closed draft 2020-12", kind)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode %s: %v", kind, err)
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		url := "https://agent-runtime.dev/lab/v1/" + string(kind) + ".schema.json"
		if err := compiler.AddResource(url, document); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile(url)
		if err != nil {
			t.Fatalf("compile %s: %v", kind, err)
		}
		for name, fixture := range fixtures {
			if fixture.Kind != kind {
				continue
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(fixture.Bytes))
			if err != nil {
				t.Fatalf("decode fixture %s: %v", name, err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("%s schema validation: %v", name, err)
			}
		}
	}
}
