package wazero

import (
	"context"
	"errors"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type preparedFootprintSource interface {
	sampleFootprint() (enginecontract.MemoryFootprint, error)
}

var (
	errFootprintMappingUnavailable = errors.New("footprint mapping unavailable")
	errFootprintReadFailed         = errors.New("read process footprint failed")
)

func observePreparedFootprint(ctx context.Context, sink enginecontract.FootprintSink, strategy enginecontract.ExecutionStrategy, instance *preparedInstance) {
	if sink == nil || instance == nil {
		return
	}
	attemptID, ok := enginecontract.AttemptIdentityFromContext(ctx)
	if !ok || !sink.ShouldSample(attemptID) {
		return
	}
	observation := enginecontract.FootprintObservation{
		AttemptID: attemptID,
		Backend:   "wazero",
		Strategy:  strategy,
		SampledAt: time.Now().UTC(),
	}
	if instance.footprintSource == nil {
		observation.Status = enginecontract.FootprintUnavailable
		observation.ErrorCode = "mapping_unavailable"
		sink.Observe(observation)
		return
	}
	footprint, err := instance.footprintSource.sampleFootprint()
	if err != nil {
		observation.Status = enginecontract.FootprintFailed
		observation.ErrorCode = footprintErrorCode(err)
		sink.Observe(observation)
		return
	}
	observation.Status = enginecontract.FootprintObserved
	observation.Memory = footprint
	if err := observation.Validate(); err != nil {
		observation.Status = enginecontract.FootprintFailed
		observation.Memory = enginecontract.MemoryFootprint{}
		observation.ErrorCode = "invalid_sample"
	}
	sink.Observe(observation)
}

func footprintErrorCode(err error) string {
	switch {
	case errors.Is(err, errSMAPSMappingNotFound):
		return "mapping_not_found"
	case errors.Is(err, errSMAPSIncompleteCoverage):
		return "incomplete_coverage"
	case errors.Is(err, errSMAPSPartialOverlap):
		return "partial_overlap"
	case errors.Is(err, errSMAPSMalformed):
		return "malformed_smaps"
	case errors.Is(err, errFootprintMappingUnavailable):
		return "mapping_unavailable"
	case errors.Is(err, errFootprintReadFailed):
		return "read_failed"
	default:
		return "sample_failed"
	}
}
