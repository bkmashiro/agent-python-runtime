package semanticspeculation

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestSemanticProviderOutcomeIncludesLiveFallbackPhysicalCall(t *testing.T) {
	treatment := &SemanticPreDispatchTreatment{config: SemanticPreDispatchTreatmentConfig{
		ProviderObservation: func() ProviderObservation {
			return ProviderObservation{Attempts: 2, ResultBytes: 14, CostUnits: 2}
		},
	}}
	attempts, resultBytes, cost, dispositions, liveCalls, err := treatment.providerOutcome(semantic.StreamingPreDispatchSnapshot{
		PhysicalIssues: 1, PhysicalResultBytes: 7, ProviderCostUnits: 1, Consumed: 1,
	})
	if err != nil || attempts != 2 || resultBytes != 14 || cost != 2 || dispositions.Consumed != 2 || liveCalls != 1 {
		t.Fatalf("attempts=%d bytes=%d cost=%d dispositions=%+v live=%d err=%v", attempts, resultBytes, cost, dispositions, liveCalls, err)
	}
}

func TestSemanticProviderOutcomeRejectsObservationBehindController(t *testing.T) {
	treatment := &SemanticPreDispatchTreatment{config: SemanticPreDispatchTreatmentConfig{
		ProviderObservation: func() ProviderObservation { return ProviderObservation{} },
	}}
	if _, _, _, _, _, err := treatment.providerOutcome(semantic.StreamingPreDispatchSnapshot{PhysicalIssues: 1}); err == nil {
		t.Fatal("provider observation behind controller was accepted")
	}
}
