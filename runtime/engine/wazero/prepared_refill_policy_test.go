package wazero

import (
	"testing"
	"time"
)

func TestPreparedRefillPolicyBackoffAndHalfOpenProbe(t *testing.T) {
	var policy preparedRefillPolicy
	now := time.Unix(100, 0)
	if got := policy.noteFailure(now); got != preparedRefillBaseBackoff {
		t.Fatalf("first backoff=%s", got)
	}
	if got := policy.noteFailure(now); got != 2*preparedRefillBaseBackoff {
		t.Fatalf("second backoff=%s", got)
	}
	if got := policy.noteFailure(now); got != preparedRefillCooldown {
		t.Fatalf("breaker backoff=%s", got)
	}
	if got := policy.schedulingLimit(now.Add(preparedRefillCooldown-time.Nanosecond), 8); got != 0 {
		t.Fatalf("open breaker scheduled %d", got)
	}
	if got := policy.schedulingLimit(now.Add(preparedRefillCooldown), 8); got != 1 {
		t.Fatalf("half-open probe limit=%d", got)
	}
	if got := policy.schedulingLimit(now.Add(preparedRefillCooldown), 8); got != 0 {
		t.Fatalf("duplicate half-open probe=%d", got)
	}
	policy.noteSuccess()
	if got := policy.schedulingLimit(now, 8); got != 8 {
		t.Fatalf("closed breaker limit=%d", got)
	}
}

func TestPreparedWatermarksAreOrderedAndBounded(t *testing.T) {
	for _, target := range []uint32{0, 1, 2, 4, 8, 64} {
		floor, critical, low, high := preparedWatermarks(target)
		if floor > critical || critical > low || low > high || high != target {
			t.Fatalf("target=%d watermarks=%d/%d/%d/%d", target, floor, critical, low, high)
		}
	}
}
