//go:build linux

package scheduler

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	wabinbinary "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

func TestProfiledBatchExecutorRealCOWMixedLoad(t *testing.T) {
	if os.Getenv("APYRUN_S12_RUN") != "1" {
		t.Skip("set APYRUN_S12_RUN=1 for the real Guest-COW batch gate")
	}
	reader, err := NewCurrentCgroupV2MemoryReader()
	if err != nil {
		t.Fatal(err)
	}
	limitSnapshot, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	const mib = uint64(1 << 20)
	profileOptions := profileConfig()
	profileOptions.HardBytes = limitSnapshot.MaximumBytes
	profileOptions.UnknownReservationBytes = 8 * mib
	profileOptions.PerAttemptMarginBytes = mib
	profileOptions.ReservationQuantileBPS = 9500
	profileOptions.ColdRuns = 16
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 4, Profiles: store})
	if err != nil {
		t.Fatal(err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Timeout = 20 * time.Second
	runner, err := (wazeroengine.Factory{
		PreparedCapacity: 4, PreparedMaxCapacity: 4, Strategy: enginecontract.StrategyCOWReadySingleUse,
		FootprintSink: bridge.FootprintSink(), ReclaimSink: bridge.ReclaimSink(),
	}).New(context.Background(), schedulerFiniteDirtyReactor(25, uint64(os.Getpagesize()), 512, 10_000_000), runConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	baseline, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	target := baseline.CurrentBytes + 8*mib
	high := baseline.CurrentBytes + 16*mib
	critical := baseline.CurrentBytes + 96*mib
	if critical >= baseline.MaximumBytes || baseline.MaximumBytes != limitSnapshot.MaximumBytes {
		t.Fatalf("cgroup limit is too small or changed during setup: before=%#v ready=%#v", limitSnapshot, baseline)
	}
	scheduler, err := New(Config{
		TargetBytes: target, HighBytes: high, CriticalBytes: critical, HardBytes: baseline.MaximumBytes,
		MaxTasks: 16, MaxAttempts: 32, RetryMarginBytes: mib, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	greedOptions := greedConfig()
	greedOptions.MinimumAttempts = 1
	controller, err := NewGreedController(greedOptions)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 4, MaxRequestBytes: runConfig.MaxRequestBytes})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCoordinatedVictimDispatcher(CoordinatedVictimDispatcherConfig{
		Scheduler: scheduler, Canceler: worker, Observer: bridge, Tracker: bridge, Sampler: worker,
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewLiveMemoryControlLoop(LiveMemoryControlLoopConfig{
		Scheduler: scheduler, Profiles: store, Controller: controller, Reader: reader, Dispatcher: dispatcher,
		Interval: time.Millisecond, MaxSamples: 20_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProfiledBatchExecutor(ProfiledBatchExecutorConfig{
		Scheduler: scheduler, Profiles: store, Worker: worker, ControlLoop: control,
		PollInterval: time.Millisecond, MaxPayloadBytes: runConfig.MaxRequestBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"run","code":"loop","inputs":{}}`)
	classes := []string{"tiny", "small", "medium", "large"}
	batch := make([]ProfiledExecution, 8)
	for index := range batch {
		profile := testProfile("a", "python_eval_"+classes[index%len(classes)], RequestSizeSmall)
		batch[index] = ProfiledExecution{
			Spec:    ProfiledTaskSpec{TaskID: "cow:task:" + string(rune('a'+index)), Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1},
			Request: request,
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := executor.Run(ctx, batch)
	if err != nil {
		t.Fatalf("Run() error=%v scheduler=%#v profiles=%#v", err, scheduler.Snapshot(), store.Snapshot())
	}
	if len(results) != len(batch) {
		t.Fatalf("results=%d want=%d", len(results), len(batch))
	}
	for _, result := range results {
		if result.Err != nil || string(result.Response) != schedulerGuestResponse {
			t.Fatalf("result=%#v", result)
		}
	}
	snapshot := scheduler.Snapshot()
	reclaimed := 0
	for _, attempt := range snapshot.Attempts {
		if attempt.State == AttemptReclaimed {
			reclaimed++
		}
	}
	if reclaimed == 0 || snapshot.ReservedBytes != 0 || len(snapshot.Queued) != 0 || store.Snapshot().ObservedSamples == 0 {
		t.Fatalf("reclaimed=%d scheduler=%#v profiles=%#v", reclaimed, snapshot, store.Snapshot())
	}
}

const schedulerGuestResponse = `{"status":"ok","result":{},"receipts":[],"metrics":{"guest_time_ms":0,"capability_calls":0,"result_bytes":2},"error":null}`

func schedulerFiniteDirtyReactor(dirtyPercent int, pageSize uint64, memoryPages uint32, iterations int32) []byte {
	i32 := wabinwasm.ValueTypeI32
	body := make([]byte, 0, 128*1024)
	memoryBytes := uint64(memoryPages) * 65536
	dirtyPages := (memoryBytes * uint64(dirtyPercent) / 100) / pageSize
	for page := uint64(0); page < dirtyPages; page++ {
		body = append(body, byte(wabinwasm.OpcodeI32Const))
		body = append(body, leb128.EncodeInt32(int32(page*pageSize))...)
		body = append(body, byte(wabinwasm.OpcodeI32Const), 1, byte(wabinwasm.OpcodeI32Store8), 0, 0)
	}
	body = append(body, byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeI32Const))
	body = append(body, leb128.EncodeInt32(iterations)...)
	body = append(body, byte(wabinwasm.OpcodeI32Store), 2, 0)
	body = append(body,
		byte(wabinwasm.OpcodeBlock), 0x40,
		byte(wabinwasm.OpcodeLoop), 0x40,
		byte(wabinwasm.OpcodeI32Const), 0,
		byte(wabinwasm.OpcodeI32Load), 2, 0,
		byte(wabinwasm.OpcodeI32Eqz),
		byte(wabinwasm.OpcodeBrIf), 1,
		byte(wabinwasm.OpcodeI32Const), 0,
		byte(wabinwasm.OpcodeI32Const), 0,
		byte(wabinwasm.OpcodeI32Load), 2, 0,
		byte(wabinwasm.OpcodeI32Const), 1,
		byte(wabinwasm.OpcodeI32Sub),
		byte(wabinwasm.OpcodeI32Store), 2, 0,
		byte(wabinwasm.OpcodeBr), 0,
		byte(wabinwasm.OpcodeEnd),
		byte(wabinwasm.OpcodeEnd),
	)
	frame := make([]byte, 4+len(schedulerGuestResponse))
	binary.LittleEndian.PutUint32(frame, uint32(len(schedulerGuestResponse)))
	copy(frame[4:], schedulerGuestResponse)
	for index, value := range frame {
		body = append(body, byte(wabinwasm.OpcodeI32Const))
		body = append(body, leb128.EncodeInt32(int32(1024+index))...)
		body = append(body, byte(wabinwasm.OpcodeI32Const))
		body = append(body, leb128.EncodeInt32(int32(value))...)
		body = append(body, byte(wabinwasm.OpcodeI32Store8), 0, 0)
	}
	body = append(body, byte(wabinwasm.OpcodeI32Const))
	body = append(body, leb128.EncodeInt32(1024)...)
	body = append(body, byte(wabinwasm.OpcodeEnd))
	return wabinbinary.EncodeModule(&wabinwasm.Module{
		TypeSection: []*wabinwasm.FunctionType{
			{},
			{Params: []wabinwasm.ValueType{i32, i32}, Results: []wabinwasm.ValueType{i32}},
			{Params: []wabinwasm.ValueType{i32}, Results: []wabinwasm.ValueType{i32}},
			{Params: []wabinwasm.ValueType{i32}},
		},
		FunctionSection: []wabinwasm.Index{0, 1, 1, 2, 3, 1},
		MemorySection:   &wabinwasm.Memory{Min: memoryPages, Max: memoryPages, IsMaxEncoded: true},
		ExportSection: []*wabinwasm.Export{
			{Name: "memory", Type: wabinwasm.ExternTypeMemory, Index: 0},
			{Name: "_initialize", Type: wabinwasm.ExternTypeFunc, Index: 0},
			{Name: "runtime_init", Type: wabinwasm.ExternTypeFunc, Index: 1},
			{Name: "runtime_prepare", Type: wabinwasm.ExternTypeFunc, Index: 2},
			{Name: "alloc", Type: wabinwasm.ExternTypeFunc, Index: 3},
			{Name: "dealloc", Type: wabinwasm.ExternTypeFunc, Index: 4},
			{Name: "execute", Type: wabinwasm.ExternTypeFunc, Index: 5},
		},
		CodeSection: []*wabinwasm.Code{
			{Body: []byte{byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 8, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeEnd)}},
			{Body: body},
		},
	})
}
