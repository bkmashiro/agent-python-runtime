package workflowbench

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func TestExecutePairedTreatmentsPreservesOutputsAndReducesQualifiedWork(t *testing.T) {
	manifest, err := GenerateManifest(20260815, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := ExecutePair(context.Background(), manifest, func(ctx context.Context, task Task) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Millisecond):
			return testDigest(task.TaskID), nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Validate() != nil || evidence.Divergences != 0 || len(evidence.Tasks) != len(manifest.Tasks) || len(evidence.Reports) != len(manifest.Tasks) {
		t.Fatalf("evidence=%+v", evidence)
	}
	byClass := map[string]TaskMetrics{}
	negativeCount := 0
	orders := map[string]int{}
	for _, metrics := range evidence.Tasks {
		orders[metrics.TreatmentOrder]++
		if metrics.OutputEquivalent != true || metrics.EffectsEquivalent != true || metrics.EvidenceComplete != true {
			t.Fatalf("incomplete task metrics=%+v", metrics)
		}
		if metrics.Class == "near_match" {
			negativeCount++
			if metrics.OptimizedPhysicalExecutions != metrics.BaselinePhysicalExecutions || metrics.RejectedDecisions != 1 {
				t.Fatalf("near match optimized unexpectedly: %+v", metrics)
			}
		} else {
			byClass[metrics.Class] = metrics
		}
	}
	if orders["baseline_optimized"] != 7 || orders["optimized_baseline"] != 7 || evidence.CPUAccounting != "process_user_plus_system_delta" {
		t.Fatalf("unbalanced order or missing CPU accounting: orders=%v accounting=%q", orders, evidence.CPUAccounting)
	}
	if negativeCount != 8 {
		t.Fatalf("negative count=%d", negativeCount)
	}
	for _, class := range []string{"preissue", "declared_parallel", "coalesced", "retained_reuse"} {
		metrics := byClass[class]
		if metrics.AdmittedDecisions != 1 {
			t.Fatalf("class %s metrics=%+v", class, metrics)
		}
	}
	if byClass["coalesced"].OptimizedPhysicalExecutions >= byClass["coalesced"].BaselinePhysicalExecutions ||
		byClass["retained_reuse"].OptimizedPhysicalExecutions >= byClass["retained_reuse"].BaselinePhysicalExecutions {
		t.Fatalf("no physical reduction: %+v", byClass)
	}
	for _, raw := range evidence.Reports {
		verified, err := observe.DecodeOptimizationReport(raw)
		if err != nil {
			t.Fatal(err)
		}
		report, _ := verified.Report()
		if report.ConsumerAdmitted {
			t.Fatal("benchmark report gained execution authority")
		}
	}
	encoded, err := EncodeEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvidence(encoded)
	if err != nil || decoded.Validate() != nil {
		t.Fatalf("decode err=%v", err)
	}
}

func TestExecutePairFailsClosedOnObservableWASMOutputDivergence(t *testing.T) {
	manifest, _ := GenerateManifest(20260815, testIdentity())
	var calls atomic.Uint32
	_, err := ExecutePair(context.Background(), manifest, func(context.Context, Task) (string, error) {
		if calls.Add(1)%2 == 0 {
			return testDigest("optimized"), nil
		}
		return testDigest("baseline-longer"), nil
	})
	if err == nil {
		t.Fatal("WASM output divergence admitted")
	}
}

func TestExecutePairFailsClosedOnWASMOrManifestDrift(t *testing.T) {
	manifest, err := GenerateManifest(20260815, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecutePair(context.Background(), manifest, func(context.Context, Task) (string, error) {
		return "", context.Canceled
	}); err == nil {
		t.Fatal("WASM failure admitted")
	}
	manifest.Tasks[0].ExpectedOutputSHA256 = testDigest("mutated")
	if _, err := ExecutePair(context.Background(), manifest, func(context.Context, Task) (string, error) {
		return testDigest("ok"), nil
	}); err == nil {
		t.Fatal("mutated manifest admitted")
	}
}

func TestEvidenceMutationAndUnknownFieldsFailClosed(t *testing.T) {
	manifest, _ := GenerateManifest(20260815, testIdentity())
	evidence, err := ExecutePair(context.Background(), manifest, func(context.Context, Task) (string, error) {
		return testDigest("wasm"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence.Divergences++
	if evidence.Validate() == nil {
		t.Fatal("sealed mutation admitted")
	}
	valid, _ := ExecutePair(context.Background(), manifest, func(context.Context, Task) (string, error) {
		return testDigest("wasm"), nil
	})
	raw, _ := EncodeEvidence(valid)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	value["private_body"] = "secret"
	changed, _ := json.Marshal(value)
	if _, err := DecodeEvidence(changed); err == nil {
		t.Fatal("unknown body field admitted")
	}
}
