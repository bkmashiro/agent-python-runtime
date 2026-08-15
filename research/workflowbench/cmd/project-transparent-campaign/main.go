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
	ID              string                                  `json:"id"`
	Family          string                                  `json:"family"`
	ReleaseOffsetMS int64                                   `json:"release_offset_ms"`
	Execution       workflowbench.CampaignExecutionContract `json:"execution"`
	Admission       string                                  `json:"admission"`
	Disposition     string                                  `json:"disposition"`
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
	svgOutput := flag.String("svg-output", "", "paper SVG path")
	markdownOutput := flag.String("markdown-output", "", "canonical Markdown report path")
	flag.Parse()
	if err := run(*evidenceRoot, *expectedArtifact, *expectedCampaign, *jsonOutput, *svgOutput, *markdownOutput); err != nil {
		log.Fatal(err)
	}
}

func run(root, expectedArtifact, expectedCampaign, jsonOutput, svgOutput, markdownOutput string) error {
	if root == "" || expectedArtifact == "" || expectedCampaign == "" || jsonOutput == "" || svgOutput == "" || markdownOutput == "" {
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
	projection := publicProjection{
		SchemaVersion:    projectionSchema,
		Source:           projectionSource{ArtifactSHA256: summary.ArtifactSHA256, ArtifactSourceCommit: summary.ArtifactSourceCommit, CampaignSourceCommit: summary.CampaignSourceCommit, ManifestSHA256: summary.ManifestSHA256, Host: summary.Host, Repetitions: summary.Repetitions},
		ValidClaim:       "For this fixed 20-program campaign on one recorded host, exact qualified sharing reduced physical executions while preserving every registered oracle and authority rejection.",
		InvalidInference: "Do not generalize these five paired repetitions to arbitrary workloads, hosts, schedulers, or steady-state production throughput.",
	}
	byRepetition := make(map[int]map[workflowbench.CampaignTreatment]projectionRun)
	var walkthroughSet bool
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
		if !walkthroughSet && run.Treatment == workflowbench.CampaignQualified {
			for _, event := range evidence.Events {
				projection.WalkthroughEvents = append(projection.WalkthroughEvents, projectionEvent(event))
			}
			walkthroughSet = true
		}
	}
	for _, program := range manifest.Programs {
		projection.Programs = append(projection.Programs, projectionProgram{ID: program.ID, Family: program.Family, ReleaseOffsetMS: program.ReleaseOffsetMS, Execution: program.Execution, Admission: program.Expected.Admission, Disposition: program.Expected.Disposition})
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
		physicalReduction = append(physicalReduction, float64(base.PhysicalExecutions-qualified.PhysicalExecutions))
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

func renderMarkdown(projection publicProjection) string {
	return fmt.Sprintf(`# Authority-transparent campaign results

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
