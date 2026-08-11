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
	Capability           string `json:"capability"`
	OperationIndex       uint32 `json:"operation_index"`
	RequestSHA256        string `json:"request_sha256"`
	ResponseSHA256       string `json:"response_sha256,omitempty"`
	Outcome              string `json:"outcome"`
}

func New(runIdentity, capabilityPlanSHA256, callID, capability string, operationIndex uint32, requestIdentity, outcome string, response []byte) Receipt {
	requestDigest := digest([]byte(requestIdentity))
	identity := sha256.New()
	for _, field := range []string{"pysolate-receipt-v1", runIdentity, capabilityPlanSHA256, callID, capability, strconv.FormatUint(uint64(operationIndex), 10), requestDigest} {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	receipt := Receipt{
		ReceiptID: "rcpt_" + hex.EncodeToString(identity.Sum(nil)), RunID: runIdentity, CapabilityPlanSHA256: capabilityPlanSHA256,
		Capability: capability, OperationIndex: operationIndex, RequestSHA256: requestDigest, Outcome: outcome,
	}
	if response != nil {
		receipt.ResponseSHA256 = digest(response)
	}
	return receipt
}

func digest(value []byte) string {
	hashed := sha256.Sum256(value)
	return hex.EncodeToString(hashed[:])
}
