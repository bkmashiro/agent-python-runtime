package main

import (
	"bytes"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/numpyreuse"
)

func TestFrozenExecutionSpecsBindExpectedResultLexemes(t *testing.T) {
	expected := map[string][]byte{
		"numpy_import_small_gap0_c1":           []byte("8192"),
		"numpy_elementwise_small_gap0_c1":      []byte("12288.5"),
		"numpy_elementwise_medium_gap10000_c1": []byte("196608.5"),
		"numpy_elementwise_large_gap45000_c1":  []byte("1572864.5"),
		"numpy_reduction_small_gap0_c1":        []byte("549755289600"),
		"numpy_reduction_small_gap10000_c2":    []byte("549755289600"),
		"numpy_matrix_medium_gap0_c1":          []byte("5559680.0"),
		"numpy_matrix_medium_gap10000_c2":      []byte("5559680.0"),
		"numpy_matrix_medium_gap45000_c4":      []byte("5559680.0"),
		"numpy_elementwise_large_gap0_c4":      []byte("1572864.5"),
	}
	for _, candidate := range numpyreuse.Cases() {
		if !candidate.EconomicsEligible {
			continue
		}
		spec, err := executionSpec(candidate)
		if err != nil || !bytes.Equal(spec.Expected, expected[candidate.ID]) {
			t.Fatalf("%s oracle mismatch: %q %v", candidate.ID, spec.Expected, err)
		}
	}
}
