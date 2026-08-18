package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

type campaign struct {
	SchemaVersion string                                   `json:"schema_version"`
	ObservedAt    string                                   `json:"observed_at"`
	Platform      string                                   `json:"platform"`
	Endpoint      string                                   `json:"endpoint"`
	FixtureSHA256 string                                   `json:"fixture_sha256"`
	Method        string                                   `json:"method"`
	Candidates    []agenttrajectory.ObservedStreamEvidence `json:"candidates"`
	Aggregate     opportunity                              `json:"simultaneous_replay_opportunity"`
}

type opportunity struct {
	NativeSequentialReadyNS  uint64 `json:"native_sequential_ready_ns"`
	NativeParallelReadyNS    uint64 `json:"native_parallel_ready_ns"`
	PrefixPreDispatchReadyNS uint64 `json:"prefix_pre_dispatch_ready_ns"`
	SavingsVsSequentialNS    uint64 `json:"savings_vs_sequential_ns"`
	SavingsVsParallelNS      uint64 `json:"savings_vs_parallel_ns"`
}

type summary struct {
	RunCount                       int    `json:"run_count"`
	CandidateObservationCount      int    `json:"candidate_observation_count"`
	ExcludedAttemptCount           int    `json:"excluded_attempt_count"`
	MedianFirstResponseByteNS      uint64 `json:"median_first_response_byte_ns"`
	MedianFirstReasoningNS         uint64 `json:"median_first_reasoning_ns"`
	MedianFirstContentNS           uint64 `json:"median_first_content_ns"`
	MedianSourceCompleteNS         uint64 `json:"median_source_complete_ns"`
	MedianEligibleWindowNS         uint64 `json:"median_eligible_window_ns"`
	MedianRunSavingsVsSequentialNS uint64 `json:"median_run_savings_vs_sequential_ns"`
	MedianRunSavingsVsParallelNS   uint64 `json:"median_run_savings_vs_parallel_ns"`
	MinRunSavingsVsParallelNS      uint64 `json:"min_run_savings_vs_parallel_ns"`
	MaxRunSavingsVsParallelNS      uint64 `json:"max_run_savings_vs_parallel_ns"`
}

type sample struct {
	SchemaVersion string     `json:"schema_version"`
	Scope         string     `json:"scope"`
	Runs          []campaign `json:"runs"`
	Summary       summary    `json:"summary"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var inputCSV, output string
	var excluded int
	flag.StringVar(&inputCSV, "inputs", "", "comma-separated projected campaign JSON files")
	flag.StringVar(&output, "output", "", "new output JSON")
	flag.IntVar(&excluded, "excluded-attempts", 0, "live attempts excluded before valid projection")
	flag.Parse()
	paths := strings.Split(inputCSV, ",")
	if len(paths) < 1 || output == "" || excluded < 0 {
		return errors.New("inputs and output are required")
	}
	runs := make([]campaign, 0, len(paths))
	var firstByte, firstReasoning, firstContent, sourceComplete, windows, sequentialSavings, parallelSavings []uint64
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var run campaign
		if json.Unmarshal(body, &run) != nil || run.SchemaVersion != "pysolate.observed-stream-campaign.v1" || len(run.Candidates) != 2 {
			return errors.New("invalid projected campaign")
		}
		runs = append(runs, run)
		for _, candidate := range run.Candidates {
			firstByte = append(firstByte, candidate.Network.FirstResponseByteNS)
			firstReasoning = append(firstReasoning, candidate.FirstReasoningNS)
			firstContent = append(firstContent, candidate.FirstContentNS)
			sourceComplete = append(sourceComplete, candidate.SourceCompleteNS)
			windows = append(windows, candidate.EligibleWindowNS)
		}
		sequentialSavings = append(sequentialSavings, run.Aggregate.SavingsVsSequentialNS)
		parallelSavings = append(parallelSavings, run.Aggregate.SavingsVsParallelNS)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ObservedAt < runs[j].ObservedAt })
	result := sample{SchemaVersion: "pysolate.observed-stream-sample.v1", Scope: "Live DeepSeek stream arrivals; deterministic travel-call latencies; simultaneous counterfactual replay; no runtime mechanism overhead.", Runs: runs,
		Summary: summary{RunCount: len(runs), CandidateObservationCount: len(firstContent), ExcludedAttemptCount: excluded,
			MedianFirstResponseByteNS: median(firstByte), MedianFirstReasoningNS: median(firstReasoning), MedianFirstContentNS: median(firstContent),
			MedianSourceCompleteNS: median(sourceComplete), MedianEligibleWindowNS: median(windows), MedianRunSavingsVsSequentialNS: median(sequentialSavings),
			MedianRunSavingsVsParallelNS: median(parallelSavings), MinRunSavingsVsParallelNS: minimum(parallelSavings), MaxRunSavingsVsParallelNS: maximum(parallelSavings)}}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(body, '\n'), 0o644)
}

func median(values []uint64) uint64 {
	copyValues := append([]uint64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[middle]
	}
	return (copyValues[middle-1] + copyValues[middle]) / 2
}
func minimum(values []uint64) uint64 {
	value := values[0]
	for _, item := range values[1:] {
		if item < value {
			value = item
		}
	}
	return value
}
func maximum(values []uint64) uint64 {
	value := values[0]
	for _, item := range values[1:] {
		if item > value {
			value = item
		}
	}
	return value
}
