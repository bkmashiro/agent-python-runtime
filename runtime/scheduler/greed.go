package scheduler

import (
	"errors"
	"math/bits"
	"sync"
)

var ErrInvalidControlWindow = errors.New("invalid greed control window")

type GreedConfig struct {
	InitialQuantileBPS   uint32
	MinimumQuantileBPS   uint32
	MaximumQuantileBPS   uint32
	QuantileStepBPS      uint32
	TargetEvictionPPM    uint32
	TargetUtilizationBPS uint32
	MinimumAttempts      uint64
}

type ControlWindow struct {
	AttemptsStarted      uint64
	AttemptsEvicted      uint64
	MemoryUtilizationBPS uint32
	Pressure             PressureLevel
	OOMEvents            uint64
}

type GreedDirection string

const (
	GreedMoreAggressive   GreedDirection = "more_aggressive"
	GreedHold             GreedDirection = "hold"
	GreedMoreConservative GreedDirection = "more_conservative"
)

type GreedDecision struct {
	PreviousQuantileBPS uint32
	NextQuantileBPS     uint32
	ObservedEvictionPPM uint32
	Direction           GreedDirection
	Reason              string
}

type GreedController struct {
	mu sync.Mutex

	config  GreedConfig
	current uint32
}

func NewGreedController(config GreedConfig) (*GreedController, error) {
	if config.MinimumQuantileBPS == 0 || config.MinimumQuantileBPS > config.InitialQuantileBPS ||
		config.InitialQuantileBPS > config.MaximumQuantileBPS || config.MaximumQuantileBPS > 10000 ||
		config.QuantileStepBPS == 0 || config.TargetEvictionPPM > 1_000_000 ||
		config.TargetUtilizationBPS == 0 || config.TargetUtilizationBPS > 10000 || config.MinimumAttempts == 0 {
		return nil, ErrInvalidConfig
	}
	return &GreedController{config: config, current: config.InitialQuantileBPS}, nil
}

func (controller *GreedController) CurrentQuantileBPS() uint32 {
	if controller == nil {
		return 0
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.current
}

func (controller *GreedController) Apply(store *ProfileStore, window ControlWindow) (GreedDecision, error) {
	if controller == nil || store == nil {
		return GreedDecision{}, ErrInvalidConfig
	}
	if err := validateControlWindow(window); err != nil {
		return GreedDecision{}, err
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	decision := controller.decideLocked(window)
	if err := store.compareAndSwapReservationQuantile(decision.PreviousQuantileBPS, decision.NextQuantileBPS); err != nil {
		return GreedDecision{}, err
	}
	controller.current = decision.NextQuantileBPS
	return decision, nil
}

func validateControlWindow(window ControlWindow) error {
	if window.AttemptsEvicted > window.AttemptsStarted || window.MemoryUtilizationBPS > 10000 {
		return ErrInvalidControlWindow
	}
	switch window.Pressure {
	case PressureNormal, PressureHigh, PressureCritical, PressureHard:
		return nil
	default:
		return ErrInvalidControlWindow
	}
}

func (controller *GreedController) decideLocked(window ControlWindow) GreedDecision {
	observedPPM := evictionPPM(window.AttemptsEvicted, window.AttemptsStarted)
	previous := controller.current
	next := previous
	reason := "within_budget"
	switch {
	case window.OOMEvents > 0:
		next = controller.moreConservative(previous)
		reason = "oom_event"
	case window.Pressure == PressureHard:
		next = controller.moreConservative(previous)
		reason = "hard_pressure"
	case window.Pressure == PressureCritical:
		next = controller.moreConservative(previous)
		reason = "critical_pressure"
	case window.Pressure == PressureHigh:
		next = controller.moreConservative(previous)
		reason = "high_pressure"
	case window.AttemptsStarted < controller.config.MinimumAttempts:
		reason = "insufficient_attempts"
	case observedPPM > controller.config.TargetEvictionPPM:
		next = controller.moreConservative(previous)
		reason = "eviction_budget_exceeded"
	case observedPPM < controller.config.TargetEvictionPPM && window.MemoryUtilizationBPS < controller.config.TargetUtilizationBPS:
		next = controller.moreAggressive(previous)
		reason = "under_budget_low_utilization"
	}
	direction := GreedHold
	if next < previous {
		direction = GreedMoreAggressive
	} else if next > previous {
		direction = GreedMoreConservative
	}
	return GreedDecision{
		PreviousQuantileBPS: previous, NextQuantileBPS: next, ObservedEvictionPPM: observedPPM,
		Direction: direction, Reason: reason,
	}
}

func (controller *GreedController) moreConservative(value uint32) uint32 {
	if value >= controller.config.MaximumQuantileBPS || controller.config.QuantileStepBPS > controller.config.MaximumQuantileBPS-value {
		return controller.config.MaximumQuantileBPS
	}
	return value + controller.config.QuantileStepBPS
}

func (controller *GreedController) moreAggressive(value uint32) uint32 {
	if value <= controller.config.MinimumQuantileBPS || controller.config.QuantileStepBPS > value-controller.config.MinimumQuantileBPS {
		return controller.config.MinimumQuantileBPS
	}
	return value - controller.config.QuantileStepBPS
}

func evictionPPM(evicted, started uint64) uint32 {
	if started == 0 || evicted == 0 {
		return 0
	}
	high, low := bits.Mul64(evicted, 1_000_000)
	quotient, _ := bits.Div64(high, low, started)
	if quotient > 1_000_000 {
		return 1_000_000
	}
	return uint32(quotient)
}
