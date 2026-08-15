package e2e_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestCampaignGuestExecutorRunsCanonicalProgramInRealGuest(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	plans, err := workflowbench.CanonicalCampaignPlans()
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workflowbench.NewCampaignGuestExecutor(workflowbench.CampaignGuestExecutorConfig{
		Artifact: artifact,
		Plans:    plans,
	})
	if err != nil {
		t.Fatal(err)
	}
	program := manifest.Programs[0]
	result, err := executor.Execute(context.Background(), workflowbench.CampaignGuestExecution{
		ExecutionID: "campaign-direct-1",
		Request: workflowbench.CampaignRequest{
			Source: program.Source, SourceSHA256: program.SourceSHA256,
			Inputs: program.Inputs, InputsSHA256: program.InputsSHA256,
			PlanSHA256: program.PlanSHA256, GrantSetSHA256: program.GrantSetSHA256,
			PrivacyPartition:       program.PrivacyPartition,
			WorkspaceFixtureSHA256: program.WorkspaceFixtureSHA256,
			Execution:              program.Execution,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, program.Expected.Oracle) {
		t.Fatalf("result=%s expected=%s", result, program.Expected.Oracle)
	}
}
