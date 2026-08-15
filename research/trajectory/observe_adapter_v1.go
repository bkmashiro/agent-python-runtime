package trajectory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime/metrics"
	"sort"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

type ObservationRecorderConfig struct {
	Profile         Profile
	ActorID         string
	RunID           string
	AttemptID       string
	PolicySHA256    string
	FreshnessSHA256 string
	GrantsSHA256    string
}

type ObservationRecorder struct {
	mu               sync.Mutex
	log              *EvidenceLog
	config           ObservationRecorderConfig
	observationIDs   map[uint32]string
	lastProduction   string
	authorityPlanSHA string
	authorityEventID string
	effectEvents     map[string]string
	effectStates     map[string]EffectState
	started          bool
	terminal         bool
	startedAt        time.Time
	startCPUNanos    uint64
}

func NewObservationRecorder(log *EvidenceLog, config ObservationRecorderConfig) (*ObservationRecorder, error) {
	if config.Profile == "" {
		config.Profile = ProfileProductionRollback
	}
	if log == nil || !config.Profile.valid() || (config.Profile == ProfileExperimentFull && log.builder.store == nil) || !evidenceIdentifier.MatchString(config.ActorID) ||
		!evidenceIdentifier.MatchString(config.RunID) || !evidenceIdentifier.MatchString(config.AttemptID) ||
		!digestPattern.MatchString(config.PolicySHA256) || !digestPattern.MatchString(config.FreshnessSHA256) || !digestPattern.MatchString(config.GrantsSHA256) {
		return nil, errors.New("invalid observation recorder configuration")
	}
	return &ObservationRecorder{
		log: log, config: config, observationIDs: make(map[uint32]string),
		effectEvents: make(map[string]string), effectStates: make(map[string]EffectState),
	}, nil
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
	if recorder.config.Profile == ProfileExperimentFull {
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
		storedObservation, err := recorder.log.builder.store.Get(body)
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
				ObservationSHA256: observationDigest(storedObservation.Body),
			},
		})
		if err != nil {
			return err
		}
		recorder.observationIDs[observed.Sequence] = rawEvent.EventID
	}
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
		recorder.startedAt = time.Now()
		recorder.startCPUNanos = processCPUNanos()
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
			recorder.authorityEventID = authority.EventID
			recorder.authorityPlanSHA = payload.CapabilityPlanSHA256
		}
		recorder.started = true
		return nil
	case observe.EventCapabilityPlan:
		if !recorder.started {
			return errors.New("capability Plan observation before execution start")
		}
		var payload observe.CapabilityPlanBoundPayload
		if json.Unmarshal(observed.Payload, &payload) != nil {
			return errors.New("decode capability Plan observation")
		}
		if recorder.authorityPlanSHA != "" {
			if recorder.authorityPlanSHA != payload.CapabilityPlanSHA256 {
				return errors.New("capability Plan observation changed authority")
			}
			return nil
		}
		authority, err := recorder.log.Append(EvidenceInput{
			Type: EventAuthoritySnapshot, ActorID: recorder.config.ActorID,
			ParentEventIDs: productionParent(recorder.lastProduction),
			Payload: AuthoritySnapshotPayload{
				RunID: recorder.config.RunID, CapabilityPlanSHA256: payload.CapabilityPlanSHA256,
				PolicySHA256: recorder.config.PolicySHA256, FreshnessSHA256: recorder.config.FreshnessSHA256,
				GrantsSHA256: recorder.config.GrantsSHA256,
			},
		})
		if err != nil {
			return err
		}
		recorder.authorityPlanSHA = payload.CapabilityPlanSHA256
		recorder.authorityEventID = authority.EventID
		recorder.lastProduction = authority.EventID
		return nil
	case observe.EventCapabilityIntent, observe.EventCapabilityStarted:
		if !recorder.started || recorder.authorityPlanSHA == "" {
			return errors.New("capability lifecycle observation before bound authority")
		}
		var payload observe.CapabilityCallLifecyclePayload
		if json.Unmarshal(observed.Payload, &payload) != nil || payload.CapabilityPlanSHA256 != recorder.authorityPlanSHA {
			return errors.New("capability lifecycle observation has no bound Host authority")
		}
		callID := capabilityRawCallIdentity(payload.CallID)
		state := EffectIntent
		parentID := recorder.authorityEventID
		if observed.Type == observe.EventCapabilityStarted {
			state = EffectStarted
			parentID = recorder.effectEvents[callID]
			if recorder.effectStates[callID] != EffectIntent || parentID == "" {
				return errors.New("capability start without matching intent")
			}
		} else if _, exists := recorder.effectEvents[callID]; exists {
			return errors.New("duplicate capability intent")
		}
		effect, err := recorder.log.Append(EvidenceInput{
			Type: EventEffectTransition, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(parentID),
			Payload: EffectTransitionPayload{CallID: callID, State: state},
		})
		if err != nil {
			return err
		}
		recorder.effectEvents[callID] = effect.EventID
		recorder.effectStates[callID] = state
		recorder.lastProduction = effect.EventID
		return nil
	case observe.EventCapabilityCall:
		if !recorder.started {
			return errors.New("capability observation before execution start")
		}
		var payload observe.CapabilityCallPayload
		if json.Unmarshal(observed.Payload, &payload) != nil || payload.ReceiptID == "" ||
			recorder.authorityPlanSHA == "" || payload.CapabilityPlanSHA256 != recorder.authorityPlanSHA {
			return errors.New("capability observation has no bound Host authority/receipt")
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
		callID := capabilityCallIdentity(payload)
		parentID := recorder.effectEvents[callID]
		if parentID == "" {
			if state != EffectDenied {
				return errors.New("capability terminal without matching intent/start")
			}
			parentID = recorder.authorityEventID
		}
		effect, err := recorder.log.Append(EvidenceInput{
			Type: EventEffectTransition, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(parentID),
			Payload: EffectTransitionPayload{CallID: callID, ReceiptID: payload.ReceiptID, State: state, ReconciliationReason: reason},
		})
		if err != nil {
			return err
		}
		recorder.effectEvents[callID] = effect.EventID
		recorder.effectStates[callID] = state
		recorder.lastProduction = effect.EventID
		if recorder.config.Profile == ProfileProductionRollback {
			return nil
		}
		approvalDisposition := "not_required"
		if payload.ApprovalRequestID != "" {
			approvalDisposition = "approved"
			if payload.Outcome == "denied" {
				approvalDisposition = "denied"
			}
		}
		mechanism := "direct"
		parentCallID := ""
		if payload.ParentCallID != "" {
			mechanism = "programmatic"
			parentCallID = capabilityRawCallIdentity(payload.ParentCallID)
		}
		tool, err := recorder.log.Append(EvidenceInput{
			Type: EventToolDecision, ActorID: recorder.config.ActorID, ParentEventIDs: []string{effect.EventID, recorder.authorityEventID},
			Payload: ToolDecisionPayload{
				ApprovalDisposition: approvalDisposition, ApprovalRequestID: payload.ApprovalRequestID,
				ArgumentsSHA256: payload.ArgumentsSHA256, BrokerOutcome: payload.Outcome,
				CallID: capabilityCallIdentity(payload), Capability: payload.Capability, CapabilityPlanSHA256: payload.CapabilityPlanSHA256,
				Mechanism: mechanism, OperationIndex: payload.OperationIndex, ParentCallID: parentCallID,
				ReceiptID: payload.ReceiptID, ResultSHA256: payload.ResultSHA256,
			},
		})
		if err != nil {
			return err
		}
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
				Type: EventSourceDecision, ActorID: recorder.config.ActorID, ParentEventIDs: []string{occurrence.EventID, effect.EventID, tool.EventID},
				Payload: SourceDecisionPayload{
					DecisionID: sourceDecisionIdentity(payload), CapabilityPlanSHA256: payload.CapabilityPlanSHA256,
					OccurrenceID: payload.Source.OccurrenceID, DynamicOccurrence: payload.Source.DynamicOccurrence,
					ClaimLevel: ClaimSourceBound, Admitted: payload.Outcome != "denied", ReceiptID: payload.ReceiptID,
				},
			}); err != nil {
				return err
			}
			if _, err := recorder.log.Append(EvidenceInput{
				Type: EventExecutedLine, ActorID: recorder.config.ActorID, ParentEventIDs: []string{occurrence.EventID},
				Payload: ExecutedLinePayload{SourceSHA256: payload.Source.SourceSHA256, Availability: NotRecorded},
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
		if status == "failed" {
			evidenceComplete = false
			if err := recorder.reconcileOutstandingEffects("execution_failed_before_terminal_receipt"); err != nil {
				return err
			}
		} else if recorder.hasOutstandingEffects() {
			return errors.New("completed execution has unterminated capability effects")
		}
		wallNanos := uint64(time.Since(recorder.startedAt).Nanoseconds())
		if wallNanos == 0 {
			wallNanos = 1
		}
		endCPU := processCPUNanos()
		cpuNanos := uint64(0)
		cpuAvailability := Unavailable
		if endCPU > recorder.startCPUNanos {
			cpuNanos = endCPU - recorder.startCPUNanos
			cpuAvailability = Available
		}
		if recorder.config.Profile == ProfileExperimentFull {
			if _, err := recorder.log.Append(EvidenceInput{
				Type: EventResourceSample, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(recorder.lastProduction),
				Payload: ResourceSamplePayload{
					Scope: "root_execution", WallNanos: wallNanos, ProcessCPUNanos: cpuNanos,
					ProcessCPUAvailability: cpuAvailability, PeakRSSAvailability: Unavailable,
				},
			}); err != nil {
				return err
			}
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

func (recorder *ObservationRecorder) hasOutstandingEffects() bool {
	for callID, state := range recorder.effectStates {
		if callID != "" && (state == EffectIntent || state == EffectStarted) {
			return true
		}
	}
	return false
}

func (recorder *ObservationRecorder) reconcileOutstandingEffects(reason string) error {
	callIDs := make([]string, 0, len(recorder.effectStates))
	for callID, state := range recorder.effectStates {
		if state == EffectIntent || state == EffectStarted {
			callIDs = append(callIDs, callID)
		}
	}
	sort.Strings(callIDs)
	for _, callID := range callIDs {
		parentID := recorder.effectEvents[callID]
		effect, err := recorder.log.Append(EvidenceInput{
			Type: EventEffectTransition, ActorID: recorder.config.ActorID, ParentEventIDs: productionParent(parentID),
			Payload: EffectTransitionPayload{CallID: callID, State: EffectReconciliationRequired, ReconciliationReason: reason},
		})
		if err != nil {
			return err
		}
		recorder.effectEvents[callID] = effect.EventID
		recorder.effectStates[callID] = EffectReconciliationRequired
		recorder.lastProduction = effect.EventID
	}
	return nil
}

func processCPUNanos() uint64 {
	samples := []metrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	seconds := samples[0].Value.Float64()
	if seconds <= 0 || seconds > float64(^uint64(0))/1e9 {
		return 0
	}
	return uint64(seconds * 1e9)
}

func observationDigest(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func capabilityCallIdentity(payload observe.CapabilityCallPayload) string {
	return capabilityRawCallIdentity(payload.CallID)
}

func capabilityRawCallIdentity(callID string) string {
	digest := sha256.Sum256([]byte("pysolate.causal-evidence.call-id.v1\x00" + callID))
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
