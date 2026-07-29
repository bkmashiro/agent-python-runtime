package scheduler

import (
	"errors"
	"testing"
)

func greedConfig() GreedConfig {
	return GreedConfig{
		InitialQuantileBPS:   9500,
		MinimumQuantileBPS:   9000,
		MaximumQuantileBPS:   10000,
		QuantileStepBPS:      250,
		TargetEvictionPPM:    10000,
		TargetUtilizationBPS: 9000,
		MinimumAttempts:      100,
	}
}

func TestGreedControllerTracksRetryBudgetAndPressure(t *testing.T) {
	profileConfig := profileConfig()
	profileConfig.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(profileConfig)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewGreedController(greedConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Apply(store, ControlWindow{AttemptsStarted: 1000, AttemptsEvicted: 0, MemoryUtilizationBPS: 8000, Pressure: PressureNormal})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedMoreAggressive || decision.PreviousQuantileBPS != 9500 || decision.NextQuantileBPS != 9250 || decision.ObservedEvictionPPM != 0 {
		t.Fatalf("aggressive decision = %#v", decision)
	}
	decision, err = controller.Apply(store, ControlWindow{AttemptsStarted: 1000, AttemptsEvicted: 20, MemoryUtilizationBPS: 9000, Pressure: PressureNormal})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedMoreConservative || decision.NextQuantileBPS != 9500 || decision.ObservedEvictionPPM != 20000 {
		t.Fatalf("retry-budget decision = %#v", decision)
	}
	decision, err = controller.Apply(store, ControlWindow{AttemptsStarted: 1, MemoryUtilizationBPS: 9000, Pressure: PressureCritical})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedMoreConservative || decision.NextQuantileBPS != 9750 || decision.Reason != "critical_pressure" {
		t.Fatalf("pressure decision = %#v", decision)
	}
	decision, err = controller.Apply(store, ControlWindow{AttemptsStarted: 1, MemoryUtilizationBPS: 9000, Pressure: PressureNormal, OOMEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if decision.NextQuantileBPS != 10000 || decision.Reason != "oom_event" || store.CurrentReservationQuantileBPS() != 10000 {
		t.Fatalf("OOM decision = %#v quantile=%d", decision, store.CurrentReservationQuantileBPS())
	}
}

func TestGreedControllerHoldsOnInsufficientEvidenceAndRejectsInvalidWindows(t *testing.T) {
	storeConfig := profileConfig()
	storeConfig.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewGreedController(greedConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Apply(store, ControlWindow{AttemptsStarted: 99, MemoryUtilizationBPS: 1000, Pressure: PressureNormal})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedHold || decision.NextQuantileBPS != 9500 || decision.Reason != "insufficient_attempts" {
		t.Fatalf("hold decision = %#v", decision)
	}
	if _, err := controller.Apply(store, ControlWindow{AttemptsStarted: 10, AttemptsEvicted: 11, MemoryUtilizationBPS: 9000, Pressure: PressureNormal}); !errors.Is(err, ErrInvalidControlWindow) {
		t.Fatalf("invalid window error = %v", err)
	}
}

func TestGreedControllerCASPreventsProfileStoreDrift(t *testing.T) {
	storeConfig := profileConfig()
	storeConfig.ReservationQuantileBPS = 9000
	store, err := NewProfileStore(storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewGreedController(greedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Apply(store, ControlWindow{AttemptsStarted: 1000, MemoryUtilizationBPS: 8000, Pressure: PressureNormal}); !errors.Is(err, ErrConflict) {
		t.Fatalf("quantile drift error = %v", err)
	}
	if controller.CurrentQuantileBPS() != 9500 || store.CurrentReservationQuantileBPS() != 9000 {
		t.Fatalf("drift mutated state: controller=%d store=%d", controller.CurrentQuantileBPS(), store.CurrentReservationQuantileBPS())
	}
}

func TestEvictionPPMHandlesUint64WindowsWithoutOverflow(t *testing.T) {
	maximum := ^uint64(0)
	if got := evictionPPM(maximum, maximum); got != 1_000_000 {
		t.Fatalf("full eviction PPM = %d", got)
	}
	if got := evictionPPM(maximum/2, maximum); got < 499_999 || got > 500_000 {
		t.Fatalf("half eviction PPM = %d", got)
	}
}

func TestGreedDecisionChangesSubsequentReservationQuantile(t *testing.T) {
	storeConfig := profileConfig()
	storeConfig.ColdRuns = 100
	storeConfig.MinimumSamples = 4
	storeConfig.ReservationQuantileBPS = 7500
	store, err := NewProfileStore(storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	addProfileSamples(t, store, profile, 10, 20, 30, 40)
	before, err := store.Estimate(profile)
	if err != nil {
		t.Fatal(err)
	}
	config := greedConfig()
	config.InitialQuantileBPS = 7500
	config.MinimumQuantileBPS = 5000
	config.QuantileStepBPS = 2500
	controller, err := NewGreedController(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Apply(store, ControlWindow{AttemptsStarted: 1, Pressure: PressureCritical, MemoryUtilizationBPS: 9500}); err != nil {
		t.Fatal(err)
	}
	after, err := store.Estimate(profile)
	if err != nil {
		t.Fatal(err)
	}
	if before.DirtyQuantileBytes != 30 || after.DirtyQuantileBytes != 40 || after.QuantileBPS != 10000 {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}
