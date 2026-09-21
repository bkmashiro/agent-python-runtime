package scheduling

import (
	"strings"
	"testing"
	"time"
)

const phaseCaptureFixture = `{"type":"metadata","preparation":"copy","cases":"all","iterations":2,"synthetic_tool_delay_ns":50000000,"external_io":true}
{"type":"sample","case":"read-finish","iteration":0,"first_attempt_ns":70000000,"tool_service_ns":50000000,"tool_queue_ns":1000,"tool_resume_ns":2000,"tool_dispatches":1}
{"type":"sample","case":"tool-chain","iteration":1,"first_attempt_ns":130000000,"tool_service_ns":100000000,"tool_queue_ns":2000,"tool_resume_ns":4000,"tool_dispatches":2}
`

func TestDecodePhaseCaptureAndBuildCalibratedTasks(t *testing.T) {
	capture, err := DecodePhaseCapture(strings.NewReader(phaseCaptureFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Samples) != 2 || !capture.Metadata.ExternalIO {
		t.Fatalf("capture=%+v", capture)
	}
	tasks, provenance, err := BuildCalibratedTasks(capture, CalibrationOptions{Case: "read-finish", ResidentBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "read-finish-000000" || tasks[0].ResidentBytes != 8<<20 {
		t.Fatalf("tasks=%+v", tasks)
	}
	if len(tasks[0].Phases) != 3 || tasks[0].Phases[1].Kind != ExternalIO || tasks[0].Phases[1].Duration != 50*time.Millisecond {
		t.Fatalf("phases=%+v", tasks[0].Phases)
	}
	local := tasks[0].Phases[0].Duration + tasks[0].Phases[2].Duration
	if want := 70*time.Millisecond - 50*time.Millisecond - time.Microsecond - 2*time.Microsecond; local != want {
		t.Fatalf("local=%s want=%s", local, want)
	}
	if provenance.CPUSplit != "equal_between_tool_calls" || provenance.ExcludedQueueNS != 1000 || provenance.ExcludedResumeNS != 2000 {
		t.Fatalf("provenance=%+v", provenance)
	}
}

func TestBuildCalibratedTasksSupportsLocalAndToolChain(t *testing.T) {
	capture, err := DecodePhaseCapture(strings.NewReader(phaseCaptureFixture))
	if err != nil {
		t.Fatal(err)
	}
	tasks, _, err := BuildCalibratedTasks(capture, CalibrationOptions{Case: "tool-chain"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || len(tasks[0].Phases) != 5 {
		t.Fatalf("tasks=%+v", tasks)
	}
	if tasks[0].Phases[1].Duration+tasks[0].Phases[3].Duration != 100*time.Millisecond {
		t.Fatalf("I/O phases=%+v", tasks[0].Phases)
	}
}

func TestBuildCalibratedTasksRejectsParkAndInvalidResidual(t *testing.T) {
	park := phaseCaptureFixture + `{"type":"sample","case":"park-readmit","iteration":0,"first_attempt_ns":70000000,"park_interval_ns":200000000,"readmit_attempt_ns":30000000,"tool_service_ns":50000000,"tool_dispatches":1}
`
	capture, err := DecodePhaseCapture(strings.NewReader(park))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildCalibratedTasks(capture, CalibrationOptions{Case: "park-readmit"}); err == nil {
		t.Fatal("durable park accepted as a live-I/O calibrated trace")
	}
	broken, err := DecodePhaseCapture(strings.NewReader(`{"type":"metadata","preparation":"copy","cases":"read-finish","iterations":1,"external_io":true}
{"type":"sample","case":"read-finish","iteration":0,"first_attempt_ns":10,"tool_service_ns":9,"tool_queue_ns":1,"tool_resume_ns":1,"tool_dispatches":1}
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildCalibratedTasks(broken, CalibrationOptions{Case: "read-finish"}); err == nil {
		t.Fatal("negative local CPU residual accepted")
	}
	broken.Samples[0].FirstAttemptNS = 10_000
	broken.Samples[0].ToolServiceNS = 5_000
	broken.Samples[0].ToolQueueNS = 0
	broken.Samples[0].ToolResumeNS = 0
	broken.Samples[0].ToolDispatches = maxCalibrationToolDispatches + 1
	if _, _, err := BuildCalibratedTasks(broken, CalibrationOptions{Case: "read-finish"}); err == nil {
		t.Fatal("excessive Tool dispatch count accepted")
	}
}

func TestDecodePhaseCaptureIsStrict(t *testing.T) {
	cases := []string{
		`{"type":"sample","case":"x","iteration":0,"first_attempt_ns":1,"tool_dispatches":0}`,
		`{"type":"metadata","preparation":"copy","cases":"x","iterations":1,"external_io":true,"unknown":1}`,
		`{"type":"metadata","preparation":"copy","cases":"x","iterations":1,"external_io":true}
{"type":"mystery"}`,
	}
	for _, fixture := range cases {
		if _, err := DecodePhaseCapture(strings.NewReader(fixture)); err == nil {
			t.Fatalf("invalid capture accepted: %s", fixture)
		}
	}
}

func TestCompareObservedBatchesUsesIdenticalCalibratedTasks(t *testing.T) {
	capture, err := DecodePhaseCapture(strings.NewReader(phaseCaptureFixture))
	if err != nil {
		t.Fatal(err)
	}
	templates, _, err := BuildCalibratedTasks(capture, CalibrationOptions{Case: "read-finish"})
	if err != nil {
		t.Fatal(err)
	}
	rows := []BatchObservation{
		{Case: "read-finish", Tasks: 2, RunningLimit: 1, ResidentLimit: 2, ToolLimit: 2, ExternalIO: false, BatchNS: 140_000_000},
		{Case: "read-finish", Tasks: 2, RunningLimit: 1, ResidentLimit: 2, ToolLimit: 2, ExternalIO: true, BatchNS: 90_000_000},
	}
	comparisons, err := CompareObservedBatches(templates, rows, FIFO, 0.15)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 2 || comparisons[0].PredictedMakespanNS <= 0 || comparisons[1].PredictedMakespanNS >= comparisons[0].PredictedMakespanNS {
		t.Fatalf("comparisons=%+v", comparisons)
	}
	if comparisons[0].RelativeError < 0 || comparisons[0].WithinTolerance != (comparisons[0].RelativeError <= 0.15) {
		t.Fatalf("comparison=%+v", comparisons[0])
	}
	rows[0].Tasks = maxCalibrationPopulation + 1
	if _, err := CompareObservedBatches(templates, rows[:1], FIFO, 0.15); err == nil {
		t.Fatal("excessive observed population accepted")
	}
	summaries := SummarizeCalibrationComparisons(append(comparisons, comparisons...))
	if len(summaries) != 2 || summaries[0].Samples != 2 || summaries[0].MeanRelativeError != comparisons[0].RelativeError {
		t.Fatalf("summaries=%+v", summaries)
	}
}
