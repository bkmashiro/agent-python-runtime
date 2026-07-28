package evidence

import (
	"bufio"
	"crypto/sha256"
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
const linuxSMAPSFileMaxBytes = 64 * 1024 * 1024
const linuxSMAPSMaxMappings = 100_000

type LinuxMetricSnapshot struct {
	Environment EnvironmentIdentity
	Process     ProcessMetrics
	Cgroup      CgroupMetrics
}

type LinuxCollector struct {
	ProcRoot   string
	CgroupRoot string
	// ProcessID selects a process under ProcRoot. Zero preserves /proc/self.
	ProcessID int
}

func (collector LinuxCollector) processPath(name string) string {
	identity := "self"
	if collector.ProcessID > 0 {
		identity = strconv.Itoa(collector.ProcessID)
	}
	return filepath.Join(collector.ProcRoot, identity, name)
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
	status, err := readLinuxMetricFile(collector.processPath("status"))
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
	pageTables, err := optionalKBMetric(statusValues, "VmPTE")
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}

	minorFaults, majorFaults, err := collector.collectFaults()
	if err != nil {
		return LinuxMetricSnapshot{}, err
	}
	fdCount, err := countOpenFileDescriptors(collector.processPath("fd"))
	if err != nil {
		return LinuxMetricSnapshot{}, fmt.Errorf("count proc file descriptors: %w", err)
	}
	vmaCount, err := countNonemptyLines(collector.processPath("maps"))
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
			PageTableBytes:    &pageTables,
		},
		Cgroup: cgroup,
	}, nil
}

func (collector LinuxCollector) collectFaults() (uint64, uint64, error) {
	content, err := readLinuxMetricFile(collector.processPath("stat"))
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
	content, err := readLinuxMetricFile(collector.processPath("smaps_rollup"))
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

func (collector LinuxCollector) CollectNamedMappings(name string) (MappingMetrics, error) {
	if !filepath.IsAbs(collector.ProcRoot) {
		return MappingMetrics{}, errors.New("Linux proc root must be absolute")
	}
	if name == "" || len(name) > 128 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\r\n") {
		return MappingMetrics{}, errors.New("Linux mapping name is invalid")
	}
	file, err := os.Open(collector.processPath("smaps"))
	if err != nil {
		return MappingMetrics{}, fmt.Errorf("open proc smaps: %w", err)
	}
	defer file.Close()

	totals := map[string]uint64{}
	metrics := MappingMetrics{Name: name}
	var current map[string]string
	currentMatched := false
	var bytesRead uint64
	var headers uint64
	finish := func() error {
		if !currentMatched {
			return nil
		}
		for _, key := range []string{"Size", "Rss", "Pss", "Shared_Clean", "Shared_Dirty", "Private_Clean", "Private_Dirty", "Referenced", "Anonymous"} {
			value, err := requiredKBMetric(current, key)
			if err != nil {
				return fmt.Errorf("parse named smaps mapping %s: %w", name, err)
			}
			if math.MaxUint64-totals[key] < value {
				return errors.New("named smaps mapping total overflows")
			}
			totals[key] += value
		}
		if metrics.MappingCount == math.MaxUint32 {
			return errors.New("named smaps mapping count overflows")
		}
		metrics.MappingCount++
		return nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += uint64(len(line) + 1)
		if bytesRead > linuxSMAPSFileMaxBytes {
			return MappingMetrics{}, errors.New("proc smaps exceeds hard bound")
		}
		if pathname, header := smapsHeaderPath(line); header {
			if err := finish(); err != nil {
				return MappingMetrics{}, err
			}
			headers++
			if headers > linuxSMAPSMaxMappings {
				return MappingMetrics{}, errors.New("proc smaps mapping count exceeds hard bound")
			}
			currentMatched = strings.Contains(pathname, name)
			if currentMatched {
				current = map[string]string{}
			} else {
				current = nil
			}
			continue
		}
		if !currentMatched {
			continue
		}
		separator := strings.IndexByte(line, ':')
		if separator <= 0 {
			continue
		}
		key := line[:separator]
		if _, duplicate := current[key]; duplicate {
			return MappingMetrics{}, fmt.Errorf("duplicate smaps field %q", key)
		}
		current[key] = strings.TrimSpace(line[separator+1:])
	}
	if err := scanner.Err(); err != nil {
		return MappingMetrics{}, fmt.Errorf("scan proc smaps: %w", err)
	}
	if err := finish(); err != nil {
		return MappingMetrics{}, err
	}
	metrics.VirtualBytes = measuredMetric(totals["Size"])
	metrics.RSSBytes = measuredMetric(totals["Rss"])
	metrics.PSSBytes = measuredMetric(totals["Pss"])
	metrics.SharedCleanBytes = measuredMetric(totals["Shared_Clean"])
	metrics.SharedDirtyBytes = measuredMetric(totals["Shared_Dirty"])
	metrics.PrivateCleanBytes = measuredMetric(totals["Private_Clean"])
	metrics.PrivateDirtyBytes = measuredMetric(totals["Private_Dirty"])
	metrics.ReferencedBytes = measuredMetric(totals["Referenced"])
	metrics.AnonymousBytes = measuredMetric(totals["Anonymous"])
	return metrics, nil
}

func smapsHeaderPath(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || !strings.Contains(fields[0], "-") || len(fields[1]) != 4 {
		return "", false
	}
	if len(fields) == 5 {
		return "", true
	}
	return strings.Join(fields[5:], " "), true
}

func (collector LinuxCollector) collectCgroup() (CgroupMetrics, error) {
	content, err := readLinuxMetricFile(collector.processPath("cgroup"))
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
	membershipDigest := sha256.Sum256([]byte(path.Clean("/" + strings.TrimPrefix(cgroupPath, "/"))))
	membershipSHA256 := fmt.Sprintf("%x", membershipDigest)
	scope, scopeReason := classifyCgroupScope(filepath.Join(leaf, "cgroup.procs"))
	if scopeReason != "" {
		return unavailableScopedCgroup("v2", "unverified", membershipSHA256, MetricSkipped, ReasonIsolationUnproven), nil
	}
	if scope == "shared" {
		return unavailableScopedCgroup("v2", scope, membershipSHA256, MetricSkipped, ReasonNonisolatedScope), nil
	}
	return unavailableScopedCgroup("v2", "unverified", membershipSHA256, MetricSkipped, ReasonIsolationUnproven), nil
}

// CollectOperationalCgroup records raw cgroup-v2 boundary counters regardless
// of whether the scope is shared. Scope remains explicit, so callers cannot
// treat shared job-level values as process attribution.
func (collector LinuxCollector) CollectOperationalCgroup() (CgroupMetrics, error) {
	content, err := readLinuxMetricFile(collector.processPath("cgroup"))
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
	digest := sha256.Sum256([]byte(path.Clean("/" + strings.TrimPrefix(cgroupPath, "/"))))
	scope, reason := classifyCgroupScope(filepath.Join(leaf, "cgroup.procs"))
	if reason != "" {
		scope = "unverified"
	}
	events := readCgroupKeyValues(filepath.Join(leaf, "memory.events"))
	pressure := readCgroupPressure(filepath.Join(leaf, "memory.pressure"))
	return CgroupMetrics{
		Version: "v2", Scope: scope, MembershipSHA256: fmt.Sprintf("%x", digest),
		MemoryCurrentBytes:       readCgroupUint(filepath.Join(leaf, "memory.current")),
		MemoryPeakBytes:          readCgroupUint(filepath.Join(leaf, "memory.peak")),
		MemorySwapCurrentBytes:   readCgroupUint(filepath.Join(leaf, "memory.swap.current")),
		MemoryEventsHighTotal:    events["high"],
		MemoryEventsOOMTotal:     events["oom"],
		MemoryEventsOOMKillTotal: events["oom_kill"],
		PressureSomeTotalUS:      pressure["some"],
		PressureFullTotalUS:      pressure["full"],
	}, nil
}

func readCgroupUint(filename string) Metric {
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

func readCgroupKeyValues(filename string) map[string]Metric {
	result := map[string]Metric{}
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		reason := reasonFromSourceError(err)
		for _, key := range []string{"high", "oom", "oom_kill"} {
			result[key] = unavailableMetricValue(reason)
		}
		return result
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr == nil {
			values[fields[0]] = value
		}
	}
	for _, key := range []string{"high", "oom", "oom_kill"} {
		value, ok := values[key]
		if !ok {
			result[key] = collectionErrorMetric()
		} else {
			result[key] = measuredMetric(value)
		}
	}
	return result
}

func readCgroupPressure(filename string) map[string]Metric {
	result := map[string]Metric{}
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		reason := reasonFromSourceError(err)
		result["some"] = unavailableMetricValue(reason)
		result["full"] = unavailableMetricValue(reason)
		return result
	}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "some" && fields[0] != "full") {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "total=") {
				continue
			}
			value, parseErr := strconv.ParseUint(strings.TrimPrefix(field, "total="), 10, 64)
			if parseErr == nil {
				result[fields[0]] = measuredMetric(value)
			}
		}
	}
	for _, key := range []string{"some", "full"} {
		if _, ok := result[key]; !ok {
			result[key] = collectionErrorMetric()
		}
	}
	return result
}

func classifyCgroupScope(filename string) (string, UnavailableReason) {
	content, err := readLinuxMetricFile(filename)
	if err != nil {
		return "unverified", reasonFromSourceError(err)
	}
	pids := map[uint64]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		pid, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
		if err != nil || pid == 0 {
			return "unverified", ReasonCollectionError
		}
		pids[pid] = struct{}{}
	}
	if len(pids) == 0 {
		return "unverified", ReasonCollectionError
	}
	if len(pids) == 1 {
		if _, current := pids[uint64(os.Getpid())]; current {
			return "unverified", ""
		}
	}
	return "shared", ""
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
	return unavailableMetricWithStatus(MetricUnsupported, reason)
}

func unavailableMetricWithStatus(status MetricStatus, reason UnavailableReason) Metric {
	return Metric{Status: status, ReasonCode: reason}
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
	return unavailableScopedCgroup(version, "unverified", "", MetricUnsupported, reason)
}

func unavailableScopedCgroup(version, scope, membershipSHA256 string, status MetricStatus, reason UnavailableReason) CgroupMetrics {
	metric := unavailableMetricWithStatus(status, reason)
	return CgroupMetrics{
		Version:                  version,
		Scope:                    scope,
		MembershipSHA256:         membershipSHA256,
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
