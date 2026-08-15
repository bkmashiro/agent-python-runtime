package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const projectionSchema = "pysolate.transparent-campaign-public-projection.v1"

type privateHost struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	Kernel    string `json:"kernel"`
}

type privateSummary struct {
	SchemaVersion        string       `json:"schema_version"`
	ArtifactSHA256       string       `json:"artifact_sha256"`
	ArtifactSourceCommit string       `json:"artifact_source_commit"`
	CampaignSourceCommit string       `json:"campaign_source_commit"`
	ManifestSHA256       string       `json:"manifest_sha256"`
	Host                 privateHost  `json:"host"`
	Repetitions          int          `json:"repetitions"`
	Runs                 []privateRun `json:"runs"`
}

type privateRun struct {
	Repetition          int                             `json:"repetition"`
	Treatment           workflowbench.CampaignTreatment `json:"treatment"`
	EvidenceFile        string                          `json:"evidence_file"`
	EvidenceSHA256      string                          `json:"evidence_sha256"`
	PhysicalExecutions  uint32                          `json:"physical_executions"`
	WallNS              int64                           `json:"wall_ns"`
	ProcessCPUNS        uint64                          `json:"process_cpu_ns,omitempty"`
	ProcessCPUAvailable bool                            `json:"process_cpu_available"`
}

type projectionSource struct {
	ArtifactSHA256       string      `json:"artifact_sha256"`
	ArtifactSourceCommit string      `json:"artifact_source_commit"`
	CampaignSourceCommit string      `json:"campaign_source_commit"`
	ManifestSHA256       string      `json:"manifest_sha256"`
	Host                 privateHost `json:"host"`
	Repetitions          int         `json:"repetitions"`
}

type metricSummary struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type treatmentMetrics struct {
	PhysicalExecutions metricSummary `json:"physical_executions"`
	WallMS             metricSummary `json:"wall_ms"`
	ProcessCPUMS       metricSummary `json:"process_cpu_ms"`
}

type pairedMetrics struct {
	PhysicalReduction metricSummary `json:"physical_reduction"`
	WallReductionMS   metricSummary `json:"wall_reduction_ms"`
	WallReductionPct  metricSummary `json:"wall_reduction_pct"`
	CPUReductionMS    metricSummary `json:"cpu_reduction_ms"`
}

type projectionRun struct {
	Repetition         int                             `json:"repetition"`
	Treatment          workflowbench.CampaignTreatment `json:"treatment"`
	PhysicalExecutions uint32                          `json:"physical_executions"`
	WallMS             float64                         `json:"wall_ms"`
	ProcessCPUMS       float64                         `json:"process_cpu_ms"`
}

type projectionProgram struct {
	ID                     string                                  `json:"id"`
	Family                 string                                  `json:"family"`
	ReleaseOffsetMS        int64                                   `json:"release_offset_ms"`
	PlanSHA256             string                                  `json:"plan_sha256"`
	GrantSetSHA256         string                                  `json:"grant_set_sha256"`
	PrivacyPartition       string                                  `json:"privacy_partition"`
	WorkspaceFixtureSHA256 string                                  `json:"workspace_fixture_sha256"`
	Execution              workflowbench.CampaignExecutionContract `json:"execution"`
	Admission              string                                  `json:"admission"`
	Disposition            string                                  `json:"disposition"`
}

type projectionEvent struct {
	Sequence            uint64 `json:"sequence"`
	ProgramID           string `json:"program_id"`
	Type                string `json:"type"`
	AtNS                int64  `json:"at_ns"`
	Reason              string `json:"reason,omitempty"`
	PhysicalExecutionID string `json:"physical_execution_id,omitempty"`
}

type publicProjection struct {
	SchemaVersion     string              `json:"schema_version"`
	Source            projectionSource    `json:"source"`
	Baseline          treatmentMetrics    `json:"baseline"`
	Qualified         treatmentMetrics    `json:"qualified"`
	Paired            pairedMetrics       `json:"paired"`
	Runs              []projectionRun     `json:"runs"`
	Programs          []projectionProgram `json:"programs"`
	WalkthroughEvents []projectionEvent   `json:"walkthrough_events"`
	ValidClaim        string              `json:"valid_claim"`
	InvalidInference  string              `json:"invalid_inference"`
}

func main() {
	evidenceRoot := flag.String("evidence-root", "", "private evidence root")
	expectedArtifact := flag.String("expected-artifact", "", "required Guest artifact SHA-256")
	expectedCampaign := flag.String("expected-campaign-commit", "", "required campaign source commit")
	jsonOutput := flag.String("json-output", "", "public projection JSON path")
	svgOutput := flag.String("svg-output", "", "paired-result SVG path")
	flowSVGOutput := flag.String("flow-svg-output", "", "arrival-to-terminal SVG path")
	markdownOutput := flag.String("markdown-output", "", "canonical Markdown report path")
	flag.Parse()
	if err := run(*evidenceRoot, *expectedArtifact, *expectedCampaign, *jsonOutput, *svgOutput, *flowSVGOutput, *markdownOutput); err != nil {
		log.Fatal(err)
	}
}

func run(root, expectedArtifact, expectedCampaign, jsonOutput, svgOutput, flowSVGOutput, markdownOutput string) error {
	if root == "" || expectedArtifact == "" || expectedCampaign == "" || jsonOutput == "" || svgOutput == "" || flowSVGOutput == "" || markdownOutput == "" {
		return errors.New("all projection inputs and outputs are required")
	}
	summary, err := loadSummary(filepath.Join(root, "summary.json"))
	if err != nil {
		return err
	}
	if summary.ArtifactSHA256 != expectedArtifact || summary.CampaignSourceCommit != expectedCampaign || summary.Repetitions < 3 || len(summary.Runs) != summary.Repetitions*2 {
		return errors.New("private summary does not match the frozen projection cohort")
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if digest, err := fileSHA256(manifestPath); err != nil || digest != summary.ManifestSHA256 {
		return errors.New("manifest digest does not match private summary")
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	manifest, err := workflowbench.DecodeCampaignManifest(manifestRaw)
	if err != nil {
		return err
	}
	canonicalManifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		return err
	}
	canonicalJSON, err := json.Marshal(canonicalManifest)
	canonicalDigest := sha256.Sum256(append(canonicalJSON, '\n'))
	if err != nil || fmt.Sprintf("sha256:%x", canonicalDigest) != summary.ManifestSHA256 {
		return errors.New("private manifest is not the canonical frozen campaign")
	}
	projection := publicProjection{
		SchemaVersion:    projectionSchema,
		Source:           projectionSource{ArtifactSHA256: summary.ArtifactSHA256, ArtifactSourceCommit: summary.ArtifactSourceCommit, CampaignSourceCommit: summary.CampaignSourceCommit, ManifestSHA256: summary.ManifestSHA256, Host: summary.Host, Repetitions: summary.Repetitions},
		ValidClaim:       "For this fixed 20-program campaign on one recorded host, exact qualified sharing reduced physical executions while preserving every registered oracle and authority rejection.",
		InvalidInference: "Do not generalize these five paired repetitions to arbitrary workloads, hosts, schedulers, or steady-state production throughput.",
	}
	byRepetition := make(map[int]map[workflowbench.CampaignTreatment]projectionRun)
	var walkthroughSet bool
	representativeRows := make(map[string]workflowbench.CampaignRow)
	seenFiles := make(map[string]struct{}, len(summary.Runs))
	for _, run := range summary.Runs {
		if run.Repetition < 0 || run.Repetition >= summary.Repetitions || (run.Treatment != workflowbench.CampaignBaseline && run.Treatment != workflowbench.CampaignQualified) || filepath.Base(run.EvidenceFile) != run.EvidenceFile {
			return errors.New("invalid private run index")
		}
		if _, duplicate := seenFiles[run.EvidenceFile]; duplicate {
			return errors.New("duplicate private evidence file")
		}
		seenFiles[run.EvidenceFile] = struct{}{}
		evidencePath := filepath.Join(root, run.EvidenceFile)
		if digest, err := fileSHA256(evidencePath); err != nil || digest != run.EvidenceSHA256 {
			return errors.New("private evidence digest mismatch")
		}
		raw, err := os.ReadFile(evidencePath)
		if err != nil {
			return err
		}
		evidence, err := workflowbench.DecodeCampaignEvidence(raw, manifest)
		if err != nil || evidence.Treatment != run.Treatment || evidence.PhysicalExecutions != run.PhysicalExecutions || evidence.WallNS != run.WallNS || evidence.ProcessCPUNS != run.ProcessCPUNS || evidence.ProcessCPUAvailable != run.ProcessCPUAvailable {
			return errors.New("private evidence and summary disagree")
		}
		projected := projectionRun{Repetition: run.Repetition, Treatment: run.Treatment, PhysicalExecutions: run.PhysicalExecutions, WallMS: float64(run.WallNS) / 1e6, ProcessCPUMS: float64(run.ProcessCPUNS) / 1e6}
		projection.Runs = append(projection.Runs, projected)
		if byRepetition[run.Repetition] == nil {
			byRepetition[run.Repetition] = make(map[workflowbench.CampaignTreatment]projectionRun)
		}
		if _, duplicate := byRepetition[run.Repetition][run.Treatment]; duplicate {
			return errors.New("duplicate repetition treatment")
		}
		byRepetition[run.Repetition][run.Treatment] = projected
		if !walkthroughSet && run.Repetition == 0 && run.Treatment == workflowbench.CampaignQualified {
			for _, event := range evidence.Events {
				projection.WalkthroughEvents = append(projection.WalkthroughEvents, projectionEvent(event))
			}
			for _, row := range evidence.Rows {
				representativeRows[row.ProgramID] = row
			}
			walkthroughSet = true
		}
	}
	for _, program := range manifest.Programs {
		row, ok := representativeRows[program.ID]
		if !ok {
			return errors.New("representative qualified evidence is missing a program row")
		}
		projection.Programs = append(projection.Programs, projectionProgram{ID: program.ID, Family: program.Family, ReleaseOffsetMS: program.ReleaseOffsetMS, PlanSHA256: program.PlanSHA256, GrantSetSHA256: program.GrantSetSHA256, PrivacyPartition: program.PrivacyPartition, WorkspaceFixtureSHA256: program.WorkspaceFixtureSHA256, Execution: program.Execution, Admission: row.AdmissionReason, Disposition: row.Disposition})
	}
	if err := computeMetrics(&projection, byRepetition, summary.Repetitions); err != nil {
		return err
	}
	if err := writeFile(jsonOutput, mustJSON(projection)); err != nil {
		return err
	}
	if err := writeFile(svgOutput, []byte(renderSVG(projection))); err != nil {
		return err
	}
	if err := writeFile(flowSVGOutput, []byte(renderFlowSVG(projection))); err != nil {
		return err
	}
	return writeFile(markdownOutput, []byte(renderMarkdown(projection)))
}

func loadSummary(path string) (privateSummary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return privateSummary{}, err
	}
	if workflowbench.ValidateUniqueJSONKeys(raw) != nil {
		return privateSummary{}, errors.New("summary contains duplicate JSON keys")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var summary privateSummary
	if decoder.Decode(&summary) != nil || decoder.Decode(&struct{}{}) != io.EOF || summary.SchemaVersion != "pysolate.transparent-campaign-run-summary.v2" {
		return privateSummary{}, errors.New("invalid private summary")
	}
	return summary, nil
}

func computeMetrics(projection *publicProjection, paired map[int]map[workflowbench.CampaignTreatment]projectionRun, repetitions int) error {
	var basePhysical, qualifiedPhysical, baseWall, qualifiedWall, baseCPU, qualifiedCPU []float64
	var physicalReduction, wallReduction, wallPct, cpuReduction []float64
	for repetition := 0; repetition < repetitions; repetition++ {
		pair := paired[repetition]
		base, baseOK := pair[workflowbench.CampaignBaseline]
		qualified, qualifiedOK := pair[workflowbench.CampaignQualified]
		if !baseOK || !qualifiedOK || base.WallMS <= 0 || base.ProcessCPUMS <= 0 {
			return errors.New("incomplete paired campaign repetition")
		}
		basePhysical = append(basePhysical, float64(base.PhysicalExecutions))
		qualifiedPhysical = append(qualifiedPhysical, float64(qualified.PhysicalExecutions))
		baseWall, qualifiedWall = append(baseWall, base.WallMS), append(qualifiedWall, qualified.WallMS)
		baseCPU, qualifiedCPU = append(baseCPU, base.ProcessCPUMS), append(qualifiedCPU, qualified.ProcessCPUMS)
		physicalReduction = append(physicalReduction, float64(base.PhysicalExecutions)-float64(qualified.PhysicalExecutions))
		wallReduction = append(wallReduction, base.WallMS-qualified.WallMS)
		wallPct = append(wallPct, (base.WallMS-qualified.WallMS)/base.WallMS*100)
		cpuReduction = append(cpuReduction, base.ProcessCPUMS-qualified.ProcessCPUMS)
	}
	projection.Baseline = treatmentMetrics{PhysicalExecutions: summarize(basePhysical), WallMS: summarize(baseWall), ProcessCPUMS: summarize(baseCPU)}
	projection.Qualified = treatmentMetrics{PhysicalExecutions: summarize(qualifiedPhysical), WallMS: summarize(qualifiedWall), ProcessCPUMS: summarize(qualifiedCPU)}
	projection.Paired = pairedMetrics{PhysicalReduction: summarize(physicalReduction), WallReductionMS: summarize(wallReduction), WallReductionPct: summarize(wallPct), CPUReductionMS: summarize(cpuReduction)}
	return nil
}

func summarize(values []float64) metricSummary {
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	if len(copyValues) == 0 {
		return metricSummary{}
	}
	middle := len(copyValues) / 2
	median := copyValues[middle]
	if len(copyValues)%2 == 0 {
		median = (copyValues[middle-1] + copyValues[middle]) / 2
	}
	return metricSummary{Median: median, Min: copyValues[0], Max: copyValues[len(copyValues)-1]}
}

func renderSVG(projection publicProjection) string {
	const width, height = 720, 330
	x := func(value uint32) float64 { return 120 + (float64(value)-16)*110 }
	var body strings.Builder
	body.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title desc">`, width, height, width, height))
	body.WriteString(`<title id="title">Paired physical execution counts</title><desc id="desc">Baseline and qualified physical execution counts for each paired repetition.</desc><rect width="720" height="330" fill="white"/>`)
	body.WriteString(`<g font-family="Arial, Helvetica, sans-serif" fill="#172033"><text x="24" y="32" font-size="16" font-weight="700">Physical executions</text><text x="24" y="53" font-size="11" fill="#5b6472">Paired fixed campaign; lower is less physical work</text>`)
	for tick := uint32(16); tick <= 20; tick++ {
		xv := x(tick)
		body.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="70" x2="%.1f" y2="286" stroke="#e1e5ea"/><text x="%.1f" y="310" text-anchor="middle" font-size="11">%d</text>`, xv, xv, xv, tick))
	}
	pairs := make(map[int]map[workflowbench.CampaignTreatment]projectionRun)
	for _, run := range projection.Runs {
		if pairs[run.Repetition] == nil {
			pairs[run.Repetition] = make(map[workflowbench.CampaignTreatment]projectionRun)
		}
		pairs[run.Repetition][run.Treatment] = run
	}
	for repetition := 0; repetition < projection.Source.Repetitions; repetition++ {
		y := 88 + repetition*40
		base := pairs[repetition][workflowbench.CampaignBaseline]
		qualified := pairs[repetition][workflowbench.CampaignQualified]
		body.WriteString(fmt.Sprintf(`<text x="24" y="%d" font-size="11">rep %d</text><line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="#8892a0" stroke-width="2"/><circle cx="%.1f" cy="%d" r="5" fill="white" stroke="#6b7280" stroke-width="2"/><rect x="%.1f" y="%d" width="10" height="10" transform="translate(-5,-5)" fill="#287d8e"/>`, y+4, repetition, x(base.PhysicalExecutions), y, x(qualified.PhysicalExecutions), y, x(base.PhysicalExecutions), y, x(qualified.PhysicalExecutions), y))
	}
	body.WriteString(`<text x="585" y="32" font-size="11" fill="#6b7280">○ baseline</text><text x="585" y="50" font-size="11" fill="#287d8e">■ qualified</text></g></svg>`)
	return strings.ReplaceAll(body.String(), `\"`, `"`) + "\n"
}

func renderFlowSVG(projection publicProjection) string {
	const width = 900
	const left = 72.0
	const right = 24.0
	const top = 72.0
	const rowHeight = 22.0
	height := int(top + float64(len(projection.Programs))*rowHeight + 48)
	maxNS := int64(1)
	for _, event := range projection.WalkthroughEvents {
		if event.AtNS > maxNS {
			maxNS = event.AtNS
		}
	}
	x := func(at int64) float64 { return left + float64(at)/float64(maxNS)*(float64(width)-left-right) }
	var body strings.Builder
	body.WriteString(fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\" role=\"img\" aria-label=\"Qualified campaign arrival-to-terminal flow\"><rect width=\"100%%\" height=\"100%%\" fill=\"white\"/><g font-family=\"Inter,Arial,sans-serif\" fill=\"#17202a\"><text x=\"24\" y=\"28\" font-size=\"17\" font-weight=\"700\">Qualified campaign arrival-to-terminal flow</text><text x=\"24\" y=\"48\" font-size=\"10\" fill=\"#667085\">repetition 0 · linear measured wall time · 20 logical lanes · physical intervals only</text>", width, height, width, height))
	for _, tick := range []float64{0, .5, 1} {
		tickX := left + tick*(float64(width)-left-right)
		body.WriteString(fmt.Sprintf("<line x1=\"%.1f\" y1=\"58\" x2=\"%.1f\" y2=\"%d\" stroke=\"#e4e7ec\"/><text x=\"%.1f\" y=\"66\" text-anchor=\"middle\" font-size=\"8\" fill=\"#667085\">%.1f s</text>", tickX, tickX, height-28, tickX, tick*float64(maxNS)/1e9))
	}
	for index, program := range projection.Programs {
		y := top + float64(index)*rowHeight
		body.WriteString(fmt.Sprintf("<text x=\"24\" y=\"%.1f\" font-size=\"9\" font-weight=\"700\">%s</text><line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#f2f4f7\"/>", y+3, html.EscapeString(program.ID), left, y, float64(width)-right, y))
		events := make([]projectionEvent, 0)
		for _, event := range projection.WalkthroughEvents {
			if event.ProgramID == program.ID {
				events = append(events, event)
			}
		}
		ends := make(map[string]projectionEvent)
		for _, event := range events {
			if event.Type == "physical.ended" {
				ends[event.PhysicalExecutionID] = event
			}
		}
		body.WriteString(fmt.Sprintf("<circle cx=\"%.1f\" cy=\"%.1f\" r=\"2\" fill=\"#d9a514\"/>", x(program.ReleaseOffsetMS*1e6), y))
		for _, event := range events {
			switch {
			case event.Type == "physical.started":
				end, ok := ends[event.PhysicalExecutionID]
				if ok {
					body.WriteString(fmt.Sprintf("<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"7\" rx=\"2\" fill=\"#287d8e\"/>", x(event.AtNS), y-3.5, math.Max(1, x(end.AtNS)-x(event.AtNS))))
				}
			case strings.HasPrefix(event.Type, "workspace.") || strings.HasPrefix(event.Type, "workflow.") || strings.HasPrefix(event.Type, "verification.") || strings.HasPrefix(event.Type, "sharing.") || strings.HasPrefix(event.Type, "authority.") || strings.HasPrefix(event.Type, "delegation."):
				body.WriteString(fmt.Sprintf("<rect x=\"%.1f\" y=\"%.1f\" width=\"2\" height=\"9\" fill=\"#667eea\"/>", x(event.AtNS)-1, y-4.5))
			case event.Type == "logical.terminal":
				stroke := "#168b5b"
				if program.Disposition == "rejected" || program.Disposition == "cancelled" {
					stroke = "#c98900"
				}
				body.WriteString(fmt.Sprintf("<circle cx=\"%.1f\" cy=\"%.1f\" r=\"3\" fill=\"white\" stroke=\"%s\" stroke-width=\"1.5\"/>", x(event.AtNS), y, stroke))
			}
		}
	}
	body.WriteString(fmt.Sprintf("<g transform=\"translate(24 %d)\" font-size=\"8\" fill=\"#667085\"><rect width=\"12\" height=\"6\" rx=\"2\" fill=\"#287d8e\"/><text x=\"18\" y=\"6\">physical Guest/verifier</text><rect x=\"144\" width=\"3\" height=\"8\" fill=\"#667eea\"/><text x=\"154\" y=\"6\">mechanism</text><circle cx=\"238\" cy=\"3\" r=\"3\" fill=\"white\" stroke=\"#168b5b\"/><text x=\"246\" y=\"6\">terminal</text></g></g></svg>", height-18))
	return body.String() + "\n"
}

func renderMarkdown(projection publicProjection) string {
	report := fmt.Sprintf(`# Authority-transparent campaign results

## Conclusion

Across %d paired repetitions of the fixed 20-program campaign, qualified execution used a median of %.0f physical executions versus %.0f for baseline: a paired reduction of %.0f executions (%.1f%%). Median wall time was %.2f s versus %.2f s; this descriptive small-sample result is not a production throughput claim.

![Paired physical execution counts](../figures/authority-transparent-campaign.svg)

| Treatment | Physical executions, median [min, max] | Wall time, median [min, max] | Process CPU, median [min, max] |
|---|---:|---:|---:|
| Baseline | %.0f [%.0f, %.0f] | %.2f s [%.2f, %.2f] | %.2f s [%.2f, %.2f] |
| Qualified | %.0f [%.0f, %.0f] | %.2f s [%.2f, %.2f] | %.2f s [%.2f, %.2f] |

## Provenance

- Campaign source: %s
- Guest artifact: %s
- Guest artifact source: %s
- Manifest: %s
- Host: %s/%s; %s; %s
- Evidence strength: paired repetitions, full min–max shown; no confidence interval inferred.

## Claim boundary

**Valid:** %s

**Invalid inference:** %s
`, projection.Source.Repetitions,
		projection.Qualified.PhysicalExecutions.Median, projection.Baseline.PhysicalExecutions.Median, projection.Paired.PhysicalReduction.Median, projection.Paired.PhysicalReduction.Median/projection.Baseline.PhysicalExecutions.Median*100,
		projection.Qualified.WallMS.Median/1000, projection.Baseline.WallMS.Median/1000,
		projection.Baseline.PhysicalExecutions.Median, projection.Baseline.PhysicalExecutions.Min, projection.Baseline.PhysicalExecutions.Max, projection.Baseline.WallMS.Median/1000, projection.Baseline.WallMS.Min/1000, projection.Baseline.WallMS.Max/1000, projection.Baseline.ProcessCPUMS.Median/1000, projection.Baseline.ProcessCPUMS.Min/1000, projection.Baseline.ProcessCPUMS.Max/1000,
		projection.Qualified.PhysicalExecutions.Median, projection.Qualified.PhysicalExecutions.Min, projection.Qualified.PhysicalExecutions.Max, projection.Qualified.WallMS.Median/1000, projection.Qualified.WallMS.Min/1000, projection.Qualified.WallMS.Max/1000, projection.Qualified.ProcessCPUMS.Median/1000, projection.Qualified.ProcessCPUMS.Min/1000, projection.Qualified.ProcessCPUMS.Max/1000,
		projection.Source.CampaignSourceCommit, projection.Source.ArtifactSHA256, projection.Source.ArtifactSourceCommit, projection.Source.ManifestSHA256,
		html.EscapeString(projection.Source.Host.GOOS), html.EscapeString(projection.Source.Host.GOARCH), html.EscapeString(projection.Source.Host.GoVersion), html.EscapeString(projection.Source.Host.Kernel), projection.ValidClaim, projection.InvalidInference)
	var appendix strings.Builder
	appendix.WriteString("\n## Qualified arrival-to-terminal flow\n\n![Qualified repetition 0 arrival-to-terminal flow](../figures/authority-transparent-campaign-flow.svg)\n\n## Fixed 20-case contract\n\n| ID | Family | Release | Typed operation | Actual admission | Actual terminal |\n|---|---|---:|---|---|---|\n")
	for _, program := range projection.Programs {
		appendix.WriteString(fmt.Sprintf("| %s | %s | +%d ms | %s | %s | %s |\n", program.ID, strings.ReplaceAll(program.Family, "_", " "), program.ReleaseOffsetMS, strings.ReplaceAll(string(program.Execution.Kind), "_", " "), program.Admission, program.Disposition))
	}
	physical := func(programID, eventType string) string {
		for _, event := range projection.WalkthroughEvents {
			if event.ProgramID == programID && event.Type == eventType {
				return event.PhysicalExecutionID
			}
		}
		return "none"
	}
	appendix.WriteString("\n## Observed logical-to-physical identity\n\n| Logical cases | Qualified physical identity | Observed boundary |\n|---|---|---|\n")
	appendix.WriteString(fmt.Sprintf("| P05 / P06 | %s / %s | exact request identity reused one physical Guest result |\n", physical("P05", "sharing.decided"), physical("P06", "sharing.decided")))
	appendix.WriteString(fmt.Sprintf("| P10 / P11 | %s / %s | exact sealed-root verifier identity reused one verifier execution |\n", physical("P10", "verification.completed"), physical("P11", "verification.completed")))
	appendix.WriteString("| P18 / P19 / P20 | none | rejected or cancelled before physical execution |\n")
	return report + appendix.String()
}

func mustJSON(value any) []byte {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(encoded, '\n')
}

func writeFile(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, value, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func fileSHA256(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}
