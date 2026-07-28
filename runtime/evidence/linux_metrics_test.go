package evidence

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestLinuxCollectorTargetsExplicitProcess(t *testing.T) {
	collector := LinuxCollector{ProcRoot: "/proc", ProcessID: 1234}
	if got := collector.processPath("status"); got != "/proc/1234/status" {
		t.Fatalf("process path=%q", got)
	}
	collector.ProcessID = 0
	if got := collector.processPath("status"); got != "/proc/self/status" {
		t.Fatalf("self path=%q", got)
	}
}

func TestLinuxCollectorAggregatesNamedCOWMappings(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	writeFixture(t, filepath.Join(procRoot, "self/smaps"), `
1000-3000 rw-p 00000000 00:01 1 /memfd:apyrun-cow-image (deleted)
Size:                  8 kB
Rss:                   7 kB
Pss:                   4 kB
Shared_Clean:          3 kB
Shared_Dirty:          1 kB
Private_Clean:         1 kB
Private_Dirty:         2 kB
Referenced:            6 kB
Anonymous:             2 kB
4000-5000 rw-p 00000000 00:01 2 /other
Size:                  4 kB
Rss:                   4 kB
Pss:                   4 kB
Private_Dirty:         4 kB
6000-9000 rw-p 00000000 00:01 1 /memfd:apyrun-cow-image (deleted)
Size:                 12 kB
Rss:                  10 kB
Pss:                   6 kB
Shared_Clean:          5 kB
Shared_Dirty:          1 kB
Private_Clean:         2 kB
Private_Dirty:         2 kB
Referenced:            9 kB
Anonymous:             2 kB
`)
	metrics, err := (LinuxCollector{ProcRoot: procRoot}).CollectNamedMappings("memfd:apyrun-cow-image")
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Name != "memfd:apyrun-cow-image" || metrics.MappingCount != 2 {
		t.Fatalf("mapping identity drifted: %#v", metrics)
	}
	for metric, want := range map[string]struct {
		got  Metric
		want uint64
	}{
		"virtual":       {metrics.VirtualBytes, 20 * 1024},
		"rss":           {metrics.RSSBytes, 17 * 1024},
		"pss":           {metrics.PSSBytes, 10 * 1024},
		"shared-clean":  {metrics.SharedCleanBytes, 8 * 1024},
		"shared-dirty":  {metrics.SharedDirtyBytes, 2 * 1024},
		"private-clean": {metrics.PrivateCleanBytes, 3 * 1024},
		"private-dirty": {metrics.PrivateDirtyBytes, 4 * 1024},
		"referenced":    {metrics.ReferencedBytes, 15 * 1024},
		"anonymous":     {metrics.AnonymousBytes, 4 * 1024},
	} {
		t.Run(metric, func(t *testing.T) { assertMeasured(t, want.got, want.want) })
	}
}

func TestLinuxCollectorOperationalCgroupRecordsRawScopedCounters(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	cgroupRoot := filepath.Join(root, "cgroup")
	writeFixture(t, filepath.Join(procRoot, "self/cgroup"), "0::/job.scope\n")
	leaf := filepath.Join(cgroupRoot, "job.scope")
	writeFixture(t, filepath.Join(leaf, "cgroup.procs"), strconv.Itoa(os.Getpid())+"\n42\n")
	writeFixture(t, filepath.Join(leaf, "memory.current"), "1000\n")
	writeFixture(t, filepath.Join(leaf, "memory.peak"), "2000\n")
	writeFixture(t, filepath.Join(leaf, "memory.swap.current"), "3\n")
	writeFixture(t, filepath.Join(leaf, "memory.events"), "low 0\nhigh 4\nmax 0\noom 5\noom_kill 6\n")
	writeFixture(t, filepath.Join(leaf, "memory.pressure"), "some avg10=0.00 avg60=0.00 avg300=0.00 total=7\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=8\n")

	metrics, err := (LinuxCollector{ProcRoot: procRoot, CgroupRoot: cgroupRoot}).CollectOperationalCgroup()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Scope != "shared" || metrics.MembershipSHA256 == "" {
		t.Fatalf("scope identity=%+v", metrics)
	}
	for name, item := range map[string]struct {
		metric Metric
		value  uint64
	}{
		"current": {metrics.MemoryCurrentBytes, 1000}, "peak": {metrics.MemoryPeakBytes, 2000}, "swap": {metrics.MemorySwapCurrentBytes, 3},
		"high": {metrics.MemoryEventsHighTotal, 4}, "oom": {metrics.MemoryEventsOOMTotal, 5}, "oom_kill": {metrics.MemoryEventsOOMKillTotal, 6},
		"pressure_some": {metrics.PressureSomeTotalUS, 7}, "pressure_full": {metrics.PressureFullTotalUS, 8},
	} {
		t.Run(name, func(t *testing.T) { assertMeasured(t, item.metric, item.value) })
	}
}

func TestLinuxCollectorDoesNotInferIsolationFromSingleLeafPID(t *testing.T) {
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
	writeFixture(t, filepath.Join(leaf, "cgroup.procs"), strconv.Itoa(os.Getpid())+"\n")

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
	if snapshot.Cgroup.Scope != "unverified" || snapshot.Cgroup.MembershipSHA256 == "" {
		t.Fatalf("cgroup scope identity = %#v", snapshot.Cgroup)
	}
	assertUnavailable(t, snapshot.Cgroup.MemoryCurrentBytes, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.MemorySwapCurrentBytes, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.MemoryPeakBytes, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.MemoryEventsHighTotal, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.MemoryEventsOOMTotal, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.MemoryEventsOOMKillTotal, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.PressureSomeTotalUS, MetricSkipped, ReasonIsolationUnproven)
	assertUnavailable(t, snapshot.Cgroup.PressureFullTotalUS, MetricSkipped, ReasonIsolationUnproven)
}

func TestLinuxCollectorDoesNotAttributeSharedCgroupMetricsToProcess(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	cgroupRoot := filepath.Join(root, "cgroup")
	writeFixture(t, filepath.Join(procRoot, "self/cgroup"), "0::/shared.scope\n")
	leaf := filepath.Join(cgroupRoot, "shared.scope")
	writeFixture(t, filepath.Join(leaf, "cgroup.procs"), strconv.Itoa(os.Getpid())+"\n999999\n")
	writeFixture(t, filepath.Join(leaf, "memory.current"), "2048\n")

	metrics, err := (LinuxCollector{ProcRoot: procRoot, CgroupRoot: cgroupRoot}).collectCgroup()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Scope != "shared" {
		t.Fatalf("shared scope identity = %#v", metrics)
	}
	assertUnavailable(t, metrics.MemoryCurrentBytes, MetricSkipped, ReasonNonisolatedScope)
	assertUnavailable(t, metrics.MemoryEventsOOMTotal, MetricSkipped, ReasonNonisolatedScope)
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
