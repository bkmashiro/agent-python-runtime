package mechanismcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

type MatchedControlConfig struct {
	ArtifactPath      string
	Fixture           agenttrajectory.Fixture
	WorkspaceRoot     string
	GenerationStep    time.Duration
	FinalizationDelay time.Duration
	Pairs             int
	CandidateSources  map[string]string
}

type MatchedPair struct {
	PairIndex          int    `json:"pair_index"`
	FirstLane          string `json:"first_lane"`
	BaselineLatencyNS  uint64 `json:"baseline_latency_ns"`
	OptimizedLatencyNS uint64 `json:"optimized_latency_ns"`
	ResultSHA256       string `json:"result_sha256"`
}

type MatchedControlResult struct {
	Pairs             []MatchedPair `json:"pairs"`
	BaselineMedianNS  uint64        `json:"baseline_median_ns"`
	OptimizedMedianNS uint64        `json:"optimized_median_ns"`
	SavingsNS         int64         `json:"savings_ns"`
}

func RunMatchedControls(ctx context.Context, config MatchedControlConfig) (MatchedControlResult, error) {
	if ctx == nil || config.ArtifactPath == "" || config.WorkspaceRoot == "" || config.Pairs < 1 || config.Pairs > 10 ||
		config.GenerationStep <= 0 || config.FinalizationDelay <= 0 {
		return MatchedControlResult{}, errors.New("invalid matched control config")
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return MatchedControlResult{}, err
	}
	result := MatchedControlResult{Pairs: make([]MatchedPair, 0, config.Pairs)}
	for pairIndex := 0; pairIndex < config.Pairs; pairIndex++ {
		first := "baseline"
		if pairIndex%2 == 1 {
			first = "optimized"
		}
		var baseline BaselineStageResult
		var optimized CandidateStageResult
		var err error
		for _, lane := range []string{first, oppositeLane(first)} {
			root := filepath.Join(config.WorkspaceRoot, fmt.Sprintf("pair-%02d-%s", pairIndex+1, lane))
			stageConfig := CandidateStageConfig{
				ArtifactPath: config.ArtifactPath, Fixture: config.Fixture, WorkspaceRoot: root,
				GenerationStep: config.GenerationStep, FinalizationDelay: config.FinalizationDelay,
				CandidateSources: config.CandidateSources,
			}
			if lane == "baseline" {
				baseline, err = RunBaselineCandidateStage(ctx, stageConfig)
			} else {
				optimized, err = RunCandidateStage(ctx, stageConfig)
			}
			if err != nil {
				return MatchedControlResult{}, err
			}
		}
		baselineSHA, err := candidateOutputsSHA256(baseline.Candidates)
		if err != nil {
			return MatchedControlResult{}, err
		}
		optimizedSHA, err := candidateOutputsSHA256(optimized.Candidates)
		if err != nil || baselineSHA != optimizedSHA {
			return MatchedControlResult{}, errors.New("matched control result mismatch")
		}
		result.Pairs = append(result.Pairs, MatchedPair{
			PairIndex: pairIndex + 1, FirstLane: first, BaselineLatencyNS: baseline.LatencyNS,
			OptimizedLatencyNS: optimized.LatencyNS, ResultSHA256: baselineSHA,
		})
	}
	baselineValues := make([]uint64, len(result.Pairs))
	optimizedValues := make([]uint64, len(result.Pairs))
	for index, pair := range result.Pairs {
		baselineValues[index] = pair.BaselineLatencyNS
		optimizedValues[index] = pair.OptimizedLatencyNS
	}
	result.BaselineMedianNS = medianUint64(baselineValues)
	result.OptimizedMedianNS = medianUint64(optimizedValues)
	result.SavingsNS = int64(result.BaselineMedianNS) - int64(result.OptimizedMedianNS)
	return result, nil
}

func candidateOutputsSHA256(outputs map[string]CandidateStageOutput) (string, error) {
	projection := make(map[string]json.RawMessage, len(outputs))
	for _, candidateID := range []string{"brighton", "oxford"} {
		output, ok := outputs[candidateID]
		if !ok || len(output.Response) == 0 {
			return "", errors.New("candidate output is missing")
		}
		var envelope struct {
			Status string          `json:"status"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(output.Response, &envelope) != nil || envelope.Status != "ok" {
			return "", errors.New("candidate output is invalid")
		}
		projection[candidateID] = envelope.Result
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func oppositeLane(value string) string {
	if value == "baseline" {
		return "optimized"
	}
	return "baseline"
}

func medianUint64(values []uint64) uint64 {
	copyValues := append([]uint64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[middle]
	}
	return (copyValues[middle-1] + copyValues[middle]) / 2
}
