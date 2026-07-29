//go:build linux

package scheduler

import (
	"os"
	"testing"
)

func TestCurrentCgroupV2MemoryReader(t *testing.T) {
	if os.Getenv("APYRUN_S7_RUN") != "1" {
		t.Skip("set APYRUN_S7_RUN=1 for the real Linux cgroup-v2 smoke")
	}
	reader, err := NewCurrentCgroupV2MemoryReader()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	if snapshot.CurrentBytes == 0 || snapshot.MaximumBytes == 0 || snapshot.UtilizationBPS() > 10000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	t.Logf("LIVE_MEMORY current=%d max=%d utilization_bps=%d oom=%d oom_kill=%d psi_some_avg10_bps=%d psi_full_avg10_bps=%d",
		snapshot.CurrentBytes, snapshot.MaximumBytes, snapshot.UtilizationBPS(), snapshot.Events.OOM, snapshot.Events.OOMKill,
		snapshot.Pressure.Some.Avg10BPS, snapshot.Pressure.Full.Avg10BPS)
}
