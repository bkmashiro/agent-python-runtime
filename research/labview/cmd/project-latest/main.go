package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func read(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("latest Lab input path is required")
	}
	return os.ReadFile(path)
}

func run(contractPath, overlapPath, censusPath, campaignPath, outputPath string) error {
	contract, err := read(contractPath)
	if err != nil {
		return err
	}
	overlap, err := read(overlapPath)
	if err != nil {
		return err
	}
	census, err := read(censusPath)
	if err != nil {
		return err
	}
	campaign, err := read(campaignPath)
	if err != nil {
		return err
	}
	snapshot, err := labview.BuildLatestSnapshot(labview.LatestInputs{
		SourcePrefixContract: contract,
		SourcePrefixEvidence: overlap,
		SourcePrefixCensus: census,
		CampaignProjection: campaign,
	})
	if err != nil {
		return err
	}
	encoded, err := labview.EncodeLatestSnapshot(snapshot)
	if err != nil {
		return err
	}
	if outputPath == "" {
		return fmt.Errorf("latest Lab output path is required")
	}
	return os.WriteFile(outputPath, encoded, 0o644)
}

func main() {
	contract := flag.String("source-prefix-contract", "", "source-prefix contract JSON")
	overlap := flag.String("source-prefix-evidence", "", "source-prefix evidence JSON")
	census := flag.String("source-prefix-census", "", "source-prefix census JSON")
	campaign := flag.String("campaign-projection", "", "transparent campaign public projection JSON")
	output := flag.String("output", "", "latest Lab snapshot output JSON")
	flag.Parse()
	if err := run(*contract, *overlap, *census, *campaign, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
