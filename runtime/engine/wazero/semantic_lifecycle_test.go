package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazerort "github.com/tetratelabs/wazero"
)

func TestSemanticAnalysisLifecycleStoreAggregatesBodyFreeEvidence(t *testing.T) {
	var store semanticAnalysisLifecycleStore
	store.add(SemanticAnalysisLifecycleEvidence{
		Invocations: 1, ModuleInstantiations: 1, InitializeCalls: 1, RuntimeInitCalls: 1, Successes: 1,
		PreparedProvisions: 1, PreparedProvisionFailures: 1, PreparedHits: 1, PreparedProvisionNanos: 31,
		InstantiateNanos: 2, InitializeNanos: 3, RuntimeInitNanos: 5, AnalyzeNanos: 7, CloseNanos: 11,
	})
	store.add(SemanticAnalysisLifecycleEvidence{
		Invocations: 1, ModuleInstantiations: 1, InitializeCalls: 1, RuntimeInitCalls: 1, Failures: 1,
		COWHits: 1, FreshFallbacks: 1, COWCloneNanos: 37,
		InstantiateNanos: 13, InitializeNanos: 17, RuntimeInitNanos: 19, AnalyzeNanos: 23, CloseNanos: 29,
	})
	got := store.get()
	if got.SchemaVersion != SemanticAnalysisLifecycleSchemaVersion || got.Invocations != 2 || got.ModuleInstantiations != 2 ||
		got.InitializeCalls != 2 || got.RuntimeInitCalls != 2 || got.Successes != 1 || got.Failures != 1 ||
		got.PreparedProvisions != 1 || got.PreparedProvisionFailures != 1 || got.PreparedHits != 1 || got.COWHits != 1 || got.FreshFallbacks != 1 ||
		got.InstantiateNanos != 15 || got.InitializeNanos != 20 || got.RuntimeInitNanos != 24 ||
		got.AnalyzeNanos != 30 || got.CloseNanos != 40 || got.PreparedProvisionNanos != 31 || got.COWCloneNanos != 37 {
		t.Fatalf("evidence=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"source", "result", "request", "response"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("lifecycle evidence leaked body-bearing field %q: %s", forbidden, encoded)
		}
	}
}

func TestNilEngineSemanticAnalysisLifecycleEvidenceIsTypedEmpty(t *testing.T) {
	var engine *Engine
	got := engine.SemanticAnalysisLifecycleEvidence()
	if got.SchemaVersion != SemanticAnalysisLifecycleSchemaVersion || got.Invocations != 0 {
		t.Fatalf("evidence=%+v", got)
	}
}

func TestSemanticAnalysisSessionCloseDropsDiagnosticBodies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &SemanticAnalysisSession{
		engine: &Engine{}, context: ctx, cancel: cancel, started: time.Now(),
		stderr: &boundedDiagnostic{buffer: []byte("sensitive source excerpt")}, stdout: &forbiddenStdout{},
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.module != nil || len(session.stderr.buffer) != 0 || !session.closed {
		t.Fatalf("closed session retained body-bearing state")
	}
}

func TestSemanticAnalysisSessionRejectsAuthorityBearingEngine(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	engine := &Engine{config: config, workspaceBinding: &workspaceBinding{}}
	_, err := engine.NewSemanticAnalysisSession(context.Background(), SemanticAnalysisSessionLimits{
		MaxRequests: 1, MaxCumulativeRequestBytes: 1, MaxDuration: time.Second,
	})
	if !errors.Is(err, ErrSemanticAnalysisSessionAuthority) {
		t.Fatalf("err=%v", err)
	}
}

func TestEngineCloseReleasesPreparedNumpyInput(t *testing.T) {
	ctx := context.Background()
	input := &PreparedNumpyInput{descriptorJSON: []byte("descriptor"), body: []byte("prepared-body")}
	engine := &Engine{runtime: wazerort.NewRuntime(ctx), preparedNumpyInput: input}
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if engine.preparedNumpyInput != nil || input.body != nil || input.descriptorJSON != nil {
		t.Fatalf("prepared input retained after close: engine=%p body=%d descriptor=%d", engine.preparedNumpyInput, len(input.body), len(input.descriptorJSON))
	}
}

func TestSemanticAnalysisSessionLeasesEngineClose(t *testing.T) {
	ctx := context.Background()
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	engine := &Engine{runtime: wazerort.NewRuntime(ctx), config: config}
	session, err := engine.NewSemanticAnalysisSession(ctx, SemanticAnalysisSessionLimits{
		MaxRequests: 1, MaxCumulativeRequestBytes: uint64(engine.config.MaxRequestBytes), MaxDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(ctx); !errors.Is(err, ErrSemanticAnalysisSessionsActive) {
		t.Fatalf("close with active session err=%v", err)
	}
	if _, err := engine.NewSemanticAnalysisSession(ctx, SemanticAnalysisSessionLimits{
		MaxRequests: 1, MaxCumulativeRequestBytes: uint64(engine.config.MaxRequestBytes), MaxDuration: time.Second,
	}); !errors.Is(err, ErrSemanticAnalysisEngineClosing) {
		t.Fatalf("new session after close began err=%v", err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSemanticRuntimeRejectsAuthorityBearingEngine(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.PreparedRuntime = true
	engine := &Engine{config: config, workspaceBinding: &workspaceBinding{}}
	if err := engine.PrepareSemanticRuntime(context.Background()); !errors.Is(err, ErrSemanticAnalysisSessionAuthority) {
		t.Fatalf("authority-bearing preprovision err=%v", err)
	}
}
