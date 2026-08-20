package preparedregion

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	testDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validPreparedRegionBinding() PreparedRegionBinding {
	return PreparedRegionBinding{
		SourceSHA256:           testDigestA,
		ASTSHA256:              testDigestA,
		AnalysisSHA256:         testDigestA,
		RegionID:               testDigestA,
		RegionSpan:             SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 5},
		RegionSourceSHA256:     testDigestA,
		LiveInsSHA256:          testDigestA,
		EnvironmentSHA256:      testDigestA,
		ExecutionProfileSHA256: testDigestA,
		ImportClosureSHA256:    testDigestA,
		CapabilityPlanSHA256:   testDigestA,
		PassConfigSHA256:       testDigestA,
		Codec:                  PreparedRegionCodecJSONScalarV1,
		OutputName:             "answer",
	}
}

func TestPreparedRegionDecisionSealDecodeAndExactBinding(t *testing.T) {
	binding := validPreparedRegionBinding()
	raw, decision, err := SealPreparedRegionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedRegionDecision(raw)
	if err != nil || decoded != decision {
		t.Fatalf("decode=(%+v,%v) want=%+v", decoded, err, decision)
	}
	if err := decoded.ValidateBinding(binding); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*PreparedRegionBinding){
		"source":      func(v *PreparedRegionBinding) { v.SourceSHA256 = testDigestB },
		"ast":         func(v *PreparedRegionBinding) { v.ASTSHA256 = testDigestB },
		"analysis":    func(v *PreparedRegionBinding) { v.AnalysisSHA256 = testDigestB },
		"region":      func(v *PreparedRegionBinding) { v.RegionID = testDigestB },
		"region span": func(v *PreparedRegionBinding) { v.RegionSpan.EndColumn++ },
		"region bytes": func(v *PreparedRegionBinding) {
			v.RegionSourceSHA256 = testDigestB
		},
		"inputs":      func(v *PreparedRegionBinding) { v.LiveInsSHA256 = testDigestB },
		"environment": func(v *PreparedRegionBinding) { v.EnvironmentSHA256 = testDigestB },
		"profile":     func(v *PreparedRegionBinding) { v.ExecutionProfileSHA256 = testDigestB },
		"imports":     func(v *PreparedRegionBinding) { v.ImportClosureSHA256 = testDigestB },
		"plan":        func(v *PreparedRegionBinding) { v.CapabilityPlanSHA256 = testDigestB },
		"pass":        func(v *PreparedRegionBinding) { v.PassConfigSHA256 = testDigestB },
		"codec":       func(v *PreparedRegionBinding) { v.Codec = "other" },
		"output":      func(v *PreparedRegionBinding) { v.OutputName = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := binding
			mutate(&candidate)
			if decoded.ValidateBinding(candidate) == nil {
				t.Fatal("mismatch accepted")
			}
		})
	}
}

func TestPreparedRegionDecisionDecoderIsStrictAndCanonical(t *testing.T) {
	raw, _, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string][]byte{
		"unknown":      bytes.Replace(raw, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1),
		"trailing":     append(append([]byte{}, raw...), []byte("{}")...),
		"noncanonical": append([]byte(" "), raw...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePreparedRegionDecision(candidate); err == nil {
				t.Fatal("invalid decision accepted")
			}
		})
	}
}

func TestPreparedRegionCapsuleAllowsOnlyBoundedCanonicalBoolOrInt64(t *testing.T) {
	_, decision, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`-42`)} {
		raw, capsule, sealErr := SealPreparedRegionCapsule(decision.IdentitySHA256, payload)
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		decoded, decodeErr := DecodePreparedRegionCapsule(raw)
		if decodeErr != nil || !reflect.DeepEqual(decoded, capsule) {
			t.Fatalf("decode=(%+v,%v) want=%+v", decoded, decodeErr, capsule)
		}
		if err := decoded.ValidateDecision(decision); err != nil {
			t.Fatal(err)
		}
	}
	for _, payload := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`1.0`), json.RawMessage(`1e3`), json.RawMessage(`"x"`), json.RawMessage(`[]`), json.RawMessage(`9223372036854775808`), json.RawMessage(strings.Repeat("1", PreparedRegionMaxPayloadBytes+1))} {
		if _, _, err := SealPreparedRegionCapsule(decision.IdentitySHA256, payload); err == nil {
			t.Fatalf("invalid payload accepted: %.32s", payload)
		}
	}
}

func TestPreparedRegionPatchBindsFinalSourceAndCarriesNoPayload(t *testing.T) {
	_, decision, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	binding := PreparedRegionPatchBinding{DecisionSHA256: decision.IdentitySHA256, FinalSourceSHA256: testDigestA, FinalASTSHA256: testDigestA, DerivedASTSHA256: testDigestB, RegionID: decision.RegionID, RegionSpan: decision.RegionSpan, OutputName: decision.OutputName}
	raw, patch, err := SealPreparedRegionPatch(binding)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedRegionPatch(raw)
	if err != nil || decoded != patch || decoded.HelperBinding != PreparedRegionHelperBinding || decoded.DecisionSHA256 != decision.IdentitySHA256 {
		t.Fatalf("decode=(%+v,%v) want=%+v", decoded, err, patch)
	}
	if bytes.Contains(raw, []byte("payload")) || bytes.Contains(raw, []byte("blob")) || bytes.Contains(raw, []byte("path")) || bytes.Contains(raw, []byte("credential")) {
		t.Fatalf("patch leaks materialisation transport: %s", raw)
	}
	if err := decoded.ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	binding.FinalSourceSHA256 = testDigestB
	if decoded.ValidateBinding(binding) == nil {
		t.Fatal("final source mismatch accepted")
	}
}

func TestPreparedRegionPatchBindingDecoderAcceptsOnlyCanonicalGuestEmission(t *testing.T) {
	_, decision, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	binding := PreparedRegionPatchBinding{DecisionSHA256: decision.IdentitySHA256, FinalSourceSHA256: testDigestA, FinalASTSHA256: testDigestA, DerivedASTSHA256: testDigestB, RegionID: decision.RegionID, RegionSpan: decision.RegionSpan, OutputName: decision.OutputName}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedRegionPatchBinding(raw)
	if err != nil || decoded != binding {
		t.Fatalf("decode=(%+v,%v) want=%+v", decoded, err, binding)
	}
	for name, candidate := range map[string][]byte{
		"unknown":  append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"extra":true}`)...),
		"trailing": append(append([]byte(nil), raw...), ' '),
		"tamper":   bytes.Replace(raw, []byte(testDigestB), []byte(`"bad"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, decodeErr := DecodePreparedRegionPatchBinding(candidate); decodeErr == nil {
				t.Fatalf("accepted %s binding", name)
			}
		})
	}
}

func TestPreparedRegionCapsuleAndPatchDecodersRejectTamperAndNoncanonicalData(t *testing.T) {
	_, decision, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	capsuleRaw, _, err := SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`42`))
	if err != nil {
		t.Fatal(err)
	}
	patchRaw, _, err := SealPreparedRegionPatch(PreparedRegionPatchBinding{DecisionSHA256: decision.IdentitySHA256, FinalSourceSHA256: testDigestA, FinalASTSHA256: testDigestA, DerivedASTSHA256: testDigestB, RegionID: decision.RegionID, RegionSpan: decision.RegionSpan, OutputName: decision.OutputName})
	if err != nil {
		t.Fatal(err)
	}
	decoders := map[string]struct {
		raw    []byte
		decode func([]byte) error
	}{
		"capsule": {capsuleRaw, func(raw []byte) error { _, decodeErr := DecodePreparedRegionCapsule(raw); return decodeErr }},
		"patch":   {patchRaw, func(raw []byte) error { _, decodeErr := DecodePreparedRegionPatch(raw); return decodeErr }},
	}
	for name, contract := range decoders {
		t.Run(name+" noncanonical", func(t *testing.T) {
			if contract.decode(append([]byte(" "), contract.raw...)) == nil {
				t.Fatal("noncanonical contract accepted")
			}
		})
		t.Run(name+" unknown", func(t *testing.T) {
			candidate := bytes.Replace(contract.raw, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1)
			if contract.decode(candidate) == nil {
				t.Fatal("unknown field accepted")
			}
		})
		t.Run(name+" identity tamper", func(t *testing.T) {
			candidate := tamperPreparedRegionIdentity(contract.raw)
			if contract.decode(candidate) == nil {
				t.Fatal("identity tamper accepted")
			}
		})
	}
}

func tamperPreparedRegionIdentity(raw []byte) []byte {
	marker := []byte(`"identity_sha256":"sha256:`)
	start := bytes.Index(raw, marker)
	if start < 0 {
		panic("identity field absent")
	}
	candidate := append([]byte(nil), raw...)
	start += len(marker)
	copy(candidate[start:start+64], strings.Repeat("b", 64))
	return candidate
}
