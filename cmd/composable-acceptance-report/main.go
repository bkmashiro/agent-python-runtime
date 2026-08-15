package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
)

func main() {
	corpusPath := flag.String("corpus", "", "canonical private Spark corpus")
	corePath := flag.String("core", "", "canonical direct-replay report")
	outputPath := flag.String("output", "", "validated body-free direct-replay report output")
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
	fmt.Println(identity)
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
