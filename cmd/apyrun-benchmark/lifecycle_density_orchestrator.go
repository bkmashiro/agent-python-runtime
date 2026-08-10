package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

const (
	defaultDensityChildOutputLimit = 4 * 1024 * 1024
	defaultDensityChildStderrLimit = 16 * 1024
)

type densitySweepSpec struct {
	SampleIndex    uint32
	RepeatIndex    uint32
	RequestedSlots uint32
	Strategy       string
	WarmupProfile  string
	MaxRSSBytes    uint64
	Timeout        time.Duration
}

func densitySweepSpecs(strategy string, repeats uint32, maxRSSBytes uint64, timeout time.Duration) ([]densitySweepSpec, error) {
	return densitySweepSpecsWithSlots(strategy, "", []uint32{1, 2, 4, 8, 16}, repeats, maxRSSBytes, timeout)
}

func numpyDensitySweepSpecs(strategy string, repeats uint32, maxRSSBytes uint64, timeout time.Duration) ([]densitySweepSpec, error) {
	if strategy != "single-use-preinitialized" && strategy != "single-use-preinitialized-shared-cache" && strategy != "cow-ready-single-use" {
		return nil, errors.New("NumPy lifecycle-density requires a prepared strategy")
	}
	return densitySweepSpecsWithSlots(strategy, "numpy-ready-v1", []uint32{1, 2, 4, 8, 16, 32, 64}, repeats, maxRSSBytes, timeout)
}

func densitySweepSpecsWithSlots(strategy, warmupProfile string, canonicalSlots []uint32, repeats uint32, maxRSSBytes uint64, timeout time.Duration) ([]densitySweepSpec, error) {
	activeStrategy := ""
	switch strategy {
	case "fresh":
		activeStrategy = "fresh-instance"
	case "single-use-preinitialized":
		activeStrategy = strategy
	case "single-use-preinitialized-shared-cache":
		activeStrategy = strategy
	case "cow-ready-single-use":
		activeStrategy = strategy
	default:
		return nil, fmt.Errorf("unsupported lifecycle-density strategy %q", strategy)
	}
	if repeats == 0 || repeats > 1000 {
		return nil, errors.New("lifecycle-density repeats must be between 1 and 1000")
	}
	if maxRSSBytes == 0 {
		return nil, errors.New("lifecycle-density max RSS guard is required")
	}
	if timeout <= 0 {
		return nil, errors.New("lifecycle-density child timeout is required")
	}
	specs := make([]densitySweepSpec, 0, len(canonicalSlots)*int(repeats))
	for _, slots := range canonicalSlots {
		for repeat := uint32(0); repeat < repeats; repeat++ {
			specs = append(specs, densitySweepSpec{
				SampleIndex:    uint32(len(specs)),
				RepeatIndex:    repeat,
				RequestedSlots: slots,
				Strategy:       activeStrategy,
				WarmupProfile:  warmupProfile,
				MaxRSSBytes:    maxRSSBytes,
				Timeout:        timeout,
			})
		}
	}
	return specs, nil
}

func preparedDensityShardCapacities(slots uint32) ([]uint32, error) {
	return preparedDensityShardCapacitiesForStrategy(slots, "single-use-preinitialized", false)
}

func preparedDensityShardCapacitiesForStrategy(slots uint32, strategy string, extended bool) ([]uint32, error) {
	switch slots {
	case 1, 2, 4, 8, 16:
	case 32, 64:
		if !extended {
			return nil, fmt.Errorf("prepared lifecycle-density slots %d require the extended NumPy-ready contract", slots)
		}
	default:
		return nil, fmt.Errorf("prepared lifecycle-density slots %d are noncanonical or unguarded", slots)
	}
	if strategy == "cow-ready-single-use" && extended {
		return []uint32{slots}, nil
	}
	if strategy != "single-use-preinitialized" && strategy != "single-use-preinitialized-shared-cache" && strategy != "cow-ready-single-use" {
		return nil, fmt.Errorf("unsupported prepared lifecycle-density strategy %q", strategy)
	}
	capacities := make([]uint32, 0, (slots+3)/4)
	remaining := slots
	for remaining > 0 {
		capacity := min(remaining, uint32(4))
		capacities = append(capacities, capacity)
		remaining -= capacity
	}
	return capacities, nil
}

type densityChildEnvelope struct {
	ProtocolVersion int                                     `json:"protocol_version"`
	ArtifactSHA256  string                                  `json:"artifact_sha256"`
	ArtifactProfile string                                  `json:"artifact_profile"`
	Backend         runtimeevidence.BackendIdentity         `json:"backend"`
	Environment     runtimeevidence.EnvironmentIdentity     `json:"environment"`
	Strategy        runtimeevidence.StrategyIdentity        `json:"strategy"`
	Warmup          *runtimeevidence.PreparedWarmupIdentity `json:"warmup,omitempty"`
	Sample          runtimeevidence.LifecycleDensitySample  `json:"sample"`
}

func validateDensityChildEnvelope(envelope densityChildEnvelope, spec densitySweepSpec, artifact artifactIdentity) error {
	if envelope.ProtocolVersion != 1 {
		return errors.New("lifecycle-density child protocol version is unsupported")
	}
	if envelope.ArtifactSHA256 != artifact.SHA256 || envelope.ArtifactProfile != artifact.ArtifactProfile {
		return errors.New("lifecycle-density child artifact identity drifted")
	}
	if envelope.Strategy.Requested != spec.Strategy || envelope.Strategy.Active != spec.Strategy || envelope.Strategy.Fallback {
		return errors.New("lifecycle-density child strategy drifted or fell back")
	}
	if spec.WarmupProfile == "" {
		if envelope.Warmup != nil {
			return errors.New("lifecycle-density v1 child carries a warmup identity")
		}
	} else {
		if envelope.Warmup == nil || envelope.Warmup.Profile != spec.WarmupProfile ||
			len(envelope.Warmup.GenerationSHA256) != 64 || strings.ToLower(envelope.Warmup.GenerationSHA256) != envelope.Warmup.GenerationSHA256 {
			return errors.New("NumPy lifecycle-density child warmup identity drifted")
		}
		if _, err := hex.DecodeString(envelope.Warmup.GenerationSHA256); err != nil {
			return errors.New("NumPy lifecycle-density child warmup generation is invalid")
		}
	}
	if envelope.Sample.RequestedSlots != spec.RequestedSlots || envelope.Sample.RuntimeShards == 0 || envelope.Sample.RuntimeShards > spec.RequestedSlots {
		return errors.New("lifecycle-density child slot or runtime-shard identity drifted")
	}
	if envelope.Backend.Name != "wazero" || envelope.Backend.Version == "" || envelope.Backend.ResetMode != "fresh-instance" {
		return errors.New("lifecycle-density child backend identity is incomplete")
	}
	if envelope.Environment.GOOS != "linux" || envelope.Environment.GOARCH == "" || envelope.Environment.GoVersion == "" ||
		envelope.Environment.KernelRelease == "" || envelope.Environment.PageSizeBytes == 0 ||
		(envelope.Environment.CgroupVersion != "v2" && envelope.Environment.CgroupVersion != "none") {
		return errors.New("lifecycle-density child environment identity is incomplete or non-Linux")
	}
	return nil
}

var errProcessRSSExited = errors.New("process exited before RSS sample")
var errProcessRSSMMReleased = errors.New("process released its userspace mm before RSS sample")

type processRSSGuardError struct {
	Observed uint64
	Limit    uint64
}

func (err *processRSSGuardError) Error() string {
	return fmt.Sprintf("lifecycle-density safety guard: RSS %d exceeds %d", err.Observed, err.Limit)
}

func processInstanceSHA256(nonce []byte, result boundedChildResult) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%x\x00%d\x00%d", nonce, result.PID, result.StartedAtUnixNS)))
	return hex.EncodeToString(digest[:])
}

type boundedChildSpec struct {
	args        []string
	environment []string
	timeout     time.Duration
	maxRSSBytes uint64
}

type boundedChildResult struct {
	PID                 int
	StartedAtUnixNS     int64
	MaxObservedRSSBytes uint64
	Stdout              []byte
	StderrTail          string
	StderrBytes         uint64
}

type boundedChildRunner struct {
	executable   string
	pollInterval time.Duration
	readRSSBytes func(int) (uint64, error)
	stdoutLimit  int
	stderrLimit  int
}

func (runner boundedChildRunner) run(parent context.Context, spec boundedChildSpec) (boundedChildResult, error) {
	if runner.executable == "" || spec.timeout <= 0 || spec.maxRSSBytes == 0 {
		return boundedChildResult{}, errors.New("bounded child command lacks executable, timeout, or RSS guard")
	}
	pollInterval := runner.pollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Millisecond
	}
	readRSSBytes := runner.readRSSBytes
	if readRSSBytes == nil {
		readRSSBytes = defaultProcessRSSBytes
	}
	stdoutLimit := runner.stdoutLimit
	if stdoutLimit <= 0 {
		stdoutLimit = defaultDensityChildOutputLimit
	}
	stderrLimit := runner.stderrLimit
	if stderrLimit <= 0 {
		stderrLimit = defaultDensityChildStderrLimit
	}

	stdout := &cappedBuffer{limit: stdoutLimit}
	stderr := &tailBuffer{limit: stderrLimit}
	command := exec.Command(runner.executable, spec.args...)
	command.Env = append(os.Environ(), spec.environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	startedAt := time.Now()
	if err := command.Start(); err != nil {
		return boundedChildResult{}, fmt.Errorf("start lifecycle-density child: %w", err)
	}
	result := boundedChildResult{PID: command.Process.Pid, StartedAtUnixNS: startedAt.UnixNano()}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	finish := func(waitErr error) (boundedChildResult, error) {
		result.Stdout = append([]byte(nil), stdout.Bytes()...)
		result.StderrTail = stderr.String()
		result.StderrBytes = stderr.written
		if stdout.overflow {
			return result, fmt.Errorf("lifecycle-density child stdout limit %d exceeded", stdoutLimit)
		}
		if waitErr != nil {
			return result, fmt.Errorf("lifecycle-density child failed: %w; stderr tail: %s", waitErr, strings.TrimSpace(result.StderrTail))
		}
		if result.StderrBytes != 0 {
			return result, fmt.Errorf("lifecycle-density child emitted %d unexpected stderr bytes; stderr tail: %s", result.StderrBytes, strings.TrimSpace(result.StderrTail))
		}
		return result, nil
	}
	timer := time.NewTimer(spec.timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var rssErr error
	var rssErrorSince time.Time
	var rssProcessExited bool
	sampleRSS := func() error {
		rss, err := readRSSBytes(result.PID)
		if err != nil {
			rssErr = err
			// Linux proc_pid_status omits VmRSS when get_task_mm() returns nil.
			// A child that already produced positive RSS cannot regain task->mm
			// after exit_mm(), but its multi-GiB mmput teardown can precede Z state.
			rssProcessExited = errors.Is(err, errProcessRSSExited) ||
				(errors.Is(err, errProcessRSSMMReleased) && result.MaxObservedRSSBytes > 0)
			if rssProcessExited {
				rssErrorSince = time.Time{}
				return nil
			}
			if rssErrorSince.IsZero() {
				rssErrorSince = time.Now()
			}
			return nil
		}
		rssErr = nil
		rssErrorSince = time.Time{}
		rssProcessExited = false
		if rss > result.MaxObservedRSSBytes {
			result.MaxObservedRSSBytes = rss
		}
		if rss > spec.maxRSSBytes {
			return &processRSSGuardError{Observed: rss, Limit: spec.maxRSSBytes}
		}
		return nil
	}
	resolveRSSGuard := func(guardErr error) (boundedChildResult, error) {
		select {
		case waitErr := <-wait:
			return finish(waitErr)
		default:
		}
		if parent.Err() != nil {
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child cancelled: %w", parent.Err())
		}
		select {
		case <-timer.C:
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child timeout after %s", spec.timeout)
		default:
		}
		// A slow RSS probe can return after the child has already exited while the
		// Wait goroutine is still waiting to publish that terminal state. Give
		// exit/cancel/timeout one bounded scheduler window before enforcing the
		// guard so a stale sample cannot mask the real disposition.
		terminalGrace := pollInterval
		if terminalGrace > 10*time.Millisecond {
			terminalGrace = 10 * time.Millisecond
		}
		graceTimer := time.NewTimer(terminalGrace)
		select {
		case waitErr := <-wait:
			graceTimer.Stop()
			return finish(waitErr)
		case <-parent.Done():
			graceTimer.Stop()
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child cancelled: %w", parent.Err())
		case <-timer.C:
			graceTimer.Stop()
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child timeout after %s", spec.timeout)
		case <-graceTimer.C:
		}

		killErr := command.Process.Kill()
		waitErr := <-wait
		result, finishErr := finish(waitErr)
		if parent.Err() != nil {
			return result, fmt.Errorf("lifecycle-density child cancelled: %w", parent.Err())
		}
		select {
		case <-timer.C:
			return result, fmt.Errorf("lifecycle-density child timeout after %s", spec.timeout)
		default:
		}
		if killErr != nil {
			if finishErr != nil {
				return result, finishErr
			}
			return result, fmt.Errorf("kill lifecycle-density child at RSS guard: %w", killErr)
		}
		var exitErr *exec.ExitError
		if finishErr == nil || !errors.As(finishErr, &exitErr) || exitErr.ExitCode() != -1 {
			if finishErr == nil {
				finishErr = errors.New("child exited without the expected guard signal")
			}
			return result, fmt.Errorf("lifecycle-density RSS guard termination was not confirmed: %w", finishErr)
		}
		return result, guardErr
	}
	if err := sampleRSS(); err != nil {
		return resolveRSSGuard(err)
	}

	for {
		if parent.Err() != nil {
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child cancelled: %w", parent.Err())
		}
		select {
		case waitErr := <-wait:
			result, finishErr := finish(waitErr)
			if finishErr != nil {
				return result, finishErr
			}
			if result.MaxObservedRSSBytes == 0 {
				if rssErr == nil {
					rssErr = errors.New("process RSS was never observed")
				}
				return result, fmt.Errorf("lifecycle-density child exited before initial RSS evidence: %w", rssErr)
			}
			return result, nil
		default:
		}
		select {
		case <-timer.C:
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child timeout after %s", spec.timeout)
		default:
		}
		select {
		case waitErr := <-wait:
			result, finishErr := finish(waitErr)
			if finishErr != nil {
				return result, finishErr
			}
			if result.MaxObservedRSSBytes == 0 {
				if rssErr == nil {
					rssErr = errors.New("process RSS was never observed")
				}
				return result, fmt.Errorf("lifecycle-density child exited before initial RSS evidence: %w", rssErr)
			}
			return result, nil
		case <-parent.Done():
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child cancelled: %w", parent.Err())
		case <-timer.C:
			_ = command.Process.Kill()
			waitErr := <-wait
			result, _ = finish(waitErr)
			return result, fmt.Errorf("lifecycle-density child timeout after %s", spec.timeout)
		case <-ticker.C:
			if err := sampleRSS(); err != nil {
				return resolveRSSGuard(err)
			}
			if !rssProcessExited && rssErr != nil && time.Since(rssErrorSince) >= 100*time.Millisecond {
				_ = command.Process.Kill()
				waitErr := <-wait
				result, _ = finish(waitErr)
				return result, fmt.Errorf("read lifecycle-density child RSS: %w", rssErr)
			}
		}
	}
}

type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (buffer *cappedBuffer) Write(content []byte) (int, error) {
	originalLength := len(content)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(content) > remaining {
			content = content[:remaining]
		}
		_, _ = buffer.buffer.Write(content)
	}
	if originalLength > remaining {
		buffer.overflow = true
	}
	return originalLength, nil
}

func (buffer *cappedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

type tailBuffer struct {
	content []byte
	limit   int
	written uint64
}

func (buffer *tailBuffer) Write(content []byte) (int, error) {
	originalLength := len(content)
	buffer.written += uint64(originalLength)
	if buffer.limit <= 0 {
		return originalLength, nil
	}
	if len(content) >= buffer.limit {
		buffer.content = append(buffer.content[:0], content[len(content)-buffer.limit:]...)
		return originalLength, nil
	}
	if overflow := len(buffer.content) + len(content) - buffer.limit; overflow > 0 {
		copy(buffer.content, buffer.content[overflow:])
		buffer.content = buffer.content[:len(buffer.content)-overflow]
	}
	buffer.content = append(buffer.content, content...)
	return originalLength, nil
}

func (buffer *tailBuffer) String() string { return string(buffer.content) }
