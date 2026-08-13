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
	conformancePath := flag.String("conformance", "", "canonical shared conformance evidence")
	outputPath := flag.String("output", "", "body-free output report")
	flag.Parse()
	if *corpusPath == "" || *corePath == "" || *conformancePath == "" || *outputPath == "" {
		fatal("all paths are required")
	}
	corpusRaw := mustRead(*corpusPath)
	corpus, _, err := composableacceptance.DecodeCorpus(corpusRaw)
	if err != nil {
		fatal(err.Error())
	}
	coreRaw := mustRead(*corePath)
	var core composableacceptance.Report
	if err := decodeReport(coreRaw, &core); err != nil {
		fatal(err.Error())
	}
	conformanceRaw := mustRead(*conformancePath)
	conformance, _, err := composableacceptance.DecodeConformance(conformanceRaw)
	if err != nil {
		fatal(err.Error())
	}
	report, err := composableacceptance.CompleteReport(corpus, core, conformance)
	if err != nil {
		fatal(err.Error())
	}
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
