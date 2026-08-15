package workflowbench_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strconv"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestTransparentCampaignManifestHasExactTwentyAuditablePrograms(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Programs) != 20 || manifest.PhysicalSlots != 3 {
		t.Fatalf("programs=%d slots=%d", len(manifest.Programs), manifest.PhysicalSlots)
	}
	families := map[string]int{}
	programs := map[string]workflowbench.CampaignProgram{}
	for index, program := range manifest.Programs {
		expectedID := "P" + []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12", "13", "14", "15", "16", "17", "18", "19", "20"}[index]
		if program.ID != expectedID || program.Source == "" || program.SourceSHA256 == "" || program.PlanSHA256 == "" || program.GrantSetSHA256 == "" || program.WorkspaceFixtureSHA256 == "" || len(program.Expected.Oracle) == 0 {
			t.Fatalf("program %d=%+v", index, program)
		}
		families[program.Family]++
		programs[program.ID] = program
	}
	wantFamilies := map[string]int{"authority_bifurcation": 4, "exact_sharing": 5, "root_verification": 3, "authority_resume": 4, "delegation_attenuation": 4}
	for family, count := range wantFamilies {
		if families[family] != count {
			t.Fatalf("family %s count=%d", family, families[family])
		}
	}

	p05, p06, p07, p08, p09 := programs["P05"], programs["P06"], programs["P07"], programs["P08"], programs["P09"]
	if p05.SourceSHA256 != p06.SourceSHA256 || p05.InputsSHA256 != p06.InputsSHA256 || p05.PlanSHA256 != p06.PlanSHA256 || p05.WorkspaceFixtureSHA256 != p06.WorkspaceFixtureSHA256 || p05.PrivacyPartition != p06.PrivacyPartition {
		t.Fatal("P05/P06 are not an exact sharing pair")
	}
	if p07.SourceSHA256 == p05.SourceSHA256 || p07.InputsSHA256 != p05.InputsSHA256 {
		t.Fatal("P07 is not the source-only near match")
	}
	if p08.SourceSHA256 != p05.SourceSHA256 || p08.InputsSHA256 == p05.InputsSHA256 {
		t.Fatal("P08 is not the input-only near match")
	}
	if p09.SourceSHA256 != p05.SourceSHA256 || p09.InputsSHA256 != p05.InputsSHA256 || p09.PrivacyPartition == p05.PrivacyPartition {
		t.Fatal("P09 is not the privacy-only near match")
	}
}

func TestTransparentCampaignSourcesCompileAsPython(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range manifest.Programs {
		program := program
		t.Run(program.ID, func(t *testing.T) {
			script := "compile(" + strconv.Quote(program.Source) + ", " + strconv.Quote(program.ID+".py") + ", 'exec')"
			if output, err := exec.Command("python3", "-c", script).CombinedOutput(); err != nil {
				t.Fatalf("source did not compile: %v: %s", err, output)
			}
		})
	}
}

func TestTransparentCampaignGenerationIsByteDeterministic(t *testing.T) {
	first, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("canonical campaign generation is not byte-deterministic")
	}
}

func TestTransparentCampaignUsesTypedResearchContractsNotExpectedLabels(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	programs := map[string]workflowbench.CampaignProgram{}
	for _, program := range manifest.Programs {
		programs[program.ID] = program
	}
	if programs["P01"].Execution.Kind != workflowbench.CampaignExecutePython || programs["P02"].Execution.Kind != workflowbench.CampaignConsumeResult || programs["P02"].Execution.SourceProgramID != "P01" {
		t.Fatal("producer/consumer operations are not typed")
	}
	if programs["P05"].Execution.Kind != workflowbench.CampaignExactRequest || programs["P06"].Execution.Kind != workflowbench.CampaignExactRequest {
		t.Fatal("exact-request operations are not typed")
	}
	if programs["P10"].Execution.Kind != workflowbench.CampaignVerifyWorkspace || programs["P10"].Execution.Verifier == nil {
		t.Fatal("workspace verification is not typed")
	}
	if programs["P13"].Execution.Kind != workflowbench.CampaignStartWorkflow || programs["P15"].Execution.Resume == nil || programs["P15"].Execution.Resume.Transition != workflowbench.CampaignResumePlanGrantChanged {
		t.Fatal("workflow operations are not typed")
	}
	if programs["P17"].Execution.Kind != workflowbench.CampaignDelegateChild || programs["P18"].Execution.Delegation == nil || programs["P18"].Execution.Delegation.ParentPlanRole != "consumer-left" {
		t.Fatal("delegation operations are not typed")
	}
	if programs["P04"].Execution.CancelPoint != workflowbench.CampaignCancelAfterWorkspaceFork || programs["P20"].Execution.CancelPoint != workflowbench.CampaignCancelAfterParentTerminal {
		t.Fatal("cancellation points are not typed")
	}
}

func TestCampaignAdapterRequestExcludesPaperLabelsAndExpectedOracle(t *testing.T) {
	typeOf := reflect.TypeOf(workflowbench.CampaignRequest{})
	for _, forbidden := range []string{"ID", "ProgramID", "Family", "Expected"} {
		if _, ok := typeOf.FieldByName(forbidden); ok {
			t.Fatalf("adapter request exposes forbidden field %s", forbidden)
		}
	}
}

func TestTransparentCampaignManifestRejectsIdentityAndRoleTampering(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*workflowbench.CampaignManifest)
	}{
		{"count", func(value *workflowbench.CampaignManifest) { value.Programs = value.Programs[:19] }},
		{"source digest", func(value *workflowbench.CampaignManifest) {
			value.Programs[0].SourceSHA256 = value.Programs[1].SourceSHA256
		}},
		{"release order", func(value *workflowbench.CampaignManifest) { value.Programs[1].ReleaseOffsetMS = -1 }},
		{"release range", func(value *workflowbench.CampaignManifest) { value.Programs[19].ReleaseOffsetMS = 60_001 }},
		{"dependency", func(value *workflowbench.CampaignManifest) { value.Programs[3].Dependencies = []string{"P20"} }},
		{"cannot prove", func(value *workflowbench.CampaignManifest) { value.Programs[0].CannotProve = nil }},
		{"unknown execution kind", func(value *workflowbench.CampaignManifest) { value.Programs[0].Execution.Kind = "paper_label" }},
		{"consumer without source", func(value *workflowbench.CampaignManifest) { value.Programs[1].Execution.SourceProgramID = "" }},
		{"resume from future", func(value *workflowbench.CampaignManifest) { value.Programs[13].Execution.Resume.FromProgramID = "P20" }},
		{"verifier missing identity", func(value *workflowbench.CampaignManifest) { value.Programs[9].Execution.Verifier.PolicySHA256 = "" }},
		{"delegation missing parent", func(value *workflowbench.CampaignManifest) {
			value.Programs[16].Execution.Delegation.ParentPlanRole = ""
		}},
		{"delegation missing parent identity", func(value *workflowbench.CampaignManifest) {
			value.Programs[16].Execution.Delegation.ParentPlanSHA256 = ""
		}},
		{"walkthrough", func(value *workflowbench.CampaignManifest) { value.WalkthroughProgramIDs[0] = "P99" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest.Clone()
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("tampered manifest accepted")
			}
		})
	}
}
