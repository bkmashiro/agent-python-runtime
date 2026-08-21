package main

import (
	"strings"
	"testing"
)

func TestMonkeypatchSourceRechecksImmutableBodyBeforeClaim(t *testing.T) {
	source := monkeypatchSource(digestA, digestText("body"), []byte{1, 2, 3})
	for _, required := range []string{
		"_pd_body = bytes(",
		"def _pd_load(",
		"_hashlib.sha256(_body).hexdigest()",
		"_host.materialize_value(",
		"np.frombuffer(_body",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("missing %q in generated source", required)
		}
	}
	if strings.Contains(source, "bytearray(") {
		t.Fatal("prepared body remained mutable")
	}
	digestCheck := strings.Index(source, "_hashlib.sha256(_body).hexdigest()")
	claim := strings.Index(source, "_host.materialize_value(")
	if digestCheck < 0 || claim < 0 || digestCheck >= claim {
		t.Fatalf("body digest must be checked before Host claim: digest=%d claim=%d", digestCheck, claim)
	}
}
