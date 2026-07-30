package scheduler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCompileProductionPolicyEndpoints(t *testing.T) {
	conservative, err := CompileProductionPolicy(ProductionPolicy{MaxMemoryBytes: 16 << 30, MaxCPU: 12, Greed: 0})
	if err != nil {
		t.Fatal(err)
	}
	if conservative.Version != ProductionPolicyVersion || conservative.Scheduler.HardBytes != 16<<30 || conservative.MaxCPU != 12 || conservative.Greed != 0 {
		t.Fatalf("conservative identity=%+v", conservative)
	}
	if conservative.TargetMemoryBPS != 8000 || conservative.HighMemoryBPS != 8800 || conservative.CriticalMemoryBPS != 9500 || conservative.MaxActive != 12 ||
		conservative.Profiles.ReservationQuantileBPS != 10000 || conservative.GreedController.TargetEvictionPPM != 0 || conservative.SpeculativeMaxEvictions != 0 ||
		conservative.ControlInterval != 200*time.Millisecond || conservative.DefaultRetryDelay != 250*time.Millisecond {
		t.Fatalf("conservative policy=%+v", conservative)
	}

	aggressive, err := CompileProductionPolicy(ProductionPolicy{MaxMemoryBytes: 16 << 30, MaxCPU: 12, Greed: 100})
	if err != nil {
		t.Fatal(err)
	}
	if aggressive.TargetMemoryBPS != 9000 || aggressive.HighMemoryBPS != 9400 || aggressive.CriticalMemoryBPS != 9800 || aggressive.MaxActive != 48 ||
		aggressive.Profiles.ReservationQuantileBPS != 9000 || aggressive.GreedController.TargetEvictionPPM != 50000 || aggressive.SpeculativeMaxEvictions != 2 ||
		aggressive.ControlInterval != 50*time.Millisecond || aggressive.DefaultRetryDelay != 50*time.Millisecond {
		t.Fatalf("aggressive policy=%+v", aggressive)
	}
	if aggressive.CPUPeriodMicros != 100000 || aggressive.CPUQuotaMicros != 1200000 {
		t.Fatalf("cpu quota=%d/%d", aggressive.CPUQuotaMicros, aggressive.CPUPeriodMicros)
	}
}

func TestCompileProductionPolicyIsBoundedAndMonotonic(t *testing.T) {
	var prior EffectivePolicy
	for greed := uint8(0); greed <= 100; greed++ {
		effective, err := CompileProductionPolicy(ProductionPolicy{MaxMemoryBytes: 64 << 30, MaxCPU: 32, Greed: greed})
		if err != nil {
			t.Fatalf("greed=%d: %v", greed, err)
		}
		if err := effective.Scheduler.validate(); err != nil {
			t.Fatalf("greed=%d scheduler: %v", greed, err)
		}
		if err := effective.Profiles.validate(); err != nil {
			t.Fatalf("greed=%d profiles: %v", greed, err)
		}
		if _, err := NewGreedController(effective.GreedController); err != nil {
			t.Fatalf("greed=%d controller: %v", greed, err)
		}
		if effective.Scheduler.HardBytes != 64<<30 || effective.MaxCPU != 32 || effective.Greed != greed || effective.MaxActive == 0 || effective.MaxActive > maxProductionActive ||
			effective.Scheduler.TargetBytes >= effective.Scheduler.HighBytes || effective.Scheduler.HighBytes >= effective.Scheduler.CriticalBytes || effective.Scheduler.CriticalBytes >= effective.Scheduler.HardBytes ||
			effective.UnknownReservationBytes == 0 || effective.UnknownReservationBytes > effective.Scheduler.TargetBytes {
			t.Fatalf("greed=%d invalid bounds: %+v", greed, effective)
		}
		if greed > 0 && (effective.MaxActive < prior.MaxActive || effective.TargetMemoryBPS < prior.TargetMemoryBPS || effective.HighMemoryBPS < prior.HighMemoryBPS ||
			effective.CriticalMemoryBPS < prior.CriticalMemoryBPS || effective.Profiles.ReservationQuantileBPS > prior.Profiles.ReservationQuantileBPS ||
			effective.GreedController.TargetEvictionPPM < prior.GreedController.TargetEvictionPPM || effective.ControlInterval > prior.ControlInterval) {
			t.Fatalf("greed=%d is not monotonic: prior=%+v current=%+v", greed, prior, effective)
		}
		prior = effective
		if greed == 100 {
			break
		}
	}
}

func TestCompileProductionPolicyRejectsInvalidPublicKnobs(t *testing.T) {
	for name, policy := range map[string]ProductionPolicy{
		"small memory": {MaxMemoryBytes: minimumProductionMemoryBytes - 1, MaxCPU: 1},
		"large memory": {MaxMemoryBytes: maximumProductionMemoryBytes + 1, MaxCPU: 1},
		"zero cpu":     {MaxMemoryBytes: 1 << 30},
		"large cpu":    {MaxMemoryBytes: 1 << 30, MaxCPU: maximumProductionCPU + 1},
		"large greed":  {MaxMemoryBytes: 1 << 30, MaxCPU: 1, Greed: 101},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CompileProductionPolicy(policy); !errors.Is(err, ErrInvalidProductionPolicy) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCompileProductionPolicyRecordBoundsDoNotOverflow(t *testing.T) {
	effective, err := CompileProductionPolicy(ProductionPolicy{MaxMemoryBytes: maximumProductionMemoryBytes, MaxCPU: maximumProductionCPU, Greed: 100})
	if err != nil {
		t.Fatal(err)
	}
	if effective.MaxActive != maxProductionActive || effective.Scheduler.MaxTasks > 1<<20 || effective.Scheduler.MaxAttempts > 1<<22 || effective.CPUQuotaMicros != uint64(maximumProductionCPU)*effective.CPUPeriodMicros {
		t.Fatalf("effective=%+v", effective)
	}
}

func TestEffectiveProductionPolicyTelemetryIsStableJSON(t *testing.T) {
	effective, err := CompileProductionPolicy(ProductionPolicy{MaxMemoryBytes: 16 << 30, MaxCPU: 12, Greed: 50})
	if err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(effective.Telemetry())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(effective.Telemetry())
	if err != nil || string(first) != string(second) {
		t.Fatalf("unstable telemetry: %s / %s / %v", first, second, err)
	}
	for _, field := range []string{`"version":"production-policy-v1"`, `"max_memory_bytes":17179869184`, `"max_cpu":12`, `"greed":50`, `"hard_memory_bytes":17179869184`, `"reservation_quantile_bps":9500`} {
		if !strings.Contains(string(first), field) {
			t.Fatalf("telemetry %s lacks %s", first, field)
		}
	}
}
