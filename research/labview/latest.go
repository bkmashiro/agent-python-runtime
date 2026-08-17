package labview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const LatestSnapshotSchema = "pysolate.lab-latest.v1"

const latestSourcePrefixContractSHA = "sha256:dab34bfa2a6ea8dce909c375c0b963569cfc67f988fa1adae56de561b1b009ff"
const latestSourcePrefixEvidenceSHA = "sha256:51e97f7604351aac6f1822b503e0c6425286f9cd44c6ebd21f0b6ea43b64da69"
const latestSourcePrefixCensusSHA = "sha256:cfedf4adfe63051d9e7b233ef8b36031fb4fda360a7d32e0e634cdce31da5604"
const latestCampaignManifestSHA = "sha256:0633e6d98dd67fee6a2aad12cfd491a6d14e5344d5d2d78d91c059e62ec0fe7e"
const latestCampaignProjectionSHA = "sha256:2955e8b19e4fcd4b450a73415697d798d8ab3fbc9f50f392dd8475e9600bb7bc"
const latestCampaignArtifactSHA = "sha256:0a37a963a09b4e763cb6a40886a771e9c13e2f6a9d3a2d295788752e319c5795"
const latestCampaignArtifactCommit = "ae922641cd9c539b68a0ea7110b5dc205e5c9a8a"
const latestCampaignSourceCommit = "40882ca5a818f4c5388bdeebe7d36ee9dc5fe7c5"

var latestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var latestCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type LatestInputs struct {
	SourcePrefixContract []byte
	SourcePrefixEvidence []byte
	SourcePrefixCensus   []byte
	CampaignManifest     []byte
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

type campaignMetricSummary struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type campaignTreatmentMetrics struct {
	PhysicalExecutions campaignMetricSummary `json:"physical_executions"`
	WallMS             campaignMetricSummary `json:"wall_ms"`
	ProcessCPUMS       campaignMetricSummary `json:"process_cpu_ms"`
}

type campaignPairedMetrics struct {
	PhysicalReduction campaignMetricSummary `json:"physical_reduction"`
	WallReductionMS   campaignMetricSummary `json:"wall_reduction_ms"`
	WallReductionPct  campaignMetricSummary `json:"wall_reduction_pct"`
	CPUReductionMS    campaignMetricSummary `json:"cpu_reduction_ms"`
}

type campaignRunProjection struct {
	Repetition         int                             `json:"repetition"`
	Treatment          workflowbench.CampaignTreatment `json:"treatment"`
	PhysicalExecutions uint32                          `json:"physical_executions"`
	WallMS             float64                         `json:"wall_ms"`
	ProcessCPUMS       float64                         `json:"process_cpu_ms"`
}

type campaignProgramProjection struct {
	ID                     string                                  `json:"id"`
	Family                 string                                  `json:"family"`
	ReleaseOffsetMS        int64                                   `json:"release_offset_ms"`
	PlanSHA256             string                                  `json:"plan_sha256"`
	GrantSetSHA256         string                                  `json:"grant_set_sha256"`
	PrivacyPartition       string                                  `json:"privacy_partition"`
	WorkspaceFixtureSHA256 string                                  `json:"workspace_fixture_sha256"`
	Execution              workflowbench.CampaignExecutionContract `json:"execution"`
	Admission              string                                  `json:"admission"`
	Sharing                string                                  `json:"sharing"`
	Disposition            string                                  `json:"disposition"`
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
	Baseline          campaignTreatmentMetrics    `json:"baseline"`
	Qualified         campaignTreatmentMetrics    `json:"qualified"`
	Paired            campaignPairedMetrics       `json:"paired"`
	Runs              []campaignRunProjection     `json:"runs"`
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

func campaignPhysicalInterval(events []campaignEventProjection, physicalID string) (int64, int64, string, error) {
	var start, end int64 = -1, -1
	var owner string
	var starts, ends int
	for _, event := range events {
		if event.PhysicalExecutionID != physicalID {
			continue
		}
		if event.Type == "physical.started" {
			starts++
			start, owner = event.AtNS, event.ProgramID
		}
		if event.Type == "physical.ended" {
			ends++
			end = event.AtNS
			if owner != "" && event.ProgramID != owner {
				return 0, 0, "", errors.New("campaign physical owner drifted")
			}
		}
	}
	if starts != 1 || ends != 1 || owner == "" || start < 0 || end <= start {
		return 0, 0, "", errors.New("campaign physical interval is incomplete or ambiguous")
	}
	return start, end, owner, nil
}

func campaignTerminal(events []campaignEventProjection, programID string) (campaignEventProjection, error) {
	var terminal campaignEventProjection
	count := 0
	for _, event := range events {
		if event.ProgramID == programID && event.Type == "logical.terminal" {
			terminal = event
			count++
		}
	}
	if count != 1 {
		return campaignEventProjection{}, errors.New("campaign terminal event is absent or ambiguous")
	}
	return terminal, nil
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

func validateLatestInputAnchors(inputs LatestInputs) error {
	if latestSHA(inputs.SourcePrefixContract) != latestSourcePrefixContractSHA || latestSHA(inputs.SourcePrefixEvidence) != latestSourcePrefixEvidenceSHA || latestSHA(inputs.SourcePrefixCensus) != latestSourcePrefixCensusSHA || latestSHA(inputs.CampaignManifest) != latestCampaignManifestSHA || latestSHA(inputs.CampaignProjection) != latestCampaignProjectionSHA {
		return errors.New("latest Lab inputs do not match accepted evidence anchors")
	}
	return nil
}

func projectedAdmission(expected string) (string, bool) {
	value, ok := map[string]string{"admit": "admitted", "reject_expired": "authority_expired", "reject_widening": "authority_widening", "reject_budget": "delegation_budget", "reject_terminal": "parent_terminal"}[expected]
	return value, ok
}

func validateCampaignPrograms(manifest workflowbench.CampaignManifest, campaign campaignProjection) error {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil || campaign.Source.ManifestSHA256 != latestSHA(append(manifestJSON, '\n')) || len(campaign.Programs) != len(manifest.Programs) {
		return errors.New("campaign projection manifest identity mismatch")
	}
	seen := make(map[string]bool, len(campaign.Programs))
	for index, expected := range manifest.Programs {
		actual := campaign.Programs[index]
		admission, admissionOK := projectedAdmission(expected.Expected.Admission)
		if seen[actual.ID] || !admissionOK || actual.ID != expected.ID || actual.Family != expected.Family || actual.ReleaseOffsetMS != expected.ReleaseOffsetMS || actual.PlanSHA256 != expected.PlanSHA256 || actual.GrantSetSHA256 != expected.GrantSetSHA256 || actual.PrivacyPartition != expected.PrivacyPartition || actual.WorkspaceFixtureSHA256 != expected.WorkspaceFixtureSHA256 || !reflect.DeepEqual(actual.Execution, expected.Execution) || actual.Admission != admission || actual.Disposition != expected.Expected.Disposition || actual.Sharing == "" {
			return errors.New("campaign projection program drifted from canonical manifest")
		}
		seen[actual.ID] = true
	}
	return nil
}

func validateCampaignEvents(manifest workflowbench.CampaignManifest, events []campaignEventProjection) error {
	programs := make(map[string]bool, len(manifest.Programs))
	for _, program := range manifest.Programs {
		programs[program.ID] = true
	}
	var lastNS int64 = -1
	for index, event := range events {
		if event.Sequence != uint64(index+1) || event.AtNS < 0 || event.AtNS < lastNS || !programs[event.ProgramID] || event.Type == "" {
			return errors.New("campaign walkthrough event ordering or program identity is invalid")
		}
		lastNS = event.AtNS
		if strings.HasPrefix(event.Type, "physical.") && event.PhysicalExecutionID == "" {
			return errors.New("campaign physical event lacks identity")
		}
	}
	for _, program := range manifest.Programs {
		if _, err := campaignTerminal(events, program.ID); err != nil {
			return err
		}
	}
	return nil
}

func summarizeCampaign(values []float64) campaignMetricSummary {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	median := sorted[middle]
	if len(sorted)%2 == 0 {
		median = (sorted[middle-1] + sorted[middle]) / 2
	}
	return campaignMetricSummary{Median: median, Min: sorted[0], Max: sorted[len(sorted)-1]}
}

func campaignMetricEqual(left, right campaignMetricSummary) bool {
	return math.Abs(left.Median-right.Median) < 1e-9 && math.Abs(left.Min-right.Min) < 1e-9 && math.Abs(left.Max-right.Max) < 1e-9
}

func validateCampaignMetrics(campaign campaignProjection) error {
	if campaign.Source.Repetitions != 5 || len(campaign.Runs) != campaign.Source.Repetitions*2 {
		return errors.New("campaign projection run set is incomplete")
	}
	pairs := make(map[int]map[workflowbench.CampaignTreatment]campaignRunProjection)
	for _, run := range campaign.Runs {
		if run.Repetition < 0 || run.Repetition >= campaign.Source.Repetitions || (run.Treatment != workflowbench.CampaignBaseline && run.Treatment != workflowbench.CampaignQualified) || run.PhysicalExecutions == 0 || run.WallMS <= 0 || run.ProcessCPUMS <= 0 {
			return errors.New("campaign projection run is invalid")
		}
		if pairs[run.Repetition] == nil {
			pairs[run.Repetition] = make(map[workflowbench.CampaignTreatment]campaignRunProjection)
		}
		if _, duplicate := pairs[run.Repetition][run.Treatment]; duplicate {
			return errors.New("campaign projection has duplicate run")
		}
		pairs[run.Repetition][run.Treatment] = run
	}
	var bp, qp, bw, qw, bc, qc, pr, wr, wp, cr []float64
	for repetition := 0; repetition < campaign.Source.Repetitions; repetition++ {
		base, baseOK := pairs[repetition][workflowbench.CampaignBaseline]
		qualified, qualifiedOK := pairs[repetition][workflowbench.CampaignQualified]
		if !baseOK || !qualifiedOK {
			return errors.New("campaign projection lacks paired treatment")
		}
		bp, qp = append(bp, float64(base.PhysicalExecutions)), append(qp, float64(qualified.PhysicalExecutions))
		bw, qw = append(bw, base.WallMS), append(qw, qualified.WallMS)
		bc, qc = append(bc, base.ProcessCPUMS), append(qc, qualified.ProcessCPUMS)
		pr, wr, wp, cr = append(pr, float64(base.PhysicalExecutions)-float64(qualified.PhysicalExecutions)), append(wr, base.WallMS-qualified.WallMS), append(wp, (base.WallMS-qualified.WallMS)/base.WallMS*100), append(cr, base.ProcessCPUMS-qualified.ProcessCPUMS)
	}
	checks := [][2]campaignMetricSummary{{campaign.Baseline.PhysicalExecutions, summarizeCampaign(bp)}, {campaign.Qualified.PhysicalExecutions, summarizeCampaign(qp)}, {campaign.Baseline.WallMS, summarizeCampaign(bw)}, {campaign.Qualified.WallMS, summarizeCampaign(qw)}, {campaign.Baseline.ProcessCPUMS, summarizeCampaign(bc)}, {campaign.Qualified.ProcessCPUMS, summarizeCampaign(qc)}, {campaign.Paired.PhysicalReduction, summarizeCampaign(pr)}, {campaign.Paired.WallReductionMS, summarizeCampaign(wr)}, {campaign.Paired.WallReductionPct, summarizeCampaign(wp)}, {campaign.Paired.CPUReductionMS, summarizeCampaign(cr)}}
	for _, check := range checks {
		if !campaignMetricEqual(check[0], check[1]) {
			return errors.New("campaign projection metrics drifted from runs")
		}
	}
	return nil
}

func validateCampaignDemoJoins(campaign campaignProjection, programs map[string]campaignProgramProjection) error {
	t05, err := campaignTerminal(campaign.WalkthroughEvents, "P05")
	if err != nil {
		return err
	}
	t06, err := campaignTerminal(campaign.WalkthroughEvents, "P06")
	if err != nil || t05.PhysicalExecutionID == "" || t05.PhysicalExecutionID != t06.PhysicalExecutionID {
		return errors.New("exact sharing pair lacks one bound physical execution")
	}
	t07, err := campaignTerminal(campaign.WalkthroughEvents, "P07")
	if err != nil || t07.PhysicalExecutionID == "" || t07.PhysicalExecutionID == t05.PhysicalExecutionID {
		return errors.New("source mismatch did not retain a distinct physical execution")
	}
	_, _, sharedOwner, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t05.PhysicalExecutionID)
	if err != nil || (sharedOwner != "P05" && sharedOwner != "P06") || programs[sharedOwner].Sharing != "independent" {
		return errors.New("exact sharing physical owner is not the independently executed pair member")
	}
	_, _, fallbackOwner, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t07.PhysicalExecutionID)
	if err != nil || fallbackOwner != "P07" {
		return errors.New("source mismatch physical execution has the wrong owner")
	}
	return nil
}

func BuildLatestSnapshot(inputs LatestInputs) (LatestSnapshot, error) {
	if err := validateLatestInputAnchors(inputs); err != nil {
		return LatestSnapshot{}, err
	}
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
	if err := strictLatestDecode(inputs.CampaignProjection, &campaign); err != nil || campaign.SchemaVersion != "pysolate.transparent-campaign-public-projection.v1" || len(campaign.WalkthroughEvents) == 0 || campaign.Source.ArtifactSHA256 != latestCampaignArtifactSHA || campaign.Source.ArtifactSourceCommit != latestCampaignArtifactCommit || campaign.Source.CampaignSourceCommit != latestCampaignSourceCommit {
		return LatestSnapshot{}, errors.New("invalid campaign projection for latest Lab")
	}
	var manifest workflowbench.CampaignManifest
	if err := strictLatestDecode(inputs.CampaignManifest, &manifest); err != nil || manifest.SchemaVersion != "pysolate.transparent-campaign-manifest.v2" || manifest.CampaignID != "authority-transparent-20-v2" || manifest.PhysicalSlots != 3 || len(manifest.Programs) != 20 {
		return LatestSnapshot{}, errors.New("invalid accepted campaign manifest body")
	}
	if err := validateCampaignPrograms(manifest, campaign); err != nil {
		return LatestSnapshot{}, err
	}
	if err := validateCampaignMetrics(campaign); err != nil {
		return LatestSnapshot{}, err
	}
	if err := validateCampaignEvents(manifest, campaign.WalkthroughEvents); err != nil {
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
	if err := validateCampaignDemoJoins(campaign, projectedPrograms); err != nil {
		return LatestSnapshot{}, err
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
	sharedStart, sharedEnd, sharedOwner, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t05.PhysicalExecutionID)
	if err != nil || (sharedOwner != "P05" && sharedOwner != "P06") {
		return LatestSnapshot{}, errors.New("exact sharing physical owner is not a member of the logical pair")
	}
	if projectedPrograms[sharedOwner].Sharing != "independent" {
		return LatestSnapshot{}, errors.New("exact sharing physical owner was not independently executed")
	}
	fallbackStart, fallbackEnd, fallbackOwner, err := campaignPhysicalInterval(campaign.WalkthroughEvents, t07.PhysicalExecutionID)
	if err != nil || fallbackOwner != "P07" {
		return LatestSnapshot{}, errors.New("source mismatch physical execution has the wrong owner")
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

func latestContainsPrivateMarker(snapshot LatestSnapshot) bool {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return true
	}
	lower := strings.ToLower(string(encoded))
	for _, marker := range []string{"/users/", "/home/", `\\users\\`, ".hermes", "file://", "private://", "body_ref", "body-reference", "prompt_body", "provider_request", "provider_response", "trace_body", "workspace_body"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func ValidateLatestSnapshot(snapshot LatestSnapshot) error {
	if snapshot.SchemaVersion != LatestSnapshotSchema || !latestDigest.MatchString(snapshot.Identity) || snapshot.Headline != (LatestHeadline{RealGuestDemos: 3, OptimizationWins: 2, SafetyControls: 1}) || len(snapshot.Demos) != 3 || latestContainsPrivateMarker(snapshot) {
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
