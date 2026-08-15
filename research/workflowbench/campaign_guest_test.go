package workflowbench_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestCampaignGuestExecutorRejectsUnboundRequestIdentityBeforeGuestCreation(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	plans, err := workflowbench.CanonicalCampaignPlans()
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workflowbench.NewCampaignGuestExecutor(workflowbench.CampaignGuestExecutorConfig{Artifact: []byte("not-reached"), Plans: plans})
	if err != nil {
		t.Fatal(err)
	}
	program := manifest.Programs[0]
	base := workflowbench.CampaignGuestExecution{
		ExecutionID: "identity-check",
		Request: workflowbench.CampaignRequest{
			Source: program.Source, SourceSHA256: program.SourceSHA256,
			Inputs: append(json.RawMessage(nil), program.Inputs...), InputsSHA256: program.InputsSHA256,
			PlanSHA256: program.PlanSHA256, GrantSetSHA256: program.GrantSetSHA256,
			PrivacyPartition: program.PrivacyPartition, WorkspaceFixtureSHA256: program.WorkspaceFixtureSHA256,
			Execution: program.Execution,
		},
	}
	for name, mutate := range map[string]func(*workflowbench.CampaignGuestExecution){
		"source": func(value *workflowbench.CampaignGuestExecution) { value.Request.Source += "\n# tampered" },
		"inputs": func(value *workflowbench.CampaignGuestExecution) {
			value.Request.Inputs = json.RawMessage(`{"values":[9]}`)
		},
		"plan": func(value *workflowbench.CampaignGuestExecution) {
			value.Request.PlanSHA256 = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		},
		"grants": func(value *workflowbench.CampaignGuestExecution) {
			value.Request.GrantSetSHA256 = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Request.Inputs = append(json.RawMessage(nil), base.Request.Inputs...)
			mutate(&candidate)
			if _, err := executor.Execute(context.Background(), candidate); !errors.Is(err, workflowbench.ErrInvalidCampaignGuestExecution) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
