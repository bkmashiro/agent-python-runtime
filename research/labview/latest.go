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

const LatestSnapshotSchema = "pysolate.lab-latest.v2"

const latestSourcePrefixContractSHA = "sha256:dab34bfa2a6ea8dce909c375c0b963569cfc67f988fa1adae56de561b1b009ff"
const latestSourcePrefixEvidenceSHA = "sha256:51e97f7604351aac6f1822b503e0c6425286f9cd44c6ebd21f0b6ea43b64da69"
const latestCampaignManifestSHA = "sha256:0633e6d98dd67fee6a2aad12cfd491a6d14e5344d5d2d78d91c059e62ec0fe7e"
const latestCampaignProjectionSHA = "sha256:2955e8b19e4fcd4b450a73415697d798d8ab3fbc9f50f392dd8475e9600bb7bc"
const latestSemanticPredispatchSHA = "sha256:2cbba19b48611d76a216c4adaf63ed02e2f57519a215cf1779b1e97ca431e21e"
const latestSemanticReuseSHA = "sha256:64ea355829ba9dfe71724df0f89eafa67e69c8e2f73886221289c1e9ed54632b"
const latestCOWGrowableSHA = "sha256:d97f5ab56f85b2ee91af6bfc220bb8d386cc5cf201fa49f7490207e28ff2fb68"
const latestColdIOSHA = "sha256:117598fcb3031d0fba97be30e65e3c5f16c74e5644c634e95f600871e1ecb361"
const latestComposableSHA = "sha256:269560ea66feee6f3015658be1c3fafe8308d973dc465625580185950f70a104"
const latestCampaignArtifactSHA = "sha256:0a37a963a09b4e763cb6a40886a771e9c13e2f6a9d3a2d295788752e319c5795"
const latestCampaignArtifactCommit = "ae922641cd9c539b68a0ea7110b5dc205e5c9a8a"
const latestCampaignSourceCommit = "40882ca5a818f4c5388bdeebe7d36ee9dc5fe7c5"

var latestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var latestCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type LatestInputs struct {
	SourcePrefixContract []byte
	SourcePrefixEvidence []byte
	CampaignManifest     []byte
	CampaignProjection   []byte
	SemanticPredispatch  []byte
	SemanticReuse        []byte
	COWGrowable          []byte
	ColdIO               []byte
	Composable           []byte
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

type LatestCodeAnnotation struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Tone      string `json:"tone"`
	Label     string `json:"label"`
	Note      string `json:"note"`
}

type LatestDemo struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Eyebrow     string                 `json:"eyebrow"`
	Status      string                 `json:"status"`
	ViewKind    string                 `json:"view_kind"`
	Summary     string                 `json:"summary"`
	Source      string                 `json:"source"`
	Annotations []LatestCodeAnnotation `json:"annotations"`
	Metrics     []LatestMetric         `json:"metrics"`
	Lanes       []LatestLane           `json:"lanes"`
}

type LatestProvenance struct {
	SourcePrefixEvidenceSHA256 string `json:"source_prefix_evidence_sha256"`
	CampaignProjectionSHA256   string `json:"campaign_projection_sha256"`
	SemanticPredispatchSHA256  string `json:"semantic_predispatch_sha256"`
	SemanticReuseSHA256        string `json:"semantic_reuse_sha256"`
	COWGrowableSHA256          string `json:"cow_growable_sha256"`
	ColdIOSHA256               string `json:"cold_io_sha256"`
	ComposableSHA256           string `json:"composable_sha256"`
	SourcePrefixArtifactSHA256 string `json:"source_prefix_artifact_sha256"`
	CampaignArtifactSHA256     string `json:"campaign_artifact_sha256"`
	SourcePrefixHarnessCommit  string `json:"source_prefix_harness_commit"`
	CampaignSourceCommit       string `json:"campaign_source_commit"`
}

type LatestSnapshot struct {
	SchemaVersion string           `json:"schema_version"`
	Identity      string           `json:"identity"`
	Demos         []LatestDemo     `json:"demos"`
	Provenance    LatestProvenance `json:"provenance"`
}

type semanticPredispatchEvidence struct {
	SchemaVersion           string `json:"schema_version"`
	ArtifactSHA256          string `json:"artifact_sha256"`
	SourceSHA256            string `json:"source_sha256"`
	CapabilityPlanSHA256    string `json:"capability_plan_sha256"`
	TrialsPerCondition      int    `json:"trials_per_condition"`
	PhysicalDelayMicros     int64  `json:"physical_delay_micros"`
	BaselineMedianMicros    int64  `json:"baseline_median_micros"`
	OptimizedMedianMicros   int64  `json:"optimized_median_micros"`
	MedianSavingsMicros     int64  `json:"median_savings_micros"`
	EquivalentResults       bool   `json:"equivalent_results"`
	NoDuplicatePhysicalCall bool   `json:"no_duplicate_physical_call"`
	ContentSHA256           string `json:"content_sha256"`
	Trials                  []struct {
		Condition      string `json:"condition"`
		LogicalCalls   int    `json:"logical_calls"`
		PhysicalCalls  int    `json:"physical_calls"`
		RejectedClaims int    `json:"rejected_claims"`
	} `json:"trials"`
}

type semanticReuseEvidence struct {
	SchemaVersion string          `json:"schema_version"`
	Status        string          `json:"status"`
	Date          string          `json:"date"`
	SourceCommit  string          `json:"source_commit"`
	TargetGuest   json.RawMessage `json:"target_guest"`
	Workload      struct {
		LogicalInvocations   int  `json:"logical_invocations"`
		PhysicalComputes     int  `json:"physical_computes"`
		ConcurrentLeaders    int  `json:"concurrent_leaders"`
		ConcurrentWaiters    int  `json:"concurrent_waiters"`
		LaterRetainedHits    int  `json:"later_retained_hits"`
		CanonicalResultBytes int  `json:"canonical_result_bytes"`
		ResultBodiesRecorded bool `json:"result_bodies_recorded"`
	} `json:"workload"`
	TimingMicros struct {
		AnalysisPhase   int64 `json:"analysis_phase_including_analyzer_guest_initialization"`
		ConcurrentBatch int64 `json:"concurrent_batch_including_one_guest_initialization_and_compute"`
		LaterLookup     int64 `json:"later_qualification_lookup_and_materialization"`
	} `json:"timing_micros"`
	PassStats  json.RawMessage `json:"pass_stats"`
	StoreStats json.RawMessage `json:"store_stats"`
	Economics  struct {
		Baseline      int64   `json:"three_physical_compute_baseline_micros"`
		Observed      int64   `json:"observed_analysis_plus_reuse_micros"`
		Saved         int64   `json:"observed_saved_micros"`
		SavedFraction float64 `json:"observed_saved_fraction"`
		BreakEven     float64 `json:"hit_probability_break_even_excluding_one_time_analysis"`
		Amortize      float64 `json:"retained_hits_to_amortize_one_time_analysis"`
	} `json:"economics"`
	Interpretation json.RawMessage `json:"interpretation"`
}

type cowGrowableEvidence struct {
	SchemaVersion string          `json:"schema_version"`
	RecordedOn    string          `json:"recorded_on"`
	ClaimScope    string          `json:"claim_scope"`
	Host          json.RawMessage `json:"host"`
	Guest         json.RawMessage `json:"guest"`
	Probe         struct {
		COWSelected bool `json:"cow_selected"`
		Fallback    bool `json:"fallback"`
	} `json:"probe"`
	SealedImage struct {
		BaselineBytes         int64 `json:"baseline_bytes"`
		MaximumVirtualBytes   int64 `json:"maximum_virtual_bytes"`
		MemfdAllocatedBytes   int64 `json:"memfd_allocated_bytes"`
		SparseGrowthTailBytes int64 `json:"sparse_growth_tail_bytes"`
	} `json:"sealed_image"`
	Outcomes struct {
		PrivateStateIsolation        bool   `json:"private_state_isolation"`
		Allocation200                string `json:"allocation_200000000_bytes"`
		PostGrowthClean              bool   `json:"post_growth_clean_checkout"`
		Allocation600                string `json:"allocation_600000000_bytes"`
		PostOverflowRefill           bool   `json:"post_overflow_refill"`
		TemporaryFilesystemIsolation bool   `json:"temporary_filesystem_isolation"`
		ModuleImportIsolation        bool   `json:"module_import_isolation"`
		CancelledRequestRecovery     bool   `json:"cancelled_request_recovery"`
		OverMaximumError             struct {
			Status    string `json:"status"`
			Code      string `json:"code"`
			Message   string `json:"message"`
			ErrorType string `json:"error_type"`
		} `json:"over_maximum_error"`
	} `json:"outcomes"`
	ProcessObservation json.RawMessage `json:"process_observation"`
	Test               string          `json:"test"`
}

type coldIOEvidence struct {
	SchemaVersion             string          `json:"schema_version"`
	Status                    string          `json:"status"`
	Date                      string          `json:"date"`
	SourceCommit              string          `json:"source_commit"`
	Host                      json.RawMessage `json:"host"`
	Fixture                   json.RawMessage `json:"fixture"`
	OfficialGuestVerification struct {
		Result         string `json:"result"`
		FreshSlotClean bool   `json:"fresh_slot_clean"`
	} `json:"official_guest_verification"`
	Observations []struct {
		Mode   string `json:"mode"`
		Before struct {
			RSSKiB int64 `json:"rss_kib"`
		} `json:"before"`
		After struct {
			RSSKiB  int64 `json:"rss_kib"`
			SwapKiB int64 `json:"swap_kib"`
		} `json:"after"`
		AfterResume struct {
			RSSKiB int64 `json:"rss_kib"`
		} `json:"after_resume"`
		ResumeMicros int64 `json:"resume_micros"`
		Bytes        int64 `json:"bytes"`
	} `json:"observations"`
	BoundedConclusion json.RawMessage `json:"bounded_conclusion"`
	NonClaims         json.RawMessage `json:"non_claims"`
}

type composableEvidence struct {
	SchemaVersion string `json:"schema_version"`
	Rows          []struct {
		ScenarioID            string  `json:"scenario_id"`
		Status                string  `json:"status"`
		TerminalDisposition   string  `json:"terminal_disposition"`
		EvidenceComplete      bool    `json:"evidence_complete"`
		RelativeElapsedMillis float64 `json:"relative_elapsed_millis"`
		Trace                 []struct {
			Action        string  `json:"action"`
			Outcome       string  `json:"outcome"`
			StartedMillis float64 `json:"started_millis"`
			EndedMillis   float64 `json:"ended_millis"`
		} `json:"trace"`
	} `json:"rows"`
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
	anchors := []struct {
		raw  []byte
		want string
	}{
		{inputs.SourcePrefixContract, latestSourcePrefixContractSHA},
		{inputs.SourcePrefixEvidence, latestSourcePrefixEvidenceSHA},
		{inputs.CampaignManifest, latestCampaignManifestSHA},
		{inputs.CampaignProjection, latestCampaignProjectionSHA},
		{inputs.SemanticPredispatch, latestSemanticPredispatchSHA},
		{inputs.SemanticReuse, latestSemanticReuseSHA},
		{inputs.COWGrowable, latestCOWGrowableSHA},
		{inputs.ColdIO, latestColdIOSHA},
		{inputs.Composable, latestComposableSHA},
	}
	for _, anchor := range anchors {
		if latestSHA(anchor.raw) != anchor.want {
			return errors.New("latest Lab inputs do not match accepted evidence anchors")
		}
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
	var predispatch semanticPredispatchEvidence
	if err := json.Unmarshal(inputs.SemanticPredispatch, &predispatch); err != nil || predispatch.SchemaVersion != "pysolate.semantic-predispatch-experiment.v0" || predispatch.TrialsPerCondition != 5 || len(predispatch.Trials) != 10 || predispatch.BaselineMedianMicros <= predispatch.OptimizedMedianMicros || predispatch.MedianSavingsMicros != predispatch.BaselineMedianMicros-predispatch.OptimizedMedianMicros || !predispatch.EquivalentResults || !predispatch.NoDuplicatePhysicalCall {
		return LatestSnapshot{}, errors.New("invalid semantic pre-dispatch evidence for latest Lab")
	}
	predispatchRejectedClaims := 0
	conditions := map[string]int{}
	for _, trial := range predispatch.Trials {
		conditions[trial.Condition]++
		predispatchRejectedClaims += trial.RejectedClaims
		if trial.LogicalCalls != 1 || trial.PhysicalCalls != 1 {
			return LatestSnapshot{}, errors.New("semantic pre-dispatch trial call counts drifted")
		}
	}
	if conditions["baseline"] != 5 || conditions["semantic_pre_dispatch"] != 5 || predispatchRejectedClaims != 0 {
		return LatestSnapshot{}, errors.New("semantic pre-dispatch trial set drifted")
	}
	var reuse semanticReuseEvidence
	if err := json.Unmarshal(inputs.SemanticReuse, &reuse); err != nil || reuse.SchemaVersion != "pysolate.semantic-reuse-observation.v0" || reuse.Workload.LogicalInvocations != 3 || reuse.Workload.PhysicalComputes != 1 || reuse.Workload.ConcurrentWaiters != 1 || reuse.Workload.LaterRetainedHits != 1 || reuse.Economics.Baseline <= reuse.Economics.Observed || reuse.TimingMicros.LaterLookup <= 0 {
		return LatestSnapshot{}, errors.New("invalid semantic reuse evidence for latest Lab")
	}
	var cow cowGrowableEvidence
	if err := json.Unmarshal(inputs.COWGrowable, &cow); err != nil || cow.SchemaVersion != "pysolate.cow-growable-evidence.v1" || !cow.Probe.COWSelected || cow.Probe.Fallback || !cow.Outcomes.PrivateStateIsolation || !cow.Outcomes.PostGrowthClean || !cow.Outcomes.PostOverflowRefill || cow.Outcomes.Allocation200 != "succeeded" || cow.Outcomes.Allocation600 != "rejected_above_declared_maximum" || cow.Outcomes.OverMaximumError.Status != "error" || cow.Outcomes.OverMaximumError.Code != "python_exception" || cow.Outcomes.OverMaximumError.Message != "MemoryError" || cow.Outcomes.OverMaximumError.ErrorType != "MemoryError" || cow.SealedImage.BaselineBytes <= 0 || cow.SealedImage.MaximumVirtualBytes <= cow.SealedImage.BaselineBytes || cow.SealedImage.SparseGrowthTailBytes != cow.SealedImage.MaximumVirtualBytes-cow.SealedImage.BaselineBytes {
		return LatestSnapshot{}, errors.New("invalid growable COW evidence for latest Lab")
	}
	var cold coldIOEvidence
	if err := json.Unmarshal(inputs.ColdIO, &cold); err != nil || cold.SchemaVersion != "pysolate.cold-io-observation.v0" || cold.OfficialGuestVerification.Result != "PASS" || !cold.OfficialGuestVerification.FreshSlotClean || len(cold.Observations) != 3 {
		return LatestSnapshot{}, errors.New("invalid cold-I/O evidence for latest Lab")
	}
	var pageoutIndex = -1
	for index, observation := range cold.Observations {
		if observation.Mode == "pageout" {
			pageoutIndex = index
		}
	}
	if pageoutIndex < 0 || cold.Observations[pageoutIndex].Before.RSSKiB <= 0 || cold.Observations[pageoutIndex].After.RSSKiB != 0 || cold.Observations[pageoutIndex].AfterResume.RSSKiB <= 0 {
		return LatestSnapshot{}, errors.New("cold-I/O pageout observation is incomplete")
	}
	pageout := cold.Observations[pageoutIndex]
	var composable composableEvidence
	if err := json.Unmarshal(inputs.Composable, &composable); err != nil {
		return LatestSnapshot{}, fmt.Errorf("decode composable evidence: %w", err)
	}
	if composable.SchemaVersion != "pysolate.composable-acceptance-report.v3" || len(composable.Rows) == 0 {
		return LatestSnapshot{}, errors.New("invalid composable evidence for latest Lab")
	}
	var reevaluationRow *struct {
		ScenarioID            string  `json:"scenario_id"`
		Status                string  `json:"status"`
		TerminalDisposition   string  `json:"terminal_disposition"`
		EvidenceComplete      bool    `json:"evidence_complete"`
		RelativeElapsedMillis float64 `json:"relative_elapsed_millis"`
		Trace                 []struct {
			Action        string  `json:"action"`
			Outcome       string  `json:"outcome"`
			StartedMillis float64 `json:"started_millis"`
			EndedMillis   float64 `json:"ended_millis"`
		} `json:"trace"`
	}
	for index := range composable.Rows {
		if composable.Rows[index].ScenarioID == "dev-wait-resume-report" {
			reevaluationRow = &composable.Rows[index]
		}
	}
	if reevaluationRow == nil || reevaluationRow.Status != "passed" || reevaluationRow.TerminalDisposition != "closed" || !reevaluationRow.EvidenceComplete {
		return LatestSnapshot{}, errors.New("fresh re-evaluation row is absent or incomplete")
	}
	var waitStart, waitEnd float64 = -1, -1
	freshObserved := false
	oracleChecks, oraclePasses := 0, 0
	for _, event := range reevaluationRow.Trace {
		switch event.Action {
		case "wait.begin":
			waitStart = event.StartedMillis
		case "wait.release":
			if event.Outcome == "ok" {
				waitEnd = event.EndedMillis
			}
		case "resume.fresh":
			freshObserved = event.Outcome == "ok"
		case "oracle.compare":
			oracleChecks++
			if event.Outcome == "ok" {
				oraclePasses++
			}
		}
	}
	if waitStart < 0 || waitEnd <= waitStart || !freshObserved || oracleChecks == 0 || oraclePasses != oracleChecks || reevaluationRow.RelativeElapsedMillis < waitEnd {
		return LatestSnapshot{}, errors.New("fresh re-evaluation trace is incomplete")
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
		Demos: []LatestDemo{
			{
				ID: "source-prefix-overlap", Title: "Check the weather while the plan is still being written", Eyebrow: "SOURCE PREFIX", Status: "measured", ViewKind: "timeline",
				Summary: "Start Oxford's weather lookup as soon as that first line is complete, while the rest of the day-plan Python is still arriving.", Source: "weather = travel.weather(\"oxford\", \"saturday\")\nneeds_indoor = weather[\"condition\"] != \"sunny\"\nresult = {\"destination\": \"oxford\", \"plan\": \"museum\" if needs_indoor else \"walking tour\", \"high_c\": weather[\"high_c\"]}\n",
				Annotations: []LatestCodeAnnotation{
					{StartLine: 1, EndLine: 1, Tone: "effect_trigger", Label: "Oxford weather", Note: "The closed prefix can start this delayed Host lookup immediately."},
					{StartLine: 2, EndLine: 3, Tone: "overlapped_tail", Label: "finish the plan", Note: "The remaining day-plan logic arrives while the weather lookup is in flight."},
				},
				Metrics: []LatestMetric{{Label: "Generate first", Value: fmt.Sprintf("%.0f ms", float64(evidence.BaselineMedianNS)/1e6), Note: "median", Tone: "baseline"}, {Label: "Stream prefix", Value: fmt.Sprintf("%.0f ms", float64(evidence.StreamingMedianNS)/1e6), Note: "median", Tone: "optimized"}, {Label: "Mechanism window", Value: fmt.Sprintf("%.3f×", float64(evidence.MedianSpeedupMilli)/1000), Note: "matched run", Tone: "win"}},
				Lanes:   []LatestLane{sourcePrefixLane("generate → execute", baseline), sourcePrefixLane("stream while generating", streaming)},
			},
			{
				ID: "semantic-predispatch", Title: "Fetch the proven train option before Guest startup", Eyebrow: "SEMANTIC PRE-DISPATCH", Status: "measured", ViewKind: "timeline",
				Summary: "The fixed London-to-Oxford train lookup is proven before execution, so the Host can start it early and consume it at the exact call site.", Source: "trains = travel.trains(\"london\", \"oxford\", \"saturday\")\nresult = trains\n",
				Annotations: []LatestCodeAnnotation{{StartLine: 1, EndLine: 1, Tone: "effect_trigger", Label: "London → Oxford trains", Note: "The staged observation is consumed only at this exact call."}},
				Metrics:     []LatestMetric{{Label: "Baseline", Value: fmt.Sprintf("%.0f ms", float64(predispatch.BaselineMedianMicros)/1000), Note: "median", Tone: "baseline"}, {Label: "Pre-dispatch", Value: fmt.Sprintf("%.0f ms", float64(predispatch.OptimizedMedianMicros)/1000), Note: "median", Tone: "optimized"}, {Label: "Saved", Value: fmt.Sprintf("%.0f ms", float64(predispatch.MedianSavingsMicros)/1000), Note: "5 matched trials", Tone: "win"}},
				Lanes: []LatestLane{
					campaignLane("ordinary dispatch", "total wall", predispatch.BaselineMedianMicros*1000, 0, predispatch.BaselineMedianMicros*1000, "physical"),
					campaignLane("semantic pre-dispatch", "total wall", predispatch.BaselineMedianMicros*1000, 0, predispatch.OptimizedMedianMicros*1000, "shared"),
				},
			},
			{
				ID: "exact-request-sharing", Title: "Two agents, one physical Guest", Eyebrow: "EXACT CONCURRENT SHARING", Status: "measured", ViewKind: "timeline",
				Summary: "A release agent and reviewer request the same deterministic workspace summary and attach to one Guest.", Source: "# Release agent\nfiles = [\"input-a.txt\", \"input-b.txt\"]\nresult = f\"{len(files)} files|status=ready\"\n\n# Reviewer — exact same program\nfiles = [\"input-a.txt\", \"input-b.txt\"]\nresult = f\"{len(files)} files|status=ready\"\n",
				Annotations: []LatestCodeAnnotation{
					{StartLine: 2, EndLine: 3, Tone: "physical_owner", Label: "release agent", Note: "This request owns the physical Guest."},
					{StartLine: 6, EndLine: 7, Tone: "shared_skip", Label: "reviewer joins", Note: "The exact same program attaches without another Guest start."},
				},
				Metrics: []LatestMetric{{Label: "Logical requests", Value: "2", Note: "agents", Tone: "baseline"}, {Label: "Physical executions", Value: "1", Note: "Guest", Tone: "optimized"}, {Label: "Guest starts avoided", Value: "1", Note: "duplicate", Tone: "win"}},
				Lanes:   []LatestLane{campaignLane("agent A", "same physical run", sharedDuration, sharedStart, sharedEnd, "shared"), campaignLane("agent B", "same physical run", sharedDuration, sharedStart, sharedEnd, "physical")},
			},
			{
				ID: "whole-run-retention", Title: "Compute once, reuse twice", Eyebrow: "WHOLE-RUN RETENTION", Status: "experimental", ViewKind: "timeline",
				Summary: "Compute one deterministic dependency report; a concurrent reviewer and a later dashboard request reuse it.", Source: "# First request\nlockfile = \"package-lock.json\"\nresult = build_dependency_report(lockfile)\n\n# Reviewer joins the in-flight report\n# Dashboard reuses the retained report\n",
				Annotations: []LatestCodeAnnotation{
					{StartLine: 2, EndLine: 3, Tone: "physical_owner", Label: "build report", Note: "The only physical Guest computes the dependency report."},
					{StartLine: 5, EndLine: 6, Tone: "shared_skip", Label: "reuse report", Note: "The reviewer joins in flight; the dashboard later hits retention."},
				},
				Metrics: []LatestMetric{{Label: "Logical", Value: fmt.Sprintf("%d", reuse.Workload.LogicalInvocations), Note: "invocations", Tone: "baseline"}, {Label: "Physical", Value: fmt.Sprintf("%d", reuse.Workload.PhysicalComputes), Note: "compute", Tone: "optimized"}, {Label: "Later lookup", Value: fmt.Sprintf("%.3f ms", float64(reuse.TimingMicros.LaterLookup)/1000), Note: "retained hit", Tone: "win"}},
				Lanes: []LatestLane{
					campaignLane("3 fresh computes", "estimated baseline", reuse.Economics.Baseline*1000, 0, reuse.Economics.Baseline*1000, "physical"),
					campaignLane("analyze + reuse", "observed path", reuse.Economics.Baseline*1000, 0, reuse.Economics.Observed*1000, "shared"),
				},
			},
			{
				ID: "cow-fresh-memory", Title: "Share the baseline, keep writes private", Eyebrow: "SINGLE-USE COW", Status: "experimental", ViewKind: "state_flow",
				Summary: "A fresh Guest opens a 200 MB in-memory workspace index without copying the initialized baseline.", Source: "workspace_index = bytearray(200_000_000)\nworkspace_index[-1] = 7\nresult = {\"index_bytes\": len(workspace_index), \"ready\": workspace_index[-1] == 7}\n",
				Annotations: []LatestCodeAnnotation{{StartLine: 1, EndLine: 3, Tone: "physical_owner", Label: "private index", Note: "The workspace index grows privately; the initialized baseline remains shared."}},
				Metrics:     []LatestMetric{{Label: "Baseline", Value: fmt.Sprintf("%d MiB", cow.SealedImage.BaselineBytes/(1<<20)), Note: "sealed image", Tone: "baseline"}, {Label: "Growth tail", Value: fmt.Sprintf("%d MiB", cow.SealedImage.SparseGrowthTailBytes/(1<<20)), Note: "sparse", Tone: "optimized"}, {Label: "Maximum", Value: fmt.Sprintf("%d MiB", cow.SealedImage.MaximumVirtualBytes/(1<<20)), Note: "bounded", Tone: "win"}},
				Lanes:       []LatestLane{},
			},
			{
				ID: "cold-io-continuation", Title: "Page out memory while waiting", Eyebrow: "COLD I/O CONTINUATION", Status: "experimental", ViewKind: "state_flow",
				Summary: "Page out a large workspace index while its Guest waits for review, then continue with the same Python state.", Source: "workspace_index = bytearray(200_000_000)\nworkspace_index[-1] = 7\nreview = {\"selected_branch\": 1}\napproved = reviews.wait(\"workspace-summary\")\nresult = {\"branch\": review[\"selected_branch\"], \"index_ready\": workspace_index[-1] == 7, \"approved\": approved}\n",
				Annotations: []LatestCodeAnnotation{{StartLine: 4, EndLine: 4, Tone: "effect_trigger", Label: "wait for review", Note: "Private pages can be reclaimed while this continuation is paused."}, {StartLine: 5, EndLine: 5, Tone: "physical_owner", Label: "same state", Note: "The selected branch and index sentinel remain available after resume."}},
				Metrics:     []LatestMetric{{Label: "Before wait", Value: fmt.Sprintf("%d MiB", pageout.Before.RSSKiB/1024), Note: "private RSS", Tone: "baseline"}, {Label: "During wait", Value: fmt.Sprintf("%d MiB", pageout.After.RSSKiB/1024), Note: "pageout", Tone: "optimized"}, {Label: "Resume", Value: fmt.Sprintf("%.1f ms", float64(pageout.ResumeMicros)/1000), Note: "full refault scan", Tone: "control"}},
				Lanes:       []LatestLane{},
			},
			{
				ID: "fresh-reevaluation", Title: "Release the Guest, resume fresh", Eyebrow: "FRESH RE-EVALUATION", Status: "experimental", ViewKind: "timeline",
				Summary: "Release the waiting Guest, then rebuild the workspace summary in a fresh Guest after the inputs are stable.", Source: "files = [\"input-a.txt\", \"input-b.txt\"]\nselected_child = 1\nresult = f\"{len(files)} files|selected={selected_child}|status=ready\"\n",
				Annotations: []LatestCodeAnnotation{{StartLine: 1, EndLine: 3, Tone: "fresh_fallback", Label: "fresh re-evaluation", Note: "After the Host wait, this workspace-summary program runs in a new Guest."}},
				Metrics:     []LatestMetric{{Label: "Wait", Value: fmt.Sprintf("%.1f s", float64(waitEnd-waitStart)/1000), Note: "Guest released", Tone: "baseline"}, {Label: "Resume execution", Value: "fresh Guest", Note: "re-evaluated", Tone: "optimized"}},
				Lanes:       []LatestLane{{Label: "logical workflow", DurationNS: int64(reevaluationRow.RelativeElapsedMillis * 1e6), Segments: []LatestSegment{{Label: "Host wait", StartNS: int64(waitStart * 1e6), EndNS: int64(waitEnd * 1e6), Tone: "effect"}}}},
			},
			{
				ID: "source-mismatch-fallback", Title: "Different source stays fresh", Eyebrow: "FAIL-CLOSED CONTROL", Status: "control", ViewKind: "timeline",
				Summary: "A reviewer changes the workspace-summary program, so the request must start an independent Guest.", Source: "# Existing request\nfiles = [\"input-a.txt\", \"input-b.txt\"]\nresult = f\"{len(files)} files|status=ready\"\n\n# Reviewer adds sorting — different source\nfiles = sorted([\"input-a.txt\", \"input-b.txt\"])\nresult = f\"{len(files)} files|status=ready\"\n",
				Annotations: []LatestCodeAnnotation{
					{StartLine: 2, EndLine: 3, Tone: "physical_owner", Label: "existing program", Note: "The first workspace summary executes normally."},
					{StartLine: 6, EndLine: 7, Tone: "fresh_fallback", Label: "changed program", Note: "Adding sorting changes source identity, so this request gets a fresh Guest."},
				},
				Metrics: []LatestMetric{{Label: "Logical requests", Value: "2", Note: "requests", Tone: "baseline"}, {Label: "Physical executions", Value: "2", Note: "Guests", Tone: "control"}, {Label: "Unsafe reuse", Value: "0", Note: "rejected", Tone: "win"}},
				Lanes:   []LatestLane{campaignLane("exact request", "physical A", sharedDuration, sharedStart, sharedEnd, "physical"), campaignLane("different source", "fresh physical B", fallbackDuration, fallbackStart, fallbackEnd, "fallback")},
			},
		},
		Provenance: LatestProvenance{
			SourcePrefixEvidenceSHA256: latestSHA(inputs.SourcePrefixEvidence), CampaignProjectionSHA256: latestSHA(inputs.CampaignProjection),
			SemanticPredispatchSHA256: latestSHA(inputs.SemanticPredispatch), SemanticReuseSHA256: latestSHA(inputs.SemanticReuse), COWGrowableSHA256: latestSHA(inputs.COWGrowable), ColdIOSHA256: latestSHA(inputs.ColdIO), ComposableSHA256: latestSHA(inputs.Composable),
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
	if snapshot.SchemaVersion != LatestSnapshotSchema || !latestDigest.MatchString(snapshot.Identity) || len(snapshot.Demos) != 8 || latestContainsPrivateMarker(snapshot) {
		return errors.New("invalid latest Lab snapshot envelope")
	}
	identity, err := latestSnapshotIdentity(snapshot)
	if err != nil || identity != snapshot.Identity {
		return errors.New("latest Lab snapshot identity mismatch")
	}
	wanted := []struct{ id, status, view string }{
		{"source-prefix-overlap", "measured", "timeline"},
		{"semantic-predispatch", "measured", "timeline"},
		{"exact-request-sharing", "measured", "timeline"},
		{"whole-run-retention", "experimental", "timeline"},
		{"cow-fresh-memory", "experimental", "state_flow"},
		{"cold-io-continuation", "experimental", "state_flow"},
		{"fresh-reevaluation", "experimental", "timeline"},
		{"source-mismatch-fallback", "control", "timeline"},
	}
	for index, demo := range snapshot.Demos {
		if demo.ID != wanted[index].id || demo.Status != wanted[index].status || demo.ViewKind != wanted[index].view || demo.Title == "" || demo.Summary == "" || demo.Source == "" || len(demo.Metrics) < 2 || len(demo.Metrics) > 3 || strings.Contains(demo.Source, "/Users/") || strings.Contains(demo.Source, ".hermes") {
			return errors.New("invalid latest Lab demo")
		}
		allowedMetricTones := map[string]bool{"baseline": true, "optimized": true, "win": true, "control": true}
		for _, metric := range demo.Metrics {
			if metric.Label == "" || metric.Value == "" || metric.Note == "" || !allowedMetricTones[metric.Tone] {
				return errors.New("invalid latest Lab metric")
			}
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
		if demo.Lanes == nil || (demo.ViewKind == "timeline" && len(demo.Lanes) == 0) || (demo.ViewKind == "state_flow" && len(demo.Lanes) != 0) {
			return errors.New("latest Lab view contract drifted")
		}
		allowedSegmentTones := map[string]bool{"generation": true, "effect": true, "finalize": true, "shared": true, "physical": true, "fallback": true}
		for _, lane := range demo.Lanes {
			if lane.Label == "" || lane.DurationNS <= 0 || len(lane.Segments) == 0 {
				return errors.New("invalid latest Lab lane")
			}
			for _, segment := range lane.Segments {
				if segment.Label == "" || segment.StartNS < 0 || segment.EndNS <= segment.StartNS || segment.EndNS > lane.DurationNS || !allowedSegmentTones[segment.Tone] {
					return errors.New("invalid latest Lab timeline segment")
				}
			}
		}
	}
	for _, value := range []string{
		snapshot.Provenance.SourcePrefixEvidenceSHA256,
		snapshot.Provenance.CampaignProjectionSHA256,
		snapshot.Provenance.SemanticPredispatchSHA256,
		snapshot.Provenance.SemanticReuseSHA256,
		snapshot.Provenance.COWGrowableSHA256,
		snapshot.Provenance.ColdIOSHA256,
		snapshot.Provenance.ComposableSHA256,
		snapshot.Provenance.SourcePrefixArtifactSHA256,
		snapshot.Provenance.CampaignArtifactSHA256,
	} {
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
