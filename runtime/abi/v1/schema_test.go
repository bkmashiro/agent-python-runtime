package v1_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var schemaNames = []string{
	"request", "response", "tool-request", "tool-response", "fetch-many-arguments", "fetch-many-result",
	"tool-catalog", "transaction-record", "effect-operation", "effect-attempt", "commit-command", "audit-evidence", "transaction-evidence", "transaction-evidence-v2",
}

func abiRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../abi/v1"))
}

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(abiRoot(t), name+".schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode %s schema: %v", name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	url := "https://agent-runtime.dev/abi/v1/" + name + ".schema.json"
	if err := compiler.AddResource(url, document); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return schema
}

func fixtureCases(t *testing.T, validity, name string) []string {
	t.Helper()
	pattern := filepath.Join(abiRoot(t), "fixtures", validity, name, "*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s fixtures for %s", validity, name)
	}
	return paths
}

func decodeFixture(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func TestABIV1Fixtures(t *testing.T) {
	for _, name := range schemaNames {
		name := name
		t.Run(name, func(t *testing.T) {
			schema := compileSchema(t, name)
			for _, path := range fixtureCases(t, "valid", name) {
				path := path
				t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
					if err := schema.Validate(decodeFixture(t, path)); err != nil {
						t.Fatalf("expected valid fixture: %v", err)
					}
				})
			}
			for _, path := range fixtureCases(t, "invalid", name) {
				path := path
				t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
					if err := schema.Validate(decodeFixture(t, path)); err == nil {
						t.Fatal("expected invalid fixture to fail")
					}
				})
			}
		})
	}
}

func TestTransactionEvidenceFixturesHaveCanonicalDigest(t *testing.T) {
	for _, path := range fixtureCases(t, "valid", "transaction-evidence-v2") {
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.DecodeAndVerifyTransactionEvidence(encoded); err != nil {
			t.Fatalf("fixture digest is not canonical: %s: %v", path, err)
		}
		tampered := bytes.Replace(encoded, []byte(`"run_id": "run_fixture"`), []byte(`"run_id": "run_tampered"`), 1)
		if bytes.Equal(tampered, encoded) {
			t.Fatal("tamper seam missing")
		}
		if _, err := transaction.DecodeAndVerifyTransactionEvidence(tampered); err == nil {
			t.Fatal("tampered evidence digest accepted")
		}
	}
}

func TestRunRequestRejectsAuthorityBearingAliases(t *testing.T) {
	schema := compileSchema(t, "request")
	for _, field := range []string{"capabilities", "credentials", "environment", "filesystem", "network", "budget"} {
		field := field
		t.Run(field, func(t *testing.T) {
			request := map[string]any{
				"run_id": "run-1",
				"code":   "result = inputs",
				"inputs": map[string]any{},
				field:    map[string]any{},
			}
			if err := schema.Validate(request); err == nil {
				t.Fatalf("authority-bearing field %q was accepted", field)
			}
		})
	}
}

func TestSchemasHaveStableIDs(t *testing.T) {
	for _, name := range schemaNames {
		path := filepath.Join(abiRoot(t), name+".schema.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("https://agent-runtime.dev/abi/v1/%s.schema.json", name)
		if got := document["$id"]; got != want {
			t.Fatalf("%s $id = %v, want %s", name, got, want)
		}
	}
}
