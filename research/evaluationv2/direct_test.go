package evaluationv2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestRunDirectPilotsThroughBroker(t *testing.T) {
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogFixture))
	}))
	defer catalog.Close()
	manifestBody, err := os.ReadFile(filepath.Join("..", "..", "runtime", "capability", "testdata", "benchmark-manifest.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(manifestBody)
	}))
	defer manifest.Close()
	definitions, err := PilotDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		t.Run(definition.Workload.ID, func(t *testing.T) {
			registry := capability.NewRegistry()
			if err := capability.RegisterDemoCatalog(registry, capability.DemoCatalogPolicy{Endpoint: catalog.URL, Timeout: time.Second, MaxResponseBytes: 4096}); err != nil {
				t.Fatal(err)
			}
			if definition.Workload.ID == "source-join-ranking" {
				if err := capability.RegisterBenchmarkManifest(registry, capability.BenchmarkManifestPolicy{Endpoint: manifest.URL, Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := registry.Seal(capability.PlanConfig{MaxCalls: definition.Workload.ExpectedCapabilityCalls})
			if err != nil {
				t.Fatal(err)
			}
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "direct-" + definition.Workload.ID, Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := RunDirect(context.Background(), definition, broker)
			if err != nil {
				t.Fatal(err)
			}
			if err := broker.Finalize(true); err != nil {
				t.Fatal(err)
			}
			if VerifyResult(definition, outcome.Result, outcome.CapabilityCalls) != nil || outcome.ControllerBoundaries != definition.Workload.ExpectedCapabilityCalls || outcome.ControllerRequestBytes == 0 || outcome.ControllerResponseBytes == 0 || outcome.BrokerRequestBytes != outcome.ControllerRequestBytes || outcome.BrokerResponseBytes != outcome.ControllerResponseBytes {
				t.Fatalf("outcome=%+v", outcome)
			}
			metrics, err := DeriveMetrics(definition, ConditionDirect, outcome.ControllerRequestBytes, outcome.ControllerResponseBytes, broker)
			if err != nil || metrics.ControllerBoundaries != definition.Workload.ExpectedCapabilityCalls || metrics.Receipts != definition.Workload.ExpectedCapabilityCalls || metrics.TranscriptEntries != definition.Workload.ExpectedCapabilityCalls {
				t.Fatalf("metrics=%+v err=%v", metrics, err)
			}
			if len(broker.SnapshotReceipts()) != int(definition.Workload.ExpectedCapabilityCalls) || len(broker.SnapshotTranscript()) != int(definition.Workload.ExpectedCapabilityCalls) {
				t.Fatal("missing Broker evidence")
			}
		})
	}
}

func TestPilotCanonicalJSONRejectsMalformedTrailingData(t *testing.T) {
	definitions, err := PilotDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyResult(definitions[0], json.RawMessage(`{"id":"gamma","score":10,"title":"Gamma"}x`), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	if err := VerifyResult(definitions[0], json.RawMessage(`{"id":"bad","id":"gamma","score":10,"title":"Gamma"}`), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate err=%v", err)
	}
}

func TestRunDirectFailsClosedOnWrongPlan(t *testing.T) {
	definitions, err := PilotDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "wrong-plan", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunDirect(context.Background(), definitions[0], broker); err == nil {
		t.Fatal("missing capability was accepted")
	}
	if broker.Finalize(false) != nil {
		t.Fatal("failed direct run did not finalize as failure")
	}
}
