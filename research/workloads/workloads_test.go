package workloads_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workloads"
)

func TestCanonicalDescriptorFixtureIsFrozen(t *testing.T) {
	produced, err := workloads.CanonicalDescriptorJSON()
	if err != nil {
		t.Fatal(err)
	}
	checked, err := os.ReadFile("testdata/descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(produced, checked) {
		t.Fatal("canonical descriptor fixture drift")
	}
}

func TestPublicDescriptorsContainOnlyDigestsAndBoundedMetadata(t *testing.T) {
	all, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, workload := range all {
		descriptor, err := workload.PublicDescriptor()
		if err != nil {
			t.Fatalf("%s: %v", workload.ID, err)
		}
		raw, err := json.Marshal(descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Identity == "" || seen[descriptor.Identity] {
			t.Fatalf("identity=%q", descriptor.Identity)
		}
		if err := descriptor.Validate(); err != nil {
			t.Fatalf("%s descriptor validation: %v", workload.ID, err)
		}
		tampered := descriptor
		tampered.ExpectedCapabilityCalls++
		if err := tampered.Validate(); !errors.Is(err, workloads.ErrOracle) {
			t.Fatalf("%s descriptor tamper=%v", workload.ID, err)
		}
		seen[descriptor.Identity] = true
		for _, body := range [][]byte{[]byte(workload.Code), workload.Inputs, workload.ExpectedResult} {
			if len(body) > 2 && bytes.Contains(raw, body) {
				t.Fatalf("%s descriptor contains body", workload.ID)
			}
		}
		for _, body := range workload.SeedFiles {
			if bytes.Contains(raw, body) {
				t.Fatalf("%s descriptor contains seed body", workload.ID)
			}
		}
		lower := bytes.ToLower(raw)
		for _, forbidden := range [][]byte{[]byte("credential"), []byte("password"), []byte("endpoint"), []byte("/users/"), []byte("http://")} {
			if bytes.Contains(lower, forbidden) {
				t.Fatalf("%s descriptor leak: %s", workload.ID, forbidden)
			}
		}
	}
}

func TestCanonicalWorkloadsAreStableAndOracleBound(t *testing.T) {
	all, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("workloads=%d", len(all))
	}
	want := []string{"structured-source-v1", "stateful-local-v1", "bounded-planning-v1"}
	wantTreatments := [][]string{
		{"supported", "supported", "supported", "unsupported"},
		{"supported", "supported", "unsupported", "unsupported"},
		{"supported", "supported", "supported", "supported"},
	}
	for i, workload := range all {
		if workload.ID != want[i] || workload.CodeSHA256 == "" || workload.InputSHA256 == "" || workload.ExpectedResultSHA256 == "" || workload.ExpectedCapabilityCalls > 2 {
			t.Fatalf("workload=%+v", workload)
		}
		for treatmentIndex, treatment := range workload.Treatments {
			if treatment.Status != wantTreatments[i][treatmentIndex] {
				t.Fatalf("%s treatment %s=%s", workload.ID, treatment.Treatment, treatment.Status)
			}
		}
		if err := workload.Validate(); err != nil {
			t.Fatalf("%s: %v", workload.ID, err)
		}
	}
	if all[1].WorkspaceSeedSHA256 == "" || len(all[1].ExpectedWorkspace) == 0 {
		t.Fatal("stateful workload omitted workspace identities")
	}
	tamperedSeed := all[1]
	tamperedSeed.SeedFiles = map[string][]byte{"metrics.csv": []byte("id,value\nalpha,8\nbeta,2\n")}
	if err := tamperedSeed.Validate(); !errors.Is(err, workloads.ErrOracle) {
		t.Fatalf("seed tamper=%v", err)
	}
	tamperedPath := all[1]
	tamperedPath.ExpectedWorkspace = append([]workloads.WorkspaceEntry(nil), all[1].ExpectedWorkspace...)
	tamperedPath.ExpectedWorkspace[0].Path = "C:/Users/seed.csv"
	if err := tamperedPath.Validate(); !errors.Is(err, workloads.ErrOracle) {
		t.Fatalf("path tamper=%v", err)
	}
	tamperedTreatment := all[1]
	tamperedTreatment.Treatments = append([]workloads.TreatmentDisposition(nil), all[1].Treatments...)
	tamperedTreatment.Treatments[2] = workloads.TreatmentDisposition{Treatment: "counterfactual_branch", Status: "supported"}
	if err := tamperedTreatment.Validate(); !errors.Is(err, workloads.ErrOracle) {
		t.Fatalf("treatment tamper=%v", err)
	}
	if len(all[2].ExpectedWorkspace) != 0 {
		t.Fatal("planning workload unexpectedly claims workspace")
	}
}

func TestOracleRejectsResultWorkspaceAndCallTamper(t *testing.T) {
	all, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range all {
		result := append(json.RawMessage(nil), workload.ExpectedResult...)
		entries := append([]workloads.WorkspaceEntry(nil), workload.ExpectedWorkspace...)
		if err := workload.Verify(result, entries, workload.ExpectedCapabilityCalls); err != nil {
			t.Fatalf("%s positive: %v", workload.ID, err)
		}
		if err := workload.Verify(json.RawMessage(`{"tampered":true}`), entries, workload.ExpectedCapabilityCalls); !errors.Is(err, workloads.ErrOracle) {
			t.Fatalf("%s result tamper=%v", workload.ID, err)
		}
		if err := workload.Verify(result, entries, workload.ExpectedCapabilityCalls+1); !errors.Is(err, workloads.ErrOracle) {
			t.Fatalf("%s call tamper=%v", workload.ID, err)
		}
		if len(entries) != 0 {
			entries[0].SHA256 = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
			if err := workload.Verify(result, entries, workload.ExpectedCapabilityCalls); !errors.Is(err, workloads.ErrOracle) {
				t.Fatalf("%s workspace tamper=%v", workload.ID, err)
			}
		}
	}
}

func TestProgramsContainNoNetworkShellOrDynamicAuthority(t *testing.T) {
	all, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range all {
		if err := workloads.ValidateProgramBoundary(workload.Code); err != nil {
			t.Fatalf("%s: %v", workload.ID, err)
		}
	}
}
