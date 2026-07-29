package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCgroupV2MemoryReaderReadsBoundedSnapshot(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"memory.current":  "943718400\n",
		"memory.max":      "1073741824\n",
		"memory.events":   "low 2\nhigh 3\nmax 4\noom 5\noom_kill 1\noom_group_kill 0\n",
		"memory.pressure": "some avg10=1.25 avg60=0.50 avg300=0.10 total=123456\nfull avg10=0.75 avg60=0.25 avg300=0.05 total=65432\n",
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sampledAt := time.Unix(100, 0).UTC()
	reader, err := NewCgroupV2MemoryReader(CgroupV2MemoryReaderConfig{Root: root, Clock: func() time.Time { return sampledAt }})
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
	if snapshot.SampledAt != sampledAt || snapshot.CurrentBytes != 943718400 || snapshot.MaximumBytes != 1073741824 ||
		snapshot.Events.OOM != 5 || snapshot.Events.OOMKill != 1 || snapshot.Events.High != 3 || snapshot.Events.Max != 4 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Pressure.Some.Avg10BPS != 125 || snapshot.Pressure.Some.Avg60BPS != 50 || snapshot.Pressure.Some.Avg300BPS != 10 || snapshot.Pressure.Some.TotalMicros != 123456 ||
		snapshot.Pressure.Full.Avg10BPS != 75 || snapshot.Pressure.Full.TotalMicros != 65432 {
		t.Fatalf("pressure = %#v", snapshot.Pressure)
	}
	if snapshot.UtilizationBPS() != 8789 {
		t.Fatalf("utilization = %d", snapshot.UtilizationBPS())
	}
}

func TestCgroupV2MemoryReaderResolvesInheritedMaximum(t *testing.T) {
	boundary := t.TempDir()
	parent := filepath.Join(boundary, "job")
	leaf := filepath.Join(parent, "task")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		filepath.Join(parent, "memory.max"):    "4294967296\n",
		filepath.Join(leaf, "memory.max"):      "max\n",
		filepath.Join(leaf, "memory.current"):  "1048576\n",
		filepath.Join(leaf, "memory.events"):   "high 0\nmax 0\noom 0\noom_kill 0\n",
		filepath.Join(leaf, "memory.pressure"): "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
	}
	for path, content := range fixtures {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := NewCgroupV2MemoryReader(CgroupV2MemoryReaderConfig{Root: leaf, BoundaryRoot: boundary})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MaximumBytes != 4294967296 {
		t.Fatalf("inherited maximum = %d", snapshot.MaximumBytes)
	}
}

func TestCgroupV2MemoryReaderFailsClosed(t *testing.T) {
	validEvents := []byte("high 0\nmax 0\noom 0\noom_kill 0\n")
	validPressure := []byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	cases := []struct {
		name     string
		current  []byte
		maximum  []byte
		events   []byte
		pressure []byte
	}{
		{name: "unbounded max", current: []byte("1\n"), maximum: []byte("max\n"), events: validEvents, pressure: validPressure},
		{name: "zero max", current: []byte("1\n"), maximum: []byte("0\n"), events: validEvents, pressure: validPressure},
		{name: "bad current", current: []byte("-1\n"), maximum: []byte("100\n"), events: validEvents, pressure: validPressure},
		{name: "missing oom kill", current: []byte("1\n"), maximum: []byte("100\n"), events: []byte("high 0\nmax 0\noom 0\n"), pressure: validPressure},
		{name: "bad psi", current: []byte("1\n"), maximum: []byte("100\n"), events: validEvents, pressure: []byte("some avg10=100.01 avg60=0 avg300=0 total=0\nfull avg10=0 avg60=0 avg300=0 total=0\n")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseCgroupV2MemorySnapshot(testCase.current, testCase.maximum, testCase.events, testCase.pressure, time.Unix(1, 0).UTC())
			if !errors.Is(err, ErrInvalidLiveMemorySnapshot) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := NewCgroupV2MemoryReader(CgroupV2MemoryReaderConfig{Root: "relative", Clock: time.Now}); !errors.Is(err, ErrInvalidLiveMemoryConfig) {
		t.Fatalf("relative root error = %v", err)
	}
}

func TestDiscoverCgroupV2MemoryRoot(t *testing.T) {
	root, err := DiscoverCgroupV2MemoryRoot([]byte("0::/system.slice/slurm.scope/job_1/task_0\n"), "/sys/fs/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/sys/fs/cgroup/system.slice/slurm.scope/job_1/task_0" {
		t.Fatalf("root = %q", root)
	}
	for _, payload := range [][]byte{
		[]byte("1:memory:/legacy\n"),
		[]byte("0::/../../escape\n"),
		[]byte("0::relative\n"),
		[]byte("0::/one\n0::/two\n"),
	} {
		if _, err := DiscoverCgroupV2MemoryRoot(payload, "/sys/fs/cgroup"); !errors.Is(err, ErrInvalidLiveMemoryConfig) {
			t.Fatalf("payload %q error = %v", payload, err)
		}
	}
}
