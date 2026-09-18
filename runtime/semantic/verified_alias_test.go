package semantic

import (
	"errors"
	"testing"
)

func TestVerifiedHandlesDetachNestedMutableState(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verified, _ := legalityVerifiedAnalysis(t, capabilityPlan, true)
	base, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}

	first, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	first.CallSites[0].CanonicalArguments[0] = '['
	first.CandidateRegions[0].CapabilityOccurrences[0] = "forged"
	first.CandidateRegions[0].ControlPredecessors = append(first.CandidateRegions[0].ControlPredecessors, "forged")
	second, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	if second.CallSites[0].CanonicalArguments[0] == '[' ||
		second.CandidateRegions[0].CapabilityOccurrences[0] == "forged" ||
		len(second.CandidateRegions[0].ControlPredecessors) != 0 {
		t.Fatalf("analysis aliases mutable state: %+v", second)
	}

	plan, _, err := BuildWholeRunPlan(base, WholeRunConfig{
		Dependencies:    []Dependency{{Kind: DependencyCanonicalInputs, IdentitySHA256: legalityDigest("inputs")}},
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindVerifiedWholeRunPlan(verified, plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Regions[0].Dependencies[0].IdentitySHA256 = legalityDigest("forged")

	boundAnalysis, boundPlan, _, err := bound.Bound()
	if err != nil {
		t.Fatal(err)
	}
	boundAnalysis.CallSites[0].CanonicalArguments[0] = '['
	boundPlan.Analysis.CandidateRegions[0].CapabilityOccurrences[0] = "forged"
	boundPlan.Regions[0].Dependencies[0].IdentitySHA256 = legalityDigest("forged")

	freshAnalysis, freshPlan, _, err := bound.Bound()
	if err != nil {
		t.Fatal(err)
	}
	if freshAnalysis.CallSites[0].CanonicalArguments[0] == '[' ||
		freshPlan.Analysis.CandidateRegions[0].CapabilityOccurrences[0] == "forged" ||
		freshPlan.Regions[0].Dependencies[0].IdentitySHA256 == legalityDigest("forged") {
		t.Fatalf("bound values alias mutable state: analysis=%+v plan=%+v", freshAnalysis, freshPlan)
	}
}

func TestVerifiedHandlesRejectZeroValuesAndMismatchedPlans(t *testing.T) {
	if _, err := (VerifiedAnalysis{}).Analysis(); !errors.Is(err, ErrUnverifiedAnalysis) {
		t.Fatalf("zero analysis error=%v", err)
	}
	if _, _, _, err := (VerifiedWholeRunPlan{}).Bound(); !errors.Is(err, ErrUnverifiedAnalysis) {
		t.Fatalf("zero bound error=%v", err)
	}

	capabilityPlan := legalityTestPlan(t, true)
	verified, _ := legalityVerifiedAnalysis(t, capabilityPlan, true)
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.SourceSHA256 = legalityDigest("other-source")
	mismatched, _, err := BuildWholeRunPlan(analysis, WholeRunConfig{
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindVerifiedWholeRunPlan(verified, mismatched); !errors.Is(err, ErrUnverifiedAnalysis) {
		t.Fatalf("mismatched plan error=%v", err)
	}
}
