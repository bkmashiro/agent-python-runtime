package labview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/mechanismcampaign"
	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const UnifiedSnapshotSchema = "pysolate.lab-unified-campaign.v1"

const (
	expectedCampaignID     = "day-trip-unified-v2"
	expectedEvidenceSHA256 = "sha256:86ac9b773b930f514e3e18039d1091d96555506a2d2e71bfeec11fb88c018a58"
	expectedSourceCommit   = "9e7535c9ca084dc05b00345af3291f49afb13b21"
	expectedArtifactSHA256 = "sha256:815f529d8c0cae18fac3730e6ffc6fe7e817fc5a01df69d709601b68d090559a"
	expectedFixtureSHA256  = "sha256:07ee8df0c2dac355229b569e1033ac5b4884e6b3fa7109ffd1f9254fa70a0ab2"
)

type UnifiedFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

type UnifiedPhase struct {
	ID       string        `json:"id"`
	Index    uint32        `json:"index"`
	Title    string        `json:"title"`
	Summary  string        `json:"summary"`
	Facts    []UnifiedFact `json:"facts"`
	EventIDs []string      `json:"event_ids"`
}

type UnifiedCandidate struct {
	ID             string          `json:"id"`
	TotalCostGBP   float64         `json:"total_cost_gbp"`
	Disposition    string          `json:"disposition"`
	PhysicalIssues uint32          `json:"physical_issues"`
	LogicalClaims  uint32          `json:"logical_claims"`
	SourceSHA256   string          `json:"source_sha256"`
	COWSelected    bool            `json:"cow_selected"`
	ModelSource    string          `json:"model_source"`
	ExecutedSource string          `json:"executed_source"`
	GuestResponse  json.RawMessage `json:"guest_response"`
}

type UnifiedMatchedControl struct {
	PairCount         uint32 `json:"pair_count"`
	BaselineMedianNS  uint64 `json:"baseline_median_ns"`
	OptimizedMedianNS uint64 `json:"optimized_median_ns"`
	MedianSavingsNS   int64  `json:"median_savings_ns"`
	EquivalentResults bool   `json:"equivalent_results"`
}

type UnifiedProvenance struct {
	EvidenceSHA256 string `json:"evidence_sha256"`
	SourceCommit   string `json:"source_commit"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	FixtureSHA256  string `json:"fixture_sha256"`
	Platform       string `json:"platform"`
}

type UnifiedSnapshot struct {
	SchemaVersion     string                    `json:"schema_version"`
	Identity          string                    `json:"identity"`
	Title             string                    `json:"title"`
	Summary           string                    `json:"summary"`
	Selected          string                    `json:"selected"`
	FinalTotalGBP     float64                   `json:"final_total_gbp"`
	MainGuestResponse json.RawMessage           `json:"main_guest_response"`
	Candidates        []UnifiedCandidate        `json:"candidates"`
	Phases            []UnifiedPhase            `json:"phases"`
	Events            []mechanismcampaign.Event `json:"events"`
	MatchedControl    UnifiedMatchedControl     `json:"matched_control"`
	Provenance        UnifiedProvenance         `json:"provenance"`
}

func BuildUnifiedSnapshot(raw []byte) (UnifiedSnapshot, error) {
	if workflowbench.ValidateUniqueJSONKeys(raw) != nil {
		return UnifiedSnapshot{}, errors.New("unified campaign evidence contains duplicate JSON keys")
	}
	var evidence mechanismcampaign.Evidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&evidence) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return UnifiedSnapshot{}, errors.New("invalid unified campaign evidence JSON")
	}
	if err := evidence.Validate(); err != nil {
		return UnifiedSnapshot{}, fmt.Errorf("invalid unified campaign evidence: %w", err)
	}
	if evidence.CampaignID != expectedCampaignID || snapshotSHA(raw) != expectedEvidenceSHA256 ||
		evidence.SourceCommit != expectedSourceCommit || evidence.ArtifactSHA256 != expectedArtifactSHA256 ||
		evidence.FixtureSHA256 != expectedFixtureSHA256 || evidence.Platform != "linux/amd64" {
		return UnifiedSnapshot{}, errors.New("unified campaign evidence is not the pinned release input")
	}
	candidateIDs := []string{"brighton", "oxford"}
	candidates := make([]UnifiedCandidate, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		candidate := evidence.FullRun.Candidates[id]
		disposition := "discarded"
		if id == evidence.FullRun.SelectedCandidate {
			disposition = "selected"
		}
		candidates = append(candidates, UnifiedCandidate{
			ID: id, TotalCostGBP: candidate.TotalCostGBP, Disposition: disposition,
			PhysicalIssues: candidate.PhysicalIssues, LogicalClaims: candidate.LogicalClaims,
			SourceSHA256: candidate.SourceSHA256, COWSelected: candidate.COWSelected, ModelSource: candidate.ModelSource, ExecutedSource: candidate.ExecutedSource, GuestResponse: candidate.GuestResponse,
		})
	}
	events := append([]mechanismcampaign.Event(nil), evidence.FullRun.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	full := evidence.FullRun
	phases := []UnifiedPhase{
		{ID: "source-predispatch", Index: 1, Title: "Generate and pre-dispatch", Summary: "Closed, straight-line read statements are analyzed from visible source prefixes. Six Host requests start before source feed completion.", EventIDs: phaseEventIDs(events, "source-predispatch"), Facts: []UnifiedFact{
			{Label: "qualified reads", Value: "6", Note: "weather, rail, attractions × 2 candidates"},
			{Label: "source schedule", Value: fmt.Sprintf("%d ms + %d ms tail", evidence.Schedule.StatementStepMS, evidence.Schedule.FinalizationDelayMS), Note: "deterministic and preregistered"},
		}},
		{ID: "fresh-execution", Index: 2, Title: "Seal, then execute fresh", Summary: "Each complete source is sealed before a fresh isolated Guest starts from line one and claims exact staged observations.", EventIDs: phaseEventIDs(events, "fresh-execution"), Facts: []UnifiedFact{
			{Label: "physical requests", Value: "6", Note: "one per qualified call"},
			{Label: "logical claims", Value: "6", Note: "no duplicate physical call"},
		}},
		{ID: "sharing-retention", Index: 3, Title: "Share origin, retain result", Summary: "Brighton and Oxford share one in-flight origin computation; Main later consumes the retained whole-run result.", EventIDs: phaseEventIDs(events, "sharing-retention"), Facts: []UnifiedFact{
			{Label: "logical calls", Value: fmt.Sprintf("%d", full.OriginLogicalCalls), Note: "two candidates plus Main"},
			{Label: "physical computes", Value: fmt.Sprintf("%d", full.OriginPhysicalComputes), Note: "single-flight owner"},
		}},
		{ID: "branch-resume", Index: 4, Title: "Select, seal, and resume", Summary: "Oxford is selected from observed Guest outputs. Its non-empty workspace is sealed, exported, imported, bound, and read by a fresh Main Guest.", EventIDs: phaseEventIDs(events, "branch-resume"), Facts: []UnifiedFact{
			{Label: "selected", Value: "Oxford · £78.00", Note: "Brighton £118.40 discarded"},
			{Label: "workspace", Value: shortDigest(full.ImportedWorkspaceSHA256), Note: "export/import identity preserved"},
		}},
		{ID: "memory-io", Index: 5, Title: "Private memory and cold I/O", Summary: "Linux candidate Guests use private COW memory. Host campaign counters report one successful page-out; a typed resume event closes the fresh Main continuation.", EventIDs: phaseEventIDs(events, "memory-io"), Facts: []UnifiedFact{
			{Label: "COW", Value: "2 / 2 selected", Note: "Brighton and Oxford candidates"},
			{Label: "cold I/O counters", Value: fmt.Sprintf("%d wait · %d page-out · %d resume", full.ColdWaits, full.PageOutSucceeded, full.ColdResumes), Note: fmt.Sprintf("Host counter; %d bytes advised", full.ColdAdvisedBytes)},
		}},
		{ID: "fail-closed", Index: 6, Title: "Reject mismatched reuse", Summary: "Source and canonical-argument mutations are rejected before an observation can cross the Host boundary.", EventIDs: phaseEventIDs(events, "fail-closed"), Facts: []UnifiedFact{
			{Label: "source mismatch", Value: passLabel(full.SourceMismatchRejected), Note: "fresh Guest not started"},
			{Label: "argument mismatch", Value: passLabel(full.ArgumentMismatchRejected), Note: "staged observation not reused"},
		}},
	}
	snapshot := UnifiedSnapshot{
		SchemaVersion: UnifiedSnapshotSchema,
		Title:         "One London day-trip campaign",
		Summary:       "A single typed evidence chain combines source-prefix pre-dispatch, fresh Guest execution, exact claims, candidate fanout, selection, workspace resume, COW, cold I/O, sharing, retention, and fail-closed controls.",
		Selected:      full.SelectedCandidate, FinalTotalGBP: full.MainTotalGBP, MainGuestResponse: full.MainGuestResponse,
		Candidates: candidates, Phases: phases, Events: events,
		MatchedControl: UnifiedMatchedControl{
			PairCount:         uint32(len(evidence.MatchedControl.Pairs)),
			BaselineMedianNS:  evidence.MatchedControl.BaselineMedianNS,
			OptimizedMedianNS: evidence.MatchedControl.OptimizedMedianNS,
			MedianSavingsNS:   evidence.MatchedControl.SavingsNS,
			EquivalentResults: true,
		},
		Provenance: UnifiedProvenance{
			EvidenceSHA256: snapshotSHA(raw), SourceCommit: evidence.SourceCommit,
			ArtifactSHA256: evidence.ArtifactSHA256, FixtureSHA256: evidence.FixtureSHA256,
			Platform: evidence.Platform,
		},
	}
	identity, err := unifiedIdentity(snapshot)
	if err != nil {
		return UnifiedSnapshot{}, err
	}
	snapshot.Identity = identity
	return snapshot, nil
}

func EncodeUnifiedSnapshot(snapshot UnifiedSnapshot) ([]byte, error) {
	if snapshot.SchemaVersion != UnifiedSnapshotSchema || snapshot.Identity == "" {
		return nil, errors.New("invalid unified Lab snapshot")
	}
	return json.MarshalIndent(snapshot, "", "  ")
}

func unifiedIdentity(snapshot UnifiedSnapshot) (string, error) {
	snapshot.Identity = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return snapshotSHA(encoded), nil
}

func snapshotSHA(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func shortDigest(value string) string {
	if len(value) <= 19 {
		return value
	}
	return value[:19] + "…"
}

func passLabel(value bool) string {
	if value {
		return "rejected"
	}
	return "not proven"
}

func phaseEventIDs(events []mechanismcampaign.Event, phase string) []string {
	candidateLogical := func(event mechanismcampaign.Event) bool {
		return strings.HasPrefix(event.LogicalID, "brighton-") || strings.HasPrefix(event.LogicalID, "oxford-")
	}
	selectors := map[string]func(mechanismcampaign.Event) bool{
		"source-predispatch": func(event mechanismcampaign.Event) bool {
			return (event.ActorID == "brighton" || event.ActorID == "oxford") && strings.HasPrefix(event.Type, "source.") && event.Type != "source.sealed" ||
				candidateLogical(event) && (strings.HasPrefix(event.Type, "semantic.") && event.Type != "semantic.claim" || strings.HasPrefix(event.Type, "request."))
		},
		"fresh-execution": func(event mechanismcampaign.Event) bool {
			return event.Type == "source.sealed" || event.Type == "semantic.claim" ||
				strings.HasPrefix(event.Type, "guest.") && (event.ActorID == "brighton" || event.ActorID == "oxford")
		},
		"sharing-retention": func(event mechanismcampaign.Event) bool { return strings.HasPrefix(event.Type, "function.") },
		"branch-resume": func(event mechanismcampaign.Event) bool {
			return strings.HasPrefix(event.Type, "branch.") || strings.HasPrefix(event.Type, "capsule.") ||
				event.ActorID == "main" && strings.HasPrefix(event.Type, "guest.")
		},
		"memory-io": func(event mechanismcampaign.Event) bool {
			return strings.HasPrefix(event.Type, "cow.") || strings.HasPrefix(event.Type, "cold_io.")
		},
		"fail-closed": func(event mechanismcampaign.Event) bool { return strings.HasPrefix(event.Type, "control.") },
	}
	selectEvent, ok := selectors[phase]
	if !ok {
		return nil
	}
	ids := make([]string, 0)
	for _, event := range events {
		if selectEvent(event) {
			ids = append(ids, event.ID)
		}
	}
	return ids
}
