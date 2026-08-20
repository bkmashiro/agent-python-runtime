package preparedregion

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
)

const (
	PreparedRegionLiveInsSchemaVersion       = "pysolate.prepared-region-live-ins.v1"
	PreparedRegionScratchResultSchemaVersion = "pysolate.prepared-region-scratch-result.v1"
)

type PreparedRegionLiveIns struct {
	SchemaVersion string                     `json:"schema_version"`
	Values        map[string]json.RawMessage `json:"values"`
}

func SealPreparedRegionLiveIns(values map[string]json.RawMessage) ([]byte, string, error) {
	liveIns := PreparedRegionLiveIns{SchemaVersion: PreparedRegionLiveInsSchemaVersion, Values: values}
	if !validPreparedRegionLiveIns(liveIns) {
		return nil, "", ErrInvalidPreparedRegion
	}
	raw, err := preparedRegionCanonicalJSON(liveIns)
	if err != nil {
		return nil, "", err
	}
	return raw, preparedRegionBytesDigest(raw), nil
}

func DecodePreparedRegionLiveIns(raw []byte) (map[string]json.RawMessage, string, error) {
	var liveIns PreparedRegionLiveIns
	if err := preparedRegionDecode(raw, &liveIns); err != nil || !validPreparedRegionLiveIns(liveIns) {
		return nil, "", ErrInvalidPreparedRegion
	}
	return liveIns.Values, preparedRegionBytesDigest(raw), nil
}

func validPreparedRegionLiveIns(liveIns PreparedRegionLiveIns) bool {
	if liveIns.SchemaVersion != PreparedRegionLiveInsSchemaVersion || liveIns.Values == nil {
		return false
	}
	for name, raw := range liveIns.Values {
		if !pythonIdentifierPattern.MatchString(name) || name == PreparedRegionHelperBinding || !validCanonicalPreparedRegionScalar(raw) {
			return false
		}
	}
	return true
}

func validCanonicalPreparedRegionScalar(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	switch typed := value.(type) {
	case bool:
	case json.Number:
		if _, err := strconv.ParseInt(string(typed), 10, 64); err != nil {
			return false
		}
	default:
		return false
	}
	canonical, err := json.Marshal(value)
	return err == nil && bytes.Equal(raw, canonical)
}

type PreparedRegionScratchStatus string

const (
	PreparedRegionScratchReady     PreparedRegionScratchStatus = "ready"
	PreparedRegionScratchRejected  PreparedRegionScratchStatus = "rejected"
	PreparedRegionScratchFailed    PreparedRegionScratchStatus = "failed"
	PreparedRegionScratchCancelled PreparedRegionScratchStatus = "cancelled"
)

type PreparedRegionScratchResult struct {
	DecisionSHA256 string                      `json:"decision_sha256"`
	ErrorType      string                      `json:"error_type"`
	Payload        json.RawMessage             `json:"payload"`
	PayloadSHA256  string                      `json:"payload_sha256"`
	SchemaVersion  string                      `json:"schema_version"`
	Status         PreparedRegionScratchStatus `json:"status"`
}

func DecodePreparedRegionScratchResult(raw []byte) (PreparedRegionScratchResult, error) {
	var result PreparedRegionScratchResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return PreparedRegionScratchResult{}, ErrInvalidPreparedRegion
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(raw, canonical) || !result.valid() {
		return PreparedRegionScratchResult{}, ErrInvalidPreparedRegion
	}
	return result, nil
}

func NewPreparedRegionCancelledResult(decisionSHA256 string, errorType string) (PreparedRegionScratchResult, error) {
	result := PreparedRegionScratchResult{
		DecisionSHA256: decisionSHA256, ErrorType: errorType, Payload: json.RawMessage(`null`),
		SchemaVersion: PreparedRegionScratchResultSchemaVersion, Status: PreparedRegionScratchCancelled,
	}
	if !result.valid() {
		return PreparedRegionScratchResult{}, ErrInvalidPreparedRegion
	}
	return result, nil
}

func (result PreparedRegionScratchResult) valid() bool {
	if result.SchemaVersion != PreparedRegionScratchResultSchemaVersion || !digestPattern.MatchString(result.DecisionSHA256) {
		return false
	}
	if result.Status == PreparedRegionScratchReady {
		return result.ErrorType == "" && digestPattern.MatchString(result.PayloadSHA256) &&
			validCanonicalPreparedRegionScalar(result.Payload) && preparedRegionBytesDigest(result.Payload) == result.PayloadSHA256
	}
	if result.Status != PreparedRegionScratchRejected && result.Status != PreparedRegionScratchFailed && result.Status != PreparedRegionScratchCancelled {
		return false
	}
	return pythonIdentifierPattern.MatchString(result.ErrorType) && result.PayloadSHA256 == "" && bytes.Equal(result.Payload, []byte(`null`))
}

func PublishPreparedRegionScratchResult(decision PreparedRegionDecision, result PreparedRegionScratchResult) ([]byte, PreparedRegionCapsule, error) {
	if !decision.valid() || !result.valid() || result.Status != PreparedRegionScratchReady || result.DecisionSHA256 != decision.IdentitySHA256 {
		return nil, PreparedRegionCapsule{}, ErrInvalidPreparedRegion
	}
	return SealPreparedRegionCapsule(decision.IdentitySHA256, result.Payload)
}
