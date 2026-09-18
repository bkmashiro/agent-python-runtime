package wazero

import (
	"context"
	"errors"
	"sync"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func TestPreparedFamilyCompilationCacheOwnsSequentialConsumersAndIsolation(t *testing.T) {
	ctx := context.Background()
	cache := wazerort.NewCompilationCache()
	config := runtimeconfig.DefaultRunConfig()
	parent, err := newEngine(ctx, tinyPreparedCacheWasm(), config, nil, nil, nil, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := newPreparedFamilyLifecycle(3, 3)
	if err != nil {
		t.Fatal(err)
	}
	family := &PreparedFamily{
		wasm: tinyPreparedCacheWasm(), lifecycle: lifecycle, compilationCache: cache,
		parent: parent, runners: make(map[uint64]*preparedFamilyRunner),
		invocations: make(map[uint64]runtimeconfig.InvocationRef), records: make(map[uint64]PreparedMemberRecord),
	}
	newMember := func(name string) (*preparedFamilyRunner, *Engine, error) {
		memberID, reserveErr := lifecycle.reserve()
		if reserveErr != nil {
			return nil, nil, reserveErr
		}
		engine, engineErr := newEngine(ctx, tinyPreparedCacheWasm(), config, nil, nil, nil, nil, cache)
		if engineErr != nil {
			_ = lifecycle.release(memberID)
			return nil, nil, engineErr
		}
		ref := runtimeconfig.InvocationRef{AgentRunID: "family", InvocationID: name, InvocationAttempt: 1, ExecutionID: name}
		runner := newPreparedFamilyRunner(engine, ref, lifecycle, memberID)
		family.runners[memberID] = runner
		family.invocations[memberID] = ref
		return runner, engine, nil
	}
	first, firstEngine, err := newMember("first")
	if err != nil {
		t.Fatal(err)
	}
	_, secondEngine, err := newMember("second")
	if err != nil {
		t.Fatal(err)
	}
	firstModule, err := instantiateTinyCacheModule(ctx, firstEngine)
	if err != nil {
		t.Fatal(err)
	}
	secondModule, err := instantiateTinyCacheModule(ctx, secondEngine)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTinyCacheValue(ctx, firstModule, 11); err != nil {
		t.Fatal(err)
	}
	if err := writeTinyCacheValue(ctx, secondModule, 22); err != nil {
		t.Fatal(err)
	}
	if got := readTinyCacheValue(ctx, firstModule); got != 11 {
		t.Fatalf("first guest memory=%d, want 11", got)
	}
	if got := readTinyCacheValue(ctx, secondModule); got != 22 {
		t.Fatalf("second guest memory=%d, want 22", got)
	}
	if err := firstModule.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}

	_, thirdEngine, err := newMember("third")
	if err != nil {
		t.Fatal(err)
	}
	thirdModule, err := instantiateTinyCacheModule(ctx, thirdEngine)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTinyCacheValue(ctx, thirdModule); got != 0 {
		t.Fatalf("sequential consumer inherited memory=%d, want 0", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := secondModule.ExportedFunction("read0").Call(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled guest call err=%v", err)
	}
	if got := readTinyCacheValue(ctx, thirdModule); got != 0 {
		t.Fatalf("cancelled consumer changed other guest state=%d, want 0", got)
	}
	if err := secondModule.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := thirdModule.Close(ctx); err != nil {
		t.Fatal(err)
	}

	var closeWait sync.WaitGroup
	closeErrors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		closeWait.Add(1)
		go func() {
			defer closeWait.Done()
			closeErrors <- family.Close(ctx)
		}()
	}
	closeWait.Wait()
	close(closeErrors)
	for closeErr := range closeErrors {
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	state := family.State()
	if !state.Closed || state.Created != 3 || state.Active != 0 || state.Terminal != 3 {
		t.Fatalf("family state=%+v", state)
	}
}

func instantiateTinyCacheModule(ctx context.Context, engine *Engine) (api.Module, error) {
	return engine.runtime.InstantiateModule(ctx, engine.compiled, wazerort.NewModuleConfig().WithStartFunctions())
}

func writeTinyCacheValue(ctx context.Context, module api.Module, value uint64) error {
	_, err := module.ExportedFunction("write0").Call(ctx, value)
	return err
}

func readTinyCacheValue(ctx context.Context, module api.Module) uint64 {
	values, err := module.ExportedFunction("read0").Call(ctx)
	if err != nil || len(values) != 1 {
		return ^uint64(0)
	}
	return values[0]
}

func tinyPreparedCacheWasm() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x09, 0x02, 0x60, 0x00, 0x01, 0x7f, 0x60, 0x01, 0x7f, 0x00,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
		0x07, 0x1b, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x05, 'r', 'e', 'a', 'd', '0', 0x00, 0x00,
		0x06, 'w', 'r', 'i', 't', 'e', '0', 0x00, 0x01,
		0x0a, 0x13, 0x02,
		0x07, 0x00, 0x41, 0x00, 0x2d, 0x00, 0x00, 0x0b,
		0x09, 0x00, 0x41, 0x00, 0x20, 0x00, 0x3a, 0x00, 0x00, 0x0b,
	}
}
