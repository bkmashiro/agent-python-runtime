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

func TestCampaignEvidenceStrictDecodeRejectsDuplicateKeys(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := append([]byte(`{"schema_version":"duplicate",`), encoded[1:]...)
	if _, err := workflowbench.DecodeCampaignManifest(duplicate); err == nil {
		t.Fatal("duplicate manifest key accepted")
	}
}

func TestCampaignEvidenceRejectsMechanismEventTamperingAfterReseal(t *testing.T) {
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workflowbench.RunTransparentCampaign(context.Background(), manifest, workflowbench.CampaignQualified, newCampaignAdapter(manifest, true))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		programID string
		eventType string
		mutate    func(*workflowbench.CampaignEvent)
	}{
		{"unknown type", "P04", "workspace.discarded", func(event *workflowbench.CampaignEvent) { event.Type = "workspace.untrusted" }},
		{"missing and duplicate", "P04", "workspace.discarded", func(event *workflowbench.CampaignEvent) { event.Type = "workspace.forked" }},
		{"forged physical identity", "P05", "sharing.decided", func(event *workflowbench.CampaignEvent) { event.PhysicalExecutionID = "physical-forged" }},
		{"wrong authority identity", "P15", "authority.refreshed", func(event *workflowbench.CampaignEvent) { event.Reason = testCampaignDigest('f') }},
		{"duplicate core event", "P01", "logical.terminal", func(event *workflowbench.CampaignEvent) { event.Type = "logical.released" }},
		{"cross-program physical identity", "P01", "physical.ended", func(event *workflowbench.CampaignEvent) { event.ProgramID = "P02" }},
		{"body-bearing core reason", "P01", "physical.started", func(event *workflowbench.CampaignEvent) { event.Reason = "private body" }},
		{"verifier root mismatch", "P10", "verification.completed", func(event *workflowbench.CampaignEvent) {
			event.Reason = testCampaignDigest('e') + ":" + testCampaignDigest('d')
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			forged := evidence.Clone()
			found := false
			for index := range forged.Events {
				if forged.Events[index].ProgramID == test.programID && forged.Events[index].Type == test.eventType {
					test.mutate(&forged.Events[index])
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("missing fixture event %s/%s", test.programID, test.eventType)
			}
			resealCampaignEvidence(&forged)
			if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
				t.Fatalf("tampered mechanism evidence accepted: %v", err)
			}
		})
	}
	t.Run("foreign owner without typed share", func(t *testing.T) {
		forged := evidence.Clone()
		forged.Rows[1].PhysicalExecutionID = forged.Rows[0].PhysicalExecutionID
		for index := range forged.Events {
			if forged.Events[index].ProgramID == "P02" && forged.Events[index].Type == "logical.terminal" {
				forged.Events[index].PhysicalExecutionID = forged.Rows[0].PhysicalExecutionID
			}
		}
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("foreign owner accepted: %v", err)
		}
	})
	t.Run("rejected program physical lifecycle", func(t *testing.T) {
		forged := evidence.Clone()
		last := forged.Events[len(forged.Events)-1]
		forged.Events = append(forged.Events,
			workflowbench.CampaignEvent{Sequence: last.Sequence + 1, AtNS: last.AtNS, ProgramID: "P16", Type: "physical.queued", Reason: "fifo", PhysicalExecutionID: "physical-rejected"},
			workflowbench.CampaignEvent{Sequence: last.Sequence + 2, AtNS: last.AtNS, ProgramID: "P16", Type: "physical.cancelled", Reason: context.Canceled.Error(), PhysicalExecutionID: "physical-rejected"},
		)
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("rejected lifecycle accepted: %v", err)
		}
	})
	t.Run("physical lifecycle order inverted", func(t *testing.T) {
		forged := evidence.Clone()
		for index := range forged.Events {
			if forged.Events[index].ProgramID != "P01" {
				continue
			}
			switch forged.Events[index].Type {
			case "physical.started":
				forged.Events[index].Type = "physical.ended"
			case "physical.ended":
				forged.Events[index].Type = "physical.started"
			}
		}
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("inverted lifecycle accepted: %v", err)
		}
	})
	t.Run("body-bearing admission reason", func(t *testing.T) {
		forged := evidence.Clone()
		forged.Rows[0].AdmissionReason = "private body"
		for index := range forged.Events {
			if forged.Events[index].ProgramID == "P01" && forged.Events[index].Type == "admission.accepted" {
				forged.Events[index].Reason = "private body"
			}
		}
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("body-bearing admission accepted: %v", err)
		}
	})
	t.Run("near-match exact identity", func(t *testing.T) {
		forged := evidence.Clone()
		foreign := forged.Rows[6].PhysicalExecutionID
		forged.Rows[4].PhysicalExecutionID = foreign
		for index := range forged.Events {
			if forged.Events[index].ProgramID == "P05" && (forged.Events[index].Type == "sharing.decided" || forged.Events[index].Type == "logical.terminal") {
				forged.Events[index].PhysicalExecutionID = foreign
			}
		}
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("near-match sharing accepted: %v", err)
		}
	})
	t.Run("physical before admission", func(t *testing.T) {
		forged := evidence.Clone()
		accepted, queued := -1, -1
		for index, event := range forged.Events {
			if event.ProgramID != "P01" {
				continue
			}
			if event.Type == "admission.accepted" {
				accepted = index
			}
			if event.Type == "physical.queued" {
				queued = index
			}
		}
		if accepted < 0 || queued < 0 {
			t.Fatal("missing temporal fixtures")
		}
		forged.Events[accepted].Type, forged.Events[queued].Type = forged.Events[queued].Type, forged.Events[accepted].Type
		forged.Events[accepted].Reason, forged.Events[queued].Reason = forged.Events[queued].Reason, forged.Events[accepted].Reason
		forged.Events[accepted].PhysicalExecutionID, forged.Events[queued].PhysicalExecutionID = forged.Events[queued].PhysicalExecutionID, forged.Events[accepted].PhysicalExecutionID
		resealCampaignEvidence(&forged)
		if err := workflowbench.ValidateCampaignEvidence(manifest, forged); !errors.Is(err, workflowbench.ErrInvalidCampaignEvidence) {
			t.Fatalf("pre-admission physical event accepted: %v", err)
		}
	})
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
	if request.Execution.CancelPoint == workflowbench.CampaignCancelAfterWorkspaceFork {
		_ = runtime.Event("workspace.forked", "private_attempt", "")
		_ = runtime.Event("workspace.discarded", string(request.Execution.CancelPoint), "")
	}
	if request.Execution.Kind == workflowbench.CampaignVerifyWorkspace {
		_ = runtime.Event("workspace.forked", "private_attempt", "")
		_ = runtime.Event("workspace.sealed", request.WorkspaceFixtureSHA256, "")
	}
	if request.Execution.Kind == workflowbench.CampaignStartWorkflow {
		_ = runtime.Event("workflow.waiting", request.Execution.WorkflowStateKey, "")
	}
	if request.Execution.Kind == workflowbench.CampaignResumeWorkflow {
		_ = runtime.Event("workflow.resumed", string(request.Execution.Resume.Transition), "")
		if request.Execution.Resume.Transition == workflowbench.CampaignResumePlanGrantChanged {
			_ = runtime.Event("authority.refreshed", request.PlanSHA256, "")
		}
	}
	if request.Execution.Kind == workflowbench.CampaignDelegateChild {
		_ = runtime.Event("delegation.child_started", request.Execution.Delegation.GroupID, "")
	}
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
		_ = runtime.Event("sharing.decided", outcome.Sharing, outcome.PhysicalExecutionID)
		return outcome
	}
	outcome := adapter.executePhysical(ctx, request, runtime, key)
	if request.Execution.Kind == workflowbench.CampaignExactRequest {
		_ = runtime.Event("sharing.decided", outcome.Sharing, outcome.PhysicalExecutionID)
	}
	if request.Execution.Kind == workflowbench.CampaignVerifyWorkspace {
		verifierJSON, err := json.Marshal(request.Execution.Verifier)
		if err != nil {
			return workflowbench.CampaignOutcome{Disposition: "failed", Sharing: outcome.Sharing, Err: err}
		}
		verifierDigest := sha256.Sum256(verifierJSON)
		verifierSHA256 := "sha256:" + hex.EncodeToString(verifierDigest[:])
		_ = runtime.Event("verification.completed", request.WorkspaceFixtureSHA256+":"+verifierSHA256+":"+outcome.Sharing, outcome.PhysicalExecutionID)
	}
	return outcome
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
