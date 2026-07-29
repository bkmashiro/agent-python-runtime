package wazero

import (
	"context"
	"errors"
	"sync"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	errActiveFootprintNotFound = errors.New("active footprint attempt not found")
	errActiveFootprintConflict = errors.New("active footprint attempt already registered")
)

type activeFootprintRegistry struct {
	mu      sync.Mutex
	sources map[string]preparedFootprintSource
}

func newActiveFootprintRegistry() *activeFootprintRegistry {
	return &activeFootprintRegistry{sources: make(map[string]preparedFootprintSource)}
}

func (engine *Engine) registerActiveFootprint(attemptID string, source preparedFootprintSource) error {
	if engine == nil || engine.activeFootprints == nil || source == nil {
		return errFootprintMappingUnavailable
	}
	if _, err := enginecontract.WithAttemptIdentity(context.Background(), attemptID); err != nil {
		return err
	}
	engine.activeFootprints.mu.Lock()
	defer engine.activeFootprints.mu.Unlock()
	if _, exists := engine.activeFootprints.sources[attemptID]; exists {
		return errActiveFootprintConflict
	}
	engine.activeFootprints.sources[attemptID] = source
	return nil
}

func (engine *Engine) unregisterActiveFootprint(attemptID string) {
	if engine == nil || engine.activeFootprints == nil {
		return
	}
	engine.activeFootprints.mu.Lock()
	delete(engine.activeFootprints.sources, attemptID)
	engine.activeFootprints.mu.Unlock()
}

// SampleActiveFootprint samples one Host-identified currently served mapping.
// It never falls back to whole-process accounting.
func (engine *Engine) SampleActiveFootprint(attemptID string) (enginecontract.FootprintObservation, error) {
	if engine == nil || engine.activeFootprints == nil {
		return enginecontract.FootprintObservation{}, errActiveFootprintNotFound
	}
	engine.activeFootprints.mu.Lock()
	source := engine.activeFootprints.sources[attemptID]
	engine.activeFootprints.mu.Unlock()
	if source == nil {
		return enginecontract.FootprintObservation{}, errActiveFootprintNotFound
	}
	observation := enginecontract.FootprintObservation{
		AttemptID: attemptID, Backend: "wazero", Strategy: engine.strategy, SampledAt: time.Now().UTC(),
	}
	footprint, err := source.sampleFootprint()
	if err != nil {
		observation.Status = enginecontract.FootprintFailed
		observation.ErrorCode = footprintErrorCode(err)
		return observation, err
	}
	observation.Status = enginecontract.FootprintObserved
	observation.Memory = footprint
	if err := observation.Validate(); err != nil {
		observation.Status = enginecontract.FootprintFailed
		observation.Memory = enginecontract.MemoryFootprint{}
		observation.ErrorCode = "invalid_sample"
		return observation, err
	}
	return observation, nil
}
