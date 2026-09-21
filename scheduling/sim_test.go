package scheduling

import (
	"testing"
	"time"
)

func TestLiveIOReusesRunningCapacity(t *testing.T) {
	workload := []Task{
		{ID: "a", ResidentBytes: 8 << 20, Phases: []Phase{{Kind: CPU, Duration: 10 * time.Millisecond}, {Kind: ExternalIO, Duration: 100 * time.Millisecond}, {Kind: CPU, Duration: 10 * time.Millisecond}}},
		{ID: "b", ResidentBytes: 8 << 20, Phases: []Phase{{Kind: CPU, Duration: 10 * time.Millisecond}, {Kind: ExternalIO, Duration: 100 * time.Millisecond}, {Kind: CPU, Duration: 10 * time.Millisecond}}},
	}
	base := Config{MaxRunning: 1, MaxResident: 2, MaxInflightTools: 2, Policy: FIFO}
	inline, err := Simulate(workload, base)
	if err != nil {
		t.Fatal(err)
	}
	base.LiveIO = true
	live, err := Simulate(workload, base)
	if err != nil {
		t.Fatal(err)
	}
	if inline.Completed != 2 || live.Completed != 2 {
		t.Fatalf("inline=%+v live=%+v", inline, live)
	}
	if inline.Makespan != 240*time.Millisecond || live.Makespan != 130*time.Millisecond {
		t.Fatalf("inline makespan=%s live=%s", inline.Makespan, live.Makespan)
	}
	if live.PeakResident != 2 || live.PeakRunning != 1 || live.PeakInflightTools != 2 {
		t.Fatalf("live peaks=%+v", live)
	}
	if live.PeakResident <= inline.PeakResident || live.RunningSlotTime >= inline.RunningSlotTime {
		t.Fatalf("live scheduling did not expose the expected capacity tradeoff: inline=%+v live=%+v", inline, live)
	}
	if difference := live.ResidentMiBSeconds - inline.ResidentMiBSeconds; difference < -1e-9 || difference > 1e-9 {
		t.Fatalf("equivalent work changed resident byte-time: inline=%v live=%v", inline.ResidentMiBSeconds, live.ResidentMiBSeconds)
	}
}

func TestReadyFirstAndFinishSoonPreferShortContinuation(t *testing.T) {
	workload := []Task{
		{ID: "a", Phases: []Phase{{Kind: CPU, Duration: time.Millisecond}, {Kind: ExternalIO, Duration: 10 * time.Millisecond}, {Kind: CPU, Duration: time.Millisecond}}},
		{ID: "b", Phases: []Phase{{Kind: CPU, Duration: 20 * time.Millisecond}}},
		{ID: "c", Arrival: 2 * time.Millisecond, Phases: []Phase{{Kind: CPU, Duration: 50 * time.Millisecond}}},
	}
	config := Config{MaxRunning: 1, MaxResident: 2, MaxInflightTools: 1, LiveIO: true, Policy: FIFO}
	fifo, err := Simulate(workload, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Policy = ReadyFirst
	ready, err := Simulate(workload, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Policy = FinishSoon
	short, err := Simulate(workload, config)
	if err != nil {
		t.Fatal(err)
	}
	if fifo.Tasks["a"].CompletedAt < fifo.Tasks["c"].CompletedAt {
		t.Fatalf("FIFO unexpectedly preferred continuation: %+v", fifo.Tasks)
	}
	if ready.Tasks["a"].CompletedAt >= ready.Tasks["c"].CompletedAt {
		t.Fatalf("ready-first did not prefer continuation: %+v", ready.Tasks)
	}
	if short.Tasks["a"].CompletedAt >= short.Tasks["c"].CompletedAt {
		t.Fatalf("finish-soon did not prefer short continuation: %+v", short.Tasks)
	}
	if ready.Makespan != fifo.Makespan || short.Makespan != fifo.Makespan {
		t.Fatalf("work conservation changed makespan: fifo=%s ready=%s short=%s", fifo.Makespan, ready.Makespan, short.Makespan)
	}
}

func TestSimulationRejectsInvalidInputsAndHonorsQueueLimit(t *testing.T) {
	if _, err := Simulate(nil, Config{}); err == nil {
		t.Fatal("invalid config accepted")
	}
	workload := []Task{
		{ID: "a", Phases: []Phase{{Kind: CPU, Duration: 10 * time.Millisecond}}},
		{ID: "b", Phases: []Phase{{Kind: CPU, Duration: 10 * time.Millisecond}}},
		{ID: "c", Phases: []Phase{{Kind: CPU, Duration: 10 * time.Millisecond}}},
	}
	result, err := Simulate(workload, Config{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1, Policy: FIFO})
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 2 || result.Rejected != 1 || !result.Tasks["c"].Rejected {
		t.Fatalf("result=%+v", result)
	}
}
