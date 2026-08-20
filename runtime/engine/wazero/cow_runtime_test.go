package wazero

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

type fakeCOWPreparedRuntime struct {
	closed    bool
	lifecycle cowCloneLifecycle
	err       error
}

func (runtime *fakeCOWPreparedRuntime) prepare(context.Context, *Engine) (*preparedInstance, cowCloneLifecycle, error) {
	return nil, runtime.lifecycle, runtime.err
}

func (runtime *fakeCOWPreparedRuntime) close() error {
	runtime.closed = true
	return nil
}

func (*fakeCOWPreparedRuntime) imageState() PreparedImageState {
	return PreparedImageState{Available: true}
}

func TestCOWCloseRetainsRuntimeUntilActiveLeaseReleased(t *testing.T) {
	fake := &fakeCOWPreparedRuntime{}
	engine := &Engine{cowRuntime: fake}
	_, release, selected, err := engine.acquireCOWRuntime()
	if err != nil || !selected {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	if err := engine.closeCOWRuntime(); !errors.Is(err, errCOWRunsActive) {
		t.Fatalf("close with active lease=%v", err)
	}
	if fake.closed || engine.cowRuntime == nil {
		t.Fatal("active close discarded COW runtime ownership")
	}
	release()
	release()
	if err := engine.closeCOWRuntime(); err != nil {
		t.Fatal(err)
	}
	if !fake.closed || engine.cowRuntime != nil {
		t.Fatal("released COW runtime was not closed")
	}
	if _, _, _, err := engine.acquireCOWRuntime(); !errors.Is(err, errCOWEngineClosing) {
		t.Fatalf("acquire after close=%v", err)
	}
}

func TestSemanticSessionCountsFailedCOWCloneLifecycle(t *testing.T) {
	cloneErr := errors.New("clone failed after initialize")
	fake := &fakeCOWPreparedRuntime{lifecycle: cowCloneLifecycle{ModuleInstantiations: 1, InitializeCalls: 1}, err: cloneErr}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, PreparedRuntime: true, MemoryCOW: true}
	engine := &Engine{config: config, preparedInitialized: true, cowRuntime: fake}
	session, err := engine.NewSemanticAnalysisSession(context.Background(), SemanticAnalysisSessionLimits{
		MaxRequests: 1, MaxCumulativeRequestBytes: uint64(config.MaxRequestBytes), MaxDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.AnalyzeSemantic(cancelled, []byte(`{"schema_version":"test"}`)); err == nil {
		t.Fatal("cancelled failed clone unexpectedly succeeded")
	}
	evidence := engine.SemanticAnalysisLifecycleEvidence()
	if evidence.ModuleInstantiations != 1 || evidence.InitializeCalls != 1 || evidence.Failures != 1 {
		t.Fatalf("failed clone lifecycle=%+v", evidence)
	}
}
