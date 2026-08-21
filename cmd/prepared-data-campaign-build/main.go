package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	campaign "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetcampaign"
	fanout "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetfanout"
)

func main() {
	manifestPath := flag.String("manifest", "", "campaign manifest JSON")
	fanoutPaths := flag.String("fanout", "", "comma-separated fanout reports")
	eagerPaths := flag.String("eager", "", "comma-separated eager reports")
	flag.Parse()
	var manifest campaign.Manifest
	if err := decode(*manifestPath, &manifest); err != nil {
		fail(err)
	}
	fanouts, eagers := split(*fanoutPaths), split(*eagerPaths)
	if len(fanouts) != manifest.Trials || len(eagers) != manifest.Trials {
		fail(campaign.ErrInvalidCampaign)
	}
	trials := make([]campaign.Trial, manifest.Trials)
	for i := range trials {
		var f fanout.Report
		var e campaign.EagerReport
		if decode(fanouts[i], &f) != nil || decode(eagers[i], &e) != nil {
			fail(campaign.ErrInvalidCampaign)
		}
		trials[i] = campaign.Trial{ID: i + 1, Fanout: f, Eager: e}
	}
	report, err := campaign.Build(manifest, trials)
	if err != nil {
		fail(err)
	}
	encoded, _ := json.Marshal(report)
	fmt.Println(string(encoded))
}
func decode(path string, target any) error {
	if path == "" {
		return campaign.ErrInvalidCampaign
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return campaign.ErrInvalidCampaign
	}
	return nil
}
func split(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
