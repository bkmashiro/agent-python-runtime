package semanticspeculation

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordingTreatment struct {
	inputs json.RawMessage
	chunks []string
}

func (t *recordingTreatment) Begin(_ context.Context, inputs json.RawMessage) error {
	t.inputs = append(json.RawMessage(nil), inputs...)
	return nil
}

func (t *recordingTreatment) ObserveChunk(_ context.Context, chunk string) error {
	t.chunks = append(t.chunks, chunk)
	return nil
}

func (t *recordingTreatment) Finalize(context.Context) (TreatmentOutcome, error) {
	return TreatmentOutcome{FinalProgramOutcome: "success", ResultSHA256: syntheticDigest([]byte(`1`))}, nil
}

func (t *recordingTreatment) Cancel(context.Context) error { return nil }

func TestRunScheduledTreatmentReleasesFrozenSourceWithoutCaseHints(t *testing.T) {
	fixture := SyntheticCase{
		ID: "scheduler_fixture", Class: "control", Inputs: json.RawMessage(`{"value":1}`),
		Chunks:          []SyntheticChunk{{Source: "value = 1\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value\n", ReleaseAfterMilliseconds: 2}},
		ExpectedOutcome: "success",
	}
	adapter := &recordingTreatment{}
	started := time.Now()
	result, err := RunScheduledTreatment(context.Background(), fixture, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 2*time.Millisecond || string(adapter.inputs) != string(fixture.Inputs) || len(adapter.chunks) != 2 ||
		adapter.chunks[0] != fixture.Chunks[0].Source || adapter.chunks[1] != fixture.Chunks[1].Source {
		t.Fatalf("adapter=%+v result=%+v", adapter, result)
	}
	if result.FinalizeNanos == 0 || result.EndedNanos < result.FinalizeNanos || result.Outcome.FinalProgramOutcome != "success" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunScheduledTreatmentCancelsAdapterOnChunkFailure(t *testing.T) {
	fixture := Phase3SyntheticCases()[5]
	adapter := &failingTreatment{}
	if _, err := RunScheduledTreatment(context.Background(), fixture, adapter); err == nil || !adapter.cancelled {
		t.Fatalf("err=%v cancelled=%v", err, adapter.cancelled)
	}
}

type failingTreatment struct{ cancelled bool }

func (*failingTreatment) Begin(context.Context, json.RawMessage) error { return nil }
func (*failingTreatment) ObserveChunk(context.Context, string) error   { return context.Canceled }
func (*failingTreatment) Finalize(context.Context) (TreatmentOutcome, error) {
	return TreatmentOutcome{}, nil
}
func (t *failingTreatment) Cancel(context.Context) error { t.cancelled = true; return nil }
