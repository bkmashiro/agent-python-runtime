package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestTau2T2DynamicModelTurnThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	manifest := os.Getenv("PYSOLATE_TAU2_T2_PRIVATE_MANIFEST")
	taskID := os.Getenv("PYSOLATE_TAU2_T2_TASK_ID")
	sourcePath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE")
	outputPath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_OUTPUT_FILE")
	capabilityName := os.Getenv("PYSOLATE_TAU2_EXPECTED_CAPABILITY")
	argumentsText := os.Getenv("PYSOLATE_TAU2_EXPECTED_ARGUMENTS")
	argumentNamesText := os.Getenv("PYSOLATE_TAU2_EXPECTED_ARGUMENT_NAMES")
	if python == "" || sourceRoot == "" || manifest == "" || taskID == "" || sourcePath == "" || outputPath == "" || capabilityName == "" || argumentsText == "" || argumentNamesText == "" {
		t.Skip("T2 dynamic turn environment is required")
	}
	for _, path := range []string{sourcePath, filepath.Dir(outputPath), manifest} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private T2 path has group/other permissions: %s", path)
		}
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil || len(source) == 0 || len(source) > 16*1024 {
		t.Fatal("dynamic source is outside bounded contract")
	}
	var arguments map[string]any
	var argumentNames []string
	if err := json.Unmarshal([]byte(argumentsText), &arguments); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(argumentNamesText), &argumentNames); err != nil || len(argumentNames) == 0 {
		t.Fatal("invalid argument names")
	}
	canonicalArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2T2ReadPlan(t, python, sourceRoot, manifest, taskID, capabilityName, argumentNames, arguments)
	planDocument, err := plan.EvidenceDocument()
	if err != nil {
		t.Fatal(err)
	}
	profile := tau2CanaryProfile(t, wasm)
	run := runTau2SourceBoundTurn(t, wasm, profile, plan, "t2-model", capabilityName, string(canonicalArguments), string(source))
	requestName := strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath)) + "-guest-request.json"
	responseName := strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath)) + "-guest-response.json"
	for name, body := range map[string][]byte{requestName: run.Request, responseName: run.Payload} {
		path := filepath.Join(filepath.Dir(outputPath), name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version": "pysolate.tau2-t2-dynamic-turn-private.v1", "task_id": taskID,
		"content": run.Content, "request_sha256": tau2Digest(run.Request), "response_sha256": tau2Digest(run.Payload), "receipt": run.Receipt,
		"capability_plan_sha256": plan.Identity(), "plan_document": json.RawMessage(planDocument),
		"grant_policy": tau2T2GrantPolicy(taskID, capabilityName, arguments),
		"raw_bodies":   map[string]string{"guest_request": requestName, "guest_response": responseName},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tau2T2ReadPlan(t *testing.T, python, sourceRoot, manifest, taskID, capabilityName string, argumentNames []string, arguments map[string]any) *capability.Plan {
	t.Helper()
	if !strings.HasPrefix(capabilityName, "tau2.airline.") {
		t.Fatal("invalid T2 capability")
	}
	method := strings.TrimPrefix(capabilityName, "tau2.airline.")
	properties := map[string]any{}
	for _, name := range argumentNames {
		value, ok := arguments[name]
		if !ok {
			t.Fatalf("argument name absent: %s", name)
		}
		properties[name] = map[string]any{"type": "string", "const": value}
	}
	inputSchema, err := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": argumentNames, "additionalProperties": false})
	if err != nil {
		t.Fatal(err)
	}
	grantBody, err := json.Marshal(tau2T2GrantPolicy(taskID, capabilityName, arguments))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := capability.NewGrant(grantBody)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	spec := capability.Spec{
		Name: capabilityName, Version: "pysolate.tau2.airline.t2-read.v1", Description: "Exact frozen tau2 T2 task-scoped READ.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "pysolate.tau2.airline.t2-read.handler." + tau2CanaryRevision,
		InputSchema:     inputSchema,
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`),
		Python:          &capability.PythonProjection{Module: "tools", Method: method, Arguments: argumentNames, ResultField: "content"},
	}
	if err := registry.Register(spec, grant, tau2T2ReadHandler(python, sourceRoot, manifest, taskID, method)); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func tau2T2GrantPolicy(taskID, capabilityName string, arguments map[string]any) map[string]any {
	canonicalArguments, _ := json.Marshal(arguments)
	return map[string]any{
		"benchmark": "tau2-t2", "domain": "airline", "effect": "external_read", "source_revision": tau2CanaryRevision,
		"task_id": taskID, "tool": strings.TrimPrefix(capabilityName, "tau2.airline."), "arguments_sha256": tau2Digest(canonicalArguments),
	}
}

func tau2T2ReadHandler(python, sourceRoot, manifest, taskID, tool string) capability.Handler {
	return capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments map[string]any
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, err
		}
		request := map[string]any{
			"schema_version": "pysolate.tau2-t2-read-request.v1", "source_revision": tau2CanaryRevision,
			"domain": "airline", "task_id": taskID, "call_id": "broker:" + tool, "tool": tool, "arguments": arguments,
		}
		input, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		script, err := filepath.Abs("../../scripts/tau2-t2-read-adapter.py")
		if err != nil {
			return nil, err
		}
		command := exec.CommandContext(ctx, python, script, "--source-root", sourceRoot, "--private-manifest", manifest, "--task-id", taskID)
		command.Stdin = bytes.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("T2 adapter failed: %w: %s", err, stderr.String())
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
			return nil, fmt.Errorf("T2 adapter trailing output")
		}
		if envelope.SchemaVersion != "pysolate.tau2-t2-read-response.v1" || envelope.SourceRevision != tau2CanaryRevision || envelope.Domain != "airline" || envelope.TaskID != taskID || envelope.CallID != "broker:"+tool || envelope.Tool != tool || strings.TrimSpace(envelope.Content) == "" {
			return nil, fmt.Errorf("T2 adapter identity mismatch")
		}
		return json.Marshal(map[string]string{"content": envelope.Content})
	})
}
