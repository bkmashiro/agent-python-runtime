package valueslot

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	CanonicalNumpyInt64FileBytes  = 8*1024*1024 + 128
	CanonicalNumpyInt64FileSHA256 = "sha256:390bd7abd4f26dfdf3440f060f77bd02526e97e0a46838337f56cb8ef978af7e"
	CanonicalNumpyInt64Sum        = "549755289600"
	canonicalNumpyInt64SumProof   = "pysolate.canonical-numpy-int64-sum-proof.v1"
)

// NewCanonicalNumpyInt64SumTable is the producer for the fixed prepared-value
// fixture. It accepts one exact immutable fixture revision;
// callers cannot attest an arbitrary scalar by copying metadata strings.
func NewCanonicalNumpyInt64SumTable(input []byte, privacyPartition string) (*Table, error) {
	if len(input) != CanonicalNumpyInt64FileBytes || fileDigest(input) != CanonicalNumpyInt64FileSHA256 {
		return nil, ErrInvalidObject
	}
	object, err := NewPreparedObject(
		KindJSONScalar, []byte(CanonicalNumpyInt64Sum), "numpy-int64-sum-v1",
		CanonicalNumpyInt64FileSHA256, privacyPartition,
	)
	if err != nil {
		return nil, err
	}
	table, err := NewTable([]Entry{{
		Spec: SlotSpec{
			ID: "slot-numpy-sum-v1", SourceOccurrence: "line-4:result",
			ProducerIdentity: "numpy-int64-sum-v1", InputIdentity: CanonicalNumpyInt64FileSHA256,
			Kind: KindJSONScalar, MaxBytes: 32, PrivacyPartition: privacyPartition,
			ClaimPolicy: ClaimSingleUse, MaxClaims: 1,
		},
		Object: object, Strategy: StrategyInlineJSON,
	}})
	if err != nil {
		return nil, err
	}
	table.adapterProof = canonicalNumpyInt64SumProof
	return table, nil
}

func (table *Table) IsCanonicalNumpyInt64Sum() bool {
	if table == nil {
		return false
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	return !table.closed && table.adapterProof == canonicalNumpyInt64SumProof
}

func fileDigest(input []byte) string {
	digest := sha256.Sum256(input)
	return "sha256:" + hex.EncodeToString(digest[:])
}
