package wazero

import (
	"context"
	"errors"
	"testing"
)

type fakeCOWPreparedRuntime struct {
	closed bool
}

func (*fakeCOWPreparedRuntime) prepare(context.Context, *Engine) (*preparedInstance, error) {
	return nil, nil
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
