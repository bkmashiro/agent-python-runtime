package evidence

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const linuxMetricFileMaxBytes = 4 * 1024 * 1024

type LinuxMetricSnapshot struct {
	Environment EnvironmentIdentity
	Process     ProcessMetrics
	Cgroup      CgroupMetrics
}

type LinuxCollector struct {
	ProcRoot   string
	CgroupRoot string
}

func DefaultLinuxCollector() LinuxCollector {
	return LinuxCollector{ProcRoot: "/proc", CgroupRoot: "/sys/fs/cgroup"}
}

func (collector LinuxCollector) Collect() (LinuxMetricSnapshot, error) {
	if !filepath.IsAbs(collector.ProcRoot) || !filepath.IsAbs(collector.CgroupRoot) {
		return LinuxMetricSnapshot{}, errors.New("Linux metric roots must be absolute")
	}
	kernelRelease, err := readLinuxMetricFile(filepath.Join(collector.ProcRoot, "sys/kernel/osrelease"))
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("read kernel release: %w", err)
	}
	kernelRelease = strings.TrimSpace(kernelRelease)
	if kernelRelease == "" {
		return LinuxMetricSnapshot{}, errors.New("kernel release is empty")
	}
	status, err := readLinuxMetricFile(filepath.Join(collector.ProcRoot, "self/status"))
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("read proc status: %w", err)
	}
	statusValues, err := parseProcStatus(status)
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("parse proc status: %w", err)
	}
	rss, err := requiredKBMetric(statusValues, "VmRSS")
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}
	virtual, err := requiredKBMetric(statusValues, "VmSize")
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}
	swap, err := optionalKBMetric(statusValues, "VmSwap")
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}

	minorFaults, majorFaults, err := collector.collectFaults()
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}
	fdCount, err := countOpenFileDescriptors(filepath.Join(collector.ProcRoot, "self/fd"))
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("count proc file descriptors: %w", err)
	}
	vmaCount, err := countNonemptyLines(filepath.Join(collector.ProcRoot, "self/maps"))
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("count proc VMAs: %w", err)
	}
	pss, privateClean, privateDirty := collector.collectSMAPSRollup()
	cgroup, err := collector.collectCgroup()
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}

	return LinuxMetricSnapshot{
		Environment: EnvironmentIdentity{
			GOOS:          "linux",
			GOARCH:        runtime.GOARCH,
			GoVersion:     runtime.Version(),
			KernelRelease: kernelRelease,
			PageSizeBytes: uint64(os.Getpagesize()),
			CgroupVersion: cgroup.Version,
		},
		Process: ProcessMetrics{
			RSSBytes:          measuredMetric(rss),
			VirtualBytes:      measuredMetric(virtual),
			PSSBytes:          pss,
			PrivateCleanBytes: privateClean,
			PrivateDirtyBytes: privateDirty,
			SwapBytes:         swap,
			MinorFaults:       measuredMetric(minorFaults),
			MajorFaults:       measuredMetric(majorFaults),
			FDCount:           measuredMetric(fdCount),
			VMACount:          measuredMetric(vmaCount),
		},
		Cgroup: cgroup,
	}, nil
}

func (collector LinuxCollector) collectFaults() (uint64, uint64, error) {
	content, err := readLinuxMetricFile(filepath.Join(collector.ProcRoot, "self/stat"))
	if err != nil {
		return 0, 0, fmt.Errorf("read proc stat: %w", err)
	}
	closing := strings.LastIndex(content, ") ")
	if closing < 0 {
		return 0, 0, errors.New("proc stat command boundary is missing")
	}
	fields := strings.Fields(content[closing+2:])
	// fields[0] is stat field 3 (state). minflt and majflt are fields 10 and 12.
	if len(fields) <= 9 {
		return 0, 0, errors.New("proc stat has too few fields")
	}
	minor, err := strconv.ParseUint(fields[7], 10, 64)
	if err != nil {
		return 0, 0, errors.New("proc stat minflt is invalid")
	}
	major, err := strconv.ParseUint(fields[9], 10, 64)
	if err != nil {
		return 0, 0, errors.New("proc stat majflt is invalid")
	}
	return minor, major, nil
}

func (collector LinuxCollector) collectSMAPSRollup() (Metric, Metric, Metric) {
	content, err := readLinuxMetricFile(filepath.Join(collector.ProcRoot, "self/smaps_rollup"))
	if err != nil {
		reason := reasonFromSourceError(err)
		return unavailableMetricValue(reason), unavailableMetricValue(reason), unavailableMetricValue(reason)
	}
	values, err := parseProcStatus(content)
	if err != nil {
		return collectionErrorMetric(), collectionErrorMetric(), collectionErrorMetric()
	}
	pss, pssErr := requiredKBMetric(values, "Pss")
	clean, cleanErr := requiredKBMetric(values, "Private_Clean")
	dirty, dirtyErr := requiredKBMetric(values, "Private_Dirty")
	if pssErr != nil || cleanErr != nil || dirtyErr != nil {
		return collectionErrorMetric(), collectionErrorMetric(), collectionErrorMetric()
	}
	return measuredMetric(pss), measuredMetric(clean), measuredMetric(dirty)
}

func (collector LinuxCollector) collectCgroup() (CgroupMetrics, error) {
	content, err := readLinuxMetricFile(filepath.Join(collector.ProcRoot, "self/cgroup"))
	if err != nil {
		return CgroupMetrics{}, fmt.Errorf("read proc cgroup: %w", err)
	}
	cgroupPath := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "0::") {
			if cgroupPath != "" {
				return CgroupMetrics{}, errors.New("multiple cgroup v2 memberships")
			}
			cgroupPath = strings.TrimPrefix(line, "0::")
		}
	}
	if cgroupPath == "" {
		return unavailableCgroup("none", ReasonNotApplicable), nil
	}
	leaf, err := confinedCgroupPath(collector.CgroupRoot, cgroupPath)
	if err != nil {
		return CgroupMetrics{}, err
	}
	current := collectScalarMetric(filepath.Join(leaf, "memory.current"))
	peak := collectScalarMetric(filepath.Join(leaf, "memory.peak"))
	swap := collectScalarMetric(filepath.Join(leaf, "memory.swap.current"))
	high, oom, oomKill := collectMemoryEvents(filepath.Join(leaf, "memory.events"))
	some, full := collectMemoryPressure(filepath.Join(leaf, "memory.pressure"))
	return CgroupMetrics{
		Version:                  "v2",
		MemoryCurrentBytes:       current,
		MemoryPeakBytes:          peak,
		MemorySwapCurrentBytes:   swap,
		MemoryEventsHighTotal:    high,
		MemoryEventsOOMTotal:     oom,
		MemoryEventsOOMKillTotal: oomKill,
		PressureSomeTotalUS:      some,
		PressureFullTotalUS:      full,
	}, nil
}

func parseProcStatus(content string) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		separator := strings.IndexByte(line, ':')
		if separator <= 0 {
			continue
		}
		key := line[:separator]
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("duplicate proc field %q", key)
		}
		values[key] = strings.TrimSpace(line[separator+1:])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func requiredKBMetric(values map[string]string, key string) (uint64, error) {
	raw, ok := values[key]
	if !ok {
		return 0, fmt.Errorf("required proc field %q is missing", key)
	}
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[1] != "kB" {
		return 0, fmt.Errorf("proc field %q is not an exact kB value", key)
	}
	kilobytes, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || kilobytes > math.MaxUint64/1024 {
		return 0, fmt.Errorf("proc field %q is invalid or overflows bytes", key)
	}
	return kilobytes * 1024, nil
}

func optionalKBMetric(values map[string]string, key string) (Metric, error) {
	if _, ok := values[key]; !ok {
		return unavailableMetricValue(ReasonSourceUnavailable), nil
	}
	value, err := requiredKBMetric(values, key)
	if err != nil {
		return Metric{}, err
	}
	return measuredMetric(value), nil
}

func collectScalarMetric(filename string) Metric {
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		return unavailableMetricValue(reasonFromSourceError(err))
	}
	value, err := strconv.ParseUint(strings.TrimSpace(content), 10, 64)
	if err != nil {
		return collectionErrorMetric()
	}
	return measuredMetric(value)
}

func collectMemoryEvents(filename string) (Metric, Metric, Metric) {
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		reason := reasonFromSourceError(err)
		return unavailableMetricValue(reason), unavailableMetricValue(reason), unavailableMetricValue(reason)
	}
	values := parseNamedCounters(content)
	return namedCounterMetric(values, "high"), namedCounterMetric(values, "oom"), namedCounterMetric(values, "oom_kill")
}

func collectMemoryPressure(filename string) (Metric, Metric) {
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		reason := reasonFromSourceError(err)
		return unavailableMetricValue(reason), unavailableMetricValue(reason)
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "total=") {
				continue
			}
			value, err := strconv.ParseUint(strings.TrimPrefix(field, "total="), 10, 64)
			if err == nil {
				values[fields[0]] = value
			}
		}
	}
	return namedCounterMetric(values, "some"), namedCounterMetric(values, "full")
}

func parseNamedCounters(content string) map[string]uint64 {
	values := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			values[fields[0]] = value
		}
	}
	return values
}

func namedCounterMetric(values map[string]uint64, name string) Metric {
	value, ok := values[name]
	if !ok {
		return collectionErrorMetric()
	}
	return measuredMetric(value)
}

func confinedCgroupPath(root, membership string) (string, error) {
	cleanMembership := path.Clean("/" + strings.TrimPrefix(membership, "/"))
	relative := strings.TrimPrefix(cleanMembership, "/")
	leaf := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, leaf)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("cgroup membership escapes configured root")
	}
	return leaf, nil
}

func countOpenFileDescriptors(directory string) (uint64, error) {
	file, err := os.Open(directory)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	collectorFD := strconv.FormatUint(uint64(file.Fd()), 10)
	count := uint64(len(entries))
	for _, entry := range entries {
		if entry.Name() == collectorFD && entry.Type()&os.ModeSymlink != 0 {
			count--
			break
		}
	}
	return count, nil
}

func countNonemptyLines(filename string) (uint64, error) {
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		return 0, err
	}
	var count uint64
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count, scanner.Err()
}

func readLinuxMetricFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, linuxMetricFileMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > linuxMetricFileMaxBytes {
		return "", errors.New("Linux metric file exceeds hard bound")
	}
	return string(content), nil
}

func measuredMetric(value uint64) Metric {
	return Metric{Status: MetricMeasured, Value: &value}
}

func unavailableMetricValue(reason UnavailableReason) Metric {
	return Metric{Status: MetricUnsupported, ReasonCode: reason}
}

func collectionErrorMetric() Metric {
	return unavailableMetricValue(ReasonCollectionError)
}

func reasonFromSourceError(err error) UnavailableReason {
	if errors.Is(err, os.ErrPermission) {
		return ReasonPermissionDenied
	}
	if errors.Is(err, os.ErrNotExist) {
		return ReasonSourceUnavailable
	}
	return ReasonCollectionError
}

func unavailableCgroup(version string, reason UnavailableReason) CgroupMetrics {
	metric := unavailableMetricValue(reason)
	return CgroupMetrics{
		Version:                  version,
		MemoryCurrentBytes:       metric,
		MemoryPeakBytes:          metric,
		MemorySwapCurrentBytes:   metric,
		MemoryEventsHighTotal:    metric,
		MemoryEventsOOMTotal:     metric,
		MemoryEventsOOMKillTotal: metric,
		PressureSomeTotalUS:      metric,
		PressureFullTotalUS:      metric,
	}
}
