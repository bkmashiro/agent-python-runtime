package main

import "testing"

func TestBenchmarkCasesAreFixedAndCoverExpectedShapes(t *testing.T) {
	cases := benchmarkCases()
	if len(cases) != 5 {
		t.Fatalf("case count=%d", len(cases))
	}
	seen := map[string]bool{}
	for _, spec := range cases {
		if spec.name == "" || spec.code == "" || len(spec.inputs) == 0 {
			t.Fatalf("incomplete case: %+v", spec)
		}
		if seen[spec.name] {
			t.Fatalf("duplicate case %q", spec.name)
		}
		seen[spec.name] = true
	}
	if !seen["read-finish"] || !seen["tool-chain"] || !seen["park-readmit"] || !seen["numpy-local"] || !seen["read-numpy"] {
		t.Fatalf("unexpected cases: %v", seen)
	}
	if !cases[2].park {
		t.Fatal("park-readmit must exercise re-admission")
	}
}

func TestSelectCasesAndPreparation(t *testing.T) {
	selected, err := selectCases("tool-chain")
	if err != nil || len(selected) != 1 || selected[0].name != "tool-chain" {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	if _, err := selectCases("unknown"); err == nil {
		t.Fatal("unknown case accepted")
	}
	fresh, err := parsePreparation("fresh")
	if err != nil || fresh != nil {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	copyMode, err := parsePreparation("copy")
	if err != nil || copyMode == nil || copyMode.Seed != benchmarkSeed || copyMode.COW {
		t.Fatalf("copy=%+v err=%v", copyMode, err)
	}
	cow, err := parsePreparation("cow")
	if err != nil || cow == nil || !cow.COW {
		t.Fatalf("cow=%+v err=%v", cow, err)
	}
	if _, err := parsePreparation("other"); err == nil {
		t.Fatal("unknown preparation accepted")
	}
}
