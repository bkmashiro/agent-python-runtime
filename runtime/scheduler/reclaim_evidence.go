package scheduler

import (
	"context"
	"fmt"
	"sync"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type ReclaimEvidenceBridgeConfig struct {
	MaxTracked uint32
	Profiles   *ProfileStore
}

type reclaimEvidenceRecord struct {
	ready     chan struct{}
	closed    bool
	footprint enginecontract.FootprintObservation
	reclaim   enginecontract.MemoryReclaimObservation
}

type ReclaimEvidenceBridge struct {
	mu         sync.Mutex
	maxTracked uint32
	profiles   *ProfileStore
	tracked    map[string]*reclaimEvidenceRecord
}

type reclaimFootprintSink struct{ bridge *ReclaimEvidenceBridge }
type reclaimEventSink struct{ bridge *ReclaimEvidenceBridge }

func NewReclaimEvidenceBridge(config ReclaimEvidenceBridgeConfig) (*ReclaimEvidenceBridge, error) {
	if config.MaxTracked == 0 || config.MaxTracked > 1<<20 {
		return nil, ErrInvalidConfig
	}
	return &ReclaimEvidenceBridge{maxTracked: config.MaxTracked, profiles: config.Profiles, tracked: make(map[string]*reclaimEvidenceRecord)}, nil
}

func (bridge *ReclaimEvidenceBridge) Track(attemptID string) error {
	if bridge == nil || !boundedIdentifier(attemptID) {
		return ErrInvalidTask
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if _, exists := bridge.tracked[attemptID]; exists {
		return nil
	}
	if uint32(len(bridge.tracked)) >= bridge.maxTracked {
		return ErrCapacity
	}
	bridge.tracked[attemptID] = &reclaimEvidenceRecord{ready: make(chan struct{})}
	return nil
}

func (bridge *ReclaimEvidenceBridge) CaptureFootprint(observation enginecontract.FootprintObservation) error {
	if bridge == nil || observation.Status != enginecontract.FootprintObserved || observation.Validate() != nil {
		return ErrConflict
	}
	bridge.mu.Lock()
	record := bridge.tracked[observation.AttemptID]
	if record == nil {
		bridge.mu.Unlock()
		return ErrNotFound
	}
	record.footprint = observation
	bridge.mu.Unlock()
	if bridge.profiles != nil && bridge.profiles.ShouldSample(observation.AttemptID) {
		bridge.profiles.Observe(observation)
	}
	return nil
}

func (bridge *ReclaimEvidenceBridge) Forget(attemptID string) {
	if bridge == nil || !boundedIdentifier(attemptID) {
		return
	}
	bridge.mu.Lock()
	delete(bridge.tracked, attemptID)
	bridge.mu.Unlock()
}

func (bridge *ReclaimEvidenceBridge) ShouldSample(attemptID string) bool {
	return bridge.isTracked(attemptID)
}

func (bridge *ReclaimEvidenceBridge) ShouldObserve(attemptID string) bool {
	return bridge.isTracked(attemptID)
}

func (bridge *ReclaimEvidenceBridge) isTracked(attemptID string) bool {
	if bridge == nil || !boundedIdentifier(attemptID) {
		return false
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	_, ok := bridge.tracked[attemptID]
	return ok
}

func (bridge *ReclaimEvidenceBridge) FootprintSink() enginecontract.FootprintSink {
	return reclaimFootprintSink{bridge: bridge}
}

func (bridge *ReclaimEvidenceBridge) ReclaimSink() enginecontract.ReclaimSink {
	return reclaimEventSink{bridge: bridge}
}

func (sink reclaimFootprintSink) ShouldSample(attemptID string) bool {
	if sink.bridge == nil {
		return false
	}
	return sink.bridge.ShouldSample(attemptID) || sink.bridge.profiles != nil && sink.bridge.profiles.ShouldSample(attemptID)
}

func (sink reclaimFootprintSink) Observe(observation enginecontract.FootprintObservation) {
	if sink.bridge == nil {
		return
	}
	sink.bridge.mu.Lock()
	if record := sink.bridge.tracked[observation.AttemptID]; record != nil {
		if record.footprint.Status != enginecontract.FootprintObserved || observation.Status == enginecontract.FootprintObserved {
			record.footprint = observation
		}
	}
	sink.bridge.mu.Unlock()
	if sink.bridge.profiles != nil && sink.bridge.profiles.ShouldSample(observation.AttemptID) {
		sink.bridge.profiles.Observe(observation)
	}
}

func (sink reclaimEventSink) ShouldObserve(attemptID string) bool {
	return sink.bridge.ShouldObserve(attemptID)
}

func (sink reclaimEventSink) ObserveReclaim(observation enginecontract.MemoryReclaimObservation) {
	if sink.bridge == nil {
		return
	}
	sink.bridge.mu.Lock()
	defer sink.bridge.mu.Unlock()
	record := sink.bridge.tracked[observation.AttemptID]
	if record == nil || record.closed {
		return
	}
	record.reclaim = observation
	record.closed = true
	close(record.ready)
}

func (bridge *ReclaimEvidenceBridge) Observe(ctx context.Context, termination Termination) (ReclaimReport, error) {
	if bridge == nil || ctx == nil || !termination.ExecutorTerminated || !boundedIdentifier(termination.AttemptID) {
		return ReclaimReport{}, ErrInvalidTask
	}
	bridge.mu.Lock()
	record := bridge.tracked[termination.AttemptID]
	bridge.mu.Unlock()
	if record == nil {
		return ReclaimReport{}, ErrNotFound
	}
	select {
	case <-record.ready:
	case <-ctx.Done():
		return ReclaimReport{}, ctx.Err()
	}
	bridge.mu.Lock()
	current := bridge.tracked[termination.AttemptID]
	if current != record {
		bridge.mu.Unlock()
		return ReclaimReport{}, ErrConflict
	}
	footprint := record.footprint
	reclaim := record.reclaim
	delete(bridge.tracked, termination.AttemptID)
	bridge.mu.Unlock()
	if footprint.AttemptID != termination.AttemptID || reclaim.AttemptID != termination.AttemptID {
		return ReclaimReport{}, fmt.Errorf("%w: reclaim evidence attempt identity mismatch", ErrConflict)
	}
	if validationErr := footprint.Validate(); validationErr != nil {
		return ReclaimReport{}, fmt.Errorf("%w: footprint status=%s error_code=%s validation=%v", ErrConflict, footprint.Status, footprint.ErrorCode, validationErr)
	}
	if validationErr := reclaim.Validate(); validationErr != nil {
		return ReclaimReport{}, fmt.Errorf("%w: reclaim status=%s error_code=%s validation=%v", ErrConflict, reclaim.Status, reclaim.ErrorCode, validationErr)
	}
	if footprint.Status != enginecontract.FootprintObserved {
		return ReclaimReport{}, fmt.Errorf("%w: footprint status=%s error_code=%s", ErrConflict, footprint.Status, footprint.ErrorCode)
	}
	if reclaim.Status != enginecontract.ReclaimReleased {
		return ReclaimReport{}, fmt.Errorf("%w: reclaim status=%s error_code=%s", ErrConflict, reclaim.Status, reclaim.ErrorCode)
	}
	return ReclaimReport{
		ExecutorTerminated:     true,
		ObservedFootprintBytes: footprint.Memory.PrivateDirtyBytes,
		ReclaimedBytes:         footprint.Memory.PrivateDirtyBytes,
	}, nil
}

var _ ReclaimObserver = (*ReclaimEvidenceBridge)(nil)
var _ enginecontract.FootprintSink = reclaimFootprintSink{}
var _ enginecontract.ReclaimSink = reclaimEventSink{}
