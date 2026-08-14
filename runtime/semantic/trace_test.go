package semantic

import "testing"

func TestCompareObservableTracesRejectsQualifiedUnclaimedPhysicalRead(t *testing.T) {
	baseline := observableTraceFixture()
	candidate := observableTraceFixture()
	candidate.Terminal.PhysicalStarted = true
	candidate.Events = append(candidate.Events, TraceEvent{
		ID: legalityDigest("physical-extra"), Kind: EventCapabilityObservation,
		Surface: SurfaceSpeculativePhysical, Capability: "sources.read", EffectClass: TraceEffectExternalRead,
		ArgumentsSHA256: legalityDigest("args"), ResourceSHA256: legalityDigest("resource"),
		FreshnessSHA256: legalityDigest("freshness"), AuthoritySHA256: legalityDigest("authority"),
		ClaimIdentitySHA256: legalityDigest("claim-identity"),
		Status:              EventOrphaned, QualifiedSpeculation: true, QualificationSHA256: legalityDigest("claim-identity"),
	})
	result := CompareObservableTraces(baseline, candidate)
	if result.Equivalent || result.Divergence != DivergenceUnclaimedPhysicalWork {
		t.Fatalf("qualified unclaimed physical read accepted: %+v", result)
	}
}

func TestCompareObservableTracesAcceptsExactlyBoundConsumedPhysicalRead(t *testing.T) {
	baseline := observableTraceFixture()
	candidate := observableTraceFixture()
	logical := candidate.Events[0]
	physical := logical
	physical.ID = legalityDigest("physical-consumed")
	physical.Surface = SurfaceSpeculativePhysical
	physical.Status = EventConsumed
	physical.Predecessors = []string{}
	physical.QualifiedSpeculation = true
	physical.QualificationSHA256 = physical.ClaimIdentitySHA256
	physical.ClaimedLogicalID = logical.ID
	candidate.Terminal.PhysicalStarted = true
	candidate.Events = append(candidate.Events, physical)
	if got := CompareObservableTraces(baseline, candidate); !got.Equivalent {
		t.Fatalf("exact consumed physical read rejected: %+v", got)
	}

	mismatched := candidate
	mismatched.Events = append([]TraceEvent(nil), candidate.Events...)
	mismatched.Events[len(mismatched.Events)-1].ArgumentsSHA256 = legalityDigest("wrong-args")
	if got := CompareObservableTraces(baseline, mismatched); got.Divergence != DivergenceTraceUnclassifiable {
		t.Fatalf("mismatched physical claim accepted: %+v", got)
	}
	badQualification := candidate
	badQualification.Events = append([]TraceEvent(nil), candidate.Events...)
	badQualification.Events[len(badQualification.Events)-1].QualificationSHA256 = legalityDigest("wrong-qualification")
	if got := CompareObservableTraces(baseline, badQualification); got.Divergence != DivergenceTraceUnclassifiable {
		t.Fatalf("mismatched qualification accepted: %+v", got)
	}

	duplicate := candidate
	duplicate.Events = append([]TraceEvent{}, candidate.Events...)
	second := physical
	second.ID = legalityDigest("physical-consumed-twice")
	duplicate.Events = append(duplicate.Events, second)
	if got := CompareObservableTraces(baseline, duplicate); got.Divergence != DivergenceTraceUnclassifiable {
		t.Fatalf("duplicate physical claim accepted: %+v", got)
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
		"result":                  {func(trace *ObservableTrace) { trace.Terminal.ValueSHA256 = legalityDigest("different") }, DivergenceResultMismatch},
		"argument":                {func(trace *ObservableTrace) { trace.Events[0].ArgumentsSHA256 = legalityDigest("different") }, DivergenceEffectArgumentMismatch},
		"missing effect":          {func(trace *ObservableTrace) { trace.Events = trace.Events[:1] }, DivergenceMissingEffectEvent},
		"event sequence":          {func(trace *ObservableTrace) { trace.Events[0], trace.Events[1] = trace.Events[1], trace.Events[0] }, DivergenceTraceUnclassifiable},
		"order":                   {func(trace *ObservableTrace) { trace.Events[1].Predecessors = []string{} }, DivergenceRequiredOrderInversion},
		"workspace":               {func(trace *ObservableTrace) { trace.Workspace.FinalSHA256 = legalityDigest("different") }, DivergenceWorkspaceStateMismatch},
		"freshness":               {func(trace *ObservableTrace) { trace.Events[0].FreshnessSHA256 = legalityDigest("different") }, DivergenceFreshnessContextMismatch},
		"claim identity":          {func(trace *ObservableTrace) { trace.Events[0].ClaimIdentitySHA256 = legalityDigest("different") }, DivergenceAuthorityBindingMismatch},
		"authority":               {func(trace *ObservableTrace) { trace.Events[0].AuthoritySHA256 = legalityDigest("different") }, DivergenceAuthorityBindingMismatch},
		"cancel":                  {func(trace *ObservableTrace) { trace.Terminal.Cancellation = "requested" }, DivergenceCancellationBoundaryMismatch},
		"ambiguity":               {func(trace *ObservableTrace) { trace.Terminal.Ambiguity = "reconciliation_required" }, DivergenceTerminalDispositionMismatch},
		"replay":                  {func(trace *ObservableTrace) { trace.Terminal.PostEffectReplay = true }, DivergencePostEffectReplay},
		"logical terminal status": {func(trace *ObservableTrace) { trace.Events[0].Status = EventOrphaned }, DivergenceTraceUnclassifiable},
		"cyclic predecessor": {func(trace *ObservableTrace) {
			trace.Events[0].Predecessors = []string{trace.Events[1].ID}
		}, DivergenceTraceUnclassifiable},
		"workspace unknown effect": {func(trace *ObservableTrace) {
			trace.Events = append(trace.Events, TraceEvent{
				ID: legalityDigest("workspace-read"), Kind: EventWorkspaceRead, Surface: SurfaceLogical,
				EffectClass: TraceEffectUnknown, ResourceSHA256: legalityDigest("workspace-resource"),
				FreshnessSHA256: legalityDigest("workspace-freshness"), AuthoritySHA256: legalityDigest("workspace-authority"),
				Status: EventSucceeded, Predecessors: []string{trace.Events[0].ID},
			})
		}, DivergenceTraceUnclassifiable},
		"physical flag without event": {func(trace *ObservableTrace) { trace.Terminal.PhysicalStarted = true }, DivergenceTraceUnclassifiable},
		"qualified write physical": {func(trace *ObservableTrace) {
			trace.Events = append(trace.Events, TraceEvent{
				ID: legalityDigest("write-physical"), Kind: EventExternalEffectAttempt,
				Surface: SurfaceSpeculativePhysical, Capability: "mail.send", EffectClass: TraceEffectExternalWrite,
				ArgumentsSHA256: legalityDigest("write-args"), ResourceSHA256: legalityDigest("write-resource"),
				FreshnessSHA256: legalityDigest("write-freshness"), AuthoritySHA256: legalityDigest("write-authority"),
				Status: EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: true,
				QualificationSHA256: legalityDigest("write-qualification"),
			})
		}, DivergenceTraceUnclassifiable},
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
		Surface: SurfaceLogical, Capability: "mail.send", EffectClass: TraceEffectExternalWrite,
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
				Capability: "sources.read", EffectClass: TraceEffectExternalRead, ArgumentsSHA256: legalityDigest("args"),
				ResourceSHA256: legalityDigest("resource"), ResultSHA256: legalityDigest("observation"),
				FreshnessSHA256: legalityDigest("freshness"), AuthoritySHA256: legalityDigest("authority"),
				ClaimIdentitySHA256: legalityDigest("claim-identity"),
				Status:              EventSucceeded, Predecessors: []string{},
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
