package scheduler

import (
	"context"
	"sync"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type ReclaimEvidenceBridgeConfig struct {
	MaxTracked uint32
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
	tracked    map[string]*reclaimEvidenceRecord
}

type reclaimFootprintSink struct{ bridge *ReclaimEvidenceBridge }
type reclaimEventSink struct{ bridge *ReclaimEvidenceBridge }

func NewReclaimEvidenceBridge(config ReclaimEvidenceBridgeConfig) (*ReclaimEvidenceBridge, error) {
	if config.MaxTracked == 0 || config.MaxTracked > 1<<20 {
		return nil, ErrInvalidConfig
	}
	return &ReclaimEvidenceBridge{maxTracked: config.MaxTracked, tracked: make(map[string]*reclaimEvidenceRecord)}, nil
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
	return sink.bridge.ShouldSample(attemptID)
}

func (sink reclaimFootprintSink) Observe(observation enginecontract.FootprintObservation) {
	if sink.bridge == nil {
		return
	}
	sink.bridge.mu.Lock()
	defer sink.bridge.mu.Unlock()
	if record := sink.bridge.tracked[observation.AttemptID]; record != nil {
		record.footprint = observation
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
	if footprint.AttemptID != termination.AttemptID || reclaim.AttemptID != termination.AttemptID ||
		footprint.Validate() != nil || reclaim.Validate() != nil ||
		footprint.Status != enginecontract.FootprintObserved || reclaim.Status != enginecontract.ReclaimReleased {
		return ReclaimReport{}, ErrConflict
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
