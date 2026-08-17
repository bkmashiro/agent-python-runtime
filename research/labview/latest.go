package labview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const LatestSnapshotSchema = "pysolate.lab-latest.v1"

var latestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var latestCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type LatestInputs struct {
	SourcePrefixContract []byte
	SourcePrefixEvidence []byte
	SourcePrefixCensus   []byte
	CampaignProjection   []byte
}

type LatestHeadline struct {
	RealGuestDemos   int `json:"real_guest_demos"`
	OptimizationWins int `json:"optimization_wins"`
	SafetyControls   int `json:"safety_controls"`
}

type LatestMetric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Note  string `json:"note"`
	Tone  string `json:"tone"`
}

type LatestSegment struct {
	Label   string `json:"label"`
	StartNS int64  `json:"start_ns"`
	EndNS   int64  `json:"end_ns"`
	Tone    string `json:"tone"`
}

type LatestLane struct {
	Label      string          `json:"label"`
	DurationNS int64           `json:"duration_ns"`
	Segments   []LatestSegment `json:"segments"`
}

type LatestFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type LatestCodeAnnotation struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Tone      string `json:"tone"`
	Label     string `json:"label"`
	Note      string `json:"note"`
}

type LatestDemo struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	Eyebrow       string                 `json:"eyebrow"`
	Status        string                 `json:"status"`
	Summary       string                 `json:"summary"`
	Source        string                 `json:"source"`
	Annotations   []LatestCodeAnnotation `json:"annotations"`
	Metrics       []LatestMetric         `json:"metrics"`
	Lanes         []LatestLane           `json:"lanes"`
	Facts         []LatestFact           `json:"facts"`
	ClaimBoundary string                 `json:"claim_boundary"`
}

type LatestBoundary struct {
	Events               int    `json:"events"`
	UniqueSources        int    `json:"unique_sources"`
	StructurallyEligible int    `json:"structurally_eligible"`
	TimingNotRecorded    int    `json:"timing_not_recorded"`
	PerformanceSupported bool   `json:"performance_supported"`
	Decision             string `json:"decision"`
}

type LatestProvenance struct {
	SourcePrefixEvidenceSHA256 string `json:"source_prefix_evidence_sha256"`
	CensusEvidenceSHA256       string `json:"census_evidence_sha256"`
	CampaignProjectionSHA256   string `json:"campaign_projection_sha256"`
	SourcePrefixArtifactSHA256 string `json:"source_prefix_artifact_sha256"`
	CampaignArtifactSHA256     string `json:"campaign_artifact_sha256"`
	SourcePrefixHarnessCommit  string `json:"source_prefix_harness_commit"`
	CampaignSourceCommit       string `json:"campaign_source_commit"`
}

type LatestSnapshot struct {
	SchemaVersion string           `json:"schema_version"`
	Identity      string           `json:"identity"`
	Headline      LatestHeadline   `json:"headline"`
	Demos         []LatestDemo     `json:"demos"`
	Boundary      LatestBoundary   `json:"boundary"`
	Provenance    LatestProvenance `json:"provenance"`
}

type campaignHost struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	Kernel    string `json:"kernel"`
}

type campaignSource struct {
	ArtifactSHA256       string       `json:"artifact_sha256"`
	ArtifactSourceCommit string       `json:"artifact_source_commit"`
	CampaignSourceCommit string       `json:"campaign_source_commit"`
	ManifestSHA256       string       `json:"manifest_sha256"`
	Host                 campaignHost `json:"host"`
	Repetitions          int          `json:"repetitions"`
}

type campaignProgramProjection struct {
	ID                     string          `json:"id"`
	Family                 string          `json:"family"`
	ReleaseOffsetMS        int64           `json:"release_offset_ms"`
	PlanSHA256             string          `json:"plan_sha256"`
	GrantSetSHA256         string          `json:"grant_set_sha256"`
	PrivacyPartition       string          `json:"privacy_partition"`
	WorkspaceFixtureSHA256 string          `json:"workspace_fixture_sha256"`
	Execution              json.RawMessage `json:"execution"`
	Admission              string          `json:"admission"`
	Sharing                string          `json:"sharing"`
	Disposition            string          `json:"disposition"`
}

type campaignEventProjection struct {
	Sequence            uint64 `json:"sequence"`
	ProgramID           string `json:"program_id"`
	Type                string `json:"type"`
	AtNS                int64  `json:"at_ns"`
	Reason              string `json:"reason,omitempty"`
	PhysicalExecutionID string `json:"physical_execution_id,omitempty"`
}

type campaignProjection struct {
	SchemaVersion     string                      `json:"schema_version"`
	Source            campaignSource              `json:"source"`
	Baseline          json.RawMessage             `json:"baseline"`
	Qualified         json.RawMessage             `json:"qualified"`
	Paired            json.RawMessage             `json:"paired"`
	Runs              json.RawMessage             `json:"runs"`
	Programs          []campaignProgramProjection `json:"programs"`
	WalkthroughEvents []campaignEventProjection   `json:"walkthrough_events"`
	ValidClaim        string                      `json:"valid_claim"`
	InvalidInference  string                      `json:"invalid_inference"`
}

func strictLatestDecode(raw []byte, target any) error {
	if workflowbench.ValidateUniqueJSONKeys(raw) != nil {
		return errors.New("latest Lab input contains duplicate JSON keys")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid latest Lab JSON")
	}
	return nil
}

func latestSHA(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func campaignPhysicalInterval(events []campaignEventProjection, physicalID string) (int64, int64, error) {
	var start, end int64 = -1, -1
	for _, event := range events {
		if event.PhysicalExecutionID != physicalID {
			continue
		}
		if event.Type == "physical.started" {
			start = event.AtNS
		}
		if event.Type == "physical.ended" {
			end = event.AtNS
		}
	}
	if start < 0 || end <= start {
		return 0, 0, errors.New("campaign physical interval is incomplete")
	}
	return start, end, nil
}

func campaignTerminal(events []campaignEventProjection, programID string) (campaignEventProjection, error) {
	for _, event := range events {
		if event.ProgramID == programID && event.Type == "logical.terminal" {
			return event, nil
		}
	}
	return campaignEventProjection{}, errors.New("campaign terminal event is absent")
}

func selectedRow(rows []workflowbench.SourcePrefixRow, treatment workflowbench.SourcePrefixTreatment) (workflowbench.SourcePrefixRow, error) {
	selected := make([]workflowbench.SourcePrefixRow, 0, len(rows))
	for _, row := range rows {
		if row.Treatment == treatment {
			selected = append(selected, row)
		}
	}
	if len(selected) == 0 {
		return workflowbench.SourcePrefixRow{}, errors.New("source-prefix lane is absent")
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].WallNS < selected[j].WallNS })
	return selected[len(selected)/2], nil
}

func sourcePrefixLane(label string, row workflowbench.SourcePrefixRow) LatestLane {
	return LatestLane{Label: label, DurationNS: row.WallNS, Segments: []LatestSegment{
		{Label: "source generation", StartNS: 0, EndNS: row.GenerationCompleteNS, Tone: "generation"},
		{Label: "Host READ", StartNS: row.ToolStartedNS, EndNS: row.ToolEndedNS, Tone: "effect"},
		{Label: "Guest finalize", StartNS: row.ToolEndedNS, EndNS: row.RunEndedNS, Tone: "finalize"},
	}}
}

func campaignLane(label, segmentLabel string, duration, start, end int64, tone string) LatestLane {
	return LatestLane{Label: label, DurationNS: duration, Segments: []LatestSegment{{Label: segmentLabel, StartNS: start, EndNS: end, Tone: tone}}}
}

func BuildLatestSnapshot(inputs LatestInputs) (LatestSnapshot, error) {
	contract, err := workflowbench.DecodeSourcePrefixExperimentContract(inputs.SourcePrefixContract)
	if err != nil {
		return LatestSnapshot{}, err
	}
	evidence, err := workflowbench.DecodeSourcePrefixEvidence(inputs.SourcePrefixEvidence, contract)
	if err != nil {
		return LatestSnapshot{}, err
	}
	census, err := workflowbench.DecodeSourcePrefixCensusEvidence(inputs.SourcePrefixCensus)
	if err != nil {
		return LatestSnapshot{}, err
	}
	var campaign campaignProjection
	if err := strictLatestDecode(inputs.CampaignProjection, &campaign); err != nil || campaign.SchemaVersion != "pysolate.transparent-campaign-public-projection.v1" || len(campaign.Programs) != 20 || len(campaign.WalkthroughEvents) == 0 || !latestDigest.MatchString(campaign.Source.ArtifactSHA256) || !latestCommit.MatchString(campaign.Source.CampaignSourceCommit) {
		return LatestSnapshot{}, errors.New("invalid campaign projection for latest Lab")
	}
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		return LatestSnapshot{}, err
	}
	manifestPrograms := map[string]workflowbench.CampaignProgram{}
	projectedPrograms := map[string]campaignProgramProjection{}
	for _, program := range manifest.Programs {
		manifestPrograms[program.ID] = program
	}
	for _, program := range campaign.Programs {
		projectedPrograms[program.ID] = program
	}
	p05, p06, p07 := manifestPrograms["P05"], manifestPrograms["P06"], manifestPrograms["P07"]
	q05, q06, q07 := projectedPrograms["P05"], projectedPrograms["P06"], projectedPrograms["P07"]
	if p05.Source == "" || p05.Source != p06.Source || p05.Source == p07.Source || q05.Sharing != "exact_shared" || q06.Sharing != "independent" || q07.Sharing != "independent" || q05.Disposition != "complete" || q06.Disposition != "complete" || q07.Disposition != "complete" {
		return LatestSnapshot{}, errors.New("campaign sharing examples do not match canonical sources")
	}
	t05, err := campaignTerminal(campaign.WalkthroughEvents, "P05")
	if err != nil {
		return LatestSnapshot{}, err
	}
	t06, err := campaignTerminal(campaign.WalkthroughEvents, "P06")
	if err != nil || t05.PhysicalExecutionID == "" || t05.PhysicalExecutionID != t06.PhysicalExecutionID {
		return LatestSnapshot{}, errors.New("exact sharing did not converge on one physical execution")
	}
	t07, err := campaignTerminal(campaign.WalkthroughEvents, "P07")
	if err != nil || t07.PhysicalExecutionID == "" || t07.PhysicalExecutionID == t05.PhysicalExecutionID {
		return LatestSnapshot{}, errors.New("source mismatch incorrectly shared physical execution")
	}
	sharedStart, sharedEnd, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t05.PhysicalExecutionID)
	if err != nil {
		return LatestSnapshot{}, err
	}
	fallbackStart, fallbackEnd, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t07.PhysicalExecutionID)
	if err != nil {
		return LatestSnapshot{}, err
	}
	baseline, err := selectedRow(evidence.Rows, workflowbench.SourcePrefixBaseline)
	if err != nil {
		return LatestSnapshot{}, err
	}
	streaming, err := selectedRow(evidence.Rows, workflowbench.SourcePrefixStreaming)
	if err != nil {
		return LatestSnapshot{}, err
	}
	source := ""
	for _, chunk := range contract.Schedule.Chunks {
		source += chunk.Source
	}
	sharedDuration := sharedEnd
	if t05.AtNS > sharedDuration {
		sharedDuration = t05.AtNS
	}
	if t06.AtNS > sharedDuration {
		sharedDuration = t06.AtNS
	}
	fallbackDuration := fallbackEnd
	if t07.AtNS > fallbackDuration {
		fallbackDuration = t07.AtNS
	}
	snapshot := LatestSnapshot{
		SchemaVersion: LatestSnapshotSchema,
		Headline:      LatestHeadline{RealGuestDemos: 3, OptimizationWins: 2, SafetyControls: 1},
		Demos: []LatestDemo{
			{
				ID: "source-prefix-overlap", Title: "Start the READ before the model finishes", Eyebrow: "REACH-GATED STREAMING", Status: "optimized",
				Summary: "The first closed Python suite reaches a Host READ while the remaining source is still being generated.", Source: source,
				Annotations: []LatestCodeAnnotation{
					{StartLine: 1, EndLine: 1, Tone: "effect_trigger", Label: "READ starts", Note: "The closed suite reaches the Host-mediated READ."},
					{StartLine: 2, EndLine: 3, Tone: "overlapped_tail", Label: "generated concurrently", Note: "This source tail arrives while the READ is in flight."},
				},
				Metrics:       []LatestMetric{{Label: "Generate first", Value: fmt.Sprintf("%.0f ms", float64(evidence.BaselineMedianNS)/1e6), Note: "median wall time", Tone: "baseline"}, {Label: "Stream prefix", Value: fmt.Sprintf("%.0f ms", float64(evidence.StreamingMedianNS)/1e6), Note: "median wall time", Tone: "optimized"}, {Label: "Mechanism window", Value: fmt.Sprintf("%.3f×", float64(evidence.MedianSpeedupMilli)/1000), Note: "authored matched fixture", Tone: "win"}},
				Lanes:         []LatestLane{sourcePrefixLane("generate → execute", baseline), sourcePrefixLane("stream while generating", streaming)},
				Facts:         []LatestFact{{Label: "Logical / physical calls", Value: "1 / 1 in both lanes"}, {Label: "Workspace", Value: "unchanged and published"}, {Label: "Fallback", Value: "none"}},
				ClaimBoundary: "Constructive mechanism demonstration for this fixed authored program; not natural-workload uplift.",
			},
			{
				ID: "exact-request-sharing", Title: "Two agents, one physical Guest", Eyebrow: "EXACT REQUEST SHARING", Status: "optimized",
				Summary: "Two logical agents submit identical source, inputs, Plan, privacy partition and workspace root. Pysolate shares one physical execution.", Source: "# Agent A\n" + p05.Source + "\n# Agent B — byte-identical request\n" + p06.Source,
				Annotations: []LatestCodeAnnotation{
					{StartLine: 2, EndLine: 2, Tone: "physical_owner", Label: "physical owner", Note: "This logical request owns the measured Guest execution."},
					{StartLine: 5, EndLine: 5, Tone: "shared_skip", Label: "physical run skipped", Note: "Exact identity attaches this logical request to the same physical result."},
				},
				Metrics:       []LatestMetric{{Label: "Logical requests", Value: "2", Note: "P05 + P06", Tone: "baseline"}, {Label: "Physical executions", Value: "1", Note: "same sealed physical ID", Tone: "optimized"}, {Label: "Oracle results", Value: "2 / 2", Note: "both complete", Tone: "win"}},
				Lanes:         []LatestLane{campaignLane("agent A · shared waiter", "same physical ID", sharedDuration, sharedStart, sharedEnd, "shared"), campaignLane("agent B · physical owner", "same physical ID", sharedDuration, sharedStart, sharedEnd, "physical")},
				Facts:         []LatestFact{{Label: "Sharing decision", Value: "exact_shared"}, {Label: "Identity relation", Value: "same physical execution"}, {Label: "Campaign context", Value: "real Guest · qualified repetition 0"}},
				ClaimBoundary: "Exact identity sharing in the fixed campaign; not arbitrary memoization or cross-authority reuse.",
			},
			{
				ID: "source-mismatch-fallback", Title: "Different source stays fresh", Eyebrow: "FAIL-CLOSED CONTROL", Status: "safety_control",
				Summary: "A semantically similar program has a different source identity. Pysolate refuses sharing and starts an independent physical execution.", Source: "# Shareable request\n" + p05.Source + "\n# Source-mismatch request\n" + p07.Source,
				Annotations: []LatestCodeAnnotation{
					{StartLine: 2, EndLine: 2, Tone: "physical_owner", Label: "physical A", Note: "The admitted exact request executes normally."},
					{StartLine: 5, EndLine: 5, Tone: "fresh_fallback", Label: "fresh physical B", Note: "The source digest differs, so sharing is rejected before execution."},
				},
				Metrics:       []LatestMetric{{Label: "Logical requests", Value: "2", Note: "different source digests", Tone: "baseline"}, {Label: "Physical executions", Value: "2", Note: "sharing rejected", Tone: "control"}, {Label: "Unsafe reuse", Value: "0", Note: "fresh execution preserved", Tone: "win"}},
				Lanes:         []LatestLane{campaignLane("exact request", "physical A", sharedDuration, sharedStart, sharedEnd, "physical"), campaignLane("source mismatch", "fresh physical B", fallbackDuration, fallbackStart, fallbackEnd, "fallback")},
				Facts:         []LatestFact{{Label: "Expected reason", Value: p07.Expected.Sharing}, {Label: "Observed disposition", Value: q07.Disposition}, {Label: "Physical relation", Value: "independent IDs"}},
				ClaimBoundary: "Safety control: source similarity never substitutes for exact bound identity.",
			},
		},
		Boundary: LatestBoundary{Events: census.Denominator.Events, UniqueSources: census.Denominator.UniqueSources, StructurallyEligible: census.Counts.StructurallyEligible, TimingNotRecorded: census.Counts.TimingNotRecorded, PerformanceSupported: census.PerformanceComparisonSupported, Decision: "Do not run trace-derived timing replay for this short READ cohort."},
		Provenance: LatestProvenance{
			SourcePrefixEvidenceSHA256: latestSHA(inputs.SourcePrefixEvidence), CensusEvidenceSHA256: latestSHA(inputs.SourcePrefixCensus), CampaignProjectionSHA256: latestSHA(inputs.CampaignProjection),
			SourcePrefixArtifactSHA256: evidence.ArtifactSHA256, CampaignArtifactSHA256: campaign.Source.ArtifactSHA256,
			SourcePrefixHarnessCommit: evidence.HarnessSourceCommit, CampaignSourceCommit: campaign.Source.CampaignSourceCommit,
		},
	}
	identity, err := latestSnapshotIdentity(snapshot)
	if err != nil {
		return LatestSnapshot{}, err
	}
	snapshot.Identity = identity
	return snapshot, ValidateLatestSnapshot(snapshot)
}

func latestSnapshotIdentity(snapshot LatestSnapshot) (string, error) {
	snapshot.Identity = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return latestSHA(encoded), nil
}

func ValidateLatestSnapshot(snapshot LatestSnapshot) error {
	if snapshot.SchemaVersion != LatestSnapshotSchema || !latestDigest.MatchString(snapshot.Identity) || snapshot.Headline != (LatestHeadline{RealGuestDemos: 3, OptimizationWins: 2, SafetyControls: 1}) || len(snapshot.Demos) != 3 {
		return errors.New("invalid latest Lab snapshot envelope")
	}
	identity, err := latestSnapshotIdentity(snapshot)
	if err != nil || identity != snapshot.Identity {
		return errors.New("latest Lab snapshot identity mismatch")
	}
	wanted := []struct{ id, status string }{{"source-prefix-overlap", "optimized"}, {"exact-request-sharing", "optimized"}, {"source-mismatch-fallback", "safety_control"}}
	for index, demo := range snapshot.Demos {
		if demo.ID != wanted[index].id || demo.Status != wanted[index].status || demo.Title == "" || demo.Summary == "" || demo.Source == "" || demo.ClaimBoundary == "" || len(demo.Metrics) < 3 || len(demo.Lanes) == 0 || len(demo.Facts) == 0 || strings.Contains(demo.Source, "/Users/") || strings.Contains(demo.Source, ".hermes") {
			return errors.New("invalid latest Lab demo")
		}
		lineCount := strings.Count(demo.Source, "\n") + 1
		seenLines := make(map[int]bool)
		allowedTones := map[string]bool{"effect_trigger": true, "overlapped_tail": true, "physical_owner": true, "shared_skip": true, "fresh_fallback": true}
		if len(demo.Annotations) == 0 {
			return errors.New("latest Lab demo lacks code annotations")
		}
		for _, annotation := range demo.Annotations {
			if annotation.StartLine < 1 || annotation.EndLine < annotation.StartLine || annotation.EndLine > lineCount || !allowedTones[annotation.Tone] || annotation.Label == "" || annotation.Note == "" {
				return errors.New("invalid latest Lab code annotation")
			}
			for line := annotation.StartLine; line <= annotation.EndLine; line++ {
				if seenLines[line] {
					return errors.New("overlapping latest Lab code annotations")
				}
				seenLines[line] = true
			}
		}
		for _, lane := range demo.Lanes {
			if lane.Label == "" || lane.DurationNS <= 0 || len(lane.Segments) == 0 {
				return errors.New("invalid latest Lab lane")
			}
			for _, segment := range lane.Segments {
				if segment.Label == "" || segment.StartNS < 0 || segment.EndNS <= segment.StartNS || segment.EndNS > lane.DurationNS {
					return errors.New("invalid latest Lab timeline segment")
				}
			}
		}
	}
	if snapshot.Boundary.Events != 36 || snapshot.Boundary.UniqueSources <= 0 || snapshot.Boundary.StructurallyEligible != 0 || snapshot.Boundary.TimingNotRecorded != 36 || snapshot.Boundary.PerformanceSupported || snapshot.Boundary.Decision == "" {
		return errors.New("invalid latest Lab claim boundary")
	}
	for _, value := range []string{snapshot.Provenance.SourcePrefixEvidenceSHA256, snapshot.Provenance.CensusEvidenceSHA256, snapshot.Provenance.CampaignProjectionSHA256, snapshot.Provenance.SourcePrefixArtifactSHA256, snapshot.Provenance.CampaignArtifactSHA256} {
		if !latestDigest.MatchString(value) {
			return errors.New("invalid latest Lab provenance digest")
		}
	}
	if !latestCommit.MatchString(snapshot.Provenance.SourcePrefixHarnessCommit) || !latestCommit.MatchString(snapshot.Provenance.CampaignSourceCommit) {
		return errors.New("invalid latest Lab provenance commit")
	}
	return nil
}

func EncodeLatestSnapshot(snapshot LatestSnapshot) ([]byte, error) {
	if err := ValidateLatestSnapshot(snapshot); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func DecodeLatestSnapshot(raw []byte) (LatestSnapshot, error) {
	var snapshot LatestSnapshot
	if err := strictLatestDecode(raw, &snapshot); err != nil {
		return LatestSnapshot{}, err
	}
	if err := ValidateLatestSnapshot(snapshot); err != nil {
		return LatestSnapshot{}, err
	}
	return snapshot, nil
}
