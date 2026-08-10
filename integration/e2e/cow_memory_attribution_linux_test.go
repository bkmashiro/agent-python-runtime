//go:build linux

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

type cowDirtyProbeResult struct {
	RequestedBytes     uint64 `json:"requested_bytes"`
	ReadyMappings      uint32 `json:"ready_mappings"`
	ActiveMappings     uint32 `json:"active_mappings"`
	ActiveRSSBytes     uint64 `json:"active_rss_bytes"`
	ActivePSSBytes     uint64 `json:"active_pss_bytes"`
	ActivePrivateDirty uint64 `json:"active_private_dirty_bytes"`
	FinalPrivateDirty  uint64 `json:"final_private_dirty_bytes"`
}

type cowDirtyProbeEvidence struct {
	WarmupProfile string                          `json:"warmup_profile,omitempty"`
	PreparedImage wazeroengine.PreparedImageState `json:"prepared_image"`
	Samples       []cowDirtyProbeResult           `json:"samples"`
}

func TestCOWReadySingleUseProductionArtifactDeterministicDirtyPages(t *testing.T) {
	requireCOWFixedArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 15 * time.Second
	warmupProfile := os.Getenv("AGENT_RUNTIME_COW_WARMUP_PROFILE")
	if warmupProfile != "" && warmupProfile != wazeroengine.COWWarmupRequestShellV1 {
		t.Fatalf("unsupported probe warmup profile %q", warmupProfile)
	}
	instance := newEngineWithFactory(t, config, wazeroengine.Factory{
		PreparedCapacity:      4,
		PreparedMaxCapacity:   4,
		PreparedRefillWorkers: 1,
		Strategy:              enginecontract.StrategyCOWReadySingleUse,
		COWWarmupProfile:      warmupProfile,
	})
	prepared, ok := instance.(interface{ PreparedReady() int })
	if !ok {
		t.Fatal("prepared readiness diagnostics are unavailable")
	}
	collector := runtimeevidence.DefaultLinuxCollector()
	code := "import time\nsize = inputs['size_bytes']\nbuffer = bytearray(size)\nfor offset in range(0, size, 4096):\n    buffer[offset] = 1\ntime.sleep(1.0)\nresult = {'size': len(buffer), 'checksum': (buffer[0] + buffer[-1]) if buffer else 0}"
	results := make([]cowDirtyProbeResult, 0, 6)
	for _, sizeMiB := range []uint64{0, 1, 4, 8, 16, 32} {
		waitPreparedReadyForProbe(t, prepared, 4)
		ready, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
		if err != nil {
			t.Fatal(err)
		}
		if ready.MappingCount != 4 || measuredMappingValue(t, ready.PrivateDirtyBytes) != 0 {
			t.Fatalf("dirty probe did not start from four clean ready mappings: %+v", ready)
		}
		request, err := json.Marshal(map[string]any{
			"run_id": fmt.Sprintf("cow-dirty-%d", sizeMiB),
			"code":   code,
			"inputs": map[string]any{"size_bytes": sizeMiB << 20},
		})
		if err != nil {
			t.Fatal(err)
		}
		type runResult struct {
			payload []byte
			err     error
		}
		done := make(chan runResult, 1)
		go func() {
			payload, runErr := instance.Run(context.Background(), request, "")
			done <- runResult{payload: payload, err: runErr}
		}()
		time.Sleep(300 * time.Millisecond)
		active, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
		if err != nil {
			t.Fatal(err)
		}
		activeDirty := measuredMappingValue(t, active.PrivateDirtyBytes)
		if activeDirty == 0 {
			t.Fatalf("%d MiB guest allocation produced no observable active private dirty pages: %+v", sizeMiB, active)
		}
		run := <-done
		if run.err != nil {
			t.Fatalf("%d MiB dirty request: %v", sizeMiB, run.err)
		}
		var response guestResponse
		if err := json.Unmarshal(run.payload, &response); err != nil {
			t.Fatalf("decode %d MiB response: %v: %s", sizeMiB, err, run.payload)
		}
		if response.Status != "ok" {
			t.Fatalf("%d MiB dirty request returned guest error: %#v", sizeMiB, response.Error)
		}
		waitPreparedReadyForProbe(t, prepared, 4)
		final, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
		if err != nil {
			t.Fatal(err)
		}
		finalDirty := measuredMappingValue(t, final.PrivateDirtyBytes)
		if final.MappingCount != 4 || finalDirty != 0 {
			t.Fatalf("%d MiB dirty request did not return to four clean ready mappings: %+v", sizeMiB, final)
		}
		results = append(results, cowDirtyProbeResult{
			RequestedBytes: sizeMiB << 20, ReadyMappings: ready.MappingCount, ActiveMappings: active.MappingCount,
			ActiveRSSBytes: measuredMappingValue(t, active.RSSBytes), ActivePSSBytes: measuredMappingValue(t, active.PSSBytes),
			ActivePrivateDirty: activeDirty, FinalPrivateDirty: finalDirty,
		})
	}
	imageState := instance.(interface {
		PreparedImageState() wazeroengine.PreparedImageState
	}).PreparedImageState()
	encoded, err := json.Marshal(cowDirtyProbeEvidence{WarmupProfile: warmupProfile, PreparedImage: imageState, Samples: results})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("COW_DIRTY_PROBE=%s", encoded)
}

func waitPreparedReadyForProbe(t *testing.T, prepared interface{ PreparedReady() int }, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for prepared.PreparedReady() != want {
		if time.Now().After(deadline) {
			t.Fatalf("prepared ready=%d, want %d", prepared.PreparedReady(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func measuredMappingValue(t *testing.T, metric runtimeevidence.Metric) uint64 {
	t.Helper()
	if metric.Status != runtimeevidence.MetricMeasured || metric.Value == nil {
		t.Fatalf("mapping metric is unavailable: %+v", metric)
	}
	return *metric.Value
}
