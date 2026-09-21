package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
	"github.com/bkmashiro/agent-python-runtime/mcpadapter"
	"github.com/bkmashiro/agent-python-runtime/mcpadapter/gosdk"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	"github.com/tetratelabs/wazero"
)

const fetchProgram = `first = catalog.lookup(sku=inputs["sku"])
item = first["structuredContent"]
second = catalog.detail(item_id=item["item_id"])
detail = second["structuredContent"]
result = {
    "sku": item["sku"],
    "item_id": item["item_id"],
    "price": item["price"],
    "currency": item["currency"],
    "note": detail["note"],
}
`

type scheduledMode struct {
	class   durable.SchedulingClass
	metrics *phaseMetrics
	runner  *durable.Runner
	store   *durable.Store
}

func (mode *scheduledMode) close() {
	_ = mode.runner.Close(context.Background())
	_ = mode.store.Close()
}

func runParent(ctx context.Context, guestPath string, tasks, iterations int, toolDelay time.Duration) error {
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	client, err := gosdk.ConnectCommand(ctx, exec.Command(executable, "-mcp-server", "-tool-delay", toolDelay.String()), time.Second)
	if err != nil {
		return err
	}
	defer client.Close()
	provider := mcpadapter.Provider{Client: client, CanonicalNamespace: "mcp.catalog", PythonNamespace: "catalog"}
	manifest, err := pysolate.ManifestFromProviders(ctx, provider)
	if err != nil {
		return err
	}
	lookup, okLookup := manifest["mcp.catalog.lookup"]
	detail, okDetail := manifest["mcp.catalog.detail"]
	if !okLookup || !okDetail || lookup.PythonPath != "catalog.lookup" || detail.PythonPath != "catalog.detail" {
		return errors.New("MCP fixture catalog was not normalized as expected")
	}

	cache := wazero.NewCompilationCache()
	defer cache.Close(context.Background())
	ctx = pysolate.WithCompilationCache(ctx, cache)
	workspaceRunner, err := pysolate.NewPreparedWorkspace(ctx, wasm, nil)
	if err != nil {
		return err
	}
	defer workspaceRunner.Close(context.Background())

	temporary, err := os.MkdirTemp("", "pysolate-mcp-workflow-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	source := filepath.Join(temporary, "source")
	if err := createSourceFixture(source); err != nil {
		return err
	}
	workspaceBase := filepath.Join(temporary, "workspaces")
	if err := os.Mkdir(workspaceBase, 0o700); err != nil {
		return err
	}
	manager, err := workspacepkg.NewManager(workspaceBase)
	if err != nil {
		return err
	}
	defer manager.Close()

	result := report{
		Guest: filepath.Base(guestPath), Tasks: tasks, Iterations: iterations,
		ToolDelayNS: toolDelay.Nanoseconds(), RunningLimit: 1, ResidentLimit: tasks,
		Boundary: "MCP fetch uses durable scheduling; fetched JSON is then passed to a separate writable-workspace Guest because writable workspaces are excluded from durable replay.",
	}
	modes := make([]*scheduledMode, 0, 2)
	for _, class := range []durable.SchedulingClass{durable.Inline, durable.ExternalIO} {
		mode, err := newScheduledMode(ctx, temporary, wasm, class, lookup, detail)
		if err != nil {
			for _, existing := range modes {
				existing.close()
			}
			return err
		}
		modes = append(modes, mode)
	}
	defer func() {
		for _, mode := range modes {
			mode.close()
		}
	}()
	rowsByMode := make(map[string][]sample, len(modes))
	for iteration := 0; iteration < iterations; iteration++ {
		order := []int{0, 1}
		if iteration%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, index := range order {
			mode := modes[index]
			mode.metrics.reset()
			row, err := runSample(ctx, mode.runner, workspaceRunner, manager, source, mode.metrics, string(mode.class), iteration, tasks)
			if err != nil {
				return err
			}
			result.Samples = append(result.Samples, row)
			rowsByMode[row.Mode] = append(rowsByMode[row.Mode], row)
		}
	}
	for _, mode := range modes {
		result.Summaries = append(result.Summaries, summarize(string(mode.class), rowsByMode[string(mode.class)]))
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func newScheduledMode(ctx context.Context, root string, wasm []byte, class durable.SchedulingClass, lookup, detail pysolate.ToolSpec) (*scheduledMode, error) {
	metrics := &phaseMetrics{}
	runner, store, err := newScheduledRunner(ctx, root, string(class), wasm, class, lookup, detail, metrics)
	if err != nil {
		return nil, err
	}
	return &scheduledMode{class: class, metrics: metrics, runner: runner, store: store}, nil
}

func newScheduledRunner(ctx context.Context, root, label string, wasm []byte, scheduling durable.SchedulingClass, lookup, detail pysolate.ToolSpec, metrics *phaseMetrics) (*durable.Runner, *durable.Store, error) {
	store, err := durable.Open(filepath.Join(root, label+".db"))
	if err != nil {
		return nil, nil, err
	}
	observer := durable.ToolObserverFunc(metrics.observe)
	tools := []durable.Tool{
		{Name: "catalog.lookup", Version: "mcp-v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Observer: observer, Call: metrics.wrap(lookup.Call)},
		{Name: "catalog.detail", Version: "mcp-v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Observer: observer, Call: metrics.wrap(detail.Call)},
	}
	runner, err := durable.NewRunner(ctx, store, wasm, "mcp-workflow-v1", tools, durable.Preparation{Seed: "mcp-workflow-seed"})
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	return runner, store, nil
}

func runSample(ctx context.Context, runner *durable.Runner, workspaceRunner *pysolate.Runner, manager *workspacepkg.Manager, source string, metrics *phaseMetrics, mode string, iteration, tasks int) (sample, error) {
	row := sample{Mode: mode, Iteration: iteration}
	totalStarted := time.Now()
	ids := make([]string, tasks)
	createStarted := time.Now()
	for index := range ids {
		ids[index] = fmt.Sprintf("%s-%d-%d", mode, iteration, index)
		inputs, _ := json.Marshal(map[string]string{"sku": "A-1"})
		_, err := runner.Create(ctx, durable.Definition{
			ID: ids[index], Code: fetchProgram, Inputs: inputs, Seed: "mcp-workflow-seed",
			ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "mcp-workflow-v1",
		})
		if err != nil {
			return row, err
		}
	}
	row.CreateNS = time.Since(createStarted).Nanoseconds()
	records, elapsed, err := runFetchBatch(ctx, runner, ids, tasks)
	if err != nil {
		return row, err
	}
	row.FetchBatchNS = elapsed.Nanoseconds()

	workspaceStarted := time.Now()
	row.ConflictFree = true
	for index, record := range records {
		changed, clean, err := runWorkspaceStage(ctx, workspaceRunner, manager, source, record, fmt.Sprintf("%s-%d-%d", mode, iteration, index))
		if err != nil {
			return row, err
		}
		row.ChangedFiles += changed
		row.ConflictFree = row.ConflictFree && clean
	}
	row.WorkspaceBatchNS = time.Since(workspaceStarted).Nanoseconds()
	row.TotalNS = time.Since(totalStarted).Nanoseconds()
	row.ToolCalls = metrics.calls.Load()
	row.PeakMCPCalls = metrics.peak.Load()
	row.ToolQueueNS = metrics.queue.Load()
	row.ToolServiceNS = metrics.service.Load()
	row.ContinuationResumeNS = metrics.resume.Load()
	row.OraclePassed = row.ToolCalls == int64(tasks*2) && row.ChangedFiles == tasks*2 && row.ConflictFree
	if !row.OraclePassed {
		return row, fmt.Errorf("workflow oracle failed: %+v", row)
	}
	return row, nil
}

func runFetchBatch(ctx context.Context, runner *durable.Runner, ids []string, tasks int) ([]workflowRecord, time.Duration, error) {
	executor, err := durable.NewExecutor(runner, durable.Limits{MaxRunning: 1, MaxResident: tasks, MaxInflightTools: tasks, MaxQueued: tasks})
	if err != nil {
		return nil, 0, err
	}
	defer executor.Close(context.Background())
	started := time.Now()
	attempts := make([]*durable.Attempt, tasks)
	for index, id := range ids {
		attempts[index], err = executor.Submit(ctx, id)
		if err != nil {
			return nil, 0, err
		}
	}
	records := make([]workflowRecord, tasks)
	for index, attempt := range attempts {
		output, err := attempt.Wait(ctx)
		if err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(output.Value, &records[index]); err != nil {
			return nil, 0, err
		}
		if err := validateRecord(records[index]); err != nil {
			return nil, 0, err
		}
	}
	return records, time.Since(started), nil
}
