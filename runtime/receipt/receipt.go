// Package receipt defines small Host-authored capability evidence.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

type Receipt struct {
	ReceiptID            string `json:"receipt_id"`
	RunID                string `json:"run_id"`
	CapabilityPlanSHA256 string `json:"capability_plan_sha256"`
	CallID               string `json:"call_id,omitempty"`
	ParentCallID         string `json:"parent_call_id,omitempty"`
	ApprovalRequestID    string `json:"approval_request_id,omitempty"`
	Capability           string `json:"capability"`
	OperationIndex       uint32 `json:"operation_index"`
	RequestSHA256        string `json:"request_sha256"`
	ResponseSHA256       string `json:"response_sha256,omitempty"`
	Outcome              string `json:"outcome"`
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

// ValidIdentity verifies the operation identity projected with a receipt. The
// identity intentionally remains stable across outcome/response changes, while
// v2 binds the programmatic parent and approval request when either is present.
func ValidIdentity(receipt Receipt) bool {
	return receipt.ReceiptID != "" && receipt.ReceiptID == operationIdentity(receipt)
}

func operationIdentity(receipt Receipt) string {
	identity := sha256.New()
	fields := []string{"pysolate-receipt-v1", receipt.RunID, receipt.CapabilityPlanSHA256, receipt.CallID, receipt.Capability, strconv.FormatUint(uint64(receipt.OperationIndex), 10), receipt.RequestSHA256}
	if receipt.ParentCallID != "" || receipt.ApprovalRequestID != "" {
		fields = []string{"pysolate-receipt-v2", receipt.RunID, receipt.CapabilityPlanSHA256, receipt.CallID, receipt.ParentCallID, receipt.ApprovalRequestID, receipt.Capability, strconv.FormatUint(uint64(receipt.OperationIndex), 10), receipt.RequestSHA256}
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
