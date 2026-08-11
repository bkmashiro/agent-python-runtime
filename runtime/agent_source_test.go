package runtime

import "testing"

func TestBindAgentSourceDerivesImports(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json", "math"})
	if err != nil {
		t.Fatal(err)
	}
	request := RunRequest{RunID: "run", Code: "import math\nimport json\nresult = 1", Inputs: []byte(`{}`), Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"subprocess"}}}
	bound, err := BindAgentSource(request, &profile)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Compatibility == nil || len(bound.Compatibility.Imports) != 2 || bound.Compatibility.Imports[0] != "json" || bound.Compatibility.Imports[1] != "math" {
		t.Fatalf("compatibility=%+v", bound.Compatibility)
	}
}

func TestBindAgentSourceRejectsUnsafeImports(t *testing.T) {
	profile, _ := NewExecutionProfile("base", []string{"json"})
	for _, code := range []string{"import subprocess\nresult=1", "result=__import__('json')"} {
		if _, err := BindAgentSource(RunRequest{Code: code}, &profile); err == nil {
			t.Fatalf("unsafe source accepted: %s", code)
		}
	}
}
