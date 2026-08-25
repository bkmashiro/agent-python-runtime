package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/releasereadiness"
)

func main() {
	artifact := flag.String("artifact", "", "absolute path to the verified Guest artifact")
	workload := flag.String("workload", "research/releasereadiness/testdata/recorded-run-1.json", "recorded workload projection")
	workspaceRoot := flag.String("workspace-root", "", "absolute private workspace root")
	output := flag.String("output", "", "absolute JSON output path")
	pairs := flag.Int("pairs", 3, "number of alternating matched pairs")
	scheduleScale := flag.Float64("schedule-scale", 1, "recorded source schedule multiplier")
	providerScale := flag.Float64("provider-scale", 1, "recorded provider latency multiplier")
	timeout := flag.Duration("lane-timeout", 90*time.Second, "timeout for each lane")
	flag.Parse()
	if *artifact == "" || *workspaceRoot == "" || *output == "" || !filepath.IsAbs(*artifact) || !filepath.IsAbs(*workspaceRoot) || !filepath.IsAbs(*output) {
		fmt.Fprintln(os.Stderr, "artifact, workspace-root and output must be absolute paths")
		os.Exit(2)
	}
	absoluteWorkload, err := filepath.Abs(*workload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result, err := releasereadiness.RunCampaign(context.Background(), releasereadiness.CampaignConfig{
		ArtifactPath: *artifact, WorkloadPath: absoluteWorkload, WorkspaceRoot: *workspaceRoot,
		Pairs: *pairs, ScheduleScale: *scheduleScale, ProviderScale: *providerScale, Timeout: *timeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(*output), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, body, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("evidence=%s\n", *output)
	fmt.Printf("pairs=%d baseline_median_ns=%d optimized_median_ns=%d paired_saving_ns=%d reportable=%t\n",
		len(result.Pairs), result.Summary.BaselineMedianNS, result.Summary.OptimizedMedianNS, result.Summary.MedianPairedSavingNS, result.Reportable)
}
