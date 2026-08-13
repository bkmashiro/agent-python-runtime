package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		if err := writeLabProjection(*labDir, report, identity); err != nil {
			fatal(err.Error())
		}
	}
	fmt.Println(identity)
}

func writeLabProjection(root string, report composableacceptance.Report, reportSHA string) error {
	projection, err := labview.ProjectComposableAcceptance(report, reportSHA)
	if err != nil {
		return err
	}
	runsDir := filepath.Join(root, "runs")
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		return err
	}
	studyRaw, studySHA, err := labview.Encode(labview.KindStudySummary, projection.Study)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "study-summary.json"), studyRaw, 0o600); err != nil {
		return err
	}
	manifest := []string{"study-summary.json " + studySHA}
	type webDataset struct {
		SchemaVersion string               `json:"schema_version"`
		ReportSHA256  string               `json:"report_sha256"`
		SourceCommit  string               `json:"source_commit"`
		CorpusSHA256  string               `json:"corpus_sha256"`
		Model         string               `json:"model"`
		Study         labview.StudySummary `json:"study"`
		Runs          []labview.RunDetail  `json:"runs"`
	}
	web := webDataset{
		SchemaVersion: "pysolate.lab-web-experiments.v1", ReportSHA256: reportSHA,
		SourceCommit: report.SourceCommit, CorpusSHA256: report.CorpusSHA256, Model: report.Model,
		Study: projection.Study, Runs: projection.Runs,
	}
	webRaw, err := json.Marshal(web)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "experiments.json"), webRaw, 0o600); err != nil {
		return err
	}
	manifest = append(manifest, "experiments.json "+composableacceptance.ArtifactIdentity(string(webRaw)))
	for _, run := range projection.Runs {
		raw, identity, err := labview.Encode(labview.KindRunDetail, run)
		if err != nil {
			return err
		}
		name := run.RunID + ".json"
		if err := os.WriteFile(filepath.Join(runsDir, name), raw, 0o600); err != nil {
			return err
		}
		manifest = append(manifest, "runs/"+name+" "+identity)
	}
	sort.Strings(manifest)
	return os.WriteFile(filepath.Join(root, "manifest.txt"), []byte(strings.Join(manifest, "\n")+"\n"), 0o600)
}

func decodeReport(raw []byte, destination *composableacceptance.Report) error {
	// EncodeReport is the canonicality oracle after strict JSON decoding through
	// a temporary wrapper supplied by this package.
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
