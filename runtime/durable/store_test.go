package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func storeTestDefinition(id string) Definition {
	return Definition{
		ID: id, Code: "result = 1", Seed: "seed-1",
		ArtifactSHA256: "artifact", EnvironmentVersion: "env-1",
		Inputs: json.RawMessage(`{"value":1}`),
	}
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "durable.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func TestStoreCreateReopenAndPendingReplay(t *testing.T) {
	store, path := openTestStore(t)
	definition := storeTestDefinition("run-reopen")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call-1", Capability: "tools.echo", Arguments: json.RawMessage(`{"x":1}`)}
	record, created, err := store.BeginCall(context.Background(), definition.ID, call)
	if err != nil || !created {
		t.Fatalf("begin call record=%#v created=%v err=%v", record, created, err)
	}
	if record.OperationKey != "run-reopen/0" || record.State != CallPending {
		t.Fatalf("record=%#v", record)
	}
	if got, err := store.CallCount(context.Background(), definition.ID); err != nil || got != 1 {
		t.Fatalf("call count=%d err=%v", got, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gotRun, err := reopened.Get(context.Background(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != StatusActive || string(gotRun.Definition.Inputs) != string(definition.Inputs) {
		t.Fatalf("reopened run=%#v", gotRun)
	}
	replayed, created, err := reopened.BeginCall(context.Background(), definition.ID, call)
	if err != nil || created || replayed.OperationKey != record.OperationKey || replayed.State != CallPending {
		t.Fatalf("replayed=%#v created=%v err=%v", replayed, created, err)
	}
	winner, err := reopened.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"ok":true}`))
	if err != nil || string(winner) != `{"ok":true}` {
		t.Fatalf("winner=%s err=%v", winner, err)
	}
}

func TestStoreConcurrentOpenNewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	const workers = 16
	stores := make([]*Store, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stores[i], errs[i] = Open(path)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Open worker %d: %v", i, err)
		}
	}
	for _, store := range stores {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreUsesWALAndFullSynchronousMode(t *testing.T) {
	store, _ := openTestStore(t)
	store.db.SetMaxIdleConns(0)
	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode=%q", journalMode)
	}
	var synchronous int
	if err := store.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 2 { // SQLITE_SYNC_FULL
		t.Fatalf("synchronous=%d", synchronous)
	}
	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d", foreignKeys)
	}
	var busyTimeout int
	if err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != busyTimeoutMS {
		t.Fatalf("busy_timeout=%d", busyTimeout)
	}
}

func TestStoreEncodesDatabasePathAndCommitsSchemaVersionWithDDL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "path with # and ?")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "durable database.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version=%d", version)
	}
	var definitionColumn int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name='definition_id'").Scan(&definitionColumn); err != nil {
		t.Fatal(err)
	}
	if definitionColumn != 0 {
		t.Fatal("runs still has duplicate definition_id column")
	}
}

func TestStoreRejectsPublishedSchemaVersionWithoutImplicitMigration(t *testing.T) {
	store, path := openTestStore(t)
	if _, err := store.db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !bytes.Contains([]byte(err.Error()), []byte("unsupported durable schema version 1")) {
		t.Fatalf("Open old schema err=%v", err)
	}
}

func TestStorePersistsToolsAndCountsThemInLogicalSize(t *testing.T) {
	store, path := openTestStore(t)
	definition := storeTestDefinition("run-tools")
	definition.Tools = json.RawMessage(`[{"name":"tools.echo","version":"v1"}]`)
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || !bytes.Equal(got.Definition.Tools, definition.Tools) {
		t.Fatalf("tools=%s want=%s err=%v", got.Definition.Tools, definition.Tools, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err = reopened.Get(context.Background(), definition.ID)
	if err != nil || !bytes.Equal(got.Definition.Tools, definition.Tools) {
		t.Fatalf("reopened tools=%s want=%s err=%v", got.Definition.Tools, definition.Tools, err)
	}
	withoutTools := definition
	withoutTools.Tools = nil
	if definitionPayload(definition) <= definitionPayload(withoutTools) {
		t.Fatal("tools were not included in definition logical size")
	}
}

func TestStoreWaitDecisionConflictAndTerminalRunDoesNotReactivate(t *testing.T) {
	store, _ := openTestStore(t)
	definition := storeTestDefinition("run-wait")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call-1", Capability: "tools.wait", Arguments: json.RawMessage(`{"request":"x"}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour).Round(0)
	spec := WaitSpec{Kind: "approval", Request: json.RawMessage(`{"scope":"x"}`), Deadline: &deadline}
	wait, err := store.EnsureWait(context.Background(), definition.ID, 0, spec)
	if err != nil {
		t.Fatal(err)
	}
	later := deadline.Add(time.Hour)
	again, err := store.EnsureWait(context.Background(), definition.ID, 0, WaitSpec{
		Kind: "different", Request: json.RawMessage(`{"scope":"recomputed"}`), Deadline: &later,
	})
	if err != nil || again.ID != wait.ID || again.Decision != nil {
		t.Fatalf("again=%#v err=%v", again, err)
	}
	if again.Spec.Kind != spec.Kind || !again.Spec.Deadline.Equal(*spec.Deadline) || !bytes.Equal(again.Spec.Request, spec.Request) {
		t.Fatalf("existing wait was recomputed: %#v", again.Spec)
	}
	first := Decision{Result: json.RawMessage(`{"approved":true}`)}
	if err := store.ResolveWait(context.Background(), wait.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveWait(context.Background(), wait.ID, first); err != nil {
		t.Fatalf("identical duplicate decision: %v", err)
	}
	if err := store.ResolveWait(context.Background(), wait.ID, Decision{Result: json.RawMessage(`{"approved":false}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting decision err=%v", err)
	}
	gotWait, err := store.GetWait(context.Background(), wait.ID)
	if err != nil || gotWait.Decision == nil || string(gotWait.Decision.Result) != string(first.Result) {
		t.Fatalf("wait=%#v err=%v", gotWait, err)
	}
	if err := store.SetState(context.Background(), definition.ID, StatusCompleted, json.RawMessage(`{"done":true}`), "finished"); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveWait(context.Background(), wait.ID, Decision{Error: "late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late conflicting decision err=%v", err)
	}
	gotRun, err := store.Get(context.Background(), definition.ID)
	if err != nil || gotRun.Status != StatusCompleted || string(gotRun.Outcome) != `{"done":true}` {
		t.Fatalf("terminal run=%#v err=%v", gotRun, err)
	}
}

func TestStoreLogicalLimitRejectsWithoutTruncation(t *testing.T) {
	store, _ := openTestStore(t)
	tooLarge := json.RawMessage(bytes.Repeat([]byte("x"), int(MaxRunPayloadBytes)))
	if err := store.Create(context.Background(), Definition{ID: "run-too-large", Inputs: tooLarge}); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversize create err=%v", err)
	}
	if _, err := store.Get(context.Background(), "run-too-large"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oversize create persisted: %v", err)
	}

	definition := storeTestDefinition("run-result-bytes")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call", Capability: "tools.echo", Arguments: json.RawMessage(`{}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"exact":[1,2,3]}`)
	winner, err := store.CompleteCall(context.Background(), definition.ID, 0, result)
	if err != nil || !bytes.Equal(winner, result) {
		t.Fatalf("result winner=%s err=%v", winner, err)
	}
	winner[0] = 'X'
	stored, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"different":true}`))
	if err != nil || string(stored) != string(result) {
		t.Fatalf("persisted winner=%s err=%v", stored, err)
	}
}

func TestStorePayloadSizeCountsUTF8BytesInSQL(t *testing.T) {
	store, _ := openTestStore(t)
	definition := Definition{
		ID: "run-字", Code: "代码", Seed: "种子", ArtifactSHA256: "散列", EnvironmentVersion: "环境",
		Inputs: json.RawMessage(`{"提示":"值"}`),
	}
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "呼叫", Capability: "工具", Arguments: json.RawMessage(`{"参数":"值"}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	spec := WaitSpec{Kind: "等待", Request: json.RawMessage(`{"说明":"通过"}`), Deadline: &deadline}
	wait, err := store.EnsureWait(context.Background(), definition.ID, 0, spec)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	got, err := payloadSizeTx(context.Background(), tx, definition.ID)
	if rollbackErr := tx.Rollback(); err == nil {
		err = rollbackErr
	}
	if err != nil {
		t.Fatal(err)
	}
	deadlineText := encodeDeadline(spec.Deadline).(string)
	want := uint64(len(definition.ID) + len(definition.Code) + len(definition.Seed) + len(definition.ArtifactSHA256) + len(definition.EnvironmentVersion) + len(definition.Inputs))
	want += uint64(len(call.CallID) + len(call.Capability) + len(operationKey(definition.ID, call.Sequence)) + len(call.Arguments))
	want += uint64(len(wait.ID) + len(spec.Kind) + len(spec.Request) + len(deadlineText))
	if got != want {
		t.Fatalf("payload bytes=%d want=%d", got, want)
	}
}

func TestStoreConcurrentCompleteCallIsSingleAssignment(t *testing.T) {
	store, _ := openTestStore(t)
	definition := storeTestDefinition("run-concurrent")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call", Capability: "tools.echo", Arguments: json.RawMessage(`{}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	const workers = 24
	results := make([]json.RawMessage, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(fmt.Sprintf(`{"winner":%d}`, i)))
		}(i)
	}
	wg.Wait()
	var winner json.RawMessage
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d err=%v", i, errs[i])
		}
		if winner == nil {
			winner = results[i]
		} else if !bytes.Equal(winner, results[i]) {
			t.Fatalf("worker %d saw %s, winner %s", i, results[i], winner)
		}
	}
	persisted, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"late":true}`))
	if err != nil || !bytes.Equal(persisted, winner) {
		t.Fatalf("persisted=%s winner=%s err=%v", persisted, winner, err)
	}
}

func TestStoreCancellationDoesNotReopenOrOverwriteTerminalRun(t *testing.T) {
	store, _ := openTestStore(t)
	definition := storeTestDefinition("run-cancel")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call", Capability: "tools.wait", Arguments: json.RawMessage(`{}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	wait, err := store.EnsureWait(context.Background(), definition.ID, 0, WaitSpec{Kind: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetState(context.Background(), definition.ID, StatusCancelled, nil, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveWait(context.Background(), wait.ID, Decision{Error: "late"}); err != nil {
		t.Fatalf("resolve after cancellation: %v", err)
	}
	if _, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"late":true}`)); err != nil {
		t.Fatalf("complete late call: %v", err)
	}
	if err := store.SetState(context.Background(), definition.ID, StatusCompleted, json.RawMessage(`{"done":true}`), ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal run was reopened: %v", err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("run=%#v err=%v", got, err)
	}
}

func TestStoreBeginCallRejectsExistingPendingCallAfterTerminalRun(t *testing.T) {
	store, _ := openTestStore(t)
	definition := storeTestDefinition("run-terminal-pending")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call", Capability: "tools.echo", Arguments: json.RawMessage(`{}`)}
	if _, created, err := store.BeginCall(context.Background(), definition.ID, call); err != nil || !created {
		t.Fatalf("initial BeginCall created=%v err=%v", created, err)
	}
	if err := store.SetState(context.Background(), definition.ID, StatusCompleted, json.RawMessage(`{"done":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal pending BeginCall err=%v", err)
	}
	if _, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"late":true}`)); err != nil {
		t.Fatalf("late CompleteCall was rejected: %v", err)
	}
}

func TestStoreBeginCallRejectsExistingWaitingCallAfterBlockedRun(t *testing.T) {
	store, _ := openTestStore(t)
	definition := storeTestDefinition("run-blocked-waiting")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := capability.LoggedCall{Sequence: 0, CallID: "call", Capability: "tools.wait", Arguments: json.RawMessage(`{}`)}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureWait(context.Background(), definition.ID, 0, WaitSpec{Kind: "approval"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetState(context.Background(), definition.ID, StatusBlocked, nil, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, call); !errors.Is(err, ErrConflict) {
		t.Fatalf("blocked waiting BeginCall err=%v", err)
	}
	if _, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"late":true}`)); err != nil {
		t.Fatalf("late waiting CompleteCall was rejected: %v", err)
	}
}

func TestStoreLocksAreNamespacedByDatabase(t *testing.T) {
	parent := t.TempDir()
	first, err := Open(filepath.Join(parent, "first.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(filepath.Join(parent, "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	definition := storeTestDefinition("same-run-id")
	if err := first.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if err := second.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	releaseFirst, err := first.Claim(definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	releaseSecond, err := second.Claim(definition.ID)
	if err != nil {
		t.Fatalf("different databases shared a run lock: %v", err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSymlinkAliasSharesCanonicalLock(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "state.db")
	alias := filepath.Join(parent, "alias.db")
	first, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	second, err := Open(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if first.lockDir != second.lockDir || first.lockDir != canonicalTarget+".lock" {
		t.Fatalf("lock dirs first=%q second=%q", first.lockDir, second.lockDir)
	}
	definition := storeTestDefinition("same-run-id")
	if err := first.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	release, err := first.Claim(definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := second.Claim(definition.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("symlink alias claim err=%v", err)
	}
}

func TestStoreRejectsHardLinkedDatabase(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "state.db")
	store, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(parent, "hardlink.db")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(hardlink); err == nil || !bytes.Contains([]byte(err.Error()), []byte("hard-linked SQLite files are unsupported")) {
		t.Fatalf("hardlink Open err=%v", err)
	}
}

func TestStoreLockCrossProcessAndKillReleases(t *testing.T) {
	store, path := openTestStore(t)
	definition := storeTestDefinition("run-lock")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestStoreLockHelper", "--")
	cmd.Env = append(os.Environ(),
		"DURABLE_STORE_LOCK_HELPER=1", "DURABLE_STORE_PATH="+path,
		"DURABLE_STORE_RUN="+definition.ID, "DURABLE_STORE_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := store.Claim(definition.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("cross-process claim err=%v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := store.Claim(definition.ID)
	if err != nil {
		t.Fatalf("claim after process death: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLockHelper(t *testing.T) {
	if os.Getenv("DURABLE_STORE_LOCK_HELPER") != "1" {
		t.Skip("lock helper")
	}
	store, err := Open(os.Getenv("DURABLE_STORE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	release, err := store.Claim(os.Getenv("DURABLE_STORE_RUN"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(os.Getenv("DURABLE_STORE_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {}
}
