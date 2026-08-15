package semantic

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestSourceBoundPlannerDefaultsOffAndProjectsVerifiedSource(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, true)

	planned, err := BuildSourceBoundPlan(verified, capabilityPlan, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	projection := planned.Projection()
	if projection.SchemaVersion != SourceBoundPlanSchemaVersion || len(projection.Documents) != 1 || len(projection.Occurrences) != 1 {
		t.Fatalf("projection=%+v", projection)
	}
	document := projection.Documents[0]
	if document.ID == "" || document.Language != "python" || document.SHA256 == "" {
		t.Fatalf("document=%+v", document)
	}
	occurrence := projection.Occurrences[0]
	if occurrence.ID != site.ID || occurrence.DocumentID != document.ID || occurrence.Span != site.Span ||
		occurrence.Capability != site.Capability || occurrence.DynamicOccurrence != site.DynamicOccurrence {
		t.Fatalf("occurrence=%+v site=%+v", occurrence, site)
	}
	if len(projection.Decisions) != 0 {
		t.Fatalf("default-off decisions=%+v", projection.Decisions)
	}
	if _, ok := planned.QualifiedCall(PassSemanticPreDispatch, site.ID); ok {
		t.Fatal("default-off plan exposed qualified call")
	}
	if planned.Identity() == "" {
		t.Fatal("missing source-bound plan identity")
	}
}

func TestSemanticPreDispatchPassReusesExistingLegalityDecision(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, true)
	config := PlannerConfig{
		Passes:          []PassConfig{{Name: PassSemanticPreDispatch, Version: SemanticPreDispatchPassVersion, Enabled: true}},
		PreissueContext: legalityContext(),
	}

	planned, err := BuildSourceBoundPlan(verified, capabilityPlan, config)
	if err != nil {
		t.Fatal(err)
	}
	projection := planned.Projection()
	if len(projection.Decisions) != 1 {
		t.Fatalf("decisions=%+v", projection.Decisions)
	}
	decision := projection.Decisions[0]
	if decision.PassName != PassSemanticPreDispatch || decision.PassVersion != SemanticPreDispatchPassVersion ||
		decision.OccurrenceID != site.ID || decision.Disposition != PassAdmitted || len(decision.RejectionReasons) != 0 {
		t.Fatalf("decision=%+v", decision)
	}
	fromPass, ok := planned.QualifiedCall(PassSemanticPreDispatch, site.ID)
	if !ok {
		t.Fatal("pass did not retain opaque qualified call")
	}
	directDecision := CanPreissue(verified, capabilityPlan, site.ID, legalityContext())
	direct, ok := directDecision.QualifiedCall()
	if !ok || fromPass.ClaimIdentitySHA256() != direct.ClaimIdentitySHA256() || fromPass.CallSiteID() != direct.CallSiteID() {
		t.Fatalf("pass=%+v direct=%+v", fromPass, direct)
	}

	again, err := BuildSourceBoundPlan(verified, capabilityPlan, config)
	if err != nil || planned.Identity() != again.Identity() || !reflect.DeepEqual(planned.Projection(), again.Projection()) {
		t.Fatalf("non-deterministic plans: first=%+v second=%+v err=%v", planned.Projection(), again.Projection(), err)
	}
}

func TestSourceBoundPlannerPreservesStableRejections(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, false)
	planned, err := BuildSourceBoundPlan(verified, capabilityPlan, PlannerConfig{
		Passes:          []PassConfig{{Name: PassSemanticPreDispatch, Version: SemanticPreDispatchPassVersion, Enabled: true}},
		PreissueContext: legalityContext(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := planned.Projection()
	if len(projection.Decisions) != 1 || projection.Decisions[0].Disposition != PassRejected ||
		projection.Decisions[0].OccurrenceID != site.ID || len(projection.Decisions[0].RejectionReasons) == 0 {
		t.Fatalf("projection=%+v", projection)
	}
	if _, ok := planned.QualifiedCall(PassSemanticPreDispatch, site.ID); ok {
		t.Fatal("rejected decision exposed qualified call")
	}
	for index := 1; index < len(projection.Decisions[0].RejectionReasons); index++ {
		if projection.Decisions[0].RejectionReasons[index-1] >= projection.Decisions[0].RejectionReasons[index] {
			t.Fatalf("rejections not sorted unique: %+v", projection.Decisions[0].RejectionReasons)
		}
	}
}

func TestSourceBoundPlannerFailsClosedForUnknownDuplicateAndVersionMismatch(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, _ := legalityVerifiedAnalysis(t, capabilityPlan, true)
	cases := []struct {
		name   string
		passes []PassConfig
	}{
		{"unknown", []PassConfig{{Name: "unknown", Version: "v0", Enabled: true}}},
		{"unknown disabled", []PassConfig{{Name: "unknown", Version: "v0", Enabled: false}}},
		{"version", []PassConfig{{Name: PassSemanticPreDispatch, Version: "wrong", Enabled: true}}},
		{"duplicate", []PassConfig{
			{Name: PassSemanticPreDispatch, Version: SemanticPreDispatchPassVersion, Enabled: true},
			{Name: PassSemanticPreDispatch, Version: SemanticPreDispatchPassVersion, Enabled: false},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildSourceBoundPlan(verified, capabilityPlan, PlannerConfig{Passes: test.passes, PreissueContext: legalityContext()})
			if !errors.Is(err, ErrInvalidPlannerConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSourceBindingResolverMatchesOnlyExactProgrammaticOccurrence(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, true)
	planned, err := BuildSourceBoundPlan(verified, capabilityPlan, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}
	request := capability.SourceBindingRequest{
		CallID: "parent:program:1", ParentCallID: "parent", Programmatic: true,
		Capability: site.Capability, OperationIndex: 0, Arguments: json.RawMessage(`{"key":"profile"}`),
	}
	bound, ok := resolver.ResolveSource(request)
	if !ok || bound.OccurrenceID != site.ID || bound.SourceSHA256 == "" || bound.StartLine != site.Span.StartLine || bound.EndColumn != site.Span.EndColumn || bound.DynamicOccurrence != 1 {
		t.Fatalf("binding=%+v ok=%v", bound, ok)
	}
	for _, candidate := range []capability.SourceBindingRequest{
		func() capability.SourceBindingRequest { value := request; value.Programmatic = false; return value }(),
		func() capability.SourceBindingRequest {
			value := request
			value.Capability = "other.read"
			return value
		}(),
		func() capability.SourceBindingRequest {
			value := request
			value.Arguments = json.RawMessage(`{"key":"other"}`)
			return value
		}(),
	} {
		if got, ok := resolver.ResolveSource(candidate); ok {
			t.Fatalf("mismatched request resolved: request=%+v binding=%+v", candidate, got)
		}
	}
}

func TestSourceBindingResolverRejectsAmbiguousStaticOccurrences(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, true)
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := site
	duplicate.ID = legalityDigest("duplicate-site")
	analysis.CallSites = append(analysis.CallSites, duplicate)
	sort.Slice(analysis.CallSites, func(i, j int) bool { return analysis.CallSites[i].ID < analysis.CallSites[j].ID })
	analysis.CandidateRegions[0].CapabilityOccurrences = append(analysis.CandidateRegions[0].CapabilityOccurrences, duplicate.ID)
	sort.Strings(analysis.CandidateRegions[0].CapabilityOccurrences)
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	planned, err := BuildSourceBoundPlan(VerifiedAnalysis{analysisJSON: encoded}, capabilityPlan, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}
	if binding, ok := resolver.ResolveSource(capability.SourceBindingRequest{
		CallID: "parent:program:1", ParentCallID: "parent", Programmatic: true,
		Capability: site.Capability, Arguments: json.RawMessage(`{"key":"profile"}`),
	}); ok {
		t.Fatalf("ambiguous occurrence resolved as %+v", binding)
	}
}

func TestSourceBoundProjectionIsDefensive(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, capabilityPlan, true)
	planned, err := BuildSourceBoundPlan(verified, capabilityPlan, PlannerConfig{
		Passes:          []PassConfig{{Name: PassSemanticPreDispatch, Version: SemanticPreDispatchPassVersion, Enabled: true}},
		PreissueContext: legalityContext(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := planned.Projection()
	projection.Documents[0].Language = "forged"
	projection.Occurrences[0].Capability = "forged"
	projection.Decisions[0].RejectionReasons = append(projection.Decisions[0].RejectionReasons, RejectCallSiteMissing)

	fresh := planned.Projection()
	if fresh.Documents[0].Language != "python" || fresh.Occurrences[0].Capability != site.Capability || len(fresh.Decisions[0].RejectionReasons) != 0 {
		t.Fatalf("internal projection mutated: %+v", fresh)
	}
}
