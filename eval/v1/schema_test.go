package v1_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"crypto/sha256"
	evalv1 "github.com/bkmashiro/agent-python-runtime/eval/v1"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var contractNames = []string{"agentic-development-trial", "comparison", "development-pilot", "experiment", "scenario", "trial-record", "trial-spec"}

func evalRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve evaluation contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func compileContract(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(evalRoot(t), "schemas", name+"-v1.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	wantID := "https://agent-runtime.dev/eval/v1/" + name + ".schema.json"
	if document["$id"] != wantID {
		t.Fatalf("%s $id=%v want=%s", name, document["$id"], wantID)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(wantID, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(wantID)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return compiled
}

func fixturePaths(t *testing.T, validity, name string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(evalRoot(t), "fixtures", validity, name, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s fixture for %s", validity, name)
	}
	sort.Strings(paths)
	return paths
}

func fixture(t *testing.T, path string) any {
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

func TestEvaluationContractFixtures(t *testing.T) {
	for _, name := range contractNames {
		t.Run(name, func(t *testing.T) {
			schema := compileContract(t, name)
			for _, path := range fixturePaths(t, "valid", name) {
				if err := schema.Validate(fixture(t, path)); err != nil {
					t.Fatalf("valid fixture %s rejected: %v", path, err)
				}
			}
			for _, path := range fixturePaths(t, "invalid", name) {
				if err := schema.Validate(fixture(t, path)); err == nil {
					t.Fatalf("invalid fixture %s accepted", path)
				}
			}
		})
	}
}

type promptManifest struct {
	SchemaVersion string            `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

func TestEvaluationComparisonSemanticIntegrity(t *testing.T) {
	experiment, err := os.ReadFile(filepath.Join(evalRoot(t), "fixtures", "valid", "experiment", "minimal.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range fixturePaths(t, "valid", "comparison") {
		comparison, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := evalv1.ValidateComparison(experiment, comparison); err != nil {
			t.Fatalf("semantically valid comparison %s rejected: %v", path, err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(evalRoot(t), "fixtures", "invalid-semantic", "comparison", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("semantic fixture discovery failed: %v", err)
	}
	for _, path := range paths {
		comparison, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := evalv1.ValidateComparison(experiment, comparison); err == nil {
			t.Fatalf("semantically invalid comparison %s accepted", path)
		}
	}
}

func TestEvaluationPromptsAreDigestBoundAndPreserveConditionBoundaries(t *testing.T) {
	promptRoot := filepath.Join(evalRoot(t), "prompts")
	data, err := os.ReadFile(filepath.Join(promptRoot, "manifest-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest promptManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "evaluation-prompt-manifest/v1" || len(manifest.Files) != 4 {
		t.Fatalf("unexpected prompt manifest: %+v", manifest)
	}
	for name, expected := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(promptRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		actual := "sha256:" + hex.EncodeToString(sum[:])
		if actual != expected {
			t.Fatalf("%s digest=%s want=%s", name, actual, expected)
		}
	}
	shared, err := os.ReadFile(filepath.Join(promptRoot, "shared-system-v1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Host ledger", "Do not fabricate", "compensation is not exact rollback", "final output schema"} {
		if !strings.Contains(string(shared), required) {
			t.Fatalf("shared prompt missing %q", required)
		}
	}
	conditions := map[string]string{
		"direct-only-v1.txt": "CONDITION: direct-only",
		"python-only-v1.txt": "CONDITION: python-only",
		"hybrid-v1.txt":      "CONDITION: hybrid",
	}
	for name, marker := range conditions {
		content, err := os.ReadFile(filepath.Join(promptRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if !strings.Contains(text, marker) || strings.Contains(strings.ToLower(text), "api key") {
			t.Fatalf("condition prompt boundary failed for %s", name)
		}
	}
	hybrid, _ := os.ReadFile(filepath.Join(promptRoot, "hybrid-v1.txt"))
	for _, forbidden := range []string{"always use Python", "prefer Python for complex tasks", "expected route"} {
		if strings.Contains(string(hybrid), forbidden) {
			t.Fatalf("hybrid prompt contains routing hint %q", forbidden)
		}
	}
}
