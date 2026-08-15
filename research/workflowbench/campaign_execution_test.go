package workflowbench_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestTransparentCampaignDriverRecordsActualFIFOPhysicalFlow(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignBaseline, newCampaignAdapter(manifest, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowbench.ValidateCampaignEvidence(manifest, baseline); err != nil {
		t.Fatal(err)
	}
	if len(baseline.Rows) != 20 || baseline.PhysicalExecutions != 16 {
		t.Fatalf("rows=%d physical=%d", len(baseline.Rows), baseline.PhysicalExecutions)
	}
	var last int64
	for index, event := range baseline.Events {
		if event.Sequence != uint64(index+1) || event.AtNS < last {
			t.Fatalf("event %d=%+v last=%d", index, event, last)
		}
		last = event.AtNS
	}

	qualified, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignQualified, newCampaignAdapter(manifest, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowbench.ValidateCampaignEvidence(manifest, qualified); err != nil {
		t.Fatal(err)
	}
	if qualified.PhysicalExecutions != 15 {
		t.Fatalf("qualified physical=%d", qualified.PhysicalExecutions)
	}
	rows := map[string]workflowbench.CampaignRow{}
	for _, row := range qualified.Rows {
		rows[row.ProgramID] = row
	}
	if rows["P05"].PhysicalExecutionID == "" || rows["P05"].PhysicalExecutionID != rows["P06"].PhysicalExecutionID {
		t.Fatalf("exact pair rows P05=%+v P06=%+v", rows["P05"], rows["P06"])
	}
}

func TestCampaignTreatmentOrderIsBalanced(t *testing.T) {
	even := workflowbench.CampaignTreatmentOrder(0)
	odd := workflowbench.CampaignTreatmentOrder(1)
	if len(even) != 2 || len(odd) != 2 || even[0] != workflowbench.CampaignBaseline || odd[0] != workflowbench.CampaignQualified || even[0] != odd[1] || even[1] != odd[0] {
		t.Fatalf("even=%v odd=%v", even, odd)
	}
}

func TestCampaignEvidenceRejectsMissingAndForgedPhysicalEvents(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignBaseline, newCampaignAdapter(manifest, false))
	if err != nil {
		t.Fatal(err)
	}
	missing := evidence.Clone()
	missing.Rows = missing.Rows[:19]
	if err := workflowbench.ValidateCampaignEvidence(manifest, missing); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
		t.Fatalf("missing row error=%v", err)
	}
	forged := evidence.Clone()
	for index := range forged.Events {
		if forged.Events[index].Type == "physical.started" {
			forged.Events[index].AtNS = -1
			break
		}
	}
	if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
		t.Fatalf("forged event error=%v", err)
	}
	corrupt := evidence.Clone()
	corrupt.Rows[0].Result = json.RawMessage(`{"normalized":[9]}`)
	resealCampaignEvidence(&corrupt)
	if err := workflowbench.ValidateCampaignEvidence(manifest, corrupt); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
		t.Fatalf("corrupt result error=%v", err)
	}
	mismatched := evidence.Clone()
	mismatched.Rows[0].PhysicalExecutionID = "physical-forged"
	resealCampaignEvidence(&mismatched)
	if err := workflowbench.ValidateCampaignEvidence(manifest, mismatched); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
		t.Fatalf("mismatched physical identity error=%v", err)
	}
}

func resealCampaignEvidence(evidence *workflowbench.CampaignEvidence) {
	evidence.SealSHA256 = ""
	encoded, err := json.Marshal(evidence)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	evidence.SealSHA256 = "sha256:" + hex.EncodeToString(digest[:])
}

type campaignAdapter struct {
	shareExact bool
	counter    atomic.Uint64
	mu         sync.Mutex
	oracles    map[string]json.RawMessage
	keyCounts  map[string]int
	shared     map[string]*campaignShared
	reserved   map[string]uint32
}

type campaignShared struct {
	once    sync.Once
	outcome workflowbench.CampaignOutcome
}

func newCampaignAdapter(manifest workflowbench.CampaignManifest, shareExact bool) *campaignAdapter {
	adapter := &campaignAdapter{
		shareExact: shareExact, oracles: make(map[string]json.RawMessage), keyCounts: make(map[string]int),
		shared: make(map[string]*campaignShared), reserved: make(map[string]uint32),
	}
	for _, program := range manifest.Programs {
		key := campaignProgramKey(program.SourceSHA256, program.InputsSHA256, program.PlanSHA256, program.GrantSetSHA256, program.WorkspaceFixtureSHA256, program.PrivacyPartition)
		adapter.oracles[key] = append(json.RawMessage(nil), program.Expected.Oracle...)
		adapter.keyCounts[key]++
	}
	return adapter
}

func (adapter *campaignAdapter) Admit(_ context.Context, request workflowbench.CampaignRequest, _ workflowbench.CampaignTreatment) workflowbench.CampaignAdmission {
	switch request.Execution.Kind {
	case workflowbench.CampaignResumeWorkflow:
		if request.Execution.Resume.Transition == workflowbench.CampaignResumeExpired {
			return workflowbench.CampaignAdmission{Reason: "authority_expired", Disposition: "rejected"}
		}
	case workflowbench.CampaignDelegateChild:
		contract := request.Execution.Delegation
		if request.Execution.CancelPoint == workflowbench.CampaignCancelAfterParentTerminal {
			return workflowbench.CampaignAdmission{Reason: "parent_terminal", Disposition: "cancelled"}
		}
		if request.PlanSHA256 != contract.ParentPlanSHA256 {
			return workflowbench.CampaignAdmission{Reason: "authority_widening", Disposition: "rejected"}
		}
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		if adapter.reserved[contract.GroupID]+contract.ChildReservedCalls > contract.MaxDelegatedCalls {
			return workflowbench.CampaignAdmission{Reason: "delegation_budget", Disposition: "rejected"}
		}
		adapter.reserved[contract.GroupID] += contract.ChildReservedCalls
	}
	return workflowbench.CampaignAdmission{Allowed: true, Reason: "admitted"}
}

func (adapter *campaignAdapter) Execute(ctx context.Context, request workflowbench.CampaignRequest, treatment workflowbench.CampaignTreatment, runtime *workflowbench.CampaignRuntime) workflowbench.CampaignOutcome {
	key := campaignProgramKey(request.SourceSHA256, request.InputsSHA256, request.PlanSHA256, request.GrantSetSHA256, request.WorkspaceFixtureSHA256, request.PrivacyPartition)
	if request.Execution.Kind == workflowbench.CampaignConsumeResult {
		if len(request.DependencyResults[request.Execution.SourceProgramID]) == 0 {
			return workflowbench.CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: errors.New("producer result missing")}
		}
	}
	if treatment == workflowbench.CampaignQualified && adapter.shareExact && request.Execution.Kind == workflowbench.CampaignExactRequest {
		adapter.mu.Lock()
		entry := adapter.shared[key]
		if entry == nil {
			entry = &campaignShared{}
			adapter.shared[key] = entry
		}
		adapter.mu.Unlock()
		entry.once.Do(func() { entry.outcome = adapter.executePhysical(ctx, request, runtime, key) })
		outcome := entry.outcome
		if adapter.keyCounts[key] > 1 {
			outcome.Sharing = "exact_shared"
		}
		return outcome
	}
	return adapter.executePhysical(ctx, request, runtime, key)
}

func (adapter *campaignAdapter) executePhysical(ctx context.Context, request workflowbench.CampaignRequest, runtime *workflowbench.CampaignRuntime, key string) workflowbench.CampaignOutcome {
	physicalID := fmt.Sprintf("physical-%d", adapter.counter.Add(1))
	value, err := runtime.Physical(ctx, physicalID, func(context.Context) ([]byte, error) {
		time.Sleep(time.Millisecond)
		return append([]byte(nil), adapter.oracles[key]...), nil
	})
	disposition := "complete"
	if request.Execution.CancelPoint == workflowbench.CampaignCancelAfterWorkspaceFork {
		disposition = "cancelled"
	}
	return workflowbench.CampaignOutcome{Disposition: disposition, Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
}

func campaignProgramKey(source, inputs, plan, grants, workspace, privacy string) string {
	return source + "\x00" + inputs + "\x00" + plan + "\x00" + grants + "\x00" + workspace + "\x00" + privacy
}
