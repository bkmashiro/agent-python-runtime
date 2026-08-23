package semantic

import (
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/passpipeline"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

var ErrPassOutcomeInvalid = errors.New("invalid semantic pass outcome")

// RecordSemanticPreDispatchPassOutcomes projects terminal, body-free occurrence
// evidence into the common prefix-overlay outcome shell. It does not start,
// cancel, claim or otherwise mutate physical or logical work.
func RecordSemanticPreDispatchPassOutcomes(
	pipeline *passpipeline.Pipeline,
	registration PassRegistration,
	verified VerifiedAnalysis,
	controller *StreamingSemanticPreDispatch,
	finalSourceBytes uint64,
	reanalyses uint32,
) ([]passpipeline.OutcomeRecord, error) {
	analysis, err := verified.Analysis()
	if err != nil || pipeline == nil || controller == nil || finalSourceBytes == 0 ||
		registration.Name() != passregistration.SemanticPreDispatch ||
		registration.Version() != SemanticPreDispatchPassVersion || registration.Consumer() != PassConsumerOverlayOnly ||
		registration.AnalyzerSHA256() != analysis.AnalyzerSHA256 {
		return nil, ErrPassOutcomeInvalid
	}
	analysisSHA256, _, err := analysis.Identity()
	if err != nil {
		return nil, ErrPassOutcomeInvalid
	}
	controller.mu.Lock()
	if !controller.finalized || !controller.sourceSealed || controller.finalSourceSHA256 != analysis.SourceSHA256 ||
		controller.plan == nil || controller.plan.Identity() != analysis.CapabilityPlanSHA256 {
		controller.mu.Unlock()
		return nil, ErrPassOutcomeInvalid
	}
	entries := append([]*streamingPreDispatchEntry(nil), controller.entries...)
	controller.mu.Unlock()

	records := make([]passpipeline.OutcomeRecord, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.controller == nil || entry.call.SourceSHA256() != analysis.SourceSHA256 {
			return nil, ErrPassOutcomeInvalid
		}
		snapshot := entry.controller.Snapshot()
		outcome, reason, ok := semanticPreDispatchOutcome(snapshot.Disposition)
		if !ok {
			return nil, ErrPassOutcomeInvalid
		}
		input := passpipeline.RecordInput{
			PassName:             passregistration.SemanticPreDispatch,
			Outcome:              outcome,
			RejectionReason:      reason,
			OriginalSourceSHA256: analysis.SourceSHA256,
			OriginalASTSHA256:    analysis.ASTSHA256,
			Bindings: map[passregistration.Binding]string{
				passregistration.SourceSHA256:           analysis.SourceSHA256,
				passregistration.ASTSHA256:              analysis.ASTSHA256,
				passregistration.AnalysisSHA256:         analysisSHA256,
				passregistration.AnalyzerSHA256:         analysis.AnalyzerSHA256,
				passregistration.ExecutionProfileSHA256: analysis.ExecutionProfileSHA256,
				passregistration.ImportClosureSHA256:    analysis.ImportClosureSHA256,
				passregistration.CapabilityPlanSHA256:   analysis.CapabilityPlanSHA256,
				passregistration.PassConfigSHA256:       registration.ConfigSHA256(),
				passregistration.OccurrenceID:           entry.call.CallSiteID(),
			},
			Usage: passpipeline.Usage{
				OriginalSourceBytes: finalSourceBytes, DerivedSourceBytes: finalSourceBytes,
				Reanalyses: reanalyses,
			},
			LogicalEvents:        snapshot.LogicalClaims,
			PhysicalEvents:       snapshot.PhysicalIssues,
			WorkspaceDisposition: "not_owned",
		}
		record, recordErr := pipeline.RecordPrefixOverlay(input)
		if recordErr != nil {
			return nil, errors.Join(ErrPassOutcomeInvalid, recordErr)
		}
		records = append(records, record)
	}
	return records, nil
}

func semanticPreDispatchOutcome(disposition streaming.ObservationDisposition) (passpipeline.Outcome, passpipeline.RejectionReason, bool) {
	switch disposition {
	case streaming.ObservationConsumed:
		return passpipeline.OutcomeApplied, "", true
	case streaming.ObservationFailed, streaming.ObservationTimedOut, streaming.ObservationCancelled,
		streaming.ObservationLate, streaming.ObservationOrphaned, streaming.ObservationFallback:
		return passpipeline.OutcomeDiscarded, passpipeline.RejectionReason(disposition), true
	default:
		return "", "", false
	}
}
