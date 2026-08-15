// Package receipt defines small Host-authored capability evidence.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

const (
	SourceBindingSchemaVersion = "pysolate.source-binding.v0"
	SourceClaimBound           = "source_bound"
)

type SourceBinding struct {
	SchemaVersion     string `json:"schema_version"`
	ClaimLevel        string `json:"claim_level"`
	DocumentID        string `json:"document_id"`
	SourceSHA256      string `json:"source_sha256"`
	OccurrenceID      string `json:"occurrence_id"`
	Capability        string `json:"capability"`
	DynamicOccurrence uint32 `json:"dynamic_occurrence"`
	StartLine         uint32 `json:"start_line"`
	StartColumn       uint32 `json:"start_column"`
	EndLine           uint32 `json:"end_line"`
	EndColumn         uint32 `json:"end_column"`
}

type Receipt struct {
	ReceiptID            string         `json:"receipt_id"`
	RunID                string         `json:"run_id"`
	CapabilityPlanSHA256 string         `json:"capability_plan_sha256"`
	CallID               string         `json:"call_id,omitempty"`
	ParentCallID         string         `json:"parent_call_id,omitempty"`
	ApprovalRequestID    string         `json:"approval_request_id,omitempty"`
	Capability           string         `json:"capability"`
	OperationIndex       uint32         `json:"operation_index"`
	RequestSHA256        string         `json:"request_sha256"`
	ResponseSHA256       string         `json:"response_sha256,omitempty"`
	Outcome              string         `json:"outcome"`
	Source               *SourceBinding `json:"source,omitempty"`
}

func New(runIdentity, capabilityPlanSHA256, callID, capability string, operationIndex uint32, requestIdentity, outcome string, response []byte) Receipt {
	return NewBound(runIdentity, capabilityPlanSHA256, callID, "", capability, operationIndex, requestIdentity, outcome, response)
}

func NewBound(runIdentity, capabilityPlanSHA256, callID, parentCallID, capability string, operationIndex uint32, requestIdentity, outcome string, response []byte) Receipt {
	return NewAuthorized(runIdentity, capabilityPlanSHA256, callID, parentCallID, "", capability, operationIndex, requestIdentity, outcome, response)
}

func NewAuthorized(runIdentity, capabilityPlanSHA256, callID, parentCallID, approvalRequestID, capability string, operationIndex uint32, requestIdentity, outcome string, response []byte) Receipt {
	receipt := Receipt{
		RunID: runIdentity, CapabilityPlanSHA256: capabilityPlanSHA256,
		Capability: capability, CallID: callID, ParentCallID: parentCallID, ApprovalRequestID: approvalRequestID,
		OperationIndex: operationIndex, RequestSHA256: digest([]byte(requestIdentity)), Outcome: outcome,
	}
	if response != nil {
		receipt.ResponseSHA256 = digest(response)
	}
	receipt.ReceiptID = operationIdentity(receipt)
	return receipt
}

// BindSource returns a new receipt whose operation identity also binds one exact
// source occurrence. The original receipt remains unchanged.
func BindSource(base Receipt, source SourceBinding) (Receipt, error) {
	if !ValidIdentity(base) || base.Source != nil || !ValidSourceBinding(source) || source.Capability != base.Capability {
		return Receipt{}, errors.New("invalid source-bound receipt")
	}
	copy := source
	base.Source = &copy
	base.ReceiptID = operationIdentity(base)
	return base, nil
}

// ValidIdentity verifies the operation identity projected with a receipt. The
// identity intentionally remains stable across outcome/response changes. v2
// binds a programmatic parent and approval request; v3 additionally binds one
// exact source occurrence.
func ValidIdentity(receipt Receipt) bool {
	return receipt.ReceiptID != "" && (receipt.Source == nil || ValidSourceBinding(*receipt.Source)) && receipt.ReceiptID == operationIdentity(receipt)
}

func ValidSourceBinding(source SourceBinding) bool {
	return source.SchemaVersion == SourceBindingSchemaVersion && source.ClaimLevel == SourceClaimBound &&
		validPrefixedDigest(source.DocumentID) && validPrefixedDigest(source.SourceSHA256) && validPrefixedDigest(source.OccurrenceID) &&
		source.Capability != "" && len(source.Capability) <= 128 && source.DynamicOccurrence > 0 && source.StartLine > 0 &&
		source.EndLine >= source.StartLine && (source.EndLine != source.StartLine || source.EndColumn >= source.StartColumn)
}

func operationIdentity(receipt Receipt) string {
	identity := sha256.New()
	fields := []string{"pysolate-receipt-v1", receipt.RunID, receipt.CapabilityPlanSHA256, receipt.CallID, receipt.Capability, strconv.FormatUint(uint64(receipt.OperationIndex), 10), receipt.RequestSHA256}
	if receipt.ParentCallID != "" || receipt.ApprovalRequestID != "" {
		fields = []string{"pysolate-receipt-v2", receipt.RunID, receipt.CapabilityPlanSHA256, receipt.CallID, receipt.ParentCallID, receipt.ApprovalRequestID, receipt.Capability, strconv.FormatUint(uint64(receipt.OperationIndex), 10), receipt.RequestSHA256}
	}
	if receipt.Source != nil {
		source := receipt.Source
		fields = []string{
			"pysolate-receipt-v3", receipt.RunID, receipt.CapabilityPlanSHA256, receipt.CallID, receipt.ParentCallID, receipt.ApprovalRequestID,
			receipt.Capability, strconv.FormatUint(uint64(receipt.OperationIndex), 10), receipt.RequestSHA256,
			source.SchemaVersion, source.ClaimLevel, source.DocumentID, source.SourceSHA256, source.OccurrenceID, source.Capability,
			strconv.FormatUint(uint64(source.DynamicOccurrence), 10), strconv.FormatUint(uint64(source.StartLine), 10),
			strconv.FormatUint(uint64(source.StartColumn), 10), strconv.FormatUint(uint64(source.EndLine), 10), strconv.FormatUint(uint64(source.EndColumn), 10),
		}
	}
	for _, field := range fields {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	return "rcpt_" + hex.EncodeToString(identity.Sum(nil))
}

func digest(value []byte) string {
	hashed := sha256.Sum256(value)
	return hex.EncodeToString(hashed[:])
}

func validPrefixedDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
