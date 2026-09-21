package scheduling

import (
	"math"
	"testing"
	"time"
)

func TestRunCanonicalSweepFindsResidentAndIORatioBoundary(t *testing.T) {
	points, boundaries, err := RunCanonicalSweep(CanonicalSweep{
		TaskCounts:          []int{2},
		IORatios:            []float64{0.1, 1, 10},
		RunningLimits:       []int{1},
		ResidentMultipliers: []int{1, 2},
		ToolLimits:          []int{2},
		Policies:            []Policy{FIFO},
		CPUBefore:           time.Millisecond,
		CPUAfter:            time.Millisecond,
		ResidentBytes:       8 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 6 || len(boundaries) != 2 {
		t.Fatalf("points=%d boundaries=%d", len(points), len(boundaries))
	}
	for _, point := range points {
		if point.ResidentMultiplier == 1 && point.Speedup != 1 {
			t.Fatalf("resident-constrained point gained capacity: %+v", point)
		}
	}
	var expanded SweepBoundary
	for _, boundary := range boundaries {
		if boundary.ResidentMultiplier == 2 {
			expanded = boundary
		}
	}
	if expanded.FirstFivePercentRatio == nil || expanded.FirstTenPercentRatio == nil {
		t.Fatalf("missing useful boundary: %+v", expanded)
	}
	if expanded.MaxSpeedup < 1.5 || expanded.MaxSpeedupRatio != 10 {
		t.Fatalf("unexpected maximum: %+v", expanded)
	}
}

func TestRunCanonicalSweepRejectsInvalidGrid(t *testing.T) {
	valid := CanonicalSweep{
		TaskCounts:          []int{2},
		IORatios:            []float64{1},
		RunningLimits:       []int{1},
		ResidentMultipliers: []int{2},
		ToolLimits:          []int{1},
		Policies:            []Policy{FIFO},
		CPUBefore:           time.Millisecond,
		CPUAfter:            time.Millisecond,
	}
	cases := []CanonicalSweep{
		{},
		func() CanonicalSweep { broken := valid; broken.IORatios = []float64{0}; return broken }(),
		func() CanonicalSweep { broken := valid; broken.IORatios = []float64{math.MaxFloat64}; return broken }(),
		func() CanonicalSweep {
			broken := valid
			broken.TaskCounts = []int{maxCanonicalTasksPerPoint + 1}
			return broken
		}(),
		func() CanonicalSweep {
			broken := valid
			broken.TaskCounts = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
			broken.IORatios = []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
			broken.RunningLimits = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
			broken.ResidentMultipliers = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
			broken.ToolLimits = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
			broken.Policies = []Policy{FIFO, ReadyFirst, FinishSoon}
			return broken
		}(),
		func() CanonicalSweep { broken := valid; broken.Policies = []Policy{"other"}; return broken }(),
	}
	for _, test := range cases {
		if _, _, err := RunCanonicalSweep(test); err == nil {
			t.Fatalf("invalid sweep accepted: %+v", test)
		}
	}
}

func TestCanonicalSweepOrderingIsStable(t *testing.T) {
	points, boundaries, err := RunCanonicalSweep(CanonicalSweep{
		TaskCounts:          []int{4, 2},
		IORatios:            []float64{10, 1},
		RunningLimits:       []int{2, 1},
		ResidentMultipliers: []int{2},
		ToolLimits:          []int{2},
		Policies:            []Policy{ReadyFirst, FIFO},
		CPUBefore:           time.Millisecond,
		CPUAfter:            time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if points[0].Tasks != 2 || points[0].IORatio != 1 || points[0].RunningLimit != 1 || points[0].Policy != FIFO {
		t.Fatalf("first point=%+v", points[0])
	}
	if boundaries[0].Tasks != 2 || boundaries[0].RunningLimit != 1 || boundaries[0].Policy != FIFO {
		t.Fatalf("first boundary=%+v", boundaries[0])
	}
}

func TestRunCanonicalSweepRejectsOverflowingResidentLimit(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	_, _, err := RunCanonicalSweep(CanonicalSweep{
		TaskCounts: []int{1}, IORatios: []float64{1}, RunningLimits: []int{maxInt},
		ResidentMultipliers: []int{2}, ToolLimits: []int{1}, Policies: []Policy{FIFO},
		CPUBefore: time.Nanosecond, CPUAfter: time.Nanosecond,
	})
	if err == nil {
		t.Fatal("overflowing resident limit accepted")
	}
}
