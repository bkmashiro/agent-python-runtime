package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

var ErrInvalidImportReceiptEvidence = errors.New("invalid import receipt evidence")

type ImportDecision string

const (
	ImportAdmitted ImportDecision = "admitted"
	ImportDenied   ImportDecision = "denied"
)

type ImportEvent struct {
	Sequence   uint32         `json:"sequence"`
	ModuleName string         `json:"module_name"`
	Decision   ImportDecision `json:"decision"`
}

type importReceiptDocument struct {
	SchemaVersion  uint32        `json:"schema_version"`
	Collector      string        `json:"collector"`
	PlanSHA256     string        `json:"plan_sha256,omitempty"`
	Events         []ImportEvent `json:"events"`
	EvidenceSHA256 string        `json:"evidence_sha256,omitempty"`
}

type ImportReceiptEvidence struct {
	collector      string
	events         []ImportEvent
	planSHA256     string
	evidenceSHA256 string
}

func DecodeGuestImportReceiptEvidence(data []byte) (ImportReceiptEvidence, error) {
	if len(data) == 0 || len(data) > maxSourceCompatibilityBytes || rejectDuplicateBoundedJSON(data) != nil {
		return ImportReceiptEvidence{}, ErrInvalidImportReceiptEvidence
	}
	var document importReceiptDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || ensureImportReceiptEOF(decoder) != nil || document.SchemaVersion != 1 || document.PlanSHA256 != "" || document.EvidenceSHA256 != "" {
		return ImportReceiptEvidence{}, ErrInvalidImportReceiptEvidence
	}
	result := ImportReceiptEvidence{collector: document.Collector, events: cloneImportEvents(document.Events)}
	if result.validateGuest() != nil {
		return ImportReceiptEvidence{}, ErrInvalidImportReceiptEvidence
	}
	return result, nil
}

func BindImportReceiptEvidence(plan FrozenRunPlan, guest ImportReceiptEvidence) (ImportReceiptEvidence, error) {
	if plan.Validate() != nil || guest.validateGuest() != nil {
		return ImportReceiptEvidence{}, ErrInvalidImportReceiptEvidence
	}
	result := ImportReceiptEvidence{collector: guest.collector, events: cloneImportEvents(guest.events), planSHA256: plan.PlanSHA256()}
	encoded, err := json.Marshal(result.document(false))
	if err != nil {
		return ImportReceiptEvidence{}, ErrInvalidImportReceiptEvidence
	}
	result.evidenceSHA256 = digestImportReceiptBytes(encoded)
	return result, result.Validate()
}

func (evidence ImportReceiptEvidence) Collector() string { return evidence.collector }
func (evidence ImportReceiptEvidence) Events() []ImportEvent {
	return cloneImportEvents(evidence.events)
}
func (evidence ImportReceiptEvidence) PlanSHA256() string     { return evidence.planSHA256 }
func (evidence ImportReceiptEvidence) EvidenceSHA256() string { return evidence.evidenceSHA256 }

func (evidence ImportReceiptEvidence) Validate() error {
	if evidence.validateGuest() != nil || !validProfileDigest(evidence.planSHA256) || !validProfileDigest(evidence.evidenceSHA256) {
		return ErrInvalidImportReceiptEvidence
	}
	encoded, err := json.Marshal(evidence.document(false))
	if err != nil || digestImportReceiptBytes(encoded) != evidence.evidenceSHA256 {
		return ErrInvalidImportReceiptEvidence
	}
	return nil
}

func (evidence ImportReceiptEvidence) validateGuest() error {
	if evidence.collector != "cpython-pre-cache-import-gate-v1" || len(evidence.events) > 1024 {
		return ErrInvalidImportReceiptEvidence
	}
	for index, event := range evidence.events {
		if event.Sequence != uint32(index) || !validModuleSet([]string{event.ModuleName}) || (event.Decision != ImportAdmitted && event.Decision != ImportDenied) {
			return ErrInvalidImportReceiptEvidence
		}
	}
	return nil
}

func (evidence ImportReceiptEvidence) document(includeDigest bool) importReceiptDocument {
	document := importReceiptDocument{SchemaVersion: 1, Collector: evidence.collector, PlanSHA256: evidence.planSHA256, Events: cloneImportEvents(evidence.events)}
	if includeDigest {
		document.EvidenceSHA256 = evidence.evidenceSHA256
	}
	return document
}

func (evidence ImportReceiptEvidence) MarshalJSON() ([]byte, error) {
	if evidence.Validate() != nil {
		return nil, ErrInvalidImportReceiptEvidence
	}
	return json.Marshal(evidence.document(true))
}

func (evidence *ImportReceiptEvidence) UnmarshalJSON(data []byte) error {
	if evidence == nil || len(data) == 0 || len(data) > maxSourceCompatibilityBytes || rejectDuplicateBoundedJSON(data) != nil {
		return ErrInvalidImportReceiptEvidence
	}
	var document importReceiptDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || ensureImportReceiptEOF(decoder) != nil || document.SchemaVersion != 1 {
		return ErrInvalidImportReceiptEvidence
	}
	candidate := ImportReceiptEvidence{
		collector: document.Collector, events: cloneImportEvents(document.Events),
		planSHA256: document.PlanSHA256, evidenceSHA256: document.EvidenceSHA256,
	}
	if candidate.Validate() != nil {
		return ErrInvalidImportReceiptEvidence
	}
	*evidence = candidate
	return nil
}

func cloneImportEvents(values []ImportEvent) []ImportEvent {
	if len(values) == 0 {
		return []ImportEvent{}
	}
	return append([]ImportEvent(nil), values...)
}

func digestImportReceiptBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func ensureImportReceiptEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrInvalidImportReceiptEvidence
	}
	return nil
}
