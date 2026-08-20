package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestExactGuestScheduledSerialWholeFileUsesFrozenPureLocalCase(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"value":"unexpected"}`), nil
	}))
	treatment, err := semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
		Artifact: artifact,
		RunConfig: func() runtimeconfig.RunConfig {
			config := runtimeconfig.DefaultRunConfig()
			config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
			return config
		}(),
		Plan: plan,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "serial-scheduled-pure-local", Plan: plan})
		},
		RunID:          "serial-scheduled-pure-local",
		WorkspaceRoot:  t.TempDir(),
		WorkspaceOwner: "serial-scheduled-pure-local",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), semanticspeculation.Phase3SyntheticCases()[5], treatment)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := playback.CanonicalSHA256(json.RawMessage(`8`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "success" || !result.Outcome.FinalPythonStarted ||
		result.Outcome.PrefixPythonExecutions != 0 || result.Outcome.ResultSHA256 != wantDigest ||
		result.Outcome.ErrorClass != "" || result.Outcome.LogicalCalls != 0 || calls.Load() != 0 ||
		result.Outcome.AuthorityDisposition != "unchanged" || result.Outcome.WorkspaceDisposition != "published" ||
		len(result.ReleaseDriftNanos) != 2 || result.FinalizeNanos == 0 || result.EndedNanos < result.FinalizeNanos {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestExactGuestScheduledSerialWholeFileDefersExternalReadUntilFinalSource(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	handler := capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		calls.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	})
	plan := eagerComparatorCapabilityPlan(t, handler)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	treatment, err := semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
		Artifact: artifact, RunConfig: config, Plan: plan,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "serial-external-read", Plan: plan})
		},
		ProviderObservation: func() semanticspeculation.ProviderObservation {
			return semanticspeculation.ProviderObservation{Attempts: calls.Load(), ResultBytes: uint64(len(`{"value":"weather"}`)), CostUnits: uint64(calls.Load()), Dispositions: semanticspeculation.PhysicalDispositions{Consumed: calls.Load()}}
		},
		RunID: "serial-external-read", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "serial-external-read",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), semanticspeculation.Phase3SyntheticCases()[2], treatment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "success" || result.Outcome.PrefixPythonExecutions != 0 || result.Outcome.LogicalCalls != 1 || calls.Load() != 1 || result.Outcome.ResultSHA256 == "" || result.Outcome.AuthorityDisposition != "read_consumed" || result.Outcome.ReadyBeforeFinalize != 0 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestExactGuestScheduledSerialWholeFileRejectsFinalSyntaxBeforePythonAndCalls(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"value":"unexpected"}`), nil
	}))
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	treatment, err := semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
		Artifact: artifact, RunConfig: config, Plan: plan,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "serial-syntax", Plan: plan})
		},
		RunID: "serial-syntax", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "serial-syntax",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), semanticspeculation.Phase3SyntheticCases()[4], treatment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "syntax_error" || result.Outcome.FinalPythonStarted || result.Outcome.LogicalCalls != 0 || calls.Load() != 0 || result.Outcome.WorkspaceDisposition != "discarded" || result.Outcome.AuthorityDisposition != "unchanged" {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}
