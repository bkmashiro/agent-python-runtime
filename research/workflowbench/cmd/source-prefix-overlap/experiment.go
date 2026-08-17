package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const fixtureHandlerContract = "pysolate.source-prefix.fixture-handler.v1"

type timedFixtureHandler struct {
	delay   time.Duration
	origin  time.Time
	calls   atomic.Uint32
	started atomic.Int64
	ended   atomic.Int64
}

func (handler *timedFixtureHandler) setOrigin(origin time.Time) { handler.origin = origin }

func (handler *timedFixtureHandler) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(arguments, &input) != nil || input.Key != "alpha" || handler.origin.IsZero() {
		return nil, errors.New("invalid source-prefix fixture call")
	}
	if handler.calls.Add(1) != 1 {
		return nil, errors.New("source-prefix fixture dispatched more than once")
	}
	handler.started.Store(maxInt64(1, time.Since(handler.origin).Nanoseconds()))
	timer := time.NewTimer(handler.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	handler.ended.Store(maxInt64(handler.started.Load()+1, time.Since(handler.origin).Nanoseconds()))
	return json.RawMessage(`{"label":"Alpha"}`), nil
}

func fixtureCapabilitySpec() capability.Spec {
	return capability.Spec{
		Name: "slow.lookup", Version: "pysolate.source-prefix.slow-lookup.v1", Description: "Authored high-latency read for source-prefix overlap.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "source-prefix-handler-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","const":"alpha"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"label":{"type":"string","const":"Alpha"}},"required":["label"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "slow", Method: "lookup", Arguments: []string{"key"}},
	}
}

func buildFixturePlan(handler capability.Handler) (*capability.Plan, error) {
	grant, err := capability.NewGrant(json.RawMessage(`{"fixture":"source-prefix-overlap","external_writes":false}`))
	if err != nil {
		return nil, err
	}
	registry := capability.NewRegistry()
	if err := registry.Register(fixtureCapabilitySpec(), grant, handler); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
}

type sourcePrefixEnvironment struct {
	artifact []byte
	manager  *workspace.Manager
	base     workspace.Ref
	config   runtimeconfig.RunConfig
}

type laneOutcome struct {
	result streaming.RunResult
	err    error
}

func (environment sourcePrefixEnvironment) executeLane(ctx context.Context, contract workflowbench.SourcePrefixExperimentContract, expectedResultSHA string, pair, order uint32, treatment workflowbench.SourcePrefixTreatment) (workflowbench.SourcePrefixRow, error) {
	handler := &timedFixtureHandler{delay: time.Duration(contract.ToolDelayMS) * time.Millisecond}
	plan, err := buildFixturePlan(handler)
	if err != nil {
		return workflowbench.SourcePrefixRow{}, err
	}
	attempt, err := environment.manager.ForkAttempt(environment.base)
	if err != nil {
		return workflowbench.SourcePrefixRow{}, err
	}
	workspaceBeforeSHA256, err := snapshotWorkspaceSHA256(environment.manager, attempt.Ref(), "source-prefix-before")
	if err != nil {
		_ = attempt.Discard()
		return workflowbench.SourcePrefixRow{}, err
	}
	runID := fmt.Sprintf("source-prefix-%d-%d", pair, order)
	var broker *capability.Broker
	factory := wazeroengine.Factory{
		WorkspaceManager: environment.manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: runID,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			broker, err = capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
			return broker, err
		},
	}
	runner, err := factory.New(ctx, environment.artifact, environment.config)
	if err != nil {
		_ = attempt.Discard()
		return workflowbench.SourcePrefixRow{}, err
	}
	streamRunner, ok := runner.(streaming.StreamRunner)
	if !ok {
		_ = runner.Close(context.Background())
		_ = attempt.Discard()
		return workflowbench.SourcePrefixRow{}, errors.New("wazero runner lacks live stream support")
	}
	request, err := json.Marshal(map[string]any{"run_id": runID, "code": "result = stream_final", "inputs": map[string]any{}})
	if err != nil {
		_ = runner.Close(context.Background())
		_ = attempt.Discard()
		return workflowbench.SourcePrefixRow{}, err
	}
	begin, err := streaming.BuildBeginPrepare(streaming.BeginConfig{Inputs: json.RawMessage(`{}`), Plan: plan})
	if err != nil {
		_ = runner.Close(context.Background())
		_ = attempt.Discard()
		return workflowbench.SourcePrefixRow{}, err
	}
	prepares := make(chan string)
	completed := make(chan laneOutcome, 1)
	laneContext, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		result, err := streaming.ExecuteStream(laneContext, streamRunner, attempt, request, prepares)
		completed <- laneOutcome{result: result, err: err}
	}()
	if outcome, err := sendPrepare(laneContext, prepares, completed, begin); err != nil {
		close(prepares)
		if outcome != nil && outcome.err != nil {
			return workflowbench.SourcePrefixRow{}, outcome.err
		}
		return workflowbench.SourcePrefixRow{}, err
	}

	origin := time.Now()
	handler.setOrigin(origin)
	lastOffset := time.Duration(contract.Schedule.Chunks[len(contract.Schedule.Chunks)-1].OffsetMS) * time.Millisecond
	var generationComplete atomic.Int64
	waitUntil := func(waitContext context.Context, offset time.Duration) error {
		delay := time.Until(origin.Add(offset))
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-waitContext.Done():
				return waitContext.Err()
			case <-timer.C:
			}
		}
		if offset == lastOffset {
			generationComplete.CompareAndSwap(0, maxInt64(1, time.Since(origin).Nanoseconds()))
		}
		return nil
	}
	events, failures, err := workflowbench.ProduceTimedSource(laneContext, contract.Schedule, treatment, waitUntil)
	if err != nil {
		close(prepares)
		return workflowbench.SourcePrefixRow{}, err
	}
	for event := range events {
		prepare, err := streaming.BuildChunkPrepare(event.Source)
		if err != nil {
			cancel()
			close(prepares)
			return workflowbench.SourcePrefixRow{}, err
		}
		if outcome, err := sendPrepare(laneContext, prepares, completed, prepare); err != nil {
			cancel()
			close(prepares)
			if outcome != nil && outcome.err != nil {
				return workflowbench.SourcePrefixRow{}, outcome.err
			}
			return workflowbench.SourcePrefixRow{}, err
		}
	}
	if producerErr := <-failures; producerErr != nil {
		cancel()
		close(prepares)
		return workflowbench.SourcePrefixRow{}, producerErr
	}
	if outcome, err := sendPrepare(laneContext, prepares, completed, streaming.BuildEndPrepare()); err != nil {
		cancel()
		close(prepares)
		if outcome != nil && outcome.err != nil {
			return workflowbench.SourcePrefixRow{}, outcome.err
		}
		return workflowbench.SourcePrefixRow{}, err
	}
	close(prepares)
	outcome := <-completed
	runEnded := maxInt64(1, time.Since(origin).Nanoseconds())
	if outcome.err != nil {
		return workflowbench.SourcePrefixRow{}, outcome.err
	}
	workspaceAfterSHA256, err := snapshotWorkspaceSHA256(environment.manager, outcome.result.PublishedWorkspace, "source-prefix-after")
	if err != nil {
		return workflowbench.SourcePrefixRow{}, err
	}
	canonicalResult, err := stableStreamResult(outcome.result.Response)
	if err != nil {
		return workflowbench.SourcePrefixRow{}, err
	}
	resultSHA := digestBytes(canonicalResult)
	if resultSHA != expectedResultSHA || broker == nil || broker.Calls() != 1 || len(broker.Receipts()) != 1 || handler.calls.Load() != 1 || outcome.result.PublishedWorkspace == "" || workspaceBeforeSHA256 != workspaceAfterSHA256 {
		return workflowbench.SourcePrefixRow{}, errors.New("source-prefix independent oracle failed")
	}
	generationNS := generationComplete.Load()
	if generationNS == 0 || handler.started.Load() <= 0 || handler.ended.Load() <= handler.started.Load() {
		return workflowbench.SourcePrefixRow{}, errors.New("source-prefix timeline is incomplete")
	}
	return workflowbench.SourcePrefixRow{
		Pair: pair, LaneOrder: order, Treatment: treatment, WallNS: runEnded, GenerationCompleteNS: generationNS,
		ToolStartedNS: handler.started.Load(), ToolEndedNS: handler.ended.Load(), RunEndedNS: runEnded,
		ResultSHA256: resultSHA, OraclePassed: true, LogicalCalls: broker.Calls(), PhysicalDispatches: handler.calls.Load(),
		GuestStarts: 1, Fallback: false, WorkspaceBeforeSHA256: workspaceBeforeSHA256, WorkspaceAfterSHA256: workspaceAfterSHA256, WorkspaceDisposition: "published",
	}, nil
}

func sendPrepare(ctx context.Context, prepares chan<- string, completed <-chan laneOutcome, prepare string) (*laneOutcome, error) {
	select {
	case prepares <- prepare:
		return nil, nil
	case outcome := <-completed:
		return &outcome, errors.New("stream execution ended before prepare delivery")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func snapshotWorkspaceSHA256(manager *workspace.Manager, ref workspace.Ref, owner string) (string, error) {
	lease, err := manager.Acquire(ref, owner)
	if err != nil {
		return "", err
	}
	snapshot, snapshotErr := lease.Snapshot()
	releaseErr := lease.Release()
	if snapshotErr != nil {
		return "", snapshotErr
	}
	if releaseErr != nil {
		return "", releaseErr
	}
	if snapshot.Info.WorkspaceSHA256 == "" {
		return "", errors.New("workspace snapshot lacks portable identity")
	}
	return snapshot.Info.WorkspaceSHA256, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
