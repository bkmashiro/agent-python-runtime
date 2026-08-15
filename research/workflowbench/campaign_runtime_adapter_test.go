package workflowbench_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type campaignRuntimeFakeGuest struct {
	results map[string]json.RawMessage
}

func (guest *campaignRuntimeFakeGuest) Execute(_ context.Context, execution workflowbench.CampaignGuestExecution) (json.RawMessage, error) {
	return append(json.RawMessage(nil), guest.results[campaignRuntimeRequestKey(execution.Request)]...), nil
}

func TestRuntimeCampaignAdapterComposesTypedMechanismsWithoutPaperLabels(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	plans, err := workflowbench.CanonicalCampaignPlans()
	if err != nil {
		t.Fatal(err)
	}
	results := make(map[string]json.RawMessage)
	for _, program := range manifest.Programs {
		request := workflowbench.CampaignRequest{
			SourceSHA256: program.SourceSHA256, InputsSHA256: program.InputsSHA256, PlanSHA256: program.PlanSHA256,
			GrantSetSHA256: program.GrantSetSHA256, WorkspaceFixtureSHA256: program.WorkspaceFixtureSHA256, PrivacyPartition: program.PrivacyPartition,
		}
		results[campaignRuntimeRequestKey(request)] = append(json.RawMessage(nil), program.Expected.Oracle...)
	}
	workspaceBase := t.TempDir()
	if err := os.Chmod(workspaceBase, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(workspaceBase)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workflowbench.NewRuntimeCampaignAdapter(workflowbench.RuntimeCampaignAdapterConfig{
		Guest: &campaignRuntimeFakeGuest{results: results}, Plans: plans, WorkspaceManager: manager, BaseWorkspaceRef: base,
		ArtifactSHA256: testCampaignDigest('a'), ExecutionProfileSHA256: testCampaignDigest('b'), CacheDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := adapter.Close(context.Background()); err != nil {
			t.Errorf("close adapter: %v", err)
		}
	}()
	forged := manifest.Programs[17]
	forgedRequest := workflowbench.CampaignRequest{
		Source: forged.Source, SourceSHA256: forged.SourceSHA256, Inputs: forged.Inputs, InputsSHA256: forged.InputsSHA256,
		PlanSHA256: forged.PlanSHA256, GrantSetSHA256: forged.GrantSetSHA256, PrivacyPartition: forged.PrivacyPartition,
		WorkspaceFixtureSHA256: forged.WorkspaceFixtureSHA256, Execution: forged.Execution,
	}
	forgedRequest.Execution.Delegation = &workflowbench.CampaignDelegationContract{
		GroupID: forged.Execution.Delegation.GroupID, ParentPlanRole: forged.Execution.Delegation.ParentPlanRole,
		ParentPlanSHA256: forged.PlanSHA256, MaxDelegatedCalls: forged.Execution.Delegation.MaxDelegatedCalls, ChildReservedCalls: forged.Execution.Delegation.ChildReservedCalls,
	}
	if admission := adapter.Admit(context.Background(), forgedRequest, workflowbench.CampaignQualified); admission.Allowed || admission.Reason != "invalid_delegation_contract" {
		t.Fatalf("forged parent authority admitted: %+v", admission)
	}
	evidence, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignQualified, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowbench.ValidateCampaignEvidence(manifest, evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.Rows) != 20 || evidence.PhysicalExecutions == 0 {
		t.Fatalf("rows=%d physical=%d", len(evidence.Rows), evidence.PhysicalExecutions)
	}
	rows := make(map[string]workflowbench.CampaignRow, len(evidence.Rows))
	for _, row := range evidence.Rows {
		rows[row.ProgramID] = row
	}
	if rows["P05"].PhysicalExecutionID == "" || rows["P05"].PhysicalExecutionID != rows["P06"].PhysicalExecutionID {
		t.Fatalf("exact request was not shared: P05=%+v P06=%+v", rows["P05"], rows["P06"])
	}
	for _, rejected := range []string{"P16", "P18", "P19", "P20"} {
		if rows[rejected].PhysicalExecutionID != "" {
			t.Fatalf("rejected %s has physical execution: %+v", rejected, rows[rejected])
		}
	}
}

func campaignRuntimeRequestKey(request workflowbench.CampaignRequest) string {
	return request.SourceSHA256 + "\x00" + request.InputsSHA256 + "\x00" + request.PlanSHA256 + "\x00" + request.GrantSetSHA256 + "\x00" + request.WorkspaceFixtureSHA256 + "\x00" + request.PrivacyPartition
}

func testCampaignDigest(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}
