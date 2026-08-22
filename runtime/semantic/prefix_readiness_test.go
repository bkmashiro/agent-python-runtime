package semantic

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestConservativePrefixReadinessFilterSelectsOnlyCandidateTransitions(t *testing.T) {
	filter, err := NewConservativePrefixReadinessFilter(legalityTestPlan(t, true))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		index  uint32
		source string
		want   bool
	}{
		{1, "base = inputs['n'] + 1\n", false},
		{2, "base = inputs['n'] + 1\nvalue = sources.read('weather')\n", true},
		{3, "base = inputs['n'] + 1\nvalue = sources.read('weather')\nresult = value\n", false},
	}
	for _, test := range tests {
		if got := filter.ShouldAnalyzePrefix(test.index, test.source); got != test.want {
			t.Fatalf("prefix %d got=%t want=%t", test.index, got, test.want)
		}
	}
}

func TestConservativePrefixReadinessFilterHandlesIncompleteAndOpaqueCalls(t *testing.T) {
	filter, err := NewConservativePrefixReadinessFilter(legalityTestPlan(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if filter.ShouldAnalyzePrefix(1, "value = sources.") {
		t.Fatal("incomplete projection was analyzed before call readiness")
	}
	if !filter.ShouldAnalyzePrefix(2, "value = sources.read('weather')\n") {
		t.Fatal("completed projection was not analyzed")
	}

	filter, _ = NewConservativePrefixReadinessFilter(legalityTestPlan(t, true))
	definition := "def fetch():\n    return sources.read('weather')\n"
	if !filter.ShouldAnalyzePrefix(1, definition) {
		t.Fatal("opaque definition containing a projected call was not analyzed")
	}
	if !filter.ShouldAnalyzePrefix(2, definition+"value = fetch()\n") {
		t.Fatal("opaque wrapper call transition was skipped")
	}
	if filter.ShouldAnalyzePrefix(3, definition+"value = fetch()\nresult = value\n") {
		t.Fatal("ordinary suffix after opaque call was analyzed")
	}
}

func TestConservativePrefixReadinessFilterAnalyzesBindingRiskAndFailsOpenToExactAnalysis(t *testing.T) {
	filter, err := NewConservativePrefixReadinessFilter(legalityTestPlan(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !filter.ShouldAnalyzePrefix(1, "sources = wrapper\n") {
		t.Fatal("projection binding change was skipped")
	}
	if !filter.ShouldAnalyzePrefix(2, "sources = wrapper\nvalue = sources.read('weather')\n") {
		t.Fatal("call after projection rebinding was skipped")
	}
	if !filter.ShouldAnalyzePrefix(1, "value = sources.read('weather')\n") {
		t.Fatal("non-monotonic source replacement did not fail open to exact analysis")
	}
}

func TestConservativePrefixReadinessFilterSkipsPureLocalPrefixes(t *testing.T) {
	filter, err := NewConservativePrefixReadinessFilter(legalityTestPlan(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if filter.ShouldAnalyzePrefix(1, "value = inputs['n'] + 1\n") ||
		filter.ShouldAnalyzePrefix(2, "value = inputs['n'] + 1\nresult = value * 2\n") {
		t.Fatal("pure-local prefixes requested exact target-Guest analysis")
	}
}

func TestGenerateVerifiedSourceSkipsPureLocalPrefixesButVerifiesFinalSource(t *testing.T) {
	plan := legalityTestPlan(t, true)
	filter, err := NewConservativePrefixReadinessFilter(plan)
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := NewPreDispatchBudget(1)
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, &queuedLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	if err != nil {
		t.Fatal(err)
	}
	chunks := make(chan string, 2)
	chunks <- "value = inputs['n'] + 1\n"
	chunks <- "result = value * 2\n"
	close(chunks)
	verified, _ := legalityVerifiedAnalysis(t, plan, true)
	var analyses atomic.Uint32
	var skipped atomic.Uint32
	generated, err := GenerateVerifiedSourceWithPreDispatch(context.Background(), VerifiedSourceGenerationConfig{
		Plan: plan, Admission: admission, SourceChunks: chunks,
		Bindings:            Bindings{ArtifactSHA256: legalityDigest("artifact"), ExecutionProfileSHA256: legalityDigest("profile"), ImportClosureSHA256: legalityDigest("imports"), CapabilityPlanSHA256: plan.Identity()},
		ShouldAnalyzePrefix: filter.ShouldAnalyzePrefix,
		Analyze: func(_ context.Context, source string, _ Bindings, _ *capability.Plan) (VerifiedAnalysis, error) {
			analyses.Add(1)
			return rebindVerifiedSource(verified, source)
		},
		Observe: func(event VerifiedSourceGenerationEvent) {
			if event.Phase == "prefix_skipped" {
				skipped.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if analyses.Load() != 1 || skipped.Load() != 2 || generated.Source() != "value = inputs['n'] + 1\nresult = value * 2\n" {
		t.Fatalf("analyses=%d skipped=%d source=%q", analyses.Load(), skipped.Load(), generated.Source())
	}
}

func TestSkippedSuffixDoesNotInvalidateDelayedExactPrefixAnalysis(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, _ := legalityVerifiedAnalysis(t, plan, true)
	filter, err := NewConservativePrefixReadinessFilter(plan)
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := NewPreDispatchBudget(1)
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, &queuedLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	if err != nil {
		t.Fatal(err)
	}
	chunks := make(chan string, 2)
	chunks <- "result = sources.read(\"profile\")\n"
	chunks <- "tail = inputs['tail']\n"
	close(chunks)
	analysisStarted := make(chan struct{})
	releaseAnalysis := make(chan struct{})
	skipped := make(chan struct{})
	result := make(chan error, 1)
	var analyses atomic.Uint32
	go func() {
		_, generationErr := GenerateVerifiedSourceWithPreDispatch(context.Background(), VerifiedSourceGenerationConfig{
			Plan: plan, Admission: admission, SourceChunks: chunks,
			Bindings:            Bindings{ArtifactSHA256: legalityDigest("artifact"), ExecutionProfileSHA256: legalityDigest("profile"), ImportClosureSHA256: legalityDigest("imports"), CapabilityPlanSHA256: plan.Identity()},
			ShouldAnalyzePrefix: filter.ShouldAnalyzePrefix,
			Analyze: func(_ context.Context, source string, _ Bindings, _ *capability.Plan) (VerifiedAnalysis, error) {
				if analyses.Add(1) == 1 {
					close(analysisStarted)
					<-releaseAnalysis
				}
				return rebindVerifiedSource(verified, source)
			},
			Observe: func(event VerifiedSourceGenerationEvent) {
				if event.Phase == "prefix_skipped" {
					close(skipped)
				}
			},
		})
		result <- generationErr
	}()
	<-analysisStarted
	<-skipped
	close(releaseAnalysis)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if snapshot := admission.Snapshot(); snapshot.PrefixCount != 1 || snapshot.SkippedPrefixCount != 1 || !snapshot.Complete || analyses.Load() != 2 {
		t.Fatalf("snapshot=%+v analyses=%d", snapshot, analyses.Load())
	}
}

func rebindVerifiedSource(verified VerifiedAnalysis, source string) (VerifiedAnalysis, error) {
	analysis, err := verified.Analysis()
	if err != nil {
		return VerifiedAnalysis{}, err
	}
	analysis.SourceSHA256 = digestText(source)
	_, encoded, err := analysis.Identity()
	if err != nil {
		return VerifiedAnalysis{}, err
	}
	return VerifiedAnalysis{analysisJSON: encoded}, nil
}
