package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const tau2CanaryRevision = "c3398666e6559e3a063da3fc04b5acf7f941464e"

func TestTau2AirlineReadCanaryThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	if python == "" || sourceRoot == "" {
		t.Skip("PYSOLATE_TAU2_PYTHON and PYSOLATE_TAU2_SOURCE_ROOT are required")
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2ReadPlan(t, python, sourceRoot)
	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, "tau2-airline-3")
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: "tau2-airline-3-authored", Plan: plan, ProgrammaticParentCallID: "tau2-airline-3",
		})
		broker = created
		return created, createErr
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request := []byte(`{"run_id":"tau2-airline-3-authored","code":"import json\nreservation = json.loads(tools.get_reservation_details('JMO1MG'))\nuser = json.loads(tools.get_user_details('anya_garcia_5901'))\npassenger_count = len(reservation['passengers'])\nfree_per_passenger = 2 if user['membership'] == 'silver' and reservation['cabin'] == 'economy' else 0\nresult = {'answer': str(passenger_count * free_per_passenger), 'cabin': reservation['cabin'], 'membership': user['membership'], 'passenger_count': passenger_count}","inputs":{}}`)
	payload, err := runner.Run(context.Background(), request, presentation.PythonPrelude)
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	var result struct {
		Answer         string `json:"answer"`
		Cabin          string `json:"cabin"`
		Membership     string `json:"membership"`
		PassengerCount int    `json:"passenger_count"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Answer != "4" || result.Cabin != "economy" || result.Membership != "silver" || result.PassengerCount != 2 {
		t.Fatalf("unexpected body-safe result metadata: %+v", result)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 2 || broker.CallCount() != 2 ||
		receipts[0].Capability != "tau2.airline.get_reservation_details" ||
		receipts[1].Capability != "tau2.airline.get_user_details" {
		t.Fatalf("receipts=%#v call_count=%d", receipts, broker.CallCount())
	}
	for _, receipt := range receipts {
		if receipt.Outcome != "ok" || receipt.ParentCallID != "tau2-airline-3" {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
	if evidenceDir := os.Getenv("PYSOLATE_TAU2_EVIDENCE_DIR"); evidenceDir != "" {
		writeTau2CanaryEvidence(t, evidenceDir, request, payload, wasm, plan, broker, result)
	}
}

func writeTau2CanaryEvidence(t *testing.T, dir string, request, payload, wasm []byte, plan *capability.Plan, broker *capability.Broker, result any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	receipts := broker.SnapshotReceipts()
	sourceClaim := "recorded"
	for _, receipt := range receipts {
		if receipt.Source == nil {
			sourceClaim = "not_recorded"
		}
	}
	manifest := map[string]any{
		"schema_version":  "pysolate.tau2-canary-private-evidence.v1",
		"source":          map[string]any{"revision": tau2CanaryRevision, "domain": "airline", "task_id": "3"},
		"artifact_sha256": tau2Digest(wasm), "request_sha256": tau2Digest(request), "guest_response_sha256": tau2Digest(payload),
		"capability_plan_sha256": plan.Identity(), "broker_call_count": broker.CallCount(), "receipts": receipts,
		"source_occurrence_claim": sourceClaim, "result": result,
		"raw_bodies": map[string]string{"agent_request": "agent-request.json", "guest_response": "guest-response.json"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	for name, body := range map[string][]byte{"agent-request.json": request, "guest-response.json": payload, "evidence-manifest.json": encoded} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func tau2Digest(value []byte) string {
	hashed := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", hashed[:])
}

func tau2ReadPlan(t *testing.T, python, sourceRoot string) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	for _, item := range []struct {
		name, method, argument, resource string
	}{
		{"tau2.airline.get_reservation_details", "get_reservation_details", "reservation_id", "JMO1MG"},
		{"tau2.airline.get_user_details", "get_user_details", "user_id", "anya_garcia_5901"},
	} {
		grant, err := capability.NewGrant(json.RawMessage(fmt.Sprintf(
			`{"benchmark":"tau2","domain":"airline","effect":"external_read","resource":%q,"source_revision":%q,"task_id":"3","tool":%q}`,
			item.resource, tau2CanaryRevision, item.method,
		)))
		if err != nil {
			t.Fatal(err)
		}
		inputSchema := json.RawMessage(fmt.Sprintf(
			`{"type":"object","properties":{%q:{"type":"string","const":%q}},"required":[%q],"additionalProperties":false}`,
			item.argument, item.resource, item.argument,
		))
		spec := capability.Spec{
			Name: item.name, Version: "pysolate.tau2.airline.read.v1",
			Description:     "Exact tau2 airline task 3 read adapter.",
			EffectClass:     capability.EffectExternalRead,
			Playback:        capability.PlaybackLiveOnly,
			HandlerIdentity: "pysolate.tau2.airline.read.handler." + tau2CanaryRevision,
			InputSchema:     inputSchema,
			OutputSchema:    json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`),
			Python:          &capability.PythonProjection{Module: "tools", Method: item.method, Arguments: []string{item.argument}, ResultField: "content"},
		}
		handler := tau2ReadHandler(python, sourceRoot, item.method)
		if err := registry.Register(spec, grant, handler); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func tau2ReadHandler(python, sourceRoot, tool string) capability.Handler {
	return capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments map[string]string
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, err
		}
		request := map[string]any{
			"schema_version": "pysolate.tau2-read-request.v1", "source_revision": tau2CanaryRevision,
			"domain": "airline", "task_id": "3", "call_id": "broker:" + tool, "tool": tool, "arguments": arguments,
		}
		input, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		script, err := filepath.Abs("../../scripts/tau2-read-adapter.py")
		if err != nil {
			return nil, err
		}
		command := exec.CommandContext(ctx, python, script, "--source-root", sourceRoot)
		command.Stdin = bytes.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("tau2 adapter failed: %w", err)
		}
		decoder := json.NewDecoder(&stdout)
		decoder.DisallowUnknownFields()
		var envelope struct {
			SchemaVersion  string `json:"schema_version"`
			SourceRevision string `json:"source_revision"`
			Domain         string `json:"domain"`
			TaskID         string `json:"task_id"`
			CallID         string `json:"call_id"`
			Tool           string `json:"tool"`
			Content        string `json:"content"`
		}
		if err := decoder.Decode(&envelope); err != nil {
			return nil, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("tau2 adapter trailing output")
		}
		if envelope.SchemaVersion != "pysolate.tau2-read-response.v1" ||
			envelope.SourceRevision != tau2CanaryRevision || envelope.Domain != "airline" || envelope.TaskID != "3" ||
			envelope.CallID != "broker:"+tool || envelope.Tool != tool || strings.TrimSpace(envelope.Content) == "" {
			return nil, fmt.Errorf("tau2 adapter identity mismatch")
		}
		return json.Marshal(map[string]string{"content": envelope.Content})
	})
}
