package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var outputRoot, artifactPath, sourceCommit, fixtureRoot, replayPath string
	flag.StringVar(&outputRoot, "output", "", "new private output directory")
	flag.StringVar(&artifactPath, "artifact", "", "pinned Pysolate Guest WASM")
	flag.StringVar(&sourceCommit, "source-commit", "", "clean implementation commit")
	flag.StringVar(&fixtureRoot, "fixture", "research/agenttrajectory/testdata/day-trip-planning", "public fixture root")
	flag.StringVar(&replayPath, "replay-prefix", "", "private 0600 JSON array of exact recorded ModelResult values")
	flag.Parse()
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if outputRoot == "" || artifactPath == "" || len(sourceCommit) != 40 || apiKey == "" {
		return errors.New("output, artifact, 40-character source-commit, and DEEPSEEK_API_KEY are required")
	}
	fixture, err := agenttrajectory.LoadFixture(fixtureRoot)
	if err != nil {
		return err
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	recorder, err := agenttrajectory.NewPrivateRecorder(outputRoot, sourceCommit)
	if err != nil {
		return err
	}
	defer recorder.Close()
	executor, err := agenttrajectory.NewPysolateCandidateExecutor(agenttrajectory.PysolateExecutorConfig{
		Artifact: artifact, Fixture: fixture, WorkspaceRoot: filepath.Join(outputRoot, "workspaces"),
	})
	if err != nil {
		return err
	}
	defer executor.Close(context.Background())
	liveMaxCalls := uint32(4)
	var replay []agenttrajectory.ModelResult
	if replayPath != "" {
		replay, err = loadReplayPrefix(replayPath)
		if err != nil || len(replay) >= 4 {
			return errors.New("invalid private replay prefix")
		}
		liveMaxCalls -= uint32(len(replay))
	}
	liveProvider, err := agenttrajectory.NewDeepSeekProvider(agenttrajectory.DeepSeekConfig{
		BaseURL: "https://api.deepseek.com", APIKey: apiKey, Model: "deepseek-v4-flash", MaxCalls: liveMaxCalls, Timeout: 90 * time.Second, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		return err
	}
	var provider agenttrajectory.ModelProvider = liveProvider
	if len(replay) > 0 {
		provider, err = agenttrajectory.NewReplayThenProvider(replay, liveProvider)
		if err != nil {
			return err
		}
	}
	harness, err := agenttrajectory.NewHarness(agenttrajectory.HarnessConfig{Fixture: fixture, Provider: provider, Recorder: recorder, Executor: executor})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := harness.Run(ctx)
	if err != nil {
		return fmt.Errorf("day-trip trajectory failed after %d model calls: %w", provider.CallCount(), err)
	}
	if provider.CallCount() != 4 {
		return fmt.Errorf("day-trip trajectory used %d model calls", provider.CallCount())
	}
	export, err := recorder.Complete(ctx)
	if err != nil {
		return err
	}
	if err := writePrivateJSON(filepath.Join(outputRoot, "harness-result.json"), result); err != nil {
		return err
	}
	if err := writePrivateJSON(filepath.Join(outputRoot, "experiment-full.json"), export); err != nil {
		return err
	}
	fmt.Printf("model_calls=%d selected=%s total_gbp=%.2f evidence_events=%d\n", provider.CallCount(), result.Final.SelectedCandidateID, result.Final.TotalCostGBP, len(export.Events))
	return nil
}

func loadReplayPrefix(path string) ([]agenttrajectory.ModelResult, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("replay path must be absolute and canonical")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("replay file must be private and bounded")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var replay []agenttrajectory.ModelResult
	if json.Unmarshal(body, &replay) != nil || len(replay) == 0 {
		return nil, errors.New("invalid replay file")
	}
	return replay, nil
}

func writePrivateJSON(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(body, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
