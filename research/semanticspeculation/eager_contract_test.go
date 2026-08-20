package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

const eagerTargetPython = "cpython-3.14.0-wasi"

func TestNewEagerStyleGateV1FreezesSemantics(t *testing.T) {
	value, err := NewEagerStyleGateV1(eagerTargetPython)
	if err != nil {
		t.Fatal(err)
	}
	if value.SchemaVersion != EagerComparatorSchemaVersion || value.Treatment != "eager_style_gate" ||
		value.Source.PDFSHA256 != "sha256:23af671ca94b7cbbc0866a37391520ae39e75c964320e7809b1612dfb3e023cb" ||
		value.Chunking.LookaheadTokens != 1 || !value.Execution.PersistentInterpreter || value.Execution.EarlyInterrupt ||
		value.Gate.DeniedAction != "seal_remaining_suffix" || len(value.Gate.DeniedModules) != 8 || len(value.Gate.DynamicNames) != 4 {
		t.Fatalf("contract=%+v", value)
	}
	sealed, err := SealEagerComparatorContract(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeEagerComparatorContract(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEagerComparatorContract(encoded)
	if err != nil || decoded.Identity != sealed.Identity || !bytes.Equal(encoded, mustEncodeEager(t, decoded)) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestCheckedInEagerStyleGateV1IsCanonical(t *testing.T) {
	raw, err := os.ReadFile("../../docs/evidence/eager-style-gate-contract-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := DecodeEagerComparatorContract(raw)
	if err != nil || value.Identity != EagerStyleGateV1Identity {
		t.Fatalf("identity=%s err=%v", value.Identity, err)
	}
}

func TestEagerStyleGateV1RejectsMutationAndBodies(t *testing.T) {
	value, err := NewEagerStyleGateV1(eagerTargetPython)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealEagerComparatorContract(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustEncodeEager(t, sealed)
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"treatment", "target_python", "source", "chunking", "execution", "gate"} {
		mutated := make(map[string]any, len(document))
		for key, item := range document {
			mutated[key] = item
		}
		mutated[field] = "tampered"
		raw, _ := json.Marshal(mutated)
		if _, err := DecodeEagerComparatorContract(raw); err == nil {
			t.Fatalf("mutation accepted: %s", field)
		}
	}
	unknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	unknown = append(unknown, []byte(`,"source_body":"private"}`)...)
	if _, err := DecodeEagerComparatorContract(unknown); err == nil {
		t.Fatal("unknown body accepted")
	}
}

func mustEncodeEager(t *testing.T, value EagerComparatorContract) []byte {
	t.Helper()
	raw, err := EncodeEagerComparatorContract(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
