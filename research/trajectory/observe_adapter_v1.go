package trajectory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

type ObservationRecorderConfig struct {
	ActorID         string
	RunID           string
	AttemptID       string
	PolicySHA256    string
	FreshnessSHA256 string
	GrantsSHA256    string
}

type ObservationRecorder struct {
	mu             sync.Mutex
	log            *EvidenceLog
	config         ObservationRecorderConfig
	observationIDs map[uint32]string
	lastProduction string
	started        bool
	terminal       bool
}

func NewObservationRecorder(log *EvidenceLog, config ObservationRecorderConfig) (*ObservationRecorder, error) {
	if log == nil || log.builder.store == nil || !evidenceIdentifier.MatchString(config.ActorID) ||
		!evidenceIdentifier.MatchString(config.RunID) || !evidenceIdentifier.MatchString(config.AttemptID) ||
		!digestPattern.MatchString(config.PolicySHA256) || !digestPattern.MatchString(config.FreshnessSHA256) || !digestPattern.MatchString(config.GrantsSHA256) {
		return nil, errors.New("invalid observation recorder configuration")
	}
	return &ObservationRecorder{log: log, config: config, observationIDs: make(map[uint32]string)}, nil
}

func (recorder *ObservationRecorder) Append(ctx context.Context, observed observe.Event) error {
	if recorder == nil || ctx == nil {
		return errors.New("invalid runtime observation")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.terminal {
		return errors.New("runtime observation after terminal event")
	}
	encoded, err := observe.Encode(observed)
	if err != nil {
		return err
	}
	body, _, err := recorder.log.builder.store.PutJSON(labstore.KindMetadataEvent, encoded, labstore.PutOptions{
		Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent,
	})
	if err != nil {
		return err
	}
	var rawParents []string
	if observed.ParentSequence != nil {
		parent, ok := recorder.observationIDs[*observed.ParentSequence]
		if !ok {
			return errors.New("runtime observation parent is unavailable")
		}
		rawParents = []string{parent}
	}
	rawEvent, err := recorder.log.Append(EvidenceInput{
		Type: EventRuntimeObservation, ActorID: recorder.config.ActorID, ParentEventIDs: rawParents, Body: &body,
		Payload: RuntimeObservationPayload{
			ObservationType: observed.Type, Sequence: observed.Sequence, ParentSequence: cloneObservationSequence(observed.ParentSequence),
			ObservationSHA256: observationDigest(encoded),
		},
	})
	if err != nil {
		return err
	}
	recorder.observationIDs[observed.Sequence] = rawEvent.EventID
	return recorder.appendProjection(observed)
}

func (recorder *ObservationRecorder) appendProjection(observed observe.Event) error {
	switch observed.Type {
	case observe.EventExecutionStarted:
		if recorder.started {
			return errors.New("duplicate runtime execution start")
		}
		var payload observe.ExecutionStartedPayload
		if json.Unmarshal(observed.Payload, &payload) != nil {
			return errors.New("decode runtime execution start")
		}
		started, err := recorder.log.Append(EvidenceInput{Type: EventTraceStarted, ActorID: recorder.config.ActorID, Payload: TraceStartedPayload{Status: "running"}})
		if err != nil {
			return err
		}
		attempt, err := recorder.log.Append(EvidenceInput{
			Type: EventExecutionAttempt, ActorID: recorder.config.ActorID, ParentEventIDs: []string{started.EventID},
			Payload: ExecutionAttemptPayload{RunID: recorder.config.RunID, AttemptID: recorder.config.AttemptID, PreparedImageSHA256: payload.ArtifactSHA256, Status: "started"},
		})
		if err != nil {
			return err
		}
		recorder.lastProduction = attempt.EventID
		if payload.CapabilityPlanSHA256 != "" {
			authority, err := recorder.log.Append(EvidenceInput{
				Type: EventAuthoritySnapshot, ActorID: recorder.config.ActorID, ParentEventIDs: []string{attempt.EventID},
				Payload: AuthoritySnapshotPayload{
					RunID: recorder.config.RunID, CapabilityPlanSHA256: payload.CapabilityPlanSHA256,
					PolicySHA256: recorder.config.PolicySHA256, FreshnessSHA256: recorder.config.FreshnessSHA256, GrantsSHA256: recorder.config.GrantsSHA256,
				},
			})
			if err != nil {
				return err
			}
			recorder.lastProduction = authority.EventID
		}
		recorder.started = true
		return nil
	case observe.EventCapabilityPlan:
		return nil
	case observe.EventCapabilityCall:
		if !recorder.started {
			return errors.New("capability observation before execution start")
		}
		var payload observe.CapabilityCallPayload
		if json.Unmarshal(observed.Payload, &payload) != nil || payload.ReceiptID == "" {
			return errors.New("capability observation has no Host receipt")
		}
		state := EffectState("")
		reason := ""
		switch payload.Outcome {
		case "ok":
			state = EffectCommitted
		case "denied":
			state = EffectDenied
		case "error":
			state = EffectFailed
		case "timeout":
			state = EffectTimedOut
		case "ambiguous":
			state = EffectReconciliationRequired
			reason = "provider_outcome_ambiguous"
		default:
			return errors.New("unknown capability outcome")
		}
		effect, err := recorder.log.Append(EvidenceInput{
			Type: EventEffectTransition, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(recorder.lastProduction),
			Payload: EffectTransitionPayload{CallID: capabilityCallIdentity(payload), ReceiptID: payload.ReceiptID, State: state, ReconciliationReason: reason},
		})
		if err != nil {
			return err
		}
		recorder.lastProduction = effect.EventID
		if payload.Source != nil {
			document, err := recorder.log.Append(EvidenceInput{
				Type: EventSourceDocument, ActorID: recorder.config.ActorID,
				Payload: SourceDocumentPayload{DocumentID: payload.Source.DocumentID, SourceSHA256: payload.Source.SourceSHA256, Availability: Available},
			})
			if err != nil {
				return err
			}
			occurrence, err := recorder.log.Append(EvidenceInput{
				Type: EventSourceOccurrence, ActorID: recorder.config.ActorID, ParentEventIDs: []string{document.EventID},
				Payload: SourceOccurrencePayload{
					DocumentID: payload.Source.DocumentID, SourceSHA256: payload.Source.SourceSHA256, OccurrenceID: payload.Source.OccurrenceID,
					StartLine: payload.Source.StartLine, StartColumn: payload.Source.StartColumn, EndLine: payload.Source.EndLine, EndColumn: payload.Source.EndColumn,
					Capability: payload.Source.Capability, DynamicOccurrence: payload.Source.DynamicOccurrence,
				},
			})
			if err != nil {
				return err
			}
			if _, err := recorder.log.Append(EvidenceInput{
				Type: EventSourceDecision, ActorID: recorder.config.ActorID, ParentEventIDs: []string{occurrence.EventID, effect.EventID},
				Payload: SourceDecisionPayload{
					DecisionID: sourceDecisionIdentity(payload), CapabilityPlanSHA256: payload.CapabilityPlanSHA256,
					OccurrenceID: payload.Source.OccurrenceID, DynamicOccurrence: payload.Source.DynamicOccurrence,
					ClaimLevel: ClaimSourceBound, Admitted: true, ReceiptID: payload.ReceiptID,
				},
			}); err != nil {
				return err
			}
		}
		return nil
	case observe.EventWorkspaceFinalized:
		if !recorder.started {
			return errors.New("workspace observation before execution start")
		}
		var payload observe.WorkspaceFinalizedPayload
		if json.Unmarshal(observed.Payload, &payload) != nil {
			return errors.New("decode workspace observation")
		}
		workspace, err := recorder.log.Append(EvidenceInput{
			Type: EventWorkspaceTerminal, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(recorder.lastProduction),
			Payload: WorkspaceTerminalPayload{BaseWorkspaceSHA256: payload.InitialWorkspaceSHA256, ResultWorkspaceSHA256: payload.FinalWorkspaceSHA256, Disposition: "finalized"},
		})
		if err != nil {
			return err
		}
		recorder.lastProduction = workspace.EventID
		return nil
	case observe.EventExecutionCompleted, observe.EventExecutionFailed:
		if !recorder.started {
			return errors.New("terminal observation before execution start")
		}
		status := "failed"
		evidenceComplete := false
		if observed.Type == observe.EventExecutionCompleted {
			var payload observe.ExecutionCompletedPayload
			if json.Unmarshal(observed.Payload, &payload) != nil {
				return errors.New("decode execution completion")
			}
			status = "completed"
			evidenceComplete = payload.EvidenceComplete
		} else {
			var payload observe.ExecutionFailedPayload
			if json.Unmarshal(observed.Payload, &payload) != nil {
				return errors.New("decode execution failure")
			}
			evidenceComplete = payload.EvidenceComplete
		}
		attempt, err := recorder.log.Append(EvidenceInput{
			Type: EventExecutionAttempt, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(recorder.lastProduction),
			Payload: ExecutionAttemptPayload{RunID: recorder.config.RunID, AttemptID: recorder.config.AttemptID, Status: status},
		})
		if err != nil {
			return err
		}
		if _, err := recorder.log.Append(EvidenceInput{
			Type: EventTraceEnded, ActorID: recorder.config.ActorID, ParentEventIDs: []string{attempt.EventID},
			Payload: TraceEndedPayload{Status: status, EvidenceComplete: evidenceComplete},
		}); err != nil {
			return err
		}
		recorder.terminal = true
		return nil
	default:
		return errors.New("unknown runtime observation type")
	}
}

func observationDigest(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func capabilityCallIdentity(payload observe.CapabilityCallPayload) string {
	encoded, _ := json.Marshal(struct {
		Capability     string `json:"capability"`
		OperationIndex uint32 `json:"operation_index"`
		ReceiptID      string `json:"receipt_id"`
	}{payload.Capability, payload.OperationIndex, payload.ReceiptID})
	digest := sha256.Sum256(encoded)
	return "call_" + hex.EncodeToString(digest[:])
}

func sourceDecisionIdentity(payload observe.CapabilityCallPayload) string {
	encoded, _ := json.Marshal(struct {
		ReceiptID    string `json:"receipt_id"`
		OccurrenceID string `json:"occurrence_id"`
	}{payload.ReceiptID, payload.Source.OccurrenceID})
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneObservationSequence(sequence *uint32) *uint32 {
	if sequence == nil {
		return nil
	}
	value := *sequence
	return &value
}

func productionParent(parent string) []string {
	if parent == "" {
		return nil
	}
	return []string{parent}
}
