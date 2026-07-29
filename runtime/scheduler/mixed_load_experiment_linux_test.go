//go:build linux

package scheduler

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type e2ActiveMapping struct {
	task        e2WorkloadTask
	attempt     AttemptSnapshot
	memory      []byte
	startedTick uint64
	dueTick     uint64
}

func TestE2MixedLoadLiveMemoryExperiment(t *testing.T) {
	if os.Getenv("APYRUN_E2_RUN") != "1" {
		t.Skip("set APYRUN_E2_RUN=1 for the bounded Linux experiment")
	}
	taskCount := e2BoundedEnv(t, "APYRUN_E2_TASKS", 200, 100, 1000)
	if taskCount%100 != 0 {
		t.Fatal("APYRUN_E2_TASKS must be a multiple of 100")
	}
	budget := uint32(e2BoundedEnv(t, "APYRUN_E2_TARGET_EVICTION_PPM", 10_000, 1_000, 50_000))
	if budget != 1_000 && budget != 10_000 && budget != 50_000 {
		t.Fatal("unsupported eviction budget")
	}
	workload, err := buildE2MixedWorkload(taskCount)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewCurrentCgroupV2MemoryReader()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	const mib = uint64(1 << 20)
	if baseline.MaximumBytes <= baseline.CurrentBytes+1024*mib {
		t.Fatalf("insufficient bounded cgroup headroom: %#v", baseline)
	}
	tick := uint64(0)
	clockBase := time.Unix(10_000, 0).UTC()
	clock := func() time.Time { return clockBase.Add(time.Duration(tick) * time.Millisecond) }
	scheduler, err := New(Config{
		TargetBytes: baseline.CurrentBytes + 256*mib, HighBytes: baseline.CurrentBytes + 320*mib,
		CriticalBytes: baseline.CurrentBytes + 448*mib, HardBytes: baseline.CurrentBytes + 768*mib,
		MaxTasks: uint32(taskCount), MaxAttempts: uint32(taskCount * 3), RetryMarginBytes: mib, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	profileOptions := profileConfig()
	profileOptions.ReservationQuantileBPS = 9500
	profileOptions.MaxProfiles = 8
	profileOptions.MaxTrackedAttempts = uint32(taskCount * 3)
	profileOptions.MaxAggregateSamples = uint32(taskCount * 3)
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	greedOptions := greedConfig()
	greedOptions.TargetEvictionPPM = budget
	greedOptions.MinimumAttempts = 1
	greedOptions.MaximumSomePSIAvg10BPS = 100
	greedOptions.MaximumFullPSIAvg10BPS = 50
	controller, err := NewGreedController(greedOptions)
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewLiveControlWindowTracker()
	tasks := make(map[string]e2WorkloadTask, len(workload))
	for _, task := range workload {
		tasks[task.TaskID] = task
		if _, err := scheduler.Submit(TaskSpec{
			TaskID: task.TaskID, ProfileKey: "e2_" + string(task.Class), Lane: LaneSpeculative,
			ReservationBytes: task.ReservationBytes, MaxEvictions: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}
	active := make(map[string]*e2ActiveMapping)
	defer func() {
		for _, mapping := range active {
			_ = unix.Munmap(mapping.memory)
		}
	}()
	firstStarted := make(map[string]uint64, taskCount)
	queueWaits := make([]uint64, 0, taskCount)
	report := e2MixedLoadReport{Schema: "apyrun.e2-mixed-load/v1", TargetEvictionPPM: budget, Tasks: uint64(taskCount)}
	observe := func() (LiveMemorySnapshot, PressureLevel) {
		snapshot, err := reader.Read()
		if err != nil {
			t.Fatal(err)
		}
		window, err := tracker.Build(scheduler, snapshot, report.Attempts, report.Evictions)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := controller.Apply(store, window)
		if err != nil {
			t.Fatal(err)
		}
		report.ControlWindows++
		switch decision.Direction {
		case GreedMoreAggressive:
			report.AggressiveDecisions++
		case GreedMoreConservative:
			report.ConservativeDecisions++
		case GreedHold:
			report.HoldDecisions++
		default:
			t.Fatalf("unknown greed direction %q", decision.Direction)
		}
		if snapshot.CurrentBytes > report.MaxCurrentBytes {
			report.MaxCurrentBytes = snapshot.CurrentBytes
		}
		if snapshot.UtilizationBPS() > report.MaxUtilizationBPS {
			report.MaxUtilizationBPS = snapshot.UtilizationBPS()
		}
		return snapshot, window.Pressure
	}
	for report.Completed < report.Tasks && tick < 5000 {
		for attemptID, mapping := range active {
			if mapping.dueTick > tick {
				continue
			}
			if err := unix.Munmap(mapping.memory); err != nil {
				t.Fatal(err)
			}
			if _, err := scheduler.Complete(attemptID); err != nil {
				t.Fatal(err)
			}
			delete(active, attemptID)
			report.Completed++
			report.UsefulBytes += mapping.task.ActualBytes
		}
		snapshot, _ := observe()
		if snapshot.CurrentBytes > scheduler.config.HighBytes {
			victims, err := scheduler.RequestEvictions(snapshot.CurrentBytes)
			if errors.Is(err, ErrInsufficientReclaim) {
				report.InsufficientVictimWindows++
				tick++
				time.Sleep(2 * time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, victim := range victims {
				mapping := active[victim.AttemptID]
				if mapping == nil {
					t.Fatalf("missing active victim %s", victim.AttemptID)
				}
				elapsed := tick - mapping.startedTick
				if elapsed > uint64(mapping.task.DurationTicks) {
					elapsed = uint64(mapping.task.DurationTicks)
				}
				report.WastedBytes += mapping.task.ActualBytes * elapsed / uint64(mapping.task.DurationTicks)
				if err := unix.Munmap(mapping.memory); err != nil {
					t.Fatal(err)
				}
				task, err := scheduler.ConfirmReclaimed(victim.AttemptID, ReclaimReport{
					ExecutorTerminated: true, ObservedFootprintBytes: mapping.task.ActualBytes, ReclaimedBytes: mapping.task.ActualBytes,
				})
				if err != nil {
					t.Fatal(err)
				}
				delete(active, victim.AttemptID)
				report.Evictions++
				report.Retries++
				if task.Lane == LaneGuaranteed {
					report.GuaranteedFallbacks++
				}
			}
			if len(victims) > 0 {
				time.Sleep(time.Millisecond)
				_, _ = observe()
			}
		}
		for len(active) < 64 {
			attempt, err := scheduler.Admit()
			if errors.Is(err, ErrNoAdmissibleTask) || errors.Is(err, ErrCapacity) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			attempt, err = scheduler.Start(attempt.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			task := tasks[attempt.TaskID]
			memory, err := unix.Mmap(-1, 0, int(task.ActualBytes), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
			if err != nil {
				t.Fatal(err)
			}
			for offset := 0; offset < len(memory); offset += unix.Getpagesize() {
				memory[offset] = byte(offset>>12) | 1
			}
			if _, err := scheduler.UpdateReclaimable(attempt.AttemptID, task.ActualBytes); err != nil {
				_ = unix.Munmap(memory)
				t.Fatal(err)
			}
			active[attempt.AttemptID] = &e2ActiveMapping{task: task, attempt: attempt, memory: memory, startedTick: tick, dueTick: tick + uint64(task.DurationTicks)}
			report.Attempts++
			if uint64(len(active)) > report.MaxActive {
				report.MaxActive = uint64(len(active))
			}
			if _, ok := firstStarted[task.TaskID]; !ok {
				firstStarted[task.TaskID] = tick
				queueWaits = append(queueWaits, tick)
			}
			if task.ActualBytes >= attempt.ReservedBytes {
				report.ReservationAbsoluteErrorBytes += task.ActualBytes - attempt.ReservedBytes
			} else {
				report.ReservationAbsoluteErrorBytes += attempt.ReservedBytes - task.ActualBytes
			}
		}
		tick++
		time.Sleep(time.Millisecond)
	}
	if report.Completed != report.Tasks || len(active) != 0 || len(queueWaits) != taskCount {
		t.Fatalf("experiment did not drain: completed=%d active=%d waits=%d tick=%d", report.Completed, len(active), len(queueWaits), tick)
	}
	finalSnapshot, _ := observe()
	if finalSnapshot.Events.OOM < baseline.Events.OOM || finalSnapshot.Events.OOMKill < baseline.Events.OOMKill {
		t.Fatal(ErrLiveMemoryCounterReset)
	}
	report.OOMEvents = finalSnapshot.Events.OOM - baseline.Events.OOM + finalSnapshot.Events.OOMKill - baseline.Events.OOMKill
	report.QueueWaitTicks, err = e2NearestRankQuantiles(queueWaits)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("report validation: %v: %#v", err, report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("E2_REPORT %s", encoded)
}

func e2BoundedEnv(t *testing.T, name string, fallback, minimum, maximum int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		t.Fatalf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed
}
