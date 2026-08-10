//go:build linux

package scheduler

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	wabinbinary "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

func TestReclaimEvidenceBridgeConsumesRealCOWEvidence(t *testing.T) {
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 1})
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "bridge:cow:1"
	if err := bridge.Track(attemptID); err != nil {
		t.Fatal(err)
	}
	runner, err := (wazeroengine.Factory{
		PreparedCapacity: 1,
		Strategy:         enginecontract.StrategyCOWReadySingleUse,
		FootprintSink:    bridge.FootprintSink(),
		ReclaimSink:      bridge.ReclaimSink(),
	}).New(context.Background(), schedulerFixedMemoryReactor(t), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	ctx, err := enginecontract.WithAttemptIdentity(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"run","code":"pass","inputs":{}}`)
	_, _ = runner.Run(ctx, request, "")
	report, err := bridge.Observe(context.Background(), Termination{AttemptID: attemptID, ExecutorTerminated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.ExecutorTerminated || report.ObservedFootprintBytes == 0 || report.ReclaimedBytes != report.ObservedFootprintBytes {
		t.Fatalf("report=%#v", report)
	}
}

func schedulerFixedMemoryReactor(t *testing.T) []byte {
	t.Helper()
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	oldMemorySection := []byte{0x05, 0x03, 0x01, 0x00, 0x01}
	fixedMemorySection := []byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x01}
	fixed := bytes.Replace(wasm, oldMemorySection, fixedMemorySection, 1)
	if bytes.Equal(fixed, wasm) {
		t.Fatal("reactor fixture memory section was not patched")
	}
	module, err := wabinbinary.DecodeModule(fixed, wabinwasm.CoreFeaturesV2)
	if err != nil {
		t.Fatal(err)
	}
	functionIndex := uint32(len(module.FunctionSection))
	module.FunctionSection = append(module.FunctionSection, 1)
	module.CodeSection = append(module.CodeSection, &wabinwasm.Code{Body: []byte{byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeEnd)}})
	module.ExportSection = append(module.ExportSection, &wabinwasm.Export{Name: "runtime_validate_source", Type: wabinwasm.ExternTypeFunc, Index: functionIndex})
	return wabinbinary.EncodeModule(module)
}
