package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/tetratelabs/wazero"
)

func replayPrivateCampaign(ctx context.Context, recordingPath, outputPath string, wasm []byte, selected []taskCase, maxTurns, maxRequests int) error {
	recording, err := readPrivateRecording(recordingPath)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(recording.Header.Limits, recordingLimits()) {
		return errors.New("recorded limits are unsupported by this replay version")
	}
	if recording.Header.GuestSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(wasm)) {
		return errors.New("private recording Guest identity mismatch")
	}
	if recording.Header.MaxTurns < 1 || recording.Header.MaxTurns > 96 || recording.Header.MaxRequests < 1 || recording.Header.MaxRequests > 96 {
		return errors.New("unsupported replay limits")
	}
	if len(recording.Episodes) == 0 {
		return errors.New("recording has no episodes")
	}
	maxTurns, maxRequests = recording.Header.MaxTurns, recording.Header.MaxRequests
	tasks := map[string]taskCase{}
	if len(selected) == 0 {
		for _, task := range allTasks() {
			selected = append(selected, task)
		}
	}
	for _, task := range selected {
		tasks[task.ID] = task
	}
	for _, ep := range recording.Episodes {
		task, ok := tasks[ep.Task]
		if !ok {
			return errors.New("recording contains an unsupported task")
		}
		if err := validateRecordedEpisode(ep, recording.Header, task); err != nil {
			return err
		}
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	encoder := json.NewEncoder(output)
	budget := &requestBudget{limit: maxRequests}
	cache := wazero.NewCompilationCache()
	defer cache.Close(context.Background())
	ctx = pysolate.WithCompilationCache(ctx, cache)
	for _, recorded := range recording.Episodes {
		if recorded.Row.CompletionStatus == "skipped" {
			row := recorded.Row
			row.Replayed = true
			if err := encoder.Encode(row); err != nil {
				return err
			}
			continue
		}
		task := offlineTask(tasks[recorded.Task])
		host := &episodeHost{}
		var runner *pysolate.Runner
		if recorded.Arm == "code" {
			runner, err = pysolate.NewPreparedRecorded(ctx, wasm, task.NewEpisode().manifest(host), recorded.Seed)
			if err != nil {
				return err
			}
		}
		provider := &playbackProvider{episode: &recorded, model: recording.Header.RequestedModel}
		rowCtx, cancel := context.WithTimeout(ctx, episodeTimeout)
		row := runEpisode(rowCtx, provider, budget, task, recorded.Repeat, recorded.Sequence, recorded.Arm, runner, 0, host, recording.Header.RequestedModel, maxTurns, recorded.ReserveRequests)
		cancel()
		if runner != nil {
			if err := runner.Close(context.Background()); err != nil {
				return err
			}
		}
		if provider.failure != nil {
			return provider.failure
		}
		if row.CompletionStatus == "replay_mismatch" {
			return errors.New("offline execution replay mismatch")
		}
		if provider.index != len(recorded.ProviderCalls) {
			return errors.New("offline provider replay left unconsumed calls")
		}
		if err := comparePrivateEpisodeRows(recorded.Row, row); err != nil {
			return fmt.Errorf("episode %d: %w", recorded.Sequence, err)
		}
		row.Replayed = true
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

// Even an accidentally reached live dispatcher has no callback to invoke.
func offlineTask(task taskCase) taskCase {
	original := task.NewEpisode
	task.NewEpisode = func() *taskEpisode {
		ep := original()
		for i := range ep.Domains {
			ep.Domains[i].Call = func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("offline live dispatch forbidden")
			}
		}
		return ep
	}
	return task
}

func comparePrivateEpisodeRows(want, got episodeRow) error {
	leftAnswer, leftErr := json.Marshal(want.Answer)
	rightAnswer, rightErr := json.Marshal(got.Answer)
	if leftErr != nil || rightErr != nil {
		return errors.New("invalid recorded answer")
	}
	want.Answer, got.Answer = leftAnswer, rightAnswer
	want.TotalNS, got.TotalNS = 0, 0
	want.ModelRoundtripNS, got.ModelRoundtripNS = 0, 0
	want.PysolateNS, got.PysolateNS = 0, 0
	want.DomainCallbackNS, got.DomainCallbackNS = 0, 0
	want.SetupNS, got.SetupNS = 0, 0
	want.Replayed, got.Replayed = false, false
	if !reflect.DeepEqual(want, got) {
		fields := []string{}
		left, right := reflect.ValueOf(want), reflect.ValueOf(got)
		for i := 0; i < left.NumField(); i++ {
			if !reflect.DeepEqual(left.Field(i).Interface(), right.Field(i).Interface()) {
				fields = append(fields, left.Type().Field(i).Name)
			}
		}
		return fmt.Errorf("observable episode fields differ: %v", fields)
	}
	return nil
}
