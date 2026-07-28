package fakejob_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakejob"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const jobCatalog = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type jobIDs struct{ next int }

func (ids *jobIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_%d", prefix, ids.next), nil
}

type fixture struct {
	provider    *fakejob.Provider
	adapter     *fakejob.Adapter
	resolver    *capability.StaticSecretResolver
	broker      *capability.Broker
	coordinator *transaction.Coordinator
	tx          transaction.Transaction
	next        int
}

func newFixture(t *testing.T, readToken, controlToken []byte) *fixture {
	t.Helper()
	now := time.Unix(1000, 0).UTC()
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{"jobs.read": readToken, "jobs.control": controlToken}, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fakejob.NewProvider([]fakejob.Recipe{{Alias: "recipe:test", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, []byte("read-token"), []byte("control-token"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := fakejob.NewAdapter(fakejob.Config{Resolver: resolver, ReadSecretRef: "jobs.read", ControlSecretRef: "jobs.control", RunIdentity: "run:jobs", TaskIdentity: "task:jobs", Tenant: "tenant:jobs", QueueAlias: "queue:test", PolicyVersion: "jobs:v1", LeaseDuration: time.Minute, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &jobIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run:jobs", CatalogDigest: jobCatalog, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, _ := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	registry := capability.NewRegistry()
	specs, _ := fakejob.HandlerSpecs(adapter)
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	grants, _ := fakejob.ToolGrants("jobs:v1", 32)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:jobs", CatalogDigest: jobCatalog, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 64, AutoCompensateTools: map[string]bool{fakejob.SubmitToolID: true}}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result := &fixture{provider: provider, adapter: adapter, resolver: resolver, broker: broker, coordinator: coordinator, tx: tx}
	t.Cleanup(func() { resolver.Close(); provider.Close() })
	return result
}

type response struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (f *fixture) call(t *testing.T, tool string, arguments any) response {
	t.Helper()
	f.next++
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"call_id": fmt.Sprintf("call:%d", f.next), "capability": tool, "catalog_digest": jobCatalog, "handler_version": fakejob.HandlerVersion, "arguments": json.RawMessage(args)})
	raw, err := f.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var value response
	if json.Unmarshal(raw, &value) != nil {
		t.Fatalf("raw=%s", raw)
	}
	return value
}

func submit(t *testing.T, f *fixture) fakejob.Job {
	t.Helper()
	value := f.call(t, fakejob.SubmitToolID, map[string]any{"recipe_alias": "recipe:test"})
	var job fakejob.Job
	if value.Status != "ok" || json.Unmarshal(value.Result, &job) != nil || job.Status != "queued" || job.Version == 0 {
		t.Fatalf("submit=%+v result=%s", value, value.Result)
	}
	return job
}

func TestFakeJobSubmitPollLogsAndArtifacts(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	if f.provider.JobCount() != 1 {
		t.Fatalf("jobs=%d", f.provider.JobCount())
	}
	if err := f.provider.Advance(job.ID, job.Version, "running", []fakejob.LogLine{{Stream: "stdout", Text: "started"}}, nil); err != nil {
		t.Fatal(err)
	}
	running := f.provider.Snapshot(job.ID)
	if err := f.provider.Advance(job.ID, running.Version, "succeeded", []fakejob.LogLine{{Stream: "stdout", Text: "done"}}, []fakejob.Artifact{{Name: "result.json", SHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Bytes: 42}}); err != nil {
		t.Fatal(err)
	}
	poll := f.call(t, fakejob.PollManyToolID, map[string]any{"job_ids": []string{job.ID}})
	var polled struct {
		Jobs []fakejob.Job `json:"jobs"`
	}
	_ = json.Unmarshal(poll.Result, &polled)
	if poll.Status != "ok" || len(polled.Jobs) != 1 || polled.Jobs[0].Status != "succeeded" {
		t.Fatalf("poll=%+v result=%s", poll, poll.Result)
	}
	logs := f.call(t, fakejob.LogsToolID, map[string]any{"job_id": job.ID, "cursor": 0, "limit": 1})
	var logResult struct {
		Lines      []fakejob.LogLine `json:"lines"`
		NextCursor *uint32           `json:"next_cursor"`
	}
	_ = json.Unmarshal(logs.Result, &logResult)
	if logs.Status != "ok" || len(logResult.Lines) != 1 || logResult.Lines[0].Sequence != 1 || logResult.NextCursor == nil || *logResult.NextCursor != 1 {
		t.Fatalf("logs=%+v result=%s", logs, logs.Result)
	}
	artifacts := f.call(t, fakejob.ArtifactsToolID, map[string]any{"job_id": job.ID})
	var artifactResult struct {
		Artifacts []fakejob.Artifact `json:"artifacts"`
	}
	_ = json.Unmarshal(artifacts.Result, &artifactResult)
	if artifacts.Status != "ok" || len(artifactResult.Artifacts) != 1 || artifactResult.Artifacts[0].Name != "result.json" {
		t.Fatalf("artifacts=%+v result=%s", artifacts, artifacts.Result)
	}
}

func TestFakeJobAbortCompensatesQueuedSubmitByCanceling(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	if err := f.broker.CompensateCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	got := f.provider.Snapshot(job.ID)
	if got.Status != "canceled" || got.Version <= job.Version {
		t.Fatalf("job=%+v", got)
	}
	inspection, err := f.broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionCompensated {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestFakeJobCompensationRefusesCompletedExternalState(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	if err := f.provider.Advance(job.ID, job.Version, "succeeded", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.broker.CompensateCurrentTransaction(context.Background(), "guest_error"); err == nil {
		t.Fatal("completed job was falsely compensated")
	}
	if got := f.provider.Snapshot(job.ID); got.Status != "succeeded" {
		t.Fatalf("job=%+v", got)
	}
}

func TestFakeJobRejectsUnknownRecipeWrongCredentialAndCommandInjection(t *testing.T) {
	unknown := newFixture(t, []byte("read-token"), []byte("control-token"))
	denied := unknown.call(t, fakejob.SubmitToolID, map[string]any{"recipe_alias": "recipe:missing"})
	if denied.Error == nil || denied.Error.Code != "job_denied" || unknown.provider.JobCount() != 0 {
		t.Fatalf("unknown=%+v jobs=%d", denied, unknown.provider.JobCount())
	}
	wrong := newFixture(t, []byte("read-token"), []byte("wrong-token"))
	credential := wrong.call(t, fakejob.SubmitToolID, map[string]any{"recipe_alias": "recipe:test"})
	if credential.Error == nil || credential.Error.Code != "credential_denied" || wrong.provider.JobCount() != 0 {
		t.Fatalf("credential=%+v", credential)
	}
	injected := newFixture(t, []byte("read-token"), []byte("control-token"))
	result := injected.call(t, fakejob.SubmitToolID, map[string]any{"recipe_alias": "recipe:test", "command": "curl https://example.com"})
	if result.Error == nil || result.Error.Code != "invalid_arguments" || injected.provider.JobCount() != 0 {
		t.Fatalf("injected=%+v", result)
	}
}
