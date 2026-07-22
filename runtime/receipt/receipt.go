// Package receipt defines bounded Host-authored capability evidence.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

type Receipt struct {
	ReceiptID      string `json:"receipt_id"`
	RunID          string `json:"run_id"`
	Capability     string `json:"capability"`
	OperationIndex uint32 `json:"operation_index"`
	RequestSHA256  string `json:"request_sha256,omitempty"`
	ResponseSHA256 string `json:"response_sha256,omitempty"`
	Outcome        string `json:"outcome"`
}

func digest(value []byte) string {
	hashed := sha256.Sum256(value)
	return hex.EncodeToString(hashed[:])
}

func New(
	runIdentity,
	callID,
	capability string,
	operationIndex uint32,
	requestIdentity,
	outcome string,
	response []byte,
) Receipt {
	requestDigest := digest([]byte(requestIdentity))
	identity := sha256.New()
	for _, field := range []string{
		"agent-python-runtime-receipt-v1",
		runIdentity,
		callID,
		capability,
		strconv.FormatUint(uint64(operationIndex), 10),
		requestDigest,
	} {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	receipt := Receipt{
		ReceiptID:      "rcpt_" + hex.EncodeToString(identity.Sum(nil)),
		RunID:          runIdentity,
		Capability:     capability,
		OperationIndex: operationIndex,
		RequestSHA256:  requestDigest,
		Outcome:        outcome,
	}
	if response != nil {
		receipt.ResponseSHA256 = digest(response)
	}
	return receipt
}
