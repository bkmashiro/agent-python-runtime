// Package receipt defines bounded Host-authored capability evidence.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

type Receipt struct {
	ID             string `json:"id"`
	Capability     string `json:"capability"`
	CallID         string `json:"call_id"`
	OperationIndex uint32 `json:"operation_index"`
	TargetDigest   string `json:"target_digest"`
	Status         string `json:"status"`
	ResponseBytes  uint32 `json:"response_bytes"`
}

func TargetDigest(target string) string {
	digest := sha256.Sum256([]byte(target))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func New(runIdentity, callID, capability string, operationIndex uint32, target, status string, responseBytes uint32) Receipt {
	targetDigest := TargetDigest(target)
	identity := sha256.New()
	for _, field := range []string{
		"agent-python-runtime-receipt-v1",
		runIdentity,
		callID,
		capability,
		strconv.FormatUint(uint64(operationIndex), 10),
		targetDigest,
	} {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	return Receipt{
		ID:             "rcpt_" + hex.EncodeToString(identity.Sum(nil)),
		Capability:     capability,
		CallID:         callID,
		OperationIndex: operationIndex,
		TargetDigest:   targetDigest,
		Status:         status,
		ResponseBytes:  responseBytes,
	}
}
