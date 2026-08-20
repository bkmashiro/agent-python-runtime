package preparedregion

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPreparedRegionLiveInsAndReadyScratchResultPublishOneBoundCapsule(t *testing.T) {
	liveInsRaw, liveInsSHA, err := SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
	if err != nil {
		t.Fatal(err)
	}
	decodedLiveIns, decodedSHA, err := DecodePreparedRegionLiveIns(liveInsRaw)
	if err != nil || decodedSHA != liveInsSHA || string(decodedLiveIns["seed"]) != "40" {
		t.Fatalf("live-ins=(%s,%s,%v) err=%v", liveInsRaw, decodedSHA, decodedLiveIns, err)
	}
	binding := validPreparedRegionBinding()
	binding.LiveInsSHA256 = liveInsSHA
	_, decision, err := SealPreparedRegionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := preparedRegionBytesDigest([]byte(`42`))
	resultRaw := canonicalScratchJSON(t, map[string]any{
		"decision_sha256": decision.IdentitySHA256, "error_type": "", "payload": 42,
		"payload_sha256": payloadSHA, "schema_version": PreparedRegionScratchResultSchemaVersion,
		"status": string(PreparedRegionScratchReady),
	})
	result, err := DecodePreparedRegionScratchResult(resultRaw)
	if err != nil || result.Status != PreparedRegionScratchReady {
		t.Fatalf("raw=%s result=%+v err=%v", resultRaw, result, err)
	}
	_, capsule, err := PublishPreparedRegionScratchResult(decision, result)
	if err != nil || string(capsule.Payload) != "42" || capsule.DecisionSHA256 != decision.IdentitySHA256 {
		t.Fatalf("capsule=%+v err=%v", capsule, err)
	}
}

func TestPreparedRegionScratchContractsRejectDriftAndNeverPublishNonready(t *testing.T) {
	if _, _, err := SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`40.0`)}); err == nil {
		t.Fatal("float live-in accepted")
	}
	if _, _, err := SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`9223372036854775808`)}); err == nil {
		t.Fatal("out-of-range live-in accepted")
	}
	binding := validPreparedRegionBinding()
	_, decision, err := SealPreparedRegionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []PreparedRegionScratchStatus{PreparedRegionScratchRejected, PreparedRegionScratchFailed, PreparedRegionScratchCancelled} {
		raw := canonicalScratchJSON(t, map[string]any{
			"decision_sha256": decision.IdentitySHA256, "error_type": "terminal_control",
			"payload": nil, "payload_sha256": "", "schema_version": PreparedRegionScratchResultSchemaVersion,
			"status": string(status),
		})
		result, decodeErr := DecodePreparedRegionScratchResult(raw)
		if decodeErr != nil {
			t.Fatalf("status=%s decode=%v", status, decodeErr)
		}
		if _, _, publishErr := PublishPreparedRegionScratchResult(decision, result); publishErr == nil {
			t.Fatalf("status=%s published a capsule", status)
		}
	}
	ready := canonicalScratchJSON(t, map[string]any{
		"decision_sha256": decision.IdentitySHA256, "error_type": "", "payload": true,
		"payload_sha256": preparedRegionBytesDigest([]byte(`true`)), "schema_version": PreparedRegionScratchResultSchemaVersion,
		"status": string(PreparedRegionScratchReady),
	})
	for name, raw := range map[string][]byte{
		"trailing":       append(bytes.Clone(ready), ' '),
		"payload digest": bytes.Replace(bytes.Clone(ready), []byte(preparedRegionBytesDigest([]byte(`true`))), []byte(testDigestB), 1),
	} {
		if _, err := DecodePreparedRegionScratchResult(raw); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	driftRaw := bytes.Replace(bytes.Clone(ready), []byte(decision.IdentitySHA256), []byte(testDigestB), 1)
	drift, err := DecodePreparedRegionScratchResult(driftRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PublishPreparedRegionScratchResult(decision, drift); err == nil {
		t.Fatal("identity drift published a capsule")
	}
}

func canonicalScratchJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
