package workflowbench_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestTransparentCampaignDriverRecordsActualFIFOPhysicalFlow(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignBaseline, &campaignAdapter{})
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

	qualified, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignQualified, &campaignAdapter{shareExact: true})
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
	evidence, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignBaseline, &campaignAdapter{})
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
	once       sync.Once
	shared     workflowbench.CampaignOutcome
}

func (adapter *campaignAdapter) Admit(_ context.Context, program workflowbench.CampaignProgram, _ workflowbench.CampaignTreatment) workflowbench.CampaignAdmission {
	if program.Expected.Admission != "admit" {
		return workflowbench.CampaignAdmission{Allowed: false, Reason: program.Expected.Admission}
	}
	return workflowbench.CampaignAdmission{Allowed: true, Reason: "admitted"}
}

func (adapter *campaignAdapter) Execute(ctx context.Context, program workflowbench.CampaignProgram, treatment workflowbench.CampaignTreatment, runtime *workflowbench.CampaignRuntime) workflowbench.CampaignOutcome {
	if treatment == workflowbench.CampaignQualified && adapter.shareExact && (program.ID == "P05" || program.ID == "P06") {
		adapter.once.Do(func() {
			value, err := runtime.Physical(ctx, "physical-exact-P05-P06", func(context.Context) ([]byte, error) {
				time.Sleep(time.Millisecond)
				return append([]byte(nil), program.Expected.Oracle...), nil
			})
			adapter.shared = workflowbench.CampaignOutcome{Disposition: program.Expected.Disposition, Result: value, PhysicalExecutionID: "physical-exact-P05-P06", Sharing: "exact_shared", Err: err}
		})
		return adapter.shared
	}
	physicalID := "physical-" + program.ID
	value, err := runtime.Physical(ctx, physicalID, func(context.Context) ([]byte, error) {
		time.Sleep(time.Millisecond)
		return append([]byte(nil), program.Expected.Oracle...), nil
	})
	return workflowbench.CampaignOutcome{Disposition: program.Expected.Disposition, Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
}
