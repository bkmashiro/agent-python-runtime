//go:build linux

package wazero

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestEngineBuildsCOWReadySingleUsePool(t *testing.T) {
	wasm := fixedMemoryReactor(t)
	runner, err := (Factory{
		PreparedCapacity: 2,
		Strategy:         enginecontract.StrategyCOWReadySingleUse,
	}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	engine := runner.(*Engine)
	properties := engine.Properties()
	if err := properties.Validate(); err != nil {
		t.Fatalf("invalid COW properties: %v", err)
	}
	if properties.ActiveStrategy != enginecontract.StrategyCOWReadySingleUse || properties.Fallback {
		t.Fatalf("unexpected COW properties: %+v", properties)
	}
	if engine.PreparedReady() != 2 || engine.cowRuntime == nil {
		t.Fatalf("COW pool was not filled: ready=%d runtime=%T", engine.PreparedReady(), engine.cowRuntime)
	}
	first := <-engine.pool.ready
	second := <-engine.pool.ready
	firstView, firstOK := first.module.Memory().Read(0, first.module.Memory().Size())
	secondView, secondOK := second.module.Memory().Read(0, second.module.Memory().Size())
	if !firstOK || !secondOK || len(firstView) == 0 || len(firstView) != len(secondView) {
		t.Fatal("COW slots do not expose equal fixed memories")
	}
	offset := len(firstView) - 1
	baseline := secondView[offset]
	firstView[offset] = baseline + 1
	if secondView[offset] != baseline {
		t.Fatal("COW slot write leaked to sibling")
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := engine.checkoutModule(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty COW pool silently fell back instead of waiting: %v", err)
	}
	if err := first.module.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.module.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEngineObservesServedCOWMappingBeforeClose(t *testing.T) {
	sink := &recordingFootprintSink{sample: true}
	reclaimSink := &recordingReclaimSink{observe: true}
	runner, err := (Factory{
		PreparedCapacity: 1,
		Strategy:         enginecontract.StrategyCOWReadySingleUse,
		FootprintSink:    sink,
		ReclaimSink:      reclaimSink,
	}).New(context.Background(), fixedMemoryReactor(t), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	ctx, err := enginecontract.WithAttemptIdentity(context.Background(), "linux:attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"run","code":"pass","inputs":{}}`)
	_, _ = runner.Run(ctx, request, "")
	if len(sink.observations) != 1 {
		t.Fatalf("footprint observations = %#v", sink.observations)
	}
	observation := sink.observations[0]
	if err := observation.Validate(); err != nil {
		t.Fatalf("invalid footprint observation: %#v: %v", observation, err)
	}
	if observation.Status != enginecontract.FootprintObserved || observation.AttemptID != "linux:attempt:1" ||
		observation.Memory.VirtualBytes != wasmLinearPageSize || observation.Memory.MappingCount == 0 {
		t.Fatalf("unexpected footprint observation: %#v", observation)
	}
	if len(reclaimSink.observations) != 1 {
		t.Fatalf("reclaim observations = %#v", reclaimSink.observations)
	}
	reclaim := reclaimSink.observations[0]
	if err := reclaim.Validate(); err != nil || reclaim.Status != enginecontract.ReclaimReleased || reclaim.AttemptID != "linux:attempt:1" {
		t.Fatalf("unexpected reclaim observation: %#v err=%v", reclaim, err)
	}
}

func TestEngineGrowsOneCOWPoolWithoutCreatingAnotherBaseline(t *testing.T) {
	wasm := fixedMemoryReactor(t)
	runner, err := (Factory{
		PreparedCapacity:    2,
		PreparedMaxCapacity: 8,
		Strategy:            enginecontract.StrategyCOWReadySingleUse,
	}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	engine := runner.(*Engine)
	defer engine.Close(context.Background())
	baseline := engine.cowRuntime
	if err := engine.GrowPreparedCapacity(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	if engine.cowRuntime != baseline {
		t.Fatal("growing the pool replaced the canonical COW baseline")
	}
	if engine.PreparedReady() != 8 || engine.PreparedCapacity() != 8 {
		t.Fatalf("grown COW pool is incomplete: ready=%d capacity=%d", engine.PreparedReady(), engine.PreparedCapacity())
	}
	if workers := engine.PreparedRefillWorkers(); workers != 4 {
		t.Fatalf("refill worker bound=%d, want 4", workers)
	}
	linuxRuntime := engine.cowRuntime.(*linuxCOWPreparedRuntime)
	linuxRuntime.image.mu.Lock()
	mappings := linuxRuntime.image.mappings
	linuxRuntime.image.mu.Unlock()
	if mappings != 8 {
		t.Fatalf("one baseline did not own all mappings: %d", mappings)
	}
	if err := engine.GrowPreparedCapacity(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("pool grew past its configured maximum: %v", err)
	}
}

func TestCOWCapacityHasASeparateLargeHardBound(t *testing.T) {
	runner, err := (Factory{
		PreparedCapacity:    8,
		PreparedMaxCapacity: 8,
		Strategy:            enginecontract.StrategyCOWReadySingleUse,
	}).New(context.Background(), fixedMemoryReactor(t), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	if got := runner.(*Engine).PreparedReady(); got != 8 {
		t.Fatalf("large COW pool ready=%d, want 8", got)
	}
}

func TestCOWFullImageVerificationIsExplicitlyOptIn(t *testing.T) {
	var phases []string
	runner, err := (Factory{
		PreparedCapacity:       1,
		Strategy:               enginecontract.StrategyCOWReadySingleUse,
		VerifyCOWPreparedImage: true,
		Observer: func(observation Observation) {
			phases = append(phases, observation.Phase)
		},
	}).New(context.Background(), fixedMemoryReactor(t), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	linuxRuntime := runner.(*Engine).cowRuntime.(*linuxCOWPreparedRuntime)
	if linuxRuntime.verifyDigest == nil {
		t.Fatal("explicit full-image verification did not retain a canonical digest")
	}
	seen := false
	for _, phase := range phases {
		seen = seen || phase == "pool_prepare_cow_verify"
	}
	if !seen {
		t.Fatalf("full-image verification phase is missing: %v", phases)
	}
}

func TestEngineRejectsCOWWhenMemoryIsNotFixed(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Factory{PreparedCapacity: 1, Strategy: enginecontract.StrategyCOWReadySingleUse}).New(
		context.Background(), wasm, runtimeconfig.DefaultRunConfig(),
	)
	if err == nil || !strings.Contains(err.Error(), "not eligible for COW") {
		t.Fatalf("non-fixed memory entered COW mode: %v", err)
	}
}

func fixedMemoryReactor(t *testing.T) []byte {
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
	return fixed
}
