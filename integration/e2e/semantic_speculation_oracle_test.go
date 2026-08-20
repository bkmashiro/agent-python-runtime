package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestSemanticPreDispatchRecordsPhysicalOrphanForLaterSyntaxError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(wasm)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("semantic-speculation-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Int32
	plan := streamingPreDispatchPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	analyzerConfig := runtimeconfig.DefaultRunConfig()
	analyzerConfig.ExecutionProfile = &profile
	analyzerConfig.Mechanisms.SemanticAnalysis = true
	analyzer, err := wazeroengine.New(ctx, wasm, analyzerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close(context.Background())
	budget, err := semantic.NewPreDispatchBudget(1)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := semantic.NewStreamingSemanticPreDispatch(plan, budget, e2eGoroutineLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := semantic.NewStreamingPrefixAdmission(plan, controller, semantic.PreissueContext{
		StreamEpoch: "stream-oracle-1", WorkflowEpoch: "workflow-oracle-1", FreshnessEpoch: "plan-epoch-1", ExpiryEpoch: "expiry-oracle-1",
		PrivacyPartition: "private-oracle-1", ParentLineageSHA256: testDigest("oracle-parent"),
	})
	if err != nil {
		t.Fatal(err)
	}
	properties := analyzer.Properties()
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: properties.ExecutionProfileBindingSHA256,
		ImportClosureSHA256: testDigest("oracle-imports"), CapabilityPlanSHA256: plan.Identity(),
	}
	chunks := make(chan string)
	go func() {
		defer close(chunks)
		select {
		case chunks <- "value = sources.read(\"weather\")\n":
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for physical.Load() == 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
		select {
		case chunks <- "if True print(\"bad\")\n":
		case <-ctx.Done():
		}
	}()
	if _, err := semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
		Analyzer: analyzer, Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: chunks,
	}); err == nil {
		t.Fatal("invalid final source was accepted")
	}
	snapshot := controller.Snapshot()
	if physical.Load() != 1 || snapshot.PhysicalIssues != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 ||
		snapshot.LogicalClaims != 0 || snapshot.Consumed != 0 || snapshot.Cancelled != 1 || snapshot.Orphaned != 0 {
		t.Fatalf("physical=%d snapshot=%+v", physical.Load(), snapshot)
	}
}

func TestWholeFileOracleSeparatesParseReachabilityAndRuntimeFailure(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, code, status, errorCode, errorType string
		calls                                    int32
	}{
		{
			name:   "later syntax error executes nothing",
			code:   "value = sources.read(\"weather\")\nif True print(\"bad\")\n",
			status: "error", errorCode: "source_invalid", calls: 0,
		},
		{
			name:   "later runtime error follows reached call",
			code:   "value = sources.read(\"weather\")\nraise RuntimeError(\"after\")\n",
			status: "error", errorCode: "python_exception", errorType: "RuntimeError", calls: 1,
		},
		{
			name:   "earlier exception prevents later call",
			code:   "raise RuntimeError(\"before\")\nvalue = sources.read(\"weather\")\n",
			status: "error", errorCode: "python_exception", errorType: "RuntimeError", calls: 0,
		},
		{
			name:   "untaken branch prevents call",
			code:   "if False:\n    value = sources.read(\"weather\")\nresult = {\"ok\": True}\n",
			status: "ok", calls: 0,
		},
		{
			name:   "valid trailing code reaches one call",
			code:   "value = sources.read(\"weather\")\nresult = {\"value\": value[\"value\"], \"tail\": 7}\n",
			status: "ok", calls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var physical atomic.Int32
			plan := streamingPreDispatchPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
				physical.Add(1)
				return json.RawMessage(`{"value":"weather"}`), nil
			}))
			var broker *capability.Broker
			runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
				created, createErr := capability.NewBroker(capability.Config{RunIdentity: "whole-file-oracle", Plan: plan})
				broker = created
				return created, createErr
			}}).New(ctx, wasm, runtimeconfig.DefaultRunConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close(context.Background())
			request, err := json.Marshal(map[string]any{"run_id": "whole-file-oracle", "code": test.code, "inputs": map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			payload, runErr := runner.Run(ctx, request, plan.PythonPrelude())
			if test.errorCode == "source_invalid" {
				if !errors.Is(runErr, runtimeconfig.ErrAgentSourceInvalid) || len(payload) != 0 {
					t.Fatalf("source validation payload=%s error=%v", payload, runErr)
				}
				logical := uint32(0)
				if broker != nil {
					logical = broker.CallCount()
				}
				if physical.Load() != 0 || logical != 0 {
					t.Fatalf("invalid source physical=%d logical=%d", physical.Load(), logical)
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			var response struct {
				Status string `json:"status"`
				Error  *struct {
					Code string `json:"code"`
					Type string `json:"error_type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(payload, &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != test.status {
				t.Fatalf("status=%q payload=%s", response.Status, payload)
			}
			if test.errorCode == "" {
				if response.Error != nil {
					t.Fatalf("unexpected error=%+v", response.Error)
				}
			} else if response.Error == nil || response.Error.Code != test.errorCode || response.Error.Type != test.errorType {
				t.Fatalf("error=%+v payload=%s", response.Error, payload)
			}
			if physical.Load() != test.calls || broker == nil || broker.CallCount() != uint32(test.calls) {
				t.Fatalf("physical=%d logical=%d want=%d", physical.Load(), broker.CallCount(), test.calls)
			}
		})
	}
}
