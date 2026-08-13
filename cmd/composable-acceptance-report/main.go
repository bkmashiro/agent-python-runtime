package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func main() {
	corpusPath := flag.String("corpus", "", "canonical private Spark corpus")
	corePath := flag.String("core", "", "canonical direct-replay report")
	outputPath := flag.String("output", "", "validated body-free direct-replay report output")
	labDir := flag.String("lab-dir", "", "optional body-free Lab projection directory")
	flag.Parse()
	if *corpusPath == "" || *corePath == "" || *outputPath == "" {
		fatal("corpus, core, and output paths are required")
	}
	corpusRaw := mustRead(*corpusPath)
	corpus, corpusSHA, err := composableacceptance.DecodeCorpus(corpusRaw)
	if err != nil {
		fatal(err.Error())
	}
	coreRaw := mustRead(*corePath)
	var core composableacceptance.Report
	if err := decodeReport(coreRaw, &core); err != nil {
		fatal(err.Error())
	}
	if core.CorpusSHA256 != corpusSHA || core.Model != corpus.Model {
		fatal("report does not match corpus identity")
	}
	report := core
	encoded, identity, err := composableacceptance.EncodeReport(report)
	if err != nil {
		fatal(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o700); err != nil {
		fatal(err.Error())
	}
	if err := os.WriteFile(*outputPath, encoded, 0o600); err != nil {
		fatal(err.Error())
	}
	if *labDir != "" {
		if err := writeLabProjection(*labDir, corpus, report, identity); err != nil {
			fatal(err.Error())
		}
	}
	fmt.Println(identity)
}

type webRef struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type webChildProgram struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	Source         string `json:"source"`
	ExpectedResult string `json:"expected_result"`
	OutputPath     string `json:"output_path"`
}

type webScenario struct {
	ID                        string            `json:"id"`
	GuestSource               string            `json:"guest_source"`
	ChildPrograms             []webChildProgram `json:"child_programs"`
	FileCount                 uint32            `json:"file_count"`
	ChildAnalysisCount        uint32            `json:"child_analysis_count"`
	SelectedChild             int               `json:"selected_child"`
	HasRepeatedTransformation bool              `json:"has_repeated_transformation"`
	HasWaitBoundary           bool              `json:"has_wait_boundary"`
	HasObservation            bool              `json:"has_observation"`
}

type webMetrics struct {
	GuestCreated          uint64  `json:"guest_created"`
	GuestDestroyed        uint64  `json:"guest_destroyed"`
	CacheHits             uint64  `json:"cache_hits"`
	FlightFollowers       uint64  `json:"flight_followers"`
	ChangedBytes          uint64  `json:"changed_bytes"`
	MaterializedBytes     uint64  `json:"materialized_bytes"`
	RelativeElapsedMillis float64 `json:"relative_elapsed_millis"`
}

type webRun struct {
	RunID               string                            `json:"run_id"`
	WorkloadID          string                            `json:"workload_id"`
	Treatment           string                            `json:"treatment"`
	RecordedStatus      string                            `json:"recorded_status"`
	TerminalDisposition string                            `json:"terminal_disposition"`
	Refs                []webRef                          `json:"refs"`
	Metrics             webMetrics                        `json:"metrics"`
	Scenario            webScenario                       `json:"scenario"`
	Trace               []composableacceptance.TraceEvent `json:"trace"`
}

type webDataset struct {
	SchemaVersion string   `json:"schema_version"`
	ReportSHA256  string   `json:"report_sha256"`
	SourceCommit  string   `json:"source_commit"`
	CorpusSHA256  string   `json:"corpus_sha256"`
	Model         string   `json:"model"`
	Runs          []webRun `json:"runs"`
}

func writeLabProjection(root string, corpus composableacceptance.Corpus, report composableacceptance.Report, reportSHA string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	projection, err := labview.ProjectComposableAcceptance(report, reportSHA)
	if err != nil {
		return err
	}
	scenariosByID := make(map[string]composableacceptance.Scenario, len(corpus.Scenarios))
	for _, scenario := range corpus.Scenarios {
		scenariosByID[scenario.ID] = scenario
	}

	rowsByKey := make(map[string]composableacceptance.Row, len(report.Rows))
	for _, row := range report.Rows {
		key := rowKey(row.ScenarioID, string(row.Treatment))
		if _, found := rowsByKey[key]; found {
			return errors.New("report rows are not unique by workload + treatment")
		}
		rowsByKey[key] = row
	}

	runsByKey := make(map[string]struct{}, len(projection.Runs))
	web := webDataset{
		SchemaVersion: "pysolate.lab-web-debugger.v4",
		ReportSHA256:  reportSHA, SourceCommit: report.SourceCommit,
		CorpusSHA256: report.CorpusSHA256, Model: report.Model,
		Runs: make([]webRun, 0, len(projection.Runs)),
	}
	for _, run := range projection.Runs {
		key := rowKey(run.WorkloadID, run.Treatment)
		if _, found := runsByKey[key]; found {
			return errors.New("runs are not unique by workload + treatment")
		}
		runsByKey[key] = struct{}{}

		row, found := rowsByKey[key]
		if !found {
			return fmt.Errorf("missing core row for run %s", key)
		}
		scenario, found := scenariosByID[row.ScenarioID]
		if !found {
			return errors.New("run workload has no matching scenario")
		}
		refs := make([]webRef, 0, len(run.Refs))
		for _, ref := range run.Refs {
			refs = append(refs, webRef{Kind: ref.Kind, SHA256: ref.SHA256})
		}
		trace := make([]composableacceptance.TraceEvent, len(row.Trace))
		copy(trace, row.Trace)
		web.Runs = append(web.Runs, webRun{
			RunID:               run.RunID,
			WorkloadID:          row.ScenarioID,
			Treatment:           string(run.Treatment),
			RecordedStatus:      row.Status,
			TerminalDisposition: row.TerminalDisposition,
			Refs:                refs,
			Metrics: webMetrics{
				GuestCreated:          row.GuestCreated,
				GuestDestroyed:        row.GuestDestroyed,
				CacheHits:             row.CacheHits,
				FlightFollowers:       row.FlightFollowers,
				ChangedBytes:          row.ChangedBytes,
				MaterializedBytes:     row.MaterializedBytes,
				RelativeElapsedMillis: row.RelativeElapsedMillis,
			},
			Scenario: projectWebScenario(scenario),
			Trace:    trace,
		})
	}
	if len(runsByKey) != len(rowsByKey) {
		return errors.New("dataset projection and report rows are not aligned")
	}

	webRaw, err := json.Marshal(web)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "debugger.json"), webRaw, 0o600); err != nil {
		return err
	}
	manifest := "debugger.json " + composableacceptance.ArtifactIdentity(string(webRaw)) + "\n"
	return os.WriteFile(filepath.Join(root, "manifest.txt"), []byte(manifest), 0o600)
}

func projectWebScenario(scenario composableacceptance.Scenario) webScenario {
	children := make([]webChildProgram, 0, len(scenario.ChildPrograms))
	for _, child := range scenario.ChildPrograms {
		children = append(children, webChildProgram{ID: child.ID, Role: child.Role, Source: child.Source, ExpectedResult: child.ExpectedResult, OutputPath: child.OutputPath})
	}
	return webScenario{
		ID:                        scenario.ID,
		GuestSource:               scenario.GuestSource,
		ChildPrograms:             children,
		FileCount:                 uint32(len(scenario.Files)),
		ChildAnalysisCount:        uint32(len(scenario.ChildAnalyses)),
		SelectedChild:             scenario.SelectedChild,
		HasRepeatedTransformation: scenario.RepeatedTransformation != "",
		HasWaitBoundary:           scenario.WaitBoundary != "",
		HasObservation:            scenario.Observation != "",
	}
}

func rowKey(scenarioID string, treatment string) string {
	return scenarioID + "/" + treatment
}

func decodeReport(raw []byte, destination *composableacceptance.Report) error {
	if err := composableacceptance.DecodeReport(raw, destination); err != nil {
		return err
	}
	return nil
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err.Error())
	}
	return data
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
