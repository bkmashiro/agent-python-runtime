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
	if report.Matched != report.Cases {
		return report, ErrDifferentialOracle
	}
	return report, nil
}

func EncodeDifferentialReport(report DifferentialReport) ([]byte, error) {
	if report.SchemaVersion != DifferentialOracleSchemaVersion || report.Cases == 0 || report.Matched != report.Cases ||
		len(report.Results) != int(report.Cases) {
		return nil, ErrDifferentialOracle
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, ErrDifferentialOracle
	}
	return append(encoded, '\n'), nil
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
				Capability: "mail.send", ArgumentsSHA256: differentialDigest("extra-args"), ResourceSHA256: differentialDigest("extra-resource"),
				FreshnessSHA256: differentialDigest("extra-freshness"), AuthoritySHA256: differentialDigest("extra-authority"),
				Status: semantic.EventSucceeded, Predecessors: []string{},
			})
		}},
		{"freshness-mismatch", semantic.DivergenceFreshnessContextMismatch, func(trace *semantic.ObservableTrace) {
			trace.Events[0].FreshnessSHA256 = differentialDigest("different")
		}},
		{"invalid-context", semantic.DivergenceTraceUnclassifiable, func(trace *semantic.ObservableTrace) { trace.FrozenContextSHA256 = "invalid" }},
		{"logical-event-order", semantic.DivergenceRequiredOrderInversion, func(trace *semantic.ObservableTrace) {
			trace.Events[0], trace.Events[1] = trace.Events[1], trace.Events[0]
		}},
		{"missing-logical-effect", semantic.DivergenceMissingEffectEvent, func(trace *semantic.ObservableTrace) { trace.Events = trace.Events[:1] }},
		{"post-effect-replay", semantic.DivergencePostEffectReplay, func(trace *semantic.ObservableTrace) { trace.Terminal.PostEffectReplay = true }},
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
				Surface: semantic.SurfaceSpeculativePhysical, Capability: "sources.read",
				ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
				FreshnessSHA256: differentialDigest("freshness"), AuthoritySHA256: differentialDigest("authority"),
				Status: semantic.EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: false,
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
		Surface: semantic.SurfaceSpeculativePhysical, Capability: "sources.read",
		ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
		FreshnessSHA256: differentialDigest("freshness"), AuthoritySHA256: differentialDigest("authority"),
		Status: semantic.EventOrphaned, Predecessors: []string{}, QualifiedSpeculation: true,
	})
	cases = append(cases, DifferentialCase{ID: "equivalent-qualified-discard", Baseline: cloneDifferentialTrace(baseline), Candidate: candidate, Expected: semantic.DivergenceNone})
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
				Capability: "sources.read", ArgumentsSHA256: differentialDigest("arguments"), ResourceSHA256: differentialDigest("resource"),
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
