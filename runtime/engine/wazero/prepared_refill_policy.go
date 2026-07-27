package wazero

import (
	"sync/atomic"
	"time"
)

const (
	preparedRefillFailureLimit = uint32(3)
	preparedRefillBaseBackoff  = 10 * time.Millisecond
	preparedRefillMaxBackoff   = 100 * time.Millisecond
	preparedRefillCooldown     = time.Second
)

type preparedRefillPolicy struct {
	consecutiveFailures atomic.Uint32
	totalFailures       atomic.Uint64
	breakerUntilNS      atomic.Int64
	halfOpen            atomic.Bool
}

func (policy *preparedRefillPolicy) noteFailure(now time.Time) time.Duration {
	failure := policy.consecutiveFailures.Add(1)
	policy.totalFailures.Add(1)
	policy.halfOpen.Store(false)
	if failure >= preparedRefillFailureLimit {
		policy.breakerUntilNS.Store(now.Add(preparedRefillCooldown).UnixNano())
		return preparedRefillCooldown
	}
	delay := preparedRefillBaseBackoff << (failure - 1)
	if delay > preparedRefillMaxBackoff {
		delay = preparedRefillMaxBackoff
	}
	return delay
}

func (policy *preparedRefillPolicy) noteSuccess() {
	policy.consecutiveFailures.Store(0)
	policy.breakerUntilNS.Store(0)
	policy.halfOpen.Store(false)
}

// schedulingLimit returns zero while the breaker is open, one for a single
// half-open probe, and deficit during normal operation.
func (policy *preparedRefillPolicy) schedulingLimit(now time.Time, deficit uint32) uint32 {
	if deficit == 0 {
		return 0
	}
	until := policy.breakerUntilNS.Load()
	if until == 0 {
		return deficit
	}
	if now.UnixNano() < until {
		return 0
	}
	if !policy.halfOpen.CompareAndSwap(false, true) {
		return 0
	}
	return 1
}

func (policy *preparedRefillPolicy) breakerOpen(now time.Time) bool {
	until := policy.breakerUntilNS.Load()
	return until != 0 && (now.UnixNano() < until || policy.halfOpen.Load())
}

func preparedWatermarks(target uint32) (floor, critical, low, high uint32) {
	if target == 0 {
		return 0, 0, 0, 0
	}
	floor = 1
	critical = target / 4
	if critical < floor {
		critical = floor
	}
	low = (target + 1) / 2
	if low < critical {
		low = critical
	}
	return floor, critical, low, target
}
