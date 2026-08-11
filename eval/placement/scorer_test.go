package placement

import (
	"encoding/json"
	"testing"
)

func raw(value string) json.RawMessage { return json.RawMessage(value) }

func TestScoreExactSemanticCalls(t *testing.T) {
	task := Task{
		ID: "t1",
		Admission: map[string]ArmAdmission{
			"direct": {Status: "admitted", Reason: "test"},
		},
		Oracle: TaskOracle{
			FinalState: raw(`{"kind":"exact_files","files":{"out.txt":"ok"}}`),
			EffectContract: raw(`{
				"kind":"exact_semantic_calls",
				"required":[
					{"name":"workspace.read_text","arguments":{"path":"in.txt"}},
					{"name":"workspace.write_text","arguments":{"content":"ok","path":"out.txt"}}
				],
				"forbidden":["ambient_network"],
				"ordering_edges":[[0,1]]
			}`),
		},
	}
	result := TrialResult{
		SchemaVersion: "placement-trial-result/v1",
		TaskID:        "t1", Arm: "direct", Mode: "scripted", Replicate: 1,
		Admission:          ObservedAdmission{Status: "admitted", Reason: "catalog"},
		Execution:          ExecutionEvidence{Status: "completed"},
		ObservedFinalState: raw(`{"files":{"out.txt":"ok"},"kind":"exact_files"}`),
		ObservedEffects: []SemanticCall{
			{Name: "workspace.read_text", Arguments: raw(`{"path":"in.txt"}`)},
			{Name: "workspace.write_text", Arguments: raw(`{"path":"out.txt","content":"ok"}`)},
		},
	}
	score, err := Score(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if !score.Pass || !score.FinalStatePass || !score.EffectPass {
		t.Fatalf("unexpected score: %+v", score)
	}

	result.ObservedEffects = append(result.ObservedEffects, SemanticCall{Name: "workspace.write_text", Arguments: raw(`{"path":"extra"}`)})
	score, err = Score(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if score.Pass || score.EffectPass || score.FailureLayer != "oracle_effect" {
		t.Fatalf("extra effect must fail closed: %+v", score)
	}
}

func TestScoreBFCLAlternativeArgumentsAndFinalMismatch(t *testing.T) {
	task := Task{
		ID:        "bfcl",
		Admission: map[string]ArmAdmission{"direct": {Status: "admitted", Reason: "test"}},
		Oracle: TaskOracle{
			FinalState: raw(`{"kind":"unchanged","state":{}}`),
			EffectContract: raw(`{
				"kind":"bfcl_expected_calls",
				"oracle":{"kind":"expected_call_trace","turns":[
					{"lookup":{"id":["42"],"region":["eu","EU"]}}
				]}
			}`),
		},
	}
	result := TrialResult{
		SchemaVersion: "placement-trial-result/v1",
		TaskID:        "bfcl", Arm: "direct", Mode: "model", Replicate: 1,
		Admission:          ObservedAdmission{Status: "admitted", Reason: "catalog"},
		Execution:          ExecutionEvidence{Status: "completed"},
		ObservedFinalState: raw(`{"kind":"unchanged","state":{}}`),
		ObservedEffects:    []SemanticCall{{Name: "lookup", Arguments: raw(`{"id":"42","region":"EU"}`)}},
		Usage:              UsageEvidence{ProviderCalls: 1, TotalTokens: 12},
	}
	score, err := Score(task, result)
	if err != nil || !score.Pass {
		t.Fatalf("BFCL option should pass: score=%+v err=%v", score, err)
	}
	result.ObservedFinalState = raw(`{"kind":"unchanged","state":{"changed":true}}`)
	score, err = Score(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if score.Pass || score.FinalStatePass || score.FailureLayer != "oracle_final_state" {
		t.Fatalf("final-state mismatch must fail: %+v", score)
	}
}

func TestScoreExpectedAdmissionRejection(t *testing.T) {
	task := Task{
		ID: "boundary",
		Admission: map[string]ArmAdmission{
			"pysolate": {Status: "rejected", Reason: "ambient authority forbidden"},
		},
		Oracle: TaskOracle{
			FinalState:     raw(`{"kind":"unchanged","files":{}}`),
			EffectContract: raw(`{"kind":"admission_rejection","required_status":"rejected","forbidden_effects":["any"]}`),
		},
	}
	result := TrialResult{
		SchemaVersion: "placement-trial-result/v1",
		TaskID:        "boundary", Arm: "pysolate", Mode: "model", Replicate: 1,
		Admission: ObservedAdmission{Status: "rejected", Reason: "profile denied network", BeforeProvider: true},
		Execution: ExecutionEvidence{Status: "not_started"},
	}
	score, err := Score(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if !score.Pass || !score.ExpectedRejection || result.Usage.ProviderCalls != 0 {
		t.Fatalf("expected pre-provider rejection should pass: %+v", score)
	}

	result.Admission.BeforeProvider = false
	score, err = Score(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if score.Pass || score.FailureLayer != "admission" {
		t.Fatalf("late rejection must fail: %+v", score)
	}
}

func TestValidateTrialResultIdentity(t *testing.T) {
	result := TrialResult{
		SchemaVersion: "placement-trial-result/v1",
		TrialID:       "trial-1", TaskID: "task-1", TaskSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Arm: "computer", Mode: "model", Replicate: 1,
		SourceCommit:          "0123456789012345678901234567890123456789",
		TreatmentSHA256:       "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RuntimeIdentitySHA256: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Admission:             ObservedAdmission{Status: "admitted", Reason: "backend"},
		Execution:             ExecutionEvidence{Status: "completed"},
		ObservedFinalState:    raw(`{}`),
		Usage:                 UsageEvidence{ProviderCalls: 1, TotalTokens: 1},
		Lifecycle:             LifecycleEvidence{WallTimeMillis: 1, StartCount: 1},
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	result.RuntimeIdentitySHA256 = ""
	if err := ValidateTrialResult(result); err == nil {
		t.Fatal("missing runtime identity accepted")
	}
}
