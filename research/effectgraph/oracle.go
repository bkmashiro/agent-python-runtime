package effectgraph

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const DifferentialOracleSchemaVersion = "pysolate.differential-oracle.v0"

var ErrDifferentialOracle = errors.New("effectgraph differential oracle failed")

type DifferentialCase struct {
	ID        string
	Baseline  semantic.ObservableTrace
	Candidate semantic.ObservableTrace
	Expected  semantic.DivergenceClass
}

type DifferentialCaseResult struct {
	ID         string                   `json:"id"`
	Expected   semantic.DivergenceClass `json:"expected"`
	Observed   semantic.DivergenceClass `json:"observed"`
	Equivalent bool                     `json:"equivalent"`
	Matched    bool                     `json:"matched"`
}

type DifferentialReport struct {
	SchemaVersion string                   `json:"schema_version"`
	Cases         uint32                   `json:"cases"`
	Matched       uint32                   `json:"matched"`
	Results       []DifferentialCaseResult `json:"results"`
	seal          [sha256.Size]byte
}

func RunDifferentialOracle(cases []DifferentialCase) (DifferentialReport, error) {
	if len(cases) == 0 || len(cases) > 128 {
		return DifferentialReport{}, ErrDifferentialOracle
	}
	report := DifferentialReport{SchemaVersion: DifferentialOracleSchemaVersion, Cases: uint32(len(cases)), Results: []DifferentialCaseResult{}}
	lastID := ""
	for _, testCase := range cases {
		if !identifierPattern.MatchString(testCase.ID) || testCase.ID <= lastID {
			return DifferentialReport{}, ErrDifferentialOracle
		}
		comparison := semantic.CompareObservableTraces(testCase.Baseline, testCase.Candidate)
		result := DifferentialCaseResult{
			ID: testCase.ID, Expected: testCase.Expected, Observed: comparison.Divergence,
			Equivalent: comparison.Equivalent, Matched: comparison.Divergence == testCase.Expected,
		}
		if testCase.Expected == semantic.DivergenceNone && !comparison.Equivalent {
			result.Matched = false
		}
		if testCase.Expected != semantic.DivergenceNone && comparison.Equivalent {
			result.Matched = false
		}
		if result.Matched {
			report.Matched++
		}
		report.Results = append(report.Results, result)
		lastID = testCase.ID
	}
	if report.Matched != report.Cases || report.validateShape() != nil {
		return report, ErrDifferentialOracle
	}
	report.seal = differentialReportSeal(report)
	return report, nil
}

func EncodeDifferentialReport(report DifferentialReport) ([]byte, error) {
	if report.Validate() != nil {
		return nil, ErrDifferentialOracle
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, ErrDifferentialOracle
	}
	return append(encoded, '\n'), nil
}

func (report DifferentialReport) Validate() error {
	if err := report.validateShape(); err != nil || report.seal != differentialReportSeal(report) {
		return ErrDifferentialOracle
	}
	return nil
}

func differentialReportSeal(report DifferentialReport) [sha256.Size]byte {
	encoded, err := json.Marshal(report)
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(encoded)
}

func (report DifferentialReport) validateShape() error {
	if report.SchemaVersion != DifferentialOracleSchemaVersion || report.Cases == 0 || report.Cases > 128 ||
		report.Matched > report.Cases || len(report.Results) != int(report.Cases) {
		return ErrDifferentialOracle
	}
	matched := uint32(0)
	lastID := ""
	for _, result := range report.Results {
		if !identifierPattern.MatchString(result.ID) || result.ID <= lastID ||
			!validDivergence(result.Expected) || !validDivergence(result.Observed) ||
			result.Equivalent != (result.Observed == semantic.DivergenceNone) ||
			result.Matched != (result.Expected == result.Observed) {
			return ErrDifferentialOracle
		}
		if result.Matched {
			matched++
		}
		lastID = result.ID
	}
	if matched != report.Matched {
		return ErrDifferentialOracle
	}
	return nil
}

func validDivergence(value semantic.DivergenceClass) bool {
	switch value {
	case semantic.DivergenceNone, semantic.DivergenceTerminalClassMismatch,
		semantic.DivergenceResultMismatch, semantic.DivergenceExceptionMismatch,
		semantic.DivergenceMissingEffectEvent, semantic.DivergenceExtraEffectEvent,
		semantic.DivergenceEffectArgumentMismatch, semantic.DivergenceRequiredOrderInversion,
		semantic.DivergenceWorkspaceStateMismatch, semantic.DivergenceFreshnessContextMismatch,
		semantic.DivergenceAuthorityBindingMismatch, semantic.DivergenceCancellationBoundaryMismatch,
		semantic.DivergenceTerminalDispositionMismatch, semantic.DivergencePostEffectReplay,
		semantic.DivergenceTraceUnclassifiable:
		return true
	default:
		return false
	}
}

func DefaultDifferentialCases() []DifferentialCase {
	baseline := differentialTraceFixture()
	type mutation struct {
		id       string
		expected semantic.DivergenceClass
		apply    func(*semantic.ObservableTrace)
	}
	mutations := []mutation{
		{"argument-mismatch", semantic.DivergenceEffectArgumentMismatch, func(trace *semantic.ObservableTrace) {
			trace.Events[0].ArgumentsSHA256 = differentialDigest("different")
		}},
		{"authority-mismatch", semantic.DivergenceAuthorityBindingMismatch, func(trace *semantic.ObservableTrace) {
			trace.Events[0].AuthoritySHA256 = differentialDigest("different")
		}},
		{"cancellation-mismatch", semantic.DivergenceCancellationBoundaryMismatch, func(trace *semantic.ObservableTrace) { trace.Terminal.Cancellation = "cancelled" }},
		{"extra-logical-effect", semantic.DivergenceExtraEffectEvent, func(trace *semantic.ObservableTrace) {
			trace.Events = append(trace.Events, semantic.TraceEvent{
				ID: differentialDigest("extra"), Kind: semantic.EventExternalEffectAttempt, Surface: semantic.SurfaceLogical,
				Capability: "mail.send", EffectClass: semantic.TraceEffectExternalWrite, ArgumentsSHA256: differentialDigest("extra-args"), ResourceSHA256: differentialDigest("extra-resource"),
				FreshnessSHA256: differentialDigest("extra-freshness"), AuthoritySHA256: differentialDigest("extra-authority"),
				Status: semantic.EventSucceeded, Predecessors: []string{},
			})
		}},
		{"freshness-mismatch", semantic.DivergenceFreshnessContextMismatch, func(trace *semantic.ObservableTrace) {
			trace.Events[0].FreshnessSHA256 = differentialDigest("different")
		}},
		{"cyclic-predecessor", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events[0].Predecessors = []string{trace.Events[1].ID}
		}},
		{"invalid-context", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) { trace.FrozenContextSHA256 = "invalid" }},
		{"logical-event-order", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events[0], trace.Events[1] = trace.Events[1], trace.Events[0]
		}},
		{"logical-invalid-status", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events[0].Status = semantic.EventOrphaned
		}},
		{"missing-logical-effect", semantic.DivergenceMissingEffectEvent, func(trace *semantic.ObservableTrace) { trace.Events = trace.Events[:1] }},
		{"physical-claim-argument-mismatch", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			logical := trace.Events[0]
			physical := logical
			physical.ID = differentialDigest("physical-mismatch")
			physical.Surface = semantic.SurfaceSpeculativePhysical
			physical.Status = semantic.EventConsumed
			physical.Predecessors = []string{}
			physical.QualifiedSpeculation = true
			physical.QualificationSHA256 = differentialDigest("qualification")
			physical.ClaimedLogicalID = logical.ID
			physical.ArgumentsSHA256 = differentialDigest("wrong")
			trace.Events = append(trace.Events, physical)
			trace.Terminal.PhysicalStarted = true
		}},
		{"physical-claim-duplicate", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			logical := trace.Events[0]
			for _, suffix := range []string{"one", "two"} {
				physical := logical
				physical.ID = differentialDigest("physical-" + suffix)
				physical.Surface = semantic.SurfaceSpeculativePhysical
				physical.Status = semantic.EventConsumed
				physical.Predecessors = []string{}
				physical.QualifiedSpeculation = true
				physical.QualificationSHA256 = differentialDigest("qualification-" + suffix)
				physical.ClaimedLogicalID = logical.ID
				trace.Events = append(trace.Events, physical)
			}
			trace.Terminal.PhysicalStarted = true
		}},
		{"physical-flag-without-event", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Terminal.PhysicalStarted = true
		}},
		{"post-effect-replay", semantic.DivergencePostEffectReplay, func(trace *semantic.ObservableTrace) { trace.Terminal.PostEffectReplay = true }},
		{"qualified-write-physical", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events = append(trace.Events, semantic.TraceEvent{
				ID: differentialDigest("write-physical"), Kind: semantic.EventExternalEffectAttempt,
				Surface: semantic.SurfaceSpeculativePhysical, Capability: "mail.send", EffectClass: semantic.TraceEffectExternalWrite,
				ArgumentsSHA256: differentialDigest("write-args"), ResourceSHA256: differentialDigest("write-resource"),
				FreshnessSHA256: differentialDigest("write-freshness"), AuthoritySHA256: differentialDigest("write-authority"),
				Status: semantic.EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: true,
				QualificationSHA256: differentialDigest("write-qualification"),
			})
		}},
		{"required-order", semantic.DivergenceRequiredOrderInversion, func(trace *semantic.ObservableTrace) { trace.Events[1].Predecessors = []string{} }},
		{"result-mismatch", semantic.DivergenceResultMismatch, func(trace *semantic.ObservableTrace) { trace.Terminal.ValueSHA256 = differentialDigest("different") }},
		{"terminal-ambiguity", semantic.DivergenceTerminalDispositionMismatch, func(trace *semantic.ObservableTrace) { trace.Terminal.Ambiguity = "ambiguous" }},
		{"terminal-class", semantic.DivergenceTerminalClassMismatch, func(trace *semantic.ObservableTrace) {
			trace.Terminal.Class = semantic.TerminalException
			trace.Terminal.ValueSHA256 = ""
			trace.Terminal.ExceptionType = "ValueError"
			trace.Terminal.ExceptionSHA256 = differentialDigest("exception")
		}},
		{"unqualified-physical", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events = append(trace.Events, semantic.TraceEvent{
				ID: differentialDigest("physical-unqualified"), Kind: semantic.EventCapabilityObservation,
				Surface: semantic.SurfaceSpeculativePhysical, Capability: "sources.read", EffectClass: semantic.TraceEffectExternalRead,
				ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
				FreshnessSHA256: differentialDigest("freshness"), AuthoritySHA256: differentialDigest("authority"),
				Status: semantic.EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: false,
			})
		}},
		{"workspace-unknown-effect", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) {
			trace.Events = append(trace.Events, semantic.TraceEvent{
				ID: differentialDigest("workspace-read"), Kind: semantic.EventWorkspaceRead, Surface: semantic.SurfaceLogical,
				EffectClass: semantic.TraceEffectUnknown, ResourceSHA256: differentialDigest("workspace-resource"),
				FreshnessSHA256: differentialDigest("workspace-freshness"), AuthoritySHA256: differentialDigest("workspace-authority"),
				Status: semantic.EventSucceeded, Predecessors: []string{trace.Events[0].ID},
			})
		}},
		{"workspace-mismatch", semantic.DivergenceWorkspaceStateMismatch, func(trace *semantic.ObservableTrace) { trace.Workspace.FinalSHA256 = differentialDigest("different") }},
	}
	cases := make([]DifferentialCase, 0, len(mutations)+1)
	for _, item := range mutations {
		candidate := cloneDifferentialTrace(baseline)
		item.apply(&candidate)
		cases = append(cases, DifferentialCase{ID: item.id, Baseline: cloneDifferentialTrace(baseline), Candidate: candidate, Expected: item.expected})
	}
	candidate := cloneDifferentialTrace(baseline)
	candidate.Events = append(candidate.Events, semantic.TraceEvent{
		ID: differentialDigest("physical-discard"), Kind: semantic.EventCapabilityObservation,
		Surface: semantic.SurfaceSpeculativePhysical, Capability: "sources.read", EffectClass: semantic.TraceEffectExternalRead,
		ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
		FreshnessSHA256: differentialDigest("freshness"), AuthoritySHA256: differentialDigest("authority"),
		Status: semantic.EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: true,
		QualificationSHA256: differentialDigest("qualification"),
	})
	candidate.Terminal.PhysicalStarted = true
	cases = append(cases, DifferentialCase{ID: "equivalent-qualified-discard", Baseline: cloneDifferentialTrace(baseline), Candidate: candidate, Expected: semantic.DivergenceNone})

	consumed := cloneDifferentialTrace(baseline)
	logical := consumed.Events[0]
	physical := logical
	physical.ID = differentialDigest("physical-consumed")
	physical.Surface = semantic.SurfaceSpeculativePhysical
	physical.Status = semantic.EventConsumed
	physical.Predecessors = []string{}
	physical.QualifiedSpeculation = true
	physical.QualificationSHA256 = differentialDigest("qualification-consumed")
	physical.ClaimedLogicalID = logical.ID
	consumed.Events = append(consumed.Events, physical)
	consumed.Terminal.PhysicalStarted = true
	cases = append(cases, DifferentialCase{ID: "equivalent-consumed-claim", Baseline: cloneDifferentialTrace(baseline), Candidate: consumed, Expected: semantic.DivergenceNone})

	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases
}

func differentialTraceFixture() semantic.ObservableTrace {
	readID := differentialDigest("read")
	return semantic.ObservableTrace{
		SchemaVersion:       semantic.ObservableTraceSchemaVersion,
		FrozenContextSHA256: differentialDigest("context"),
		Events: []semantic.TraceEvent{
			{ID: readID, Kind: semantic.EventCapabilityObservation, Surface: semantic.SurfaceLogical,
				Capability: "sources.read", EffectClass: semantic.TraceEffectExternalRead, ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
				ResultSHA256: differentialDigest("observation"), FreshnessSHA256: differentialDigest("freshness"),
				AuthoritySHA256: differentialDigest("authority"), Status: semantic.EventSucceeded, Predecessors: []string{}},
			{ID: differentialDigest("result-event"), Kind: semantic.EventResult, Surface: semantic.SurfaceLogical,
				ResultSHA256: differentialDigest("result"), Status: semantic.EventSucceeded, Predecessors: []string{readID}},
		},
		Workspace: semantic.WorkspaceTrace{StartSHA256: differentialDigest("workspace-start"), FinalSHA256: differentialDigest("workspace-final"), Disposition: "committed"},
		Terminal: semantic.TerminalTrace{
			Class: semantic.TerminalResult, ValueSHA256: differentialDigest("result"),
			Cancellation: "not_cancelled", Ambiguity: "not_ambiguous", Reconciliation: "not_required",
			WorkspaceDisposition: "committed", EffectDisposition: "completed",
		},
	}
}

func cloneDifferentialTrace(value semantic.ObservableTrace) semantic.ObservableTrace {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var clone semantic.ObservableTrace
	if err := json.Unmarshal(encoded, &clone); err != nil {
		panic(err)
	}
	return clone
}

func differentialDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}
