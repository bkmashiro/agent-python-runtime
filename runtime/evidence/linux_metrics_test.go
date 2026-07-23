package evidence

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLinuxCollectorReadsProcessAndCgroupV2Metrics(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	cgroupRoot := filepath.Join(root, "cgroup")
	writeFixture(t, filepath.Join(procRoot, "sys/kernel/osrelease"), "6.8.0-fixture\n")
	writeFixture(t, filepath.Join(procRoot, "self/status"), "Name:\tfixture\nVmSize:\t2000 kB\nVmRSS:\t1000 kB\nVmSwap:\t3 kB\n")
	writeFixture(t, filepath.Join(procRoot, "self/smaps_rollup"), "Pss:\t900 kB\nPrivate_Clean:\t100 kB\nPrivate_Dirty:\t800 kB\n")
	writeFixture(t, filepath.Join(procRoot, "self/stat"), "123 (agent worker) R 1 2 3 4 5 6 17 8 4 11\n")
	writeFixture(t, filepath.Join(procRoot, "self/maps"), "a-b r--p 0 00:00 0\nc-d rw-p 0 00:00 0\n")
	if err := os.MkdirAll(filepath.Join(procRoot, "self/fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0", "1", "7"} {
		writeFixture(t, filepath.Join(procRoot, "self/fd", name), "")
	}
	writeFixture(t, filepath.Join(procRoot, "self/cgroup"), "0::/user.slice/agent.scope\n")
	leaf := filepath.Join(cgroupRoot, "user.slice/agent.scope")
	writeFixture(t, filepath.Join(leaf, "memory.current"), "2048\n")
	writeFixture(t, filepath.Join(leaf, "memory.peak"), "4096\n")
	writeFixture(t, filepath.Join(leaf, "memory.swap.current"), "0\n")
	writeFixture(t, filepath.Join(leaf, "memory.events"), "low 0\nhigh 2\nmax 3\noom 4\noom_kill 1\n")
	writeFixture(t, filepath.Join(leaf, "memory.pressure"), "some avg10=0.00 avg60=0.00 avg300=0.00 total=99\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=7\n")

	snapshot, err := (LinuxCollector{ProcRoot: procRoot, CgroupRoot: cgroupRoot}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Environment.GOOS != "linux" || snapshot.Environment.KernelRelease != "6.8.0-fixture" || snapshot.Environment.CgroupVersion != "v2" || snapshot.Environment.PageSizeBytes == 0 {
		t.Fatalf("environment identity is incomplete: %#v", snapshot.Environment)
	}
	assertMeasured(t, snapshot.Process.RSSBytes, 1000*1024)
	assertMeasured(t, snapshot.Process.VirtualBytes, 2000*1024)
	assertMeasured(t, snapshot.Process.PSSBytes, 900*1024)
	assertMeasured(t, snapshot.Process.PrivateCleanBytes, 100*1024)
	assertMeasured(t, snapshot.Process.PrivateDirtyBytes, 800*1024)
	assertMeasured(t, snapshot.Process.SwapBytes, 3*1024)
	assertMeasured(t, snapshot.Process.MinorFaults, 17)
	assertMeasured(t, snapshot.Process.MajorFaults, 4)
	assertMeasured(t, snapshot.Process.FDCount, 3)
	assertMeasured(t, snapshot.Process.VMACount, 2)
	if snapshot.Cgroup.Version != "v2" {
		t.Fatalf("cgroup version = %q", snapshot.Cgroup.Version)
	}
	assertMeasured(t, snapshot.Cgroup.MemoryCurrentBytes, 2048)
	assertMeasured(t, snapshot.Cgroup.MemoryPeakBytes, 4096)
	assertMeasured(t, snapshot.Cgroup.MemoryEventsHighTotal, 2)
	assertMeasured(t, snapshot.Cgroup.MemoryEventsOOMTotal, 4)
	assertMeasured(t, snapshot.Cgroup.MemoryEventsOOMKillTotal, 1)
	assertMeasured(t, snapshot.Cgroup.PressureSomeTotalUS, 99)
	assertMeasured(t, snapshot.Cgroup.PressureFullTotalUS, 7)
}

func TestLinuxCollectorMarksOptionalSourcesUnavailableWithoutFakeZero(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	writeFixture(t, filepath.Join(procRoot, "sys/kernel/osrelease"), "6.8.0-fixture\n")
	writeFixture(t, filepath.Join(procRoot, "self/status"), "VmSize:\t2 kB\nVmRSS:\t1 kB\nVmSwap:\t0 kB\n")
	writeFixture(t, filepath.Join(procRoot, "self/stat"), "123 (fixture) R 1 2 3 4 5 6 1 0 0 0 0\n")
	writeFixture(t, filepath.Join(procRoot, "self/maps"), "a-b r--p 0 00:00 0\n")
	if err := os.MkdirAll(filepath.Join(procRoot, "self/fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(procRoot, "self/cgroup"), "1:name=systemd:/\n")

	snapshot, err := (LinuxCollector{ProcRoot: procRoot, CgroupRoot: filepath.Join(root, "cgroup")}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	assertUnavailable(t, snapshot.Process.PSSBytes, MetricUnsupported, ReasonSourceUnavailable)
	assertUnavailable(t, snapshot.Process.PrivateCleanBytes, MetricUnsupported, ReasonSourceUnavailable)
	if snapshot.Cgroup.Version != "none" {
		t.Fatalf("cgroup version = %q", snapshot.Cgroup.Version)
	}
	if snapshot.Environment.CgroupVersion != "none" {
		t.Fatalf("environment cgroup version = %q", snapshot.Environment.CgroupVersion)
	}
	assertUnavailable(t, snapshot.Cgroup.MemoryCurrentBytes, MetricUnsupported, ReasonNotApplicable)
	assertUnavailable(t, snapshot.Cgroup.MemoryEventsOOMKillTotal, MetricUnsupported, ReasonNotApplicable)
}

func TestLinuxCollectorRejectsMalformedRequiredProcMetrics(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	writeFixture(t, filepath.Join(procRoot, "sys/kernel/osrelease"), "6.8.0-fixture\n")
	writeFixture(t, filepath.Join(procRoot, "self/status"), "VmRSS:\tnot-a-number kB\n")
	writeFixture(t, filepath.Join(procRoot, "self/stat"), "malformed\n")
	writeFixture(t, filepath.Join(procRoot, "self/maps"), "")
	if err := os.MkdirAll(filepath.Join(procRoot, "self/fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(procRoot, "self/cgroup"), "1:name=systemd:/\n")
	if _, err := (LinuxCollector{ProcRoot: procRoot, CgroupRoot: filepath.Join(root, "cgroup")}).Collect(); err == nil {
		t.Fatal("malformed required proc metrics were accepted")
	}
}

func TestDefaultLinuxCollectorLiveSmoke(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux /proc and cgroup smoke")
	}
	snapshot, err := DefaultLinuxCollector().Collect()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRawMetric(snapshot.Process.RSSBytes); err != nil || snapshot.Process.RSSBytes.Status != MetricMeasured {
		t.Fatalf("live RSS was not measured: %#v err=%v", snapshot.Process.RSSBytes, err)
	}
	if snapshot.Cgroup.Version != "v2" && snapshot.Cgroup.Version != "none" {
		t.Fatalf("unexpected live cgroup version %q", snapshot.Cgroup.Version)
	}
	if snapshot.Environment.GOOS != "linux" || snapshot.Environment.CgroupVersion != snapshot.Cgroup.Version {
		t.Fatalf("live environment identity drifted: %#v", snapshot.Environment)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMeasured(t *testing.T, metric Metric, want uint64) {
	t.Helper()
	if metric.Status != MetricMeasured || metric.Value == nil || *metric.Value != want || metric.ReasonCode != "" {
		t.Fatalf("metric = %#v, want measured %d", metric, want)
	}
}

func assertUnavailable(t *testing.T, metric Metric, status MetricStatus, reason UnavailableReason) {
	t.Helper()
	if metric.Status != status || metric.Value != nil || metric.ReasonCode != reason {
		t.Fatalf("metric = %#v, want %s/%s", metric, status, reason)
	}
}
