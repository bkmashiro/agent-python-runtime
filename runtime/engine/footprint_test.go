package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAttemptIdentityIsValidatedAndRoundTripsThroughContext(t *testing.T) {
	ctx, err := WithAttemptIdentity(context.Background(), "task:attempt:2")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := AttemptIdentityFromContext(ctx); !ok || got != "task:attempt:2" {
		t.Fatalf("identity = %q ok=%v", got, ok)
	}
	for _, value := range []string{"", "contains space", string(make([]byte, 129))} {
		if _, err := WithAttemptIdentity(context.Background(), value); !errors.Is(err, ErrInvalidAttemptIdentity) {
			t.Fatalf("WithAttemptIdentity(%q) error = %v", value, err)
		}
	}
	if _, ok := AttemptIdentityFromContext(context.Background()); ok {
		t.Fatal("untrusted context unexpectedly had an attempt identity")
	}
}

func TestMemoryReclaimObservationValidation(t *testing.T) {
	valid := MemoryReclaimObservation{
		AttemptID: "attempt:1", ObservedAt: time.Unix(12, 0).UTC(), Backend: "linux_proc_smaps",
		Strategy: StrategyCOWReadySingleUse, Status: ReclaimReleased, CloseDuration: time.Millisecond,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid reclaim observation: %v", err)
	}
	invalid := valid
	invalid.AttemptID = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("missing attempt identity was accepted")
	}
	invalid = valid
	invalid.Status = ReclaimStillMapped
	if err := invalid.Validate(); err == nil {
		t.Fatal("still-mapped observation without an error code was accepted")
	}
	invalid.ErrorCode = "mapping_present"
	if err := invalid.Validate(); err != nil {
		t.Fatalf("bounded still-mapped observation: %v", err)
	}
	invalid = valid
	invalid.Status = ReclaimReleased
	invalid.ErrorCode = "close_failed"
	if err := invalid.Validate(); err == nil {
		t.Fatal("released observation carried an error code")
	}
}

func TestFootprintObservationValidation(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	valid := FootprintObservation{
		AttemptID: "task:attempt:1",
		Backend:   "wazero",
		Strategy:  StrategyCOWReadySingleUse,
		Status:    FootprintObserved,
		SampledAt: now,
		Memory: MemoryFootprint{
			MappingCount:      1,
			VirtualBytes:      128 << 20,
			RSSBytes:          20 << 20,
			PSSBytes:          20 << 20,
			PrivateCleanBytes: 1 << 20,
			PrivateDirtyBytes: 19 << 20,
			AnonymousBytes:    19 << 20,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]FootprintObservation{
		"missing attempt":  func() FootprintObservation { value := valid; value.AttemptID = ""; return value }(),
		"invalid strategy": func() FootprintObservation { value := valid; value.Strategy = "unknown"; return value }(),
		"zero mapping":     func() FootprintObservation { value := valid; value.Memory.MappingCount = 0; return value }(),
		"rss beyond virtual": func() FootprintObservation {
			value := valid
			value.Memory.RSSBytes = value.Memory.VirtualBytes + 1
			return value
		}(),
		"pss beyond rss": func() FootprintObservation {
			value := valid
			value.Memory.PSSBytes = value.Memory.RSSBytes + 1
			return value
		}(),
		"error with observed": func() FootprintObservation { value := valid; value.ErrorCode = "read_failed"; return value }(),
		"metrics with failed": func() FootprintObservation {
			value := valid
			value.Status = FootprintFailed
			value.ErrorCode = "read_failed"
			return value
		}(),
		"failed without code": func() FootprintObservation {
			value := valid
			value.Status = FootprintFailed
			value.Memory = MemoryFootprint{}
			return value
		}(),
	}
	for name, observation := range cases {
		t.Run(name, func(t *testing.T) {
			if err := observation.Validate(); !errors.Is(err, ErrInvalidFootprintObservation) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	failed := FootprintObservation{AttemptID: "task:attempt:1", Backend: "wazero", Strategy: StrategyCOWReadySingleUse, Status: FootprintFailed, SampledAt: now, ErrorCode: "mapping_not_found"}
	if err := failed.Validate(); err != nil {
		t.Fatal(err)
	}
}
