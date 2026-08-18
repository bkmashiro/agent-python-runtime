package mechanismcampaign

import (
	"encoding/json"
	"errors"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const SchemaVersion = "pysolate.unified-mechanism-campaign.v2"

var ErrInvalidEvidence = errors.New("invalid unified mechanism campaign evidence")

var (
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,95}$`)
)

var mechanismNames = []string{
	"source_prefix", "semantic_pre_dispatch", "child_fanout", "memory_cow",
	"exact_concurrent_sharing", "whole_run_retention", "cold_io_continuation",
	"serializable_resume", "fail_closed_controls",
}

var privateMarkers = []string{
	"/users/", "/home/", `\\users\\`, ".hermes", "file://", "private://",
	"bearer ", "api_key", "apikey", "password", "secret", "provider_request", "provider_response",
}

var eventTypes = map[string]bool{
	"source.generation.start": true, "source.statement.complete": true, "source.feed.complete": true, "source.sealed": true,
	"semantic.qualified": true, "semantic.issue": true, "semantic.claim": true,
	"request.start": true, "request.finish": true,
	"guest.start": true, "guest.end": true, "guest.complete": true,
	"function.logical": true, "function.leader": true, "function.waiter": true,
	"function.retained": true, "function.physical.start": true, "function.physical.end": true,
	"branch.discard": true, "branch.seal": true, "cow.selected": true,
	"capsule.export": true, "capsule.import": true, "capsule.bind": true,
	"cold_io.resume": true, "control.argument_mismatch": true, "control.source_mismatch": true,
}

type Event struct {
	Sequence       uint64 `json:"sequence"`
	ID             string `json:"id"`
	AtNS           int64  `json:"at_ns"`
	Type           string `json:"type"`
	ActorID        string `json:"actor_id"`
	LogicalID      string `json:"logical_id,omitempty"`
	PhysicalID     string `json:"physical_id,omitempty"`
	IdentitySHA256 string `json:"identity_sha256,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
}

type Mechanism struct {
	Name        string   `json:"name"`
	Disposition string   `json:"disposition"`
	EventIDs    []string `json:"event_ids"`
}

type GenerationSchedule struct {
	StatementStepMS     int64 `json:"statement_step_ms"`
	FinalizationDelayMS int64 `json:"finalization_delay_ms"`
}

type CandidateEvidence struct {
	TotalCostGBP   float64 `json:"total_cost_gbp"`
	SourceSHA256   string  `json:"source_sha256"`
	PhysicalIssues uint32  `json:"physical_issues"`
	LogicalClaims  uint32  `json:"logical_claims"`
	SourceSealed   bool    `json:"source_sealed"`
	COWSelected    bool    `json:"cow_selected"`
}

type FullRunEvidence struct {
	DurationNS               uint64                       `json:"duration_ns"`
	OriginLogicalCalls       int                          `json:"origin_logical_calls"`
	OriginPhysicalComputes   int32                        `json:"origin_physical_computes"`
	OriginRetained           bool                         `json:"origin_retained"`
	Candidates               map[string]CandidateEvidence `json:"candidates"`
	CandidateResultSHA256    string                       `json:"candidate_result_sha256"`
	SelectedCandidate        string                       `json:"selected_candidate"`
	SelectedRootSHA256       string                       `json:"selected_root_sha256"`
	SelectedTreeSHA256       string                       `json:"selected_tree_sha256"`
	ImportedWorkspaceSHA256  string                       `json:"imported_workspace_sha256"`
	BoundRootSHA256          string                       `json:"bound_root_sha256"`
	MainSelected             string                       `json:"main_selected"`
	MainTotalGBP             float64                      `json:"main_total_gbp"`
	ColdWaits                uint64                       `json:"cold_waits"`
	ColdSucceeded            uint64                       `json:"cold_succeeded"`
	PageOutSucceeded         uint64                       `json:"page_out_succeeded"`
	ColdResumes              uint64                       `json:"cold_resumes"`
	ColdAdvisedBytes         uint64                       `json:"cold_advised_bytes"`
	ArgumentMismatchRejected bool                         `json:"argument_mismatch_rejected"`
	SourceMismatchRejected   bool                         `json:"source_mismatch_rejected"`
	Events                   []Event                      `json:"events"`
}

type Evidence struct {
	SchemaVersion  string               `json:"schema_version"`
	CampaignID     string               `json:"campaign_id"`
	SourceCommit   string               `json:"source_commit"`
	ArtifactSHA256 string               `json:"artifact_sha256"`
	FixtureSHA256  string               `json:"fixture_sha256"`
	Platform       string               `json:"platform"`
	Schedule       GenerationSchedule   `json:"generation_schedule"`
	FullRun        FullRunEvidence      `json:"full_run"`
	MatchedControl MatchedControlResult `json:"matched_control"`
	Mechanisms     []Mechanism          `json:"mechanisms"`
}

func ProjectEvidence(campaign CampaignResult, matched MatchedControlResult, campaignID, sourceCommit, artifactSHA256, fixtureSHA256 string, schedule GenerationSchedule) (Evidence, error) {
	var mainEnvelope struct {
		Result struct {
			Selected string  `json:"selected"`
			Total    float64 `json:"total_gbp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(campaign.Resume.Response, &mainEnvelope); err != nil {
		return Evidence{}, err
	}
	candidates := make(map[string]CandidateEvidence, 2)
	for _, id := range []string{"brighton", "oxford"} {
		candidate := campaign.Candidates.Candidates[id]
		candidates[id] = CandidateEvidence{
			TotalCostGBP: candidate.TotalCostGBP, SourceSHA256: candidate.SourceSHA256,
			PhysicalIssues: candidate.ControllerSnapshot.PhysicalIssues, LogicalClaims: candidate.ControllerSnapshot.LogicalClaims,
			SourceSealed: candidate.ControllerSnapshot.SourceSealed, COWSelected: candidate.COWSelected,
		}
	}
	candidateResultSHA256, err := candidateOutputsSHA256(campaign.Candidates.Candidates)
	if err != nil {
		return Evidence{}, err
	}
	full := FullRunEvidence{
		DurationNS: campaign.DurationNS, OriginLogicalCalls: len(campaign.Origin.LogicalDispositions) + 1,
		OriginPhysicalComputes: campaign.Origin.PhysicalComputes, OriginRetained: campaign.Origin.Retained.Disposition == "retained",
		Candidates: candidates, CandidateResultSHA256: candidateResultSHA256,
		SelectedCandidate: "oxford", SelectedRootSHA256: campaign.Candidates.SelectedRoot.IdentitySHA256,
		SelectedTreeSHA256:      campaign.Candidates.SelectedInfo.TreeSHA256,
		ImportedWorkspaceSHA256: campaign.Resume.ImportedInfo.WorkspaceSHA256, BoundRootSHA256: campaign.Resume.BoundRoot.IdentitySHA256,
		MainSelected: mainEnvelope.Result.Selected, MainTotalGBP: mainEnvelope.Result.Total,
		ColdWaits: campaign.Resume.ColdIO.Waits, ColdSucceeded: campaign.Resume.ColdIO.ColdSucceeded,
		PageOutSucceeded: campaign.Resume.ColdIO.PageOutSucceeded, ColdResumes: campaign.Resume.ColdIO.Resumes,
		ColdAdvisedBytes:         campaign.Resume.ColdIO.AdvisedBytes,
		ArgumentMismatchRejected: campaign.Controls.ArgumentMismatchRejected, SourceMismatchRejected: campaign.Controls.SourceMismatchRejected,
		Events: append([]Event(nil), campaign.Events...),
	}
	evidence := Evidence{
		SchemaVersion: SchemaVersion, CampaignID: campaignID, SourceCommit: sourceCommit,
		ArtifactSHA256: artifactSHA256, FixtureSHA256: fixtureSHA256, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Schedule: schedule, FullRun: full, MatchedControl: matched,
	}
	evidence.Mechanisms = projectMechanisms(full.Events)
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != SchemaVersion || !identifierPattern.MatchString(evidence.CampaignID) ||
		!commitPattern.MatchString(evidence.SourceCommit) || !digestPattern.MatchString(evidence.ArtifactSHA256) ||
		!digestPattern.MatchString(evidence.FixtureSHA256) || evidence.Platform == "" || evidence.Schedule.StatementStepMS <= 0 ||
		evidence.Schedule.FinalizationDelayMS <= 0 || evidence.FullRun.DurationNS == 0 || len(evidence.FullRun.Events) == 0 ||
		len(evidence.Mechanisms) != len(mechanismNames) {
		return ErrInvalidEvidence
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return ErrInvalidEvidence
	}
	lowered := strings.ToLower(string(encoded))
	for _, marker := range privateMarkers {
		if strings.Contains(lowered, marker) {
			return ErrInvalidEvidence
		}
	}
	events, err := validateEvents(evidence.FullRun.Events)
	if err != nil || validateMechanisms(evidence.Mechanisms, events) != nil || validateRun(evidence.FullRun, events) != nil ||
		validateMatched(evidence.MatchedControl, evidence.FullRun.CandidateResultSHA256) != nil {
		return ErrInvalidEvidence
	}
	return nil
}

func validateEvents(input []Event) (map[string]Event, error) {
	events := make(map[string]Event, len(input))
	var lastNS int64 = -1
	for index, event := range input {
		if event.Sequence != uint64(index+1) || !identifierPattern.MatchString(event.ID) || event.AtNS < lastNS || !eventTypes[event.Type] ||
			!identifierPattern.MatchString(event.ActorID) || (event.LogicalID != "" && !identifierPattern.MatchString(event.LogicalID)) ||
			(event.PhysicalID != "" && !identifierPattern.MatchString(event.PhysicalID)) ||
			(event.IdentitySHA256 != "" && !digestPattern.MatchString(event.IdentitySHA256)) {
			return nil, ErrInvalidEvidence
		}
		if _, exists := events[event.ID]; exists {
			return nil, ErrInvalidEvidence
		}
		events[event.ID] = event
		lastNS = event.AtNS
	}
	return events, nil
}

func validateMechanisms(mechanisms []Mechanism, events map[string]Event) error {
	for index, mechanism := range mechanisms {
		if mechanism.Name != mechanismNames[index] || mechanism.Disposition != "selected" || len(mechanism.EventIDs) == 0 {
			return ErrInvalidEvidence
		}
		for _, id := range mechanism.EventIDs {
			if _, ok := events[id]; !ok {
				return ErrInvalidEvidence
			}
		}
	}
	return nil
}

func validateRun(run FullRunEvidence, events map[string]Event) error {
	if run.OriginLogicalCalls != 3 || run.OriginPhysicalComputes != 1 || !run.OriginRetained || !digestPattern.MatchString(run.CandidateResultSHA256) || run.SelectedCandidate != "oxford" ||
		run.MainSelected != "oxford" || run.MainTotalGBP != 78 || !digestPattern.MatchString(run.SelectedRootSHA256) ||
		!digestPattern.MatchString(run.SelectedTreeSHA256) || run.SelectedRootSHA256 != run.BoundRootSHA256 || !digestPattern.MatchString(run.ImportedWorkspaceSHA256) ||
		run.ColdWaits != 1 || run.ColdSucceeded != 1 || run.PageOutSucceeded != 1 || run.ColdResumes != 1 || run.ColdAdvisedBytes == 0 ||
		!run.ArgumentMismatchRejected || !run.SourceMismatchRejected {
		return ErrInvalidEvidence
	}
	for _, id := range []string{"brighton", "oxford"} {
		candidate, ok := run.Candidates[id]
		if !ok || !digestPattern.MatchString(candidate.SourceSHA256) || candidate.PhysicalIssues != 3 || candidate.LogicalClaims != 3 ||
			!candidate.SourceSealed || !candidate.COWSelected {
			return ErrInvalidEvidence
		}
	}
	if run.Candidates["brighton"].TotalCostGBP != 118.4 || run.Candidates["oxford"].TotalCostGBP != 78 {
		return ErrInvalidEvidence
	}
	for _, candidateID := range []string{"brighton", "oxford"} {
		feedComplete := firstEvent(events, "source.feed.complete", candidateID)
		sealed := firstEvent(events, "source.sealed", candidateID)
		guest := firstEvent(events, "guest.start", candidateID)
		if feedComplete.ID == "" || sealed.ID == "" || guest.ID == "" || sealed.AtNS < feedComplete.AtNS || guest.AtNS <= sealed.AtNS {
			return ErrInvalidEvidence
		}
		for _, api := range []string{"weather", "rail", "attractions"} {
			start := eventByPhysicalType(events, "request-"+candidateID+"-"+api, "request.start")
			if start.ID == "" || start.AtNS >= feedComplete.AtNS {
				return ErrInvalidEvidence
			}
		}
	}
	leader := firstEvent(events, "function.leader", "brighton")
	if leader.ID == "" {
		leader = firstEvent(events, "function.leader", "oxford")
	}
	waiter := firstEvent(events, "function.waiter", "brighton")
	if waiter.ID == "" {
		waiter = firstEvent(events, "function.waiter", "oxford")
	}
	physical := firstEvent(events, "function.physical.start", "host")
	retained := firstEvent(events, "function.retained", "main")
	var candidateEnd int64 = -1
	for _, event := range events {
		if event.Type == "guest.end" && (event.ActorID == "brighton" || event.ActorID == "oxford") && event.AtNS > candidateEnd {
			candidateEnd = event.AtNS
		}
	}
	if leader.ID == "" || waiter.ID == "" || physical.ID == "" || retained.ID == "" ||
		leader.PhysicalID != waiter.PhysicalID || leader.PhysicalID != physical.PhysicalID || retained.PhysicalID != physical.PhysicalID ||
		candidateEnd < 0 || retained.AtNS <= candidateEnd {
		return ErrInvalidEvidence
	}
	exported := firstEvent(events, "capsule.export", "host")
	imported := firstEvent(events, "capsule.import", "host")
	bound := firstEvent(events, "capsule.bind", "host")
	if exported.ID == "" || imported.ID == "" || bound.ID == "" || exported.IdentitySHA256 != imported.IdentitySHA256 ||
		bound.IdentitySHA256 != run.BoundRootSHA256 || firstEvent(events, "cold_io.resume", "main").ID == "" ||
		firstEvent(events, "control.argument_mismatch", "host").Outcome != "rejected" || firstEvent(events, "control.source_mismatch", "host").Outcome != "rejected" {
		return ErrInvalidEvidence
	}
	return nil
}

func validateMatched(matched MatchedControlResult, expectedResultSHA256 string) error {
	if len(matched.Pairs) != 3 || matched.BaselineMedianNS == 0 || matched.OptimizedMedianNS == 0 || !digestPattern.MatchString(expectedResultSHA256) {
		return ErrInvalidEvidence
	}
	baseline := make([]uint64, 0, len(matched.Pairs))
	optimized := make([]uint64, 0, len(matched.Pairs))
	for index, pair := range matched.Pairs {
		expectedLane := "baseline"
		if index%2 == 1 {
			expectedLane = "optimized"
		}
		if pair.PairIndex != index+1 || pair.FirstLane != expectedLane ||
			pair.BaselineLatencyNS == 0 || pair.OptimizedLatencyNS == 0 || !digestPattern.MatchString(pair.ResultSHA256) {
			return ErrInvalidEvidence
		}
		if pair.ResultSHA256 != expectedResultSHA256 {
			return ErrInvalidEvidence
		}
		baseline = append(baseline, pair.BaselineLatencyNS)
		optimized = append(optimized, pair.OptimizedLatencyNS)
	}
	baselineMedian := medianUint64(baseline)
	optimizedMedian := medianUint64(optimized)
	if matched.BaselineMedianNS != baselineMedian || matched.OptimizedMedianNS != optimizedMedian ||
		matched.SavingsNS != int64(baselineMedian)-int64(optimizedMedian) {
		return ErrInvalidEvidence
	}
	return nil
}

func projectMechanisms(events []Event) []Mechanism {
	selectors := map[string]func(Event) bool{
		"source_prefix": func(event Event) bool {
			return event.Type == "source.statement.complete" || event.Type == "source.feed.complete" || event.Type == "source.sealed"
		},
		"semantic_pre_dispatch": func(event Event) bool {
			return strings.HasPrefix(event.Type, "semantic.") || event.Type == "request.start"
		},
		"child_fanout": func(event Event) bool {
			return (event.ActorID == "brighton" || event.ActorID == "oxford") && strings.HasPrefix(event.Type, "guest.")
		},
		"memory_cow": func(event Event) bool { return event.Type == "cow.selected" },
		"exact_concurrent_sharing": func(event Event) bool {
			return strings.HasPrefix(event.Type, "function.") && event.Type != "function.retained"
		},
		"whole_run_retention":  func(event Event) bool { return event.Type == "function.retained" },
		"cold_io_continuation": func(event Event) bool { return event.Type == "cold_io.resume" },
		"serializable_resume":  func(event Event) bool { return strings.HasPrefix(event.Type, "capsule.") },
		"fail_closed_controls": func(event Event) bool { return strings.HasPrefix(event.Type, "control.") },
	}
	result := make([]Mechanism, 0, len(mechanismNames))
	for _, name := range mechanismNames {
		var ids []string
		for _, event := range events {
			if selectors[name](event) {
				ids = append(ids, event.ID)
			}
		}
		if len(ids) > 24 {
			ids = ids[:24]
		}
		result = append(result, Mechanism{Name: name, Disposition: "selected", EventIDs: ids})
	}
	return result
}

func firstEvent(events map[string]Event, eventType, actor string) Event {
	values := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Type == eventType && event.ActorID == actor {
			values = append(values, event)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].AtNS < values[j].AtNS })
	if len(values) == 0 {
		return Event{}
	}
	return values[0]
}

func eventByPhysicalType(events map[string]Event, physicalID, eventType string) Event {
	for _, event := range events {
		if event.PhysicalID == physicalID && event.Type == eventType {
			return event
		}
	}
	return Event{}
}
