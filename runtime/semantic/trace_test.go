package semantic

import "testing"

func TestCompareObservableTracesAcceptsEquivalentLogicalTraceAndQualifiedDiscard(t *testing.T) {
	baseline := observableTraceFixture()
	candidate := observableTraceFixture()
	candidate.Events = append(candidate.Events, TraceEvent{
		ID: legalityDigest("physical-extra"), Kind: EventCapabilityObservation,
		Surface: SurfaceSpeculativePhysical, Capability: "sources.read",
		ArgumentsSHA256: legalityDigest("args"), ResourceSHA256: legalityDigest("resource"),
		FreshnessSHA256: legalityDigest("freshness"), AuthoritySHA256: legalityDigest("authority"),
		Status: EventOrphaned, QualifiedSpeculation: true,
	})
	result := CompareObservableTraces(baseline, candidate)
	if !result.Equivalent || result.Divergence != DivergenceNone {
		t.Fatalf("comparison=%+v", result)
	}
}

func TestCompareObservableTracesClassifiesAdversarialDivergence(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*ObservableTrace)
		want   DivergenceClass
	}{
		"terminal": {func(trace *ObservableTrace) {
			trace.Terminal.Class = TerminalException
			trace.Terminal.ValueSHA256 = ""
			trace.Terminal.ExceptionType = "ValueError"
			trace.Terminal.ExceptionSHA256 = legalityDigest("exception")
		}, DivergenceTerminalClassMismatch},
		"result":         {func(trace *ObservableTrace) { trace.Terminal.ValueSHA256 = legalityDigest("different") }, DivergenceResultMismatch},
		"argument":       {func(trace *ObservableTrace) { trace.Events[0].ArgumentsSHA256 = legalityDigest("different") }, DivergenceEffectArgumentMismatch},
		"missing effect": {func(trace *ObservableTrace) { trace.Events = trace.Events[:1] }, DivergenceMissingEffectEvent},
		"event sequence": {func(trace *ObservableTrace) { trace.Events[0], trace.Events[1] = trace.Events[1], trace.Events[0] }, DivergenceRequiredOrderInversion},
		"order":          {func(trace *ObservableTrace) { trace.Events[1].Predecessors = []string{} }, DivergenceRequiredOrderInversion},
		"workspace":      {func(trace *ObservableTrace) { trace.Workspace.FinalSHA256 = legalityDigest("different") }, DivergenceWorkspaceStateMismatch},
		"freshness":      {func(trace *ObservableTrace) { trace.Events[0].FreshnessSHA256 = legalityDigest("different") }, DivergenceFreshnessContextMismatch},
		"authority":      {func(trace *ObservableTrace) { trace.Events[0].AuthoritySHA256 = legalityDigest("different") }, DivergenceAuthorityBindingMismatch},
		"cancel":         {func(trace *ObservableTrace) { trace.Terminal.Cancellation = "requested" }, DivergenceCancellationBoundaryMismatch},
		"ambiguity":      {func(trace *ObservableTrace) { trace.Terminal.Ambiguity = "reconciliation_required" }, DivergenceTerminalDispositionMismatch},
		"replay":         {func(trace *ObservableTrace) { trace.Terminal.PostEffectReplay = true }, DivergencePostEffectReplay},
		"unqualified physical": {func(trace *ObservableTrace) {
			trace.Events = append(trace.Events, TraceEvent{ID: legalityDigest("bad-physical"), Kind: EventCapabilityObservation, Surface: SurfaceSpeculativePhysical, Status: EventReady})
		}, DivergenceTraceUnclassifiable},
	} {
		t.Run(name, func(t *testing.T) {
			baseline := observableTraceFixture()
			candidate := observableTraceFixture()
			test.mutate(&candidate)
			result := CompareObservableTraces(baseline, candidate)
			if result.Equivalent || result.Divergence != test.want {
				t.Fatalf("comparison=%+v want=%s", result, test.want)
			}
		})
	}
}

func TestCompareObservableTracesRejectsExtraLogicalEventAndInvalidContext(t *testing.T) {
	baseline := observableTraceFixture()
	candidate := observableTraceFixture()
	candidate.Events = append(candidate.Events, TraceEvent{
		ID: legalityDigest("extra-logical"), Kind: EventExternalEffectAttempt,
		Surface: SurfaceLogical, Capability: "mail.send",
		ArgumentsSHA256: legalityDigest("extra-args"), ResourceSHA256: legalityDigest("extra-resource"),
		FreshnessSHA256: legalityDigest("extra-freshness"), AuthoritySHA256: legalityDigest("extra-authority"),
		Status: EventSucceeded, Predecessors: []string{},
	})
	if got := CompareObservableTraces(baseline, candidate); got.Divergence != DivergenceExtraEffectEvent {
		t.Fatalf("extra logical=%+v", got)
	}
	candidate = observableTraceFixture()
	candidate.FrozenContextSHA256 = "invalid"
	if got := CompareObservableTraces(baseline, candidate); got.Divergence != DivergenceTraceUnclassifiable {
		t.Fatalf("invalid trace=%+v", got)
	}
}

func observableTraceFixture() ObservableTrace {
	first := legalityDigest("logical-read")
	second := legalityDigest("result-event")
	return ObservableTrace{
		SchemaVersion:       ObservableTraceSchemaVersion,
		FrozenContextSHA256: legalityDigest("context"),
		Workspace:           WorkspaceTrace{StartSHA256: legalityDigest("workspace-start"), FinalSHA256: legalityDigest("workspace-final"), Disposition: "committed"},
		Events: []TraceEvent{
			{
				ID: first, Kind: EventCapabilityObservation, Surface: SurfaceLogical,
				Capability: "sources.read", ArgumentsSHA256: legalityDigest("args"),
				ResourceSHA256: legalityDigest("resource"), ResultSHA256: legalityDigest("observation"),
				FreshnessSHA256: legalityDigest("freshness"), AuthoritySHA256: legalityDigest("authority"),
				Status: EventSucceeded, Predecessors: []string{},
			},
			{
				ID: second, Kind: EventResult, Surface: SurfaceLogical,
				ResultSHA256: legalityDigest("result"), Status: EventSucceeded,
				Predecessors: []string{first},
			},
		},
		Terminal: TerminalTrace{Class: TerminalResult, ValueSHA256: legalityDigest("result"), WorkspaceDisposition: "committed", EffectDisposition: "not_started"},
	}
}
