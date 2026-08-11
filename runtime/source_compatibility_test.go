package runtime

import (
	"errors"
	"reflect"
	"testing"
)

func TestInferStaticImportRootsFromPreamble(t *testing.T) {
	imports, err := InferStaticImportRoots(`# comment
from __future__ import annotations
import json, math as m
from collections import Counter
result = Counter([m.floor(1.2)])
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"collections", "json", "math"}; !reflect.DeepEqual(imports, want) {
		t.Fatalf("imports=%v want=%v", imports, want)
	}
}

func TestInferStaticImportRootsFailsClosed(t *testing.T) {
	for name, source := range map[string]string{
		"dynamic":   `result = __import__("json")`,
		"nested":    "if True:\n    import json",
		"late":      "result = 1\nimport json",
		"relative":  "from .local import value",
		"multiline": "from x import (a, b)",
		"compound":  "import json; result = 1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InferStaticImportRoots(source); !errors.Is(err, ErrSourceCompatibilityIndeterminate) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
