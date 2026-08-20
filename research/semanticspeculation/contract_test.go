package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"testing"
)

const parentCommit = "f604ce16b5bc7135c92e1dc70f9b91b124cf9f2c"

func validPreregistration() Preregistration {
	value, err := NewV1Preregistration(parentCommit)
	if err != nil {
		panic(err)
	}
	return value
}

func TestNewV1PreregistrationIsDeterministicAndUnsealed(t *testing.T) {
	left, err := NewV1Preregistration(parentCommit)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewV1Preregistration(parentCommit)
	if err != nil {
		t.Fatal(err)
	}
	if left.Identity != "" || right.Identity != "" {
		t.Fatal("new preregistration was already sealed")
	}
	leftSealed, err := SealPreregistration(left)
	if err != nil {
		t.Fatal(err)
	}
	rightSealed, err := SealPreregistration(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftSealed.Identity != rightSealed.Identity {
		t.Fatalf("non-deterministic identity: %s != %s", leftSealed.Identity, rightSealed.Identity)
	}
	if _, err := NewV1Preregistration("not-a-commit"); err == nil {
		t.Fatal("invalid parent commit accepted")
	}
}

func TestPreregistrationRoundTripAndSeal(t *testing.T) {
	preregistration := validPreregistration()
	sealed, err := SealPreregistration(preregistration)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePreregistration(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreregistration(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodePreregistration(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) || decoded.Identity == "" {
		t.Fatalf("non-canonical round trip: %s", encoded)
	}
}

func TestPreregistrationRejectsMutationAndUnknownFields(t *testing.T) {
	sealed, err := SealPreregistration(validPreregistration())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePreregistration(sealed)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	document["shuffle_seed"] = float64(1)
	mutated, _ := json.Marshal(document)
	if _, err := DecodePreregistration(mutated); err == nil {
		t.Fatal("identity mutation accepted")
	}
	unknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, err := DecodePreregistration(unknown); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestPreregistrationRejectsOrderDuplicatesAndUnsafeClaims(t *testing.T) {
	for name, mutate := range map[string]func(*Preregistration){
		"treatment order": func(value *Preregistration) {
			value.Treatments[0], value.Treatments[1] = value.Treatments[1], value.Treatments[0]
		},
		"duplicate case": func(value *Preregistration) { value.Cases[1] = value.Cases[0] },
		"unsafe claim": func(value *Preregistration) {
			value.ClaimBoundary.Supported = append(value.ClaimBoundary.Supported, "production_speedup")
		},
		"zero trials": func(value *Preregistration) { value.TrialsPerTreatment = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			value := validPreregistration()
			mutate(&value)
			if _, err := SealPreregistration(value); err == nil {
				t.Fatal("invalid preregistration accepted")
			}
		})
	}
}
