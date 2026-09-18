//go:build linux

package wazero

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"golang.org/x/sys/unix"
)

type linuxColdIOContinuation struct {
	mu       sync.Mutex
	memory   *cowLinearMemory
	policy   runtimeconfig.ColdIOPolicy
	evidence ColdIOEvidence
	probe    *cgroupPressureProbe
	probeErr error
}

type cgroupBudget struct {
	current string
	high    string
	max     string
}

type cgroupPressureProbe struct {
	budgets []cgroupBudget
}

var (
	errPressureUnsupported  = errors.New("cgroup v2 pressure probe unavailable")
	errPressureUnknownLimit = errors.New("cgroup v2 pressure limit is unknown")
)

func newColdIOContinuation(memory *cowLinearMemory, policy runtimeconfig.ColdIOPolicy) (*linuxColdIOContinuation, error) {
	if memory == nil {
		return nil, errColdIOState
	}
	continuation := &linuxColdIOContinuation{memory: memory, policy: policy, evidence: newColdIOEvidence()}
	if policy.Strategy == runtimeconfig.ColdIOPressure {
		continuation.probe, continuation.probeErr = discoverCgroupPressureProbe()
	}
	return continuation, nil
}

func newColdIOEvidence() ColdIOEvidence {
	return ColdIOEvidence{
		SchemaVersion: ColdIOEvidenceSchemaVersion,
		Selected:      true,
		State:         ColdIORunning,
		Blockers:      []string{},
	}
}

func (continuation *linuxColdIOContinuation) beginWait() error {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if continuation.evidence.State != ColdIORunning {
		return errColdIOState
	}
	continuation.evidence.State = ColdIOWaiting
	continuation.evidence.Waits++
	return nil
}

func (continuation *linuxColdIOContinuation) wait(ctx context.Context, call func(context.Context) ([]byte, error)) ([]byte, error) {
	if err := continuation.beginWait(); err != nil {
		return nil, err
	}
	defer continuation.resume()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make(chan coldIOCallResult, 1)
	go func() {
		payload, err := call(ctx)
		results <- coldIOCallResult{payload: payload, err: err}
	}()
	start := time.Now()
	var timer *time.Timer
	var tick <-chan time.Time
	if continuation.policy.Strategy != runtimeconfig.ColdIONatural {
		timer = time.NewTimer(continuation.policy.ColdAfter)
		tick = timer.C
		defer timer.Stop()
	}
	coldDone, pageOutDone := false, false
	for {
		select {
		case result := <-results:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return result.payload, result.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			eligible := true
			if continuation.policy.Strategy == runtimeconfig.ColdIOPressure {
				var err error
				eligible, err = continuation.samplePressure()
				if err != nil {
					tick = nil
					continue
				}
			}
			if eligible {
				if !coldDone {
					continuation.advise(unix.MADV_COLD, ColdIOCold, coldAdviceFailed)
					coldDone = true
				}
				if !pageOutDone && continuation.policy.PageOutAfter != 0 && time.Since(start) >= continuation.policy.PageOutAfter {
					continuation.advise(unix.MADV_PAGEOUT, ColdIOPageOut, pageOutAdviceFailed)
					pageOutDone = true
				}
			}
			if coldDone && (continuation.policy.PageOutAfter == 0 || pageOutDone) {
				tick = nil
				continue
			}
			delay := time.Until(start.Add(continuation.policy.PageOutAfter))
			if continuation.policy.Strategy == runtimeconfig.ColdIOPressure {
				delay = pressurePollInterval(continuation.policy.ColdAfter)
			}
			timer.Reset(delay)
		}
	}
}

func pressurePollInterval(coldAfter time.Duration) time.Duration {
	return max(coldAfter, 50*time.Millisecond)
}

func (continuation *linuxColdIOContinuation) samplePressure() (bool, error) {
	continuation.mu.Lock()
	continuation.evidence.PressureChecks++
	probeErr := continuation.probeErr
	probe := continuation.probe
	threshold := continuation.policy.PressureThreshold
	continuation.mu.Unlock()
	if probeErr != nil && probe == nil {
		continuation.recordPressureError()
		return false, probeErr
	}
	if probe == nil {
		continuation.recordPressureError()
		return false, errPressureUnsupported
	}
	pressured, err := probe.pressured(threshold)
	if err != nil {
		continuation.recordPressureError()
		return false, err
	}
	if pressured {
		continuation.mu.Lock()
		continuation.evidence.PressureHits++
		continuation.mu.Unlock()
	}
	return pressured, nil
}

func (continuation *linuxColdIOContinuation) recordPressureError() {
	continuation.mu.Lock()
	continuation.evidence.PressureErrors++
	continuation.mu.Unlock()
}

func (continuation *linuxColdIOContinuation) advise(advice int, state ColdIOState, blocker string) {
	bytes, err := continuation.memory.advise(advice)
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if state == ColdIOCold {
		continuation.evidence.ColdAttempts++
	} else {
		continuation.evidence.PageOutAttempts++
	}
	if err != nil {
		continuation.evidence.AdviceFailures++
		if !containsString(continuation.evidence.Blockers, blocker) {
			continuation.evidence.Blockers = append(continuation.evidence.Blockers, blocker)
		}
		return
	}
	if state == ColdIOCold {
		continuation.evidence.ColdSucceeded++
	} else {
		continuation.evidence.PageOutSucceeded++
	}
	continuation.evidence.AdvisedBytes += bytes
	continuation.evidence.State = state
}

func (continuation *linuxColdIOContinuation) resume() {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if continuation.evidence.State == ColdIOTerminal {
		return
	}
	continuation.evidence.State = ColdIORunning
	continuation.evidence.Resumes++
}

func (continuation *linuxColdIOContinuation) finish() ColdIOEvidence {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	continuation.evidence.State = ColdIOTerminal
	copy := continuation.evidence
	copy.Blockers = append([]string{}, continuation.evidence.Blockers...)
	return copy
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func discoverCgroupPressureProbe() (*cgroupPressureProbe, error) {
	return discoverCgroupPressureProbeFromFiles("/proc/self/cgroup", "/proc/self/mountinfo")
}

func discoverCgroupPressureProbeFromFiles(cgroupFile, mountInfoFile string) (*cgroupPressureProbe, error) {
	cgroupData, err := os.ReadFile(cgroupFile)
	if err != nil {
		return nil, fmt.Errorf("read cgroup membership: %w", err)
	}
	cgroupPath, ok := unifiedCgroupPath(string(cgroupData))
	if !ok {
		return nil, errPressureUnsupported
	}
	mountData, err := os.ReadFile(mountInfoFile)
	if err != nil {
		return nil, fmt.Errorf("read cgroup mounts: %w", err)
	}
	mountPoint, root, ok := unifiedCgroupMount(string(mountData))
	if !ok {
		return nil, errPressureUnsupported
	}
	if !pathWithinCgroupRoot(cgroupPath, root) {
		return nil, errPressureUnsupported
	}

	budgets := make([]cgroupBudget, 0, 4)
	for current := cgroupPath; ; current = cgroupParent(current) {
		relative := strings.TrimPrefix(strings.TrimPrefix(current, root), "/")
		hostPath := mountPoint
		if relative != "" {
			hostPath = filepath.Join(hostPath, filepath.FromSlash(relative))
		}
		budget := cgroupBudget{
			current: filepath.Join(hostPath, "memory.current"),
			high:    filepath.Join(hostPath, "memory.high"),
			max:     filepath.Join(hostPath, "memory.max"),
		}
		available, err := cgroupBudgetFilesAvailable(budget.high, budget.max)
		if err != nil {
			return nil, fmt.Errorf("inspect cgroup memory budget: %w", err)
		}
		if available {
			budgets = append(budgets, budget)
		}
		if current == root || current == "/" {
			break
		}
	}
	if len(budgets) == 0 {
		return nil, errPressureUnsupported
	}
	return &cgroupPressureProbe{budgets: budgets}, nil
}

func cgroupBudgetFilesAvailable(high, max string) (bool, error) {
	available := false
	for _, path := range []string{high, max} {
		_, err := os.Stat(path)
		if err == nil {
			available = true
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return false, err
	}
	return available, nil
}

func (probe *cgroupPressureProbe) pressured(threshold float64) (bool, error) {
	if probe == nil || len(probe.budgets) == 0 {
		return false, errPressureUnsupported
	}
	finiteBudget := false
	for _, budget := range probe.budgets {
		limits := make([]uint64, 0, 2)
		for _, limitPath := range []string{budget.high, budget.max} {
			limit, known, err := readCgroupLimit(limitPath)
			if err != nil {
				return false, err
			}
			if known {
				finiteBudget = true
				limits = append(limits, limit)
			}
		}
		if len(limits) == 0 {
			continue
		}
		current, err := readCgroupUint(budget.current)
		if err != nil {
			return false, err
		}
		for _, limit := range limits {
			if limit == 0 || float64(current)/float64(limit) >= threshold {
				return true, nil
			}
		}
	}
	if !finiteBudget {
		return false, errPressureUnknownLimit
	}
	return false, nil
}

func readCgroupUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func readCgroupLimit(path string) (uint64, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if value == "max" {
		return 0, false, nil
	}
	limit, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return limit, true, nil
}

func unifiedCgroupPath(data string) (string, bool) {
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			path := filepath.Clean(parts[2])
			if strings.HasPrefix(path, "/") {
				return path, true
			}
		}
	}
	return "", false
}

func unifiedCgroupMount(data string) (mountPoint, root string, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}
		pre := strings.Fields(parts[0])
		post := strings.Fields(parts[1])
		if len(pre) < 6 || len(post) < 1 || post[0] != "cgroup2" {
			continue
		}
		return unescapeMountInfo(pre[4]), unescapeMountInfo(pre[3]), true
	}
	return "", "", false
}

func unescapeMountInfo(value string) string {
	value = strings.ReplaceAll(value, `\040`, " ")
	value = strings.ReplaceAll(value, `\011`, "	")
	value = strings.ReplaceAll(value, `\012`, "\n")
	value = strings.ReplaceAll(value, `\134`, `\`)
	return value
}

func pathWithinCgroupRoot(path, root string) bool {
	return path == root || root == "/" || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func cgroupParent(path string) string {
	if path == "/" || path == "" {
		return "/"
	}
	parent := filepath.Dir(path)
	if parent == "." {
		return "/"
	}
	return parent
}

var _ coldIOContinuation = (*linuxColdIOContinuation)(nil)
