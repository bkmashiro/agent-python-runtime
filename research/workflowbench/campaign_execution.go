package workflowbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const CampaignEvidenceSchemaVersion = "pysolate.transparent-campaign-evidence.v2"

var ErrInvalidCampaignEvidence = errors.New("invalid transparent campaign evidence")

func ValidateUniqueJSONKeys(raw []byte) error {
	return rejectDuplicateJSON(raw)
}

func DecodeCampaignManifest(raw []byte) (CampaignManifest, error) {
	var manifest CampaignManifest
	if rejectDuplicateJSON(raw) != nil {
		return manifest, ErrInvalidCampaignManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) != io.EOF || manifest.Validate() != nil {
		return CampaignManifest{}, ErrInvalidCampaignManifest
	}
	return manifest, nil
}

func DecodeCampaignEvidence(raw []byte, manifest CampaignManifest) (CampaignEvidence, error) {
	var evidence CampaignEvidence
	if rejectDuplicateJSON(raw) != nil {
		return evidence, ErrInvalidCampaignEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&evidence) != nil || decoder.Decode(&struct{}{}) != io.EOF || ValidateCampaignEvidence(manifest, evidence) != nil {
		return CampaignEvidence{}, ErrInvalidCampaignEvidence
	}
	return evidence, nil
}

type CampaignTreatment string

const (
	CampaignBaseline  CampaignTreatment = "baseline"
	CampaignQualified CampaignTreatment = "qualified"
)

type CampaignAdmission struct {
	Allowed     bool
	Reason      string
	Disposition string
}

type CampaignOutcome struct {
	Disposition         string
	Result              json.RawMessage
	PhysicalExecutionID string
	Sharing             string
	Err                 error
}

// CampaignRequest deliberately excludes program ID, family and Expected. Adapters can
// invoke typed mechanisms, but cannot dispatch on paper labels or validation oracles.
type CampaignRequest struct {
	Source                 string
	SourceSHA256           string
	Inputs                 json.RawMessage
	InputsSHA256           string
	PlanSHA256             string
	GrantSetSHA256         string
	PrivacyPartition       string
	WorkspaceFixtureSHA256 string
	Execution              CampaignExecutionContract
	DependencyResults      map[string]json.RawMessage
}

type CampaignAdapter interface {
	Admit(context.Context, CampaignRequest, CampaignTreatment) CampaignAdmission
	Execute(context.Context, CampaignRequest, CampaignTreatment, *CampaignRuntime) CampaignOutcome
}

type CampaignEvent struct {
	Sequence            uint64 `json:"sequence"`
	ProgramID           string `json:"program_id"`
	Type                string `json:"type"`
	AtNS                int64  `json:"at_ns"`
	Reason              string `json:"reason,omitempty"`
	PhysicalExecutionID string `json:"physical_execution_id,omitempty"`
}

type CampaignRow struct {
	ProgramID           string          `json:"program_id"`
	ReleaseNS           int64           `json:"release_ns"`
	AdmissionNS         int64           `json:"admission_ns"`
	EndNS               int64           `json:"end_ns"`
	AdmissionReason     string          `json:"admission_reason"`
	Disposition         string          `json:"disposition"`
	Sharing             string          `json:"sharing"`
	PhysicalExecutionID string          `json:"physical_execution_id,omitempty"`
	Result              json.RawMessage `json:"result,omitempty"`
	Error               string          `json:"error,omitempty"`
}

type CampaignEvidence struct {
	SchemaVersion       string            `json:"schema_version"`
	ManifestSHA256      string            `json:"manifest_sha256"`
	Treatment           CampaignTreatment `json:"treatment"`
	PhysicalSlots       uint32            `json:"physical_slots"`
	WallNS              int64             `json:"wall_ns"`
	ProcessCPUNS        uint64            `json:"process_cpu_ns,omitempty"`
	ProcessCPUAvailable bool              `json:"process_cpu_available"`
	PhysicalExecutions  uint32            `json:"physical_executions"`
	Rows                []CampaignRow     `json:"rows"`
	Events              []CampaignEvent   `json:"events"`
	SealSHA256          string            `json:"seal_sha256"`
}

type campaignRecorder struct {
	mu       sync.Mutex
	started  time.Time
	events   []CampaignEvent
	physical uint32
}

func (recorder *campaignRecorder) emit(programID, eventType, reason, physicalID string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, CampaignEvent{
		Sequence: uint64(len(recorder.events) + 1), ProgramID: programID, Type: eventType,
		AtNS: time.Since(recorder.started).Nanoseconds(), Reason: reason, PhysicalExecutionID: physicalID,
	})
	if eventType == "physical.started" {
		recorder.physical++
	}
}

type physicalGate struct {
	mu        sync.Mutex
	cond      *sync.Cond
	limit     uint32
	active    uint32
	next      uint64
	serving   uint64
	cancelled map[uint64]struct{}
}

func newPhysicalGate(limit uint32) *physicalGate {
	gate := &physicalGate{limit: limit, cancelled: make(map[uint64]struct{})}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (gate *physicalGate) acquire(ctx context.Context) (func(), error) {
	gate.mu.Lock()
	ticket := gate.next
	gate.next++
	for {
		if err := ctx.Err(); err != nil {
			gate.cancelled[ticket] = struct{}{}
			gate.advanceServing()
			gate.cond.Broadcast()
			gate.mu.Unlock()
			return nil, err
		}
		gate.advanceServing()
		if ticket == gate.serving && gate.active < gate.limit {
			break
		}
		gate.cond.Wait()
	}
	gate.serving++
	gate.active++
	gate.cond.Broadcast()
	gate.mu.Unlock()
	return func() {
		gate.mu.Lock()
		gate.active--
		gate.advanceServing()
		gate.cond.Broadcast()
		gate.mu.Unlock()
	}, nil
}

func (gate *physicalGate) advanceServing() {
	for {
		if _, cancelled := gate.cancelled[gate.serving]; !cancelled {
			return
		}
		delete(gate.cancelled, gate.serving)
		gate.serving++
	}
}

type CampaignRuntime struct {
	programID string
	recorder  *campaignRecorder
	gate      *physicalGate
}

func (runtime *CampaignRuntime) Event(eventType, reason, physicalID string) error {
	if runtime == nil || runtime.recorder == nil || eventType == "" {
		return ErrInvalidCampaignEvidence
	}
	runtime.recorder.emit(runtime.programID, eventType, reason, physicalID)
	return nil
}

func (runtime *CampaignRuntime) Physical(ctx context.Context, physicalID string, execute func(context.Context) ([]byte, error)) ([]byte, error) {
	if runtime == nil || runtime.recorder == nil || runtime.gate == nil || physicalID == "" || execute == nil {
		return nil, ErrInvalidCampaignEvidence
	}
	runtime.recorder.emit(runtime.programID, "physical.queued", "fifo", physicalID)
	release, err := runtime.gate.acquire(ctx)
	if err != nil {
		runtime.recorder.emit(runtime.programID, "physical.cancelled", err.Error(), physicalID)
		return nil, err
	}
	runtime.recorder.emit(runtime.programID, "physical.started", "", physicalID)
	value, executeErr := execute(ctx)
	runtime.recorder.emit(runtime.programID, "physical.ended", errorString(executeErr), physicalID)
	release()
	return value, executeErr
}

func CampaignTreatmentOrder(repetition int) []CampaignTreatment {
	if repetition%2 == 0 {
		return []CampaignTreatment{CampaignBaseline, CampaignQualified}
	}
	return []CampaignTreatment{CampaignQualified, CampaignBaseline}
}

func newCampaignRequest(program CampaignProgram, rows []CampaignRow, indices map[string]int) CampaignRequest {
	request := CampaignRequest{
		Source: program.Source, SourceSHA256: program.SourceSHA256,
		Inputs: append(json.RawMessage(nil), program.Inputs...), InputsSHA256: program.InputsSHA256,
		PlanSHA256: program.PlanSHA256, GrantSetSHA256: program.GrantSetSHA256,
		PrivacyPartition: program.PrivacyPartition, WorkspaceFixtureSHA256: program.WorkspaceFixtureSHA256,
		Execution: cloneCampaignExecution(program.Execution), DependencyResults: make(map[string]json.RawMessage, len(program.Dependencies)),
	}
	for _, dependency := range program.Dependencies {
		request.DependencyResults[dependency] = append(json.RawMessage(nil), rows[indices[dependency]].Result...)
	}
	return request
}

func cloneCampaignExecution(execution CampaignExecutionContract) CampaignExecutionContract {
	clone := execution
	if execution.Verifier != nil {
		value := *execution.Verifier
		clone.Verifier = &value
	}
	if execution.Resume != nil {
		value := *execution.Resume
		clone.Resume = &value
	}
	if execution.Delegation != nil {
		value := *execution.Delegation
		clone.Delegation = &value
	}
	return clone
}

func RunTransparentCampaign(ctx context.Context, manifest CampaignManifest, treatment CampaignTreatment, adapter CampaignAdapter) (CampaignEvidence, error) {
	if manifest.Validate() != nil || adapter == nil || (treatment != CampaignBaseline && treatment != CampaignQualified) {
		return CampaignEvidence{}, ErrInvalidCampaignEvidence
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return CampaignEvidence{}, err
	}
	started := time.Now()
	cpuStarted, cpuAvailable := processCPUTimeNanos()
	recorder := &campaignRecorder{started: started}
	gate := newPhysicalGate(manifest.PhysicalSlots)
	rows := make([]CampaignRow, len(manifest.Programs))
	done := make([]chan struct{}, len(manifest.Programs))
	indices := make(map[string]int, len(manifest.Programs))
	for index, program := range manifest.Programs {
		done[index] = make(chan struct{})
		indices[program.ID] = index
	}
	var wait sync.WaitGroup
	for index, program := range manifest.Programs {
		target := started.Add(time.Duration(program.ReleaseOffsetMS) * time.Millisecond)
		if delay := time.Until(target); delay > 0 {
			select {
			case <-ctx.Done():
				return CampaignEvidence{}, ctx.Err()
			case <-time.After(delay):
			}
		}
		releaseNS := time.Since(started).Nanoseconds()
		recorder.emit(program.ID, "logical.released", "manifest_offset", "")
		wait.Add(1)
		go func(index int, program CampaignProgram, releaseNS int64) {
			defer wait.Done()
			defer close(done[index])
			for _, dependency := range program.Dependencies {
				select {
				case <-ctx.Done():
					rows[index] = CampaignRow{ProgramID: program.ID, ReleaseNS: releaseNS, EndNS: time.Since(started).Nanoseconds(), AdmissionReason: "context_cancelled", Disposition: "cancelled", Error: ctx.Err().Error()}
					return
				case <-done[indices[dependency]]:
				}
			}
			request := newCampaignRequest(program, rows, indices)
			admission := adapter.Admit(ctx, request, treatment)
			admissionNS := time.Since(started).Nanoseconds()
			if admission.Reason == "" {
				admission.Reason = "unspecified"
			}
			if !admission.Allowed {
				recorder.emit(program.ID, "admission.rejected", admission.Reason, "")
				endNS := time.Since(started).Nanoseconds()
				rows[index] = CampaignRow{ProgramID: program.ID, ReleaseNS: releaseNS, AdmissionNS: admissionNS, EndNS: endNS, AdmissionReason: admission.Reason, Disposition: admission.Disposition, Sharing: "no_execution"}
				recorder.emit(program.ID, "logical.terminal", admission.Disposition, "")
				return
			}
			recorder.emit(program.ID, "admission.accepted", admission.Reason, "")
			recorder.emit(program.ID, "logical.started", "", "")
			outcome := adapter.Execute(ctx, request, treatment, &CampaignRuntime{programID: program.ID, recorder: recorder, gate: gate})
			endNS := time.Since(started).Nanoseconds()
			rows[index] = CampaignRow{
				ProgramID: program.ID, ReleaseNS: releaseNS, AdmissionNS: admissionNS, EndNS: endNS,
				AdmissionReason: admission.Reason, Disposition: outcome.Disposition, Sharing: outcome.Sharing,
				PhysicalExecutionID: outcome.PhysicalExecutionID, Result: append(json.RawMessage(nil), outcome.Result...), Error: errorString(outcome.Err),
			}
			recorder.emit(program.ID, "logical.terminal", outcome.Disposition, outcome.PhysicalExecutionID)
		}(index, program, releaseNS)
	}
	wait.Wait()
	cpuEnded, cpuEndedAvailable := processCPUTimeNanos()
	recorder.mu.Lock()
	events := append([]CampaignEvent(nil), recorder.events...)
	physical := recorder.physical
	recorder.mu.Unlock()
	evidence := CampaignEvidence{
		SchemaVersion: CampaignEvidenceSchemaVersion, ManifestSHA256: digestCampaignEvidence(manifestJSON), Treatment: treatment,
		PhysicalSlots: manifest.PhysicalSlots, WallNS: time.Since(started).Nanoseconds(), PhysicalExecutions: physical,
		Rows: rows, Events: events,
	}
	if cpuAvailable && cpuEndedAvailable && cpuEnded >= cpuStarted {
		evidence.ProcessCPUAvailable = true
		evidence.ProcessCPUNS = cpuEnded - cpuStarted
	}
	evidence.SealSHA256 = campaignEvidenceSeal(evidence)
	if err := ValidateCampaignEvidence(manifest, evidence); err != nil {
		return CampaignEvidence{}, err
	}
	return evidence, nil
}

func ValidateCampaignEvidence(manifest CampaignManifest, evidence CampaignEvidence) error {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil || manifest.Validate() != nil || evidence.SchemaVersion != CampaignEvidenceSchemaVersion || evidence.ManifestSHA256 != digestCampaignEvidence(manifestJSON) ||
		(evidence.Treatment != CampaignBaseline && evidence.Treatment != CampaignQualified) || evidence.PhysicalSlots != manifest.PhysicalSlots || evidence.WallNS < 0 || len(evidence.Rows) != len(manifest.Programs) || len(evidence.Events) == 0 || evidence.SealSHA256 != campaignEvidenceSeal(evidence) {
		return ErrInvalidCampaignEvidence
	}
	physicalStarts := uint32(0)
	physicalIdentities := make(map[string]struct{})
	terminal := make(map[string]bool, len(manifest.Programs))
	var lastNS int64
	for index, event := range evidence.Events {
		if event.Sequence != uint64(index+1) || event.AtNS < 0 || event.AtNS < lastNS || event.ProgramID == "" || event.Type == "" {
			return ErrInvalidCampaignEvidence
		}
		lastNS = event.AtNS
		if event.Type == "physical.started" {
			if event.PhysicalExecutionID == "" {
				return ErrInvalidCampaignEvidence
			}
			physicalStarts++
			physicalIdentities[event.PhysicalExecutionID] = struct{}{}
		}
		if event.Type == "logical.terminal" {
			terminal[event.ProgramID] = true
		}
	}
	if physicalStarts != evidence.PhysicalExecutions {
		return ErrInvalidCampaignEvidence
	}
	for index, row := range evidence.Rows {
		program := manifest.Programs[index]
		if row.ProgramID != program.ID || row.ReleaseNS < 0 || row.AdmissionNS < row.ReleaseNS || row.EndNS < row.AdmissionNS || row.AdmissionReason == "" || row.Disposition != program.Expected.Disposition || !terminal[row.ProgramID] {
			return ErrInvalidCampaignEvidence
		}
		if program.Expected.Admission == "admit" {
			_, physicalExists := physicalIdentities[row.PhysicalExecutionID]
			if row.PhysicalExecutionID == "" || !physicalExists || !bytes.Equal(row.Result, program.Expected.Oracle) {
				return ErrInvalidCampaignEvidence
			}
		} else if row.PhysicalExecutionID != "" || len(row.Result) != 0 || row.Sharing != "no_execution" {
			return ErrInvalidCampaignEvidence
		}
	}
	if err := validateCampaignMechanismEvents(manifest, evidence, physicalIdentities); err != nil {
		return err
	}
	return nil
}

func validateCampaignMechanismEvents(manifest CampaignManifest, evidence CampaignEvidence, physicalIdentities map[string]struct{}) error {
	core := map[string]struct{}{"logical.released": {}, "admission.accepted": {}, "admission.rejected": {}, "logical.started": {}, "physical.queued": {}, "physical.started": {}, "physical.ended": {}, "physical.cancelled": {}, "logical.terminal": {}}
	mechanism := map[string]struct{}{"workspace.forked": {}, "workspace.sealed": {}, "workspace.discarded": {}, "verification.completed": {}, "sharing.decided": {}, "workflow.waiting": {}, "workflow.resumed": {}, "authority.refreshed": {}, "delegation.child_started": {}}
	programs := make(map[string]CampaignProgram, len(manifest.Programs))
	rows := make(map[string]CampaignRow, len(evidence.Rows))
	for _, program := range manifest.Programs {
		programs[program.ID] = program
	}
	for _, row := range evidence.Rows {
		rows[row.ProgramID] = row
	}
	byProgram := make(map[string]map[string][]CampaignEvent, len(manifest.Programs))
	physicalCore := make(map[string]map[string]int)
	physicalEvents := make(map[string]map[string]CampaignEvent)
	physicalOwner := make(map[string]string)
	for _, event := range evidence.Events {
		if _, ok := programs[event.ProgramID]; !ok {
			return ErrInvalidCampaignEvidence
		}
		if _, ok := core[event.Type]; !ok {
			if _, ok := mechanism[event.Type]; !ok {
				return ErrInvalidCampaignEvidence
			}
			requiresPhysical := event.Type == "sharing.decided" || event.Type == "verification.completed"
			if requiresPhysical != (event.PhysicalExecutionID != "") {
				return ErrInvalidCampaignEvidence
			}
			if event.PhysicalExecutionID != "" {
				if _, ok := physicalIdentities[event.PhysicalExecutionID]; !ok {
					return ErrInvalidCampaignEvidence
				}
			}
		}
		if byProgram[event.ProgramID] == nil {
			byProgram[event.ProgramID] = make(map[string][]CampaignEvent)
		}
		byProgram[event.ProgramID][event.Type] = append(byProgram[event.ProgramID][event.Type], event)
		if strings.HasPrefix(event.Type, "physical.") && event.PhysicalExecutionID == "" {
			return ErrInvalidCampaignEvidence
		}
		if strings.HasPrefix(event.Type, "physical.") {
			if owner := physicalOwner[event.PhysicalExecutionID]; owner != "" && owner != event.ProgramID {
				return ErrInvalidCampaignEvidence
			}
			physicalOwner[event.PhysicalExecutionID] = event.ProgramID
			if physicalCore[event.PhysicalExecutionID] == nil {
				physicalCore[event.PhysicalExecutionID] = make(map[string]int)
				physicalEvents[event.PhysicalExecutionID] = make(map[string]CampaignEvent)
			}
			physicalCore[event.PhysicalExecutionID][event.Type]++
			physicalEvents[event.PhysicalExecutionID][event.Type] = event
		}
	}
	for identity, counts := range physicalCore {
		_, started := physicalIdentities[identity]
		events := physicalEvents[identity]
		if started {
			if counts["physical.queued"] != 1 || counts["physical.started"] != 1 || counts["physical.ended"] != 1 || counts["physical.cancelled"] != 0 || events["physical.queued"].Sequence >= events["physical.started"].Sequence || events["physical.started"].Sequence >= events["physical.ended"].Sequence {
				return ErrInvalidCampaignEvidence
			}
		} else if counts["physical.queued"] != 1 || counts["physical.cancelled"] != 1 || counts["physical.started"] != 0 || counts["physical.ended"] != 0 || events["physical.queued"].Sequence >= events["physical.cancelled"].Sequence {
			return ErrInvalidCampaignEvidence
		}
	}
	expectedAdmissions := campaignExpectedAdmissionReasons(manifest)
	for _, program := range manifest.Programs {
		events := byProgram[program.ID]
		row := rows[program.ID]
		released := events["logical.released"]
		terminalEvents := events["logical.terminal"]
		if len(released) != 1 || released[0].Reason != "manifest_offset" || len(terminalEvents) != 1 || terminalEvents[0].Reason != row.Disposition || terminalEvents[0].PhysicalExecutionID != row.PhysicalExecutionID || row.AdmissionReason != expectedAdmissions[program.ID] {
			return ErrInvalidCampaignEvidence
		}
		terminal := terminalEvents[0]
		if program.Expected.Admission == "admit" {
			accepted, startedEvents := events["admission.accepted"], events["logical.started"]
			if len(accepted) != 1 || len(events["admission.rejected"]) != 0 || len(startedEvents) != 1 || startedEvents[0].Reason != "" || accepted[0].Reason != row.AdmissionReason || !(released[0].Sequence < accepted[0].Sequence && accepted[0].Sequence < startedEvents[0].Sequence && startedEvents[0].Sequence < terminal.Sequence) {
				return ErrInvalidCampaignEvidence
			}
			owner := physicalOwner[row.PhysicalExecutionID]
			if owner == "" || (owner != program.ID && (program.Execution.Kind != CampaignExactRequest || !campaignExactIdentityEqual(program, programs[owner]))) {
				return ErrInvalidCampaignEvidence
			}
			for eventType, values := range events {
				if strings.HasPrefix(eventType, "physical.") || (eventType != "logical.released" && eventType != "admission.accepted" && eventType != "logical.started" && eventType != "logical.terminal") {
					for _, event := range values {
						if event.Sequence <= startedEvents[0].Sequence || event.Sequence >= terminal.Sequence {
							return ErrInvalidCampaignEvidence
						}
					}
				}
			}
		} else {
			rejected := events["admission.rejected"]
			if len(events["admission.accepted"]) != 0 || len(rejected) != 1 || len(events["logical.started"]) != 0 || rejected[0].Reason != row.AdmissionReason || !(released[0].Sequence < rejected[0].Sequence && rejected[0].Sequence < terminal.Sequence) || len(events["physical.queued"])+len(events["physical.started"])+len(events["physical.ended"])+len(events["physical.cancelled"]) != 0 {
				return ErrInvalidCampaignEvidence
			}
		}
		for _, event := range events["physical.queued"] {
			if event.Reason != "fifo" {
				return ErrInvalidCampaignEvidence
			}
		}
		for _, event := range events["physical.started"] {
			if event.Reason != "" {
				return ErrInvalidCampaignEvidence
			}
		}
		for _, event := range events["physical.ended"] {
			if event.Reason != "" {
				return ErrInvalidCampaignEvidence
			}
		}
		for _, event := range events["physical.cancelled"] {
			if event.Reason != context.Canceled.Error() && event.Reason != context.DeadlineExceeded.Error() {
				return ErrInvalidCampaignEvidence
			}
		}
		expected := make(map[string]bool)
		if program.Execution.CancelPoint == CampaignCancelAfterWorkspaceFork {
			expected["workspace.forked"], expected["workspace.discarded"] = true, true
		}
		if program.Execution.Kind == CampaignVerifyWorkspace {
			expected["workspace.forked"], expected["workspace.sealed"], expected["verification.completed"] = true, true, true
		}
		if program.Execution.Kind == CampaignExactRequest {
			expected["sharing.decided"] = true
		}
		if program.Execution.Kind == CampaignStartWorkflow {
			expected["workflow.waiting"] = true
		}
		if program.Execution.Kind == CampaignResumeWorkflow && program.Expected.Admission == "admit" {
			expected["workflow.resumed"] = true
			if program.Execution.Resume != nil && program.Execution.Resume.Transition == CampaignResumePlanGrantChanged {
				expected["authority.refreshed"] = true
			}
		}
		if program.Execution.Kind == CampaignDelegateChild && program.Expected.Admission == "admit" {
			expected["delegation.child_started"] = true
		}
		for eventType := range mechanism {
			count := len(events[eventType])
			if expected[eventType] && count != 1 {
				return ErrInvalidCampaignEvidence
			}
			if !expected[eventType] && count != 0 {
				return ErrInvalidCampaignEvidence
			}
		}
		if values := events["sharing.decided"]; len(values) == 1 {
			if values[0].Reason != row.Sharing || values[0].PhysicalExecutionID != row.PhysicalExecutionID {
				return ErrInvalidCampaignEvidence
			}
			owner := physicalOwner[values[0].PhysicalExecutionID]
			if owner != program.ID {
				ownerShares := byProgram[owner]["sharing.decided"]
				if len(ownerShares) != 1 || ownerShares[0].PhysicalExecutionID != values[0].PhysicalExecutionID {
					return ErrInvalidCampaignEvidence
				}
			}
		}
		if values := events["workspace.forked"]; len(values) == 1 && values[0].Reason != "private_attempt" {
			return ErrInvalidCampaignEvidence
		}
		if values := events["workspace.discarded"]; len(values) == 1 && values[0].Reason != string(program.Execution.CancelPoint) {
			return ErrInvalidCampaignEvidence
		}
		sealedRoot := ""
		if values := events["workspace.sealed"]; len(values) == 1 {
			sealedRoot = values[0].Reason
			if !campaignDigestPattern.MatchString(sealedRoot) {
				return ErrInvalidCampaignEvidence
			}
		}
		if values := events["verification.completed"]; len(values) == 1 {
			verifierJSON, err := json.Marshal(program.Execution.Verifier)
			verifierPrefix := sealedRoot + ":" + digestCampaignEvidence(verifierJSON) + ":"
			if err != nil || values[0].Reason != verifierPrefix+row.Sharing {
				return ErrInvalidCampaignEvidence
			}
			owner := physicalOwner[values[0].PhysicalExecutionID]
			ownerProgram, ok := programs[owner]
			ownerValues := byProgram[owner]["verification.completed"]
			if !ok || ownerProgram.Execution.Kind != CampaignVerifyWorkspace || len(ownerValues) != 1 || ownerValues[0].PhysicalExecutionID != values[0].PhysicalExecutionID || !strings.HasPrefix(ownerValues[0].Reason, verifierPrefix) {
				return ErrInvalidCampaignEvidence
			}
		}
		if values := events["workflow.waiting"]; len(values) == 1 && values[0].Reason != program.Execution.WorkflowStateKey {
			return ErrInvalidCampaignEvidence
		}
		if values := events["workflow.resumed"]; len(values) == 1 && (program.Execution.Resume == nil || values[0].Reason != string(program.Execution.Resume.Transition)) {
			return ErrInvalidCampaignEvidence
		}
		if values := events["authority.refreshed"]; len(values) == 1 && values[0].Reason != program.PlanSHA256 {
			return ErrInvalidCampaignEvidence
		}
		if values := events["delegation.child_started"]; len(values) == 1 && (program.Execution.Delegation == nil || values[0].Reason != program.Execution.Delegation.GroupID) {
			return ErrInvalidCampaignEvidence
		}
	}
	return nil
}

func campaignExpectedAdmissionReasons(manifest CampaignManifest) map[string]string {
	reasons := make(map[string]string, len(manifest.Programs))
	reserved := make(map[string]uint64)
	for _, program := range manifest.Programs {
		reason := "admitted"
		switch program.Execution.Kind {
		case CampaignResumeWorkflow:
			if program.Execution.Resume != nil && program.Execution.Resume.Transition == CampaignResumeExpired {
				reason = "authority_expired"
			}
		case CampaignDelegateChild:
			contract := program.Execution.Delegation
			switch {
			case program.Execution.CancelPoint == CampaignCancelAfterParentTerminal:
				reason = "parent_terminal"
			case contract == nil || program.PlanSHA256 != contract.ParentPlanSHA256:
				reason = "authority_widening"
			case reserved[contract.GroupID]+uint64(contract.ChildReservedCalls) > uint64(contract.MaxDelegatedCalls):
				reason = "delegation_budget"
			default:
				reserved[contract.GroupID] += uint64(contract.ChildReservedCalls)
			}
		}
		reasons[program.ID] = reason
	}
	return reasons
}

func campaignExactIdentityEqual(left, right CampaignProgram) bool {
	return left.Execution.Kind == CampaignExactRequest && right.Execution.Kind == CampaignExactRequest &&
		left.SourceSHA256 == right.SourceSHA256 && left.InputsSHA256 == right.InputsSHA256 &&
		left.PlanSHA256 == right.PlanSHA256 && left.GrantSetSHA256 == right.GrantSetSHA256 &&
		left.WorkspaceFixtureSHA256 == right.WorkspaceFixtureSHA256 && left.PrivacyPartition == right.PrivacyPartition
}

func (evidence CampaignEvidence) Clone() CampaignEvidence {
	clone := evidence
	clone.Rows = append([]CampaignRow(nil), evidence.Rows...)
	for index := range clone.Rows {
		clone.Rows[index].Result = append(json.RawMessage(nil), evidence.Rows[index].Result...)
	}
	clone.Events = append([]CampaignEvent(nil), evidence.Events...)
	return clone
}

func campaignEvidenceSeal(evidence CampaignEvidence) string {
	candidate := evidence
	candidate.SealSHA256 = ""
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return ""
	}
	return digestCampaignEvidence(encoded)
}

func digestCampaignEvidence(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
