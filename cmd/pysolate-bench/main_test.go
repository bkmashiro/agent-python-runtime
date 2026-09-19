package main

import (
	"strings"
	"testing"
)

func TestProgramOracle(t *testing.T) {
	for _, c := range []struct {
		n        int
		expected string
	}{{0, "0"}, {2, "1"}, {8, "28"}} {
		source, want, err := program("tools", c.n)
		if err != nil || want != c.expected || strings.Count(source, " = read(") != c.n {
			t.Fatalf("source=%s expected=%s err=%v", source, want, err)
		}
	}
	if _, _, err := program("unknown", 1); err == nil {
		t.Fatal("unknown workload accepted")
	}
}
