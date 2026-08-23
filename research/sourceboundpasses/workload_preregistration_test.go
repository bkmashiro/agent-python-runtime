package sourceboundpasses

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func TestAuthoredWorkloadPreregistrationIsFrozenBodyFreeAndCanonical(t *testing.T) {
	value := AuthoredWorkloadPreregistrationV1()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	wantCategories := []string{
		"repeated_repository_reads", "bounded_projection", "batch_reads",
		"independent_reads", "pure_parsing", "prepared_array_setup",
	}
	gotCategories := make([]string, len(value.Cases))
	for index, item := range value.Cases {
		gotCategories[index] = item.Category
	}
	if !reflect.DeepEqual(gotCategories, wantCategories) {
		t.Fatalf("categories=%v", gotCategories)
	}
	if !reflect.DeepEqual(value.Treatments, []string{"pass_off", "semantic_pre_dispatch_only", "prepared_pure_region_only", "all_admitted"}) {
		t.Fatalf("treatments=%v", value.Treatments)
	}
	raw, err := EncodeAuthoredWorkloadPreregistration(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("workspace.read_text"), []byte("source_body"), []byte("result_body")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("preregistration leaked body marker %q", forbidden)
		}
	}
	checked, err := os.ReadFile("../../docs/evidence/source-bound-pass-authored-workload-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(checked, append(raw, '\n')) {
		t.Fatal("checked workload preregistration drifted from canonical generator")
	}
}
