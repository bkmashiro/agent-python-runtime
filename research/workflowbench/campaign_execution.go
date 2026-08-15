package workflowbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const CampaignEvidenceSchemaVersion = "pysolate.transparent-campaign-evidence.v1"

var ErrInvalidCampaignEvidence = errors.New("invalid transparent campaign evidence")

type CampaignTreatment string

const (
	CampaignBaseline  CampaignTreatment = "baseline"
	CampaignQualified CampaignTreatment = "qualified"
)

type CampaignAdmission struct {
	Allowed bool
	Reason  string
}

type CampaignOutcome struct {
	Disposition         string
	Result              json.RawMessage
	PhysicalExecutionID string
	Sharing             string
	Err                 error
}

type CampaignAdapter interface {
	Admit(context.Context, CampaignProgram, CampaignTreatment) CampaignAdmission
	Execute(context.Context, CampaignProgram, CampaignTreatment, *CampaignRuntime) CampaignOutcome
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
	mu      sync.Mutex
	cond    *sync.Cond
	limit   uint32
	active  uint32
	next    uint64
	serving uint64
}

func newPhysicalGate(limit uint32) *physicalGate {
	gate := &physicalGate{limit: limit}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (gate *physicalGate) acquire(ctx context.Context) (func(), error) {
	gate.mu.Lock()
	ticket := gate.next
	gate.next++
	for ticket != gate.serving || gate.active >= gate.limit {
		if err := ctx.Err(); err != nil {
			gate.mu.Unlock()
			return nil, err
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
		gate.cond.Broadcast()
		gate.mu.Unlock()
	}, nil
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
			admission := adapter.Admit(ctx, program, treatment)
			admissionNS := time.Since(started).Nanoseconds()
			if admission.Reason == "" {
				admission.Reason = "unspecified"
			}
			if !admission.Allowed {
				recorder.emit(program.ID, "admission.rejected", admission.Reason, "")
				endNS := time.Since(started).Nanoseconds()
				rows[index] = CampaignRow{ProgramID: program.ID, ReleaseNS: releaseNS, AdmissionNS: admissionNS, EndNS: endNS, AdmissionReason: admission.Reason, Disposition: program.Expected.Disposition, Sharing: "no_execution"}
				recorder.emit(program.ID, "logical.terminal", program.Expected.Disposition, "")
				return
			}
			recorder.emit(program.ID, "admission.accepted", admission.Reason, "")
			recorder.emit(program.ID, "logical.started", "", "")
			outcome := adapter.Execute(ctx, program, treatment, &CampaignRuntime{programID: program.ID, recorder: recorder, gate: gate})
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
			if row.PhysicalExecutionID == "" || !bytes.Equal(row.Result, program.Expected.Oracle) {
				return ErrInvalidCampaignEvidence
			}
		} else if row.PhysicalExecutionID != "" || len(row.Result) != 0 || row.Sharing != "no_execution" {
			return ErrInvalidCampaignEvidence
		}
	}
	return nil
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
