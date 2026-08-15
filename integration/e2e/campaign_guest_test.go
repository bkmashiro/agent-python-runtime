package e2e_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
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

func TestRuntimeCampaignAdapterRunsCanonicalCampaignInRealGuest(t *testing.T) {
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
	executor, err := workflowbench.NewCampaignGuestExecutor(workflowbench.CampaignGuestExecutorConfig{Artifact: artifact, Plans: plans})
	if err != nil {
		t.Fatal(err)
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
		Guest: executor, Plans: plans, WorkspaceManager: manager, BaseWorkspaceRef: base,
		ArtifactSHA256: executor.ArtifactSHA256(), ExecutionProfileSHA256: executor.ExecutionProfileSHA256(), CacheDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := adapter.Close(context.Background()); err != nil {
			t.Errorf("close adapter: %v", err)
		}
	}()
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
		t.Fatalf("real exact requests did not share: P05=%+v P06=%+v", rows["P05"], rows["P06"])
	}
	if rows["P10"].Sharing != "root_exact_shared" && rows["P11"].Sharing != "root_exact_shared" {
		t.Fatalf("equivalent workspace roots did not share verifier: P10=%+v P11=%+v", rows["P10"], rows["P11"])
	}
	t.Logf("qualified real campaign physical=%d wall_ns=%d cpu_ns=%d", evidence.PhysicalExecutions, evidence.WallNS, evidence.ProcessCPUNS)
}
