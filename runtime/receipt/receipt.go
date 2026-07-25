// Package receipt defines bounded Host-authored capability evidence.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

type Receipt struct {
	ReceiptID             string `json:"receipt_id"`
	RunID                 string `json:"run_id"`
	Capability            string `json:"capability"`
	OperationIndex        uint32 `json:"operation_index"`
	TransactionID         string `json:"transaction_id,omitempty"`
	OperationID           string `json:"operation_id,omitempty"`
	AttemptID             string `json:"attempt_id,omitempty"`
	CatalogDigest         string `json:"catalog_digest,omitempty"`
	HandlerVersion        string `json:"handler_version,omitempty"`
	EffectClass           string `json:"effect_class,omitempty"`
	Policy                string `json:"policy,omitempty"`
	ManifestDigest        string `json:"manifest_digest,omitempty"`
	ProviderRequestDigest string `json:"provider_request_digest,omitempty"`
	RequestSHA256         string `json:"request_sha256,omitempty"`
	ResponseSHA256        string `json:"response_sha256,omitempty"`
	Outcome               string `json:"outcome"`
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

func NewBoundFromRequestDigest(
	runIdentity, callID, capability string,
	operationIndex uint32,
	transactionID, operationID, attemptID, catalogDigest, handlerVersion, effectClass, policy, manifestDigest, providerRequestDigest, requestDigest, outcome string,
	response []byte,
) Receipt {
	identity := sha256.New()
	for _, field := range []string{
		"agent-python-runtime-receipt-v2-bound",
		runIdentity, callID, capability, strconv.FormatUint(uint64(operationIndex), 10),
		transactionID, operationID, attemptID,
		catalogDigest, handlerVersion, effectClass, policy, manifestDigest, providerRequestDigest, requestDigest,
	} {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	receipt := Receipt{
		ReceiptID: "rcpt_" + hex.EncodeToString(identity.Sum(nil)), RunID: runIdentity, Capability: capability,
		OperationIndex: operationIndex, TransactionID: transactionID, OperationID: operationID, AttemptID: attemptID,
		CatalogDigest: catalogDigest, HandlerVersion: handlerVersion, EffectClass: effectClass, Policy: policy,
		ManifestDigest: manifestDigest, ProviderRequestDigest: providerRequestDigest, RequestSHA256: requestDigest, Outcome: outcome,
	}
	if response != nil {
		receipt.ResponseSHA256 = digest(response)
	}
	return receipt
}

func NewBound(
	runIdentity,
	callID,
	capability string,
	operationIndex uint32,
	transactionID,
	operationID,
	attemptID,
	catalogDigest,
	handlerVersion,
	effectClass,
	policy,
	manifestDigest,
	providerRequestDigest,
	requestIdentity,
	outcome string,
	response []byte,
) Receipt {
	requestDigest := digest([]byte(requestIdentity))
	identity := sha256.New()
	for _, field := range []string{
		"agent-python-runtime-receipt-v2-bound",
		runIdentity, callID, capability, strconv.FormatUint(uint64(operationIndex), 10),
		transactionID, operationID, attemptID,
		catalogDigest, handlerVersion, effectClass, policy, manifestDigest, providerRequestDigest, requestDigest,
	} {
		identity.Write([]byte(field))
		identity.Write([]byte{0})
	}
	receipt := Receipt{
		ReceiptID:             "rcpt_" + hex.EncodeToString(identity.Sum(nil)),
		RunID:                 runIdentity,
		Capability:            capability,
		OperationIndex:        operationIndex,
		TransactionID:         transactionID,
		OperationID:           operationID,
		AttemptID:             attemptID,
		CatalogDigest:         catalogDigest,
		HandlerVersion:        handlerVersion,
		EffectClass:           effectClass,
		Policy:                policy,
		ManifestDigest:        manifestDigest,
		ProviderRequestDigest: providerRequestDigest,
		RequestSHA256:         requestDigest,
		Outcome:               outcome,
	}
	if response != nil {
		receipt.ResponseSHA256 = digest(response)
	}
	return receipt
}
