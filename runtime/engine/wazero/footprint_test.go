package wazero

import (
	"context"
	"errors"
	"testing"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type recordingFootprintSink struct {
	sample       bool
	observations []enginecontract.FootprintObservation
}

func (sink *recordingFootprintSink) ShouldSample(string) bool { return sink.sample }
func (sink *recordingFootprintSink) Observe(observation enginecontract.FootprintObservation) {
	sink.observations = append(sink.observations, observation)
}

type fakePreparedFootprintSource struct {
	footprint enginecontract.MemoryFootprint
	err       error
	calls     int
}

func (source *fakePreparedFootprintSource) sampleFootprint() (enginecontract.MemoryFootprint, error) {
	source.calls++
	return source.footprint, source.err
}

func footprintTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, err := enginecontract.WithAttemptIdentity(context.Background(), "task:attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestObservePreparedFootprintRequiresHostIdentityAndSamplingPolicy(t *testing.T) {
	source := &fakePreparedFootprintSource{}
	sink := &recordingFootprintSink{sample: true}
	observePreparedFootprint(context.Background(), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source})
	if source.calls != 0 || len(sink.observations) != 0 {
		t.Fatalf("unidentified run sampled: calls=%d observations=%#v", source.calls, sink.observations)
	}
	sink.sample = false
	observePreparedFootprint(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source})
	if source.calls != 0 || len(sink.observations) != 0 {
		t.Fatalf("sampling policy ignored: calls=%d observations=%#v", source.calls, sink.observations)
	}
}

func TestObservePreparedFootprintReportsObservedAndFailedSamples(t *testing.T) {
	valid := enginecontract.MemoryFootprint{MappingCount: 1, VirtualBytes: 128 << 20, RSSBytes: 20 << 20, PSSBytes: 20 << 20, PrivateDirtyBytes: 19 << 20, AnonymousBytes: 19 << 20}
	source := &fakePreparedFootprintSource{footprint: valid}
	sink := &recordingFootprintSink{sample: true}
	observePreparedFootprint(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source})
	if len(sink.observations) != 1 || sink.observations[0].Status != enginecontract.FootprintObserved || sink.observations[0].Memory != valid {
		t.Fatalf("observations = %#v", sink.observations)
	}
	if err := sink.observations[0].Validate(); err != nil {
		t.Fatal(err)
	}

	failedSource := &fakePreparedFootprintSource{err: errSMAPSMappingNotFound}
	failedSink := &recordingFootprintSink{sample: true}
	observePreparedFootprint(footprintTestContext(t), failedSink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: failedSource})
	if len(failedSink.observations) != 1 || failedSink.observations[0].Status != enginecontract.FootprintFailed || failedSink.observations[0].ErrorCode != "mapping_not_found" {
		t.Fatalf("failed observations = %#v", failedSink.observations)
	}
	if err := failedSink.observations[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestObservePreparedFootprintReportsUnavailableSource(t *testing.T) {
	sink := &recordingFootprintSink{sample: true}
	observePreparedFootprint(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{})
	if len(sink.observations) != 1 || sink.observations[0].Status != enginecontract.FootprintUnavailable || sink.observations[0].ErrorCode != "mapping_unavailable" {
		t.Fatalf("observations = %#v", sink.observations)
	}
}

type recordingReclaimSink struct {
	observe      bool
	observations []enginecontract.MemoryReclaimObservation
}

func (sink *recordingReclaimSink) ShouldObserve(string) bool { return sink.observe }
func (sink *recordingReclaimSink) ObserveReclaim(observation enginecontract.MemoryReclaimObservation) {
	sink.observations = append(sink.observations, observation)
}

type fakePreparedReclaimSource struct {
	fakePreparedFootprintSource
	reclaimErr   error
	reclaimCalls int
}

func (source *fakePreparedReclaimSource) verifyReclaimed() error {
	source.reclaimCalls++
	return source.reclaimErr
}

func TestObservePreparedReclaimRequiresIdentityAndReportsReleased(t *testing.T) {
	source := &fakePreparedReclaimSource{}
	sink := &recordingReclaimSink{observe: true}
	observePreparedReclaim(context.Background(), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source}, time.Millisecond, nil)
	if source.reclaimCalls != 0 || len(sink.observations) != 0 {
		t.Fatalf("unidentified reclaim observed: calls=%d observations=%#v", source.reclaimCalls, sink.observations)
	}
	observePreparedReclaim(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source}, time.Millisecond, nil)
	if source.reclaimCalls != 1 || len(sink.observations) != 1 || sink.observations[0].Status != enginecontract.ReclaimReleased {
		t.Fatalf("released observations = %#v calls=%d", sink.observations, source.reclaimCalls)
	}
	if err := sink.observations[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestObservePreparedReclaimReportsCloseAndMappingFailures(t *testing.T) {
	source := &fakePreparedReclaimSource{}
	sink := &recordingReclaimSink{observe: true}
	observePreparedReclaim(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source}, time.Millisecond, errors.New("close"))
	if source.reclaimCalls != 0 || len(sink.observations) != 1 || sink.observations[0].Status != enginecontract.ReclaimFailed || sink.observations[0].ErrorCode != "close_failed" {
		t.Fatalf("close failure = %#v calls=%d", sink.observations, source.reclaimCalls)
	}

	source.reclaimErr = errFootprintMappingStillPresent
	sink.observations = nil
	observePreparedReclaim(footprintTestContext(t), sink, enginecontract.StrategyCOWReadySingleUse, &preparedInstance{footprintSource: source}, time.Millisecond, nil)
	if len(sink.observations) != 1 || sink.observations[0].Status != enginecontract.ReclaimStillMapped || sink.observations[0].ErrorCode != "mapping_present" {
		t.Fatalf("mapping failure = %#v", sink.observations)
	}
}

func TestEngineSamplesRegisteredActiveFootprint(t *testing.T) {
	valid := enginecontract.MemoryFootprint{MappingCount: 1, VirtualBytes: 128 << 20, RSSBytes: 20 << 20, PSSBytes: 20 << 20, PrivateDirtyBytes: 19 << 20, AnonymousBytes: 19 << 20}
	source := &fakePreparedFootprintSource{footprint: valid}
	engine := &Engine{strategy: enginecontract.StrategyCOWReadySingleUse, activeFootprints: newActiveFootprintRegistry()}
	if err := engine.registerActiveFootprint("task:attempt:1", source); err != nil {
		t.Fatal(err)
	}
	observation, err := engine.SampleActiveFootprint("task:attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != enginecontract.FootprintObserved || observation.Memory != valid || source.calls != 1 {
		t.Fatalf("active observation = %#v calls=%d", observation, source.calls)
	}
	if err := engine.registerActiveFootprint("task:attempt:1", source); !errors.Is(err, errActiveFootprintConflict) {
		t.Fatalf("duplicate active identity error = %v", err)
	}
	engine.unregisterActiveFootprint("task:attempt:1")
	if _, err := engine.SampleActiveFootprint("task:attempt:1"); !errors.Is(err, errActiveFootprintNotFound) {
		t.Fatalf("sampled unregistered attempt: %v", err)
	}
}

var _ enginecontract.FootprintSink = (*recordingFootprintSink)(nil)
var _ enginecontract.ReclaimSink = (*recordingReclaimSink)(nil)
var _ preparedFootprintSource = (*fakePreparedFootprintSource)(nil)
var _ preparedFootprintSource = (*fakePreparedReclaimSource)(nil)
var _ preparedReclaimSource = (*fakePreparedReclaimSource)(nil)
