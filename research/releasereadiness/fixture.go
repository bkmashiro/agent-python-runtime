package releasereadiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type ProviderEvent struct {
	Phase      string
	Capability string
	AtNS       int64
}

type ProviderObserver func(phase, capability string)

var fixtureOutputs = map[string]json.RawMessage{
	"observability.query_metrics": json.RawMessage(`{
		"error_rate":[{"timestamp":1700000000,"value":0.01},{"timestamp":1700000300,"value":0.02},{"timestamp":1700000600,"value":0.015}],
		"p99_latency_ms":[{"timestamp":1700000000,"value":280},{"timestamp":1700000300,"value":320},{"timestamp":1700000600,"value":300}],
		"cpu_usage_percent":[{"timestamp":1700000000,"value":60},{"timestamp":1700000300,"value":65},{"timestamp":1700000600,"value":70}],
		"memory_usage_percent":[{"timestamp":1700000000,"value":72},{"timestamp":1700000300,"value":74},{"timestamp":1700000600,"value":73}]
	}`),
	"observability.query_logs": json.RawMessage(`[
		{"timestamp":1700000600,"message":"validation failed for coupon","error_type":"validation_error","pod":"checkout-7d9","trace_id":"trace-a"},
		{"timestamp":1700000900,"message":"validation failed for coupon","error_type":"validation_error","pod":"checkout-7d9","trace_id":"trace-b"}
	]`),
	"github.latest_deployment":   json.RawMessage(`{"commit":"abc123def456","version":"checkout-2026.08.18","deployed_at":1700000300,"status":"success","author":"release-bot"}`),
	"kubernetes.read_deployment": json.RawMessage(`{"feature_flags":{"new_checkout":true},"log_level":"info","replicas":4}`),
}

var fixtureArguments = map[string]map[string]any{
	"observability.query_metrics": {"service": "checkout", "window": "6h"},
	"observability.query_logs":    {"service": "checkout", "severity": "error", "window": "6h"},
	"github.latest_deployment":    {"repository": "shop/checkout"},
	"kubernetes.read_deployment":  {"cluster": "prod-eu", "namespace": "checkout"},
}

const expectedResultJSON = `{"confidence":80.0,"config_drift":{"drift":false,"reasons":[]},"deployment":{"author":"release-bot","commit":"abc123def456","deployed_at":1700000300.0,"status":"success","version":"checkout-2026.08.18"},"error_logs":{"count":2,"top_types":[["validation_error",2]]},"evidence":[{"detail":"error_rate","source":"metrics","type":"metric"},{"detail":"top_error_types","source":"logs","type":"log"},{"detail":"deployment","source":"deployment","type":"deployment"},{"detail":"config_drift","source":"config","type":"config"},{"count":2,"error_type":"validation_error","type":"error"}],"gates":[{"name":"recent_metric_violations","pass":true},{"details":"Insufficient data","name":"post_deploy_error_spike","pass":true},{"name":"severe_error_types","pass":true},{"name":"config_drift","pass":true},{"name":"deployment_status","pass":true}],"metrics_summary":{"error_rate":{"avg":0.015,"buckets":3,"max":0.02},"p99_latency_ms":{"buckets":3,"max":320.0}},"release_ready":true,"summary":"Release readiness analysis for checkout service","threshold_violations":[]}`

func ExpectedReleaseResult() any {
	var result any
	decoder := json.NewDecoder(bytes.NewReader([]byte(expectedResultJSON)))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	return result
}

func ValidateReleaseResult(value any) error {
	if digestJSON(value) != ExpectedFixtureResultSHA256 {
		return errors.New("release-readiness result drifted from the independent fixture oracle")
	}
	var result struct {
		Summary      string  `json:"summary"`
		Confidence   float64 `json:"confidence"`
		ReleaseReady bool    `json:"release_ready"`
		ErrorLogs    struct {
			Count int `json:"count"`
		} `json:"error_logs"`
		Gates []struct {
			Name string `json:"name"`
			Pass bool   `json:"pass"`
		} `json:"gates"`
	}
	body, _ := json.Marshal(value)
	if json.Unmarshal(body, &result) != nil || result.Summary != "Release readiness analysis for checkout service" ||
		result.Confidence != 80 || !result.ReleaseReady || result.ErrorLogs.Count != 2 || len(result.Gates) != 5 {
		return errors.New("release-readiness semantic oracle failed")
	}
	for _, gate := range result.Gates {
		if gate.Name == "" || !gate.Pass {
			return errors.New("release-readiness gate oracle failed")
		}
	}
	return nil
}

func newFixturePlan(workload RecordedWorkload, providerScale float64, observe ProviderObserver) (*capability.Plan, error) {
	if providerScale <= 0 {
		return nil, errors.New("provider scale must be positive")
	}
	latencies := make(map[string]time.Duration, len(workload.ToolCalls))
	for _, call := range workload.ToolCalls {
		latencies[call.Capability] = scaledDuration(call.LatencyNS, providerScale)
	}
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"fixture":"checkout-readiness-v1","network":false}`))
	if err != nil {
		return nil, err
	}
	type specInput struct {
		name      string
		module    string
		method    string
		arguments []string
		input     json.RawMessage
		output    json.RawMessage
		resource  capability.ResourceReference
	}
	specs := []specInput{
		{name: "observability.query_metrics", module: "ops", method: "query_metrics", arguments: []string{"service", "window"}, input: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string","const":"checkout"},"window":{"type":"string","const":"6h"}},"required":["service","window"],"additionalProperties":false}`), output: json.RawMessage(`{"type":"object"}`), resource: capability.ResourceReference{Namespace: "observability-metrics", Argument: "service"}},
		{name: "observability.query_logs", module: "ops", method: "query_logs", arguments: []string{"service", "severity", "window"}, input: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string","const":"checkout"},"severity":{"type":"string","const":"error"},"window":{"type":"string","const":"6h"}},"required":["service","severity","window"],"additionalProperties":false}`), output: json.RawMessage(`{"type":"array","items":{"type":"object"}}`), resource: capability.ResourceReference{Namespace: "observability-logs", Argument: "service"}},
		{name: "github.latest_deployment", module: "ops", method: "latest_deployment", arguments: []string{"repository"}, input: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","const":"shop/checkout"}},"required":["repository"],"additionalProperties":false}`), output: json.RawMessage(`{"type":"object"}`), resource: capability.ResourceReference{Namespace: "github-deployment", Argument: "repository"}},
		{name: "kubernetes.read_deployment", module: "ops", method: "read_deployment", arguments: []string{"cluster", "namespace"}, input: json.RawMessage(`{"type":"object","properties":{"cluster":{"type":"string","const":"prod-eu"},"namespace":{"type":"string","const":"checkout"}},"required":["cluster","namespace"],"additionalProperties":false}`), output: json.RawMessage(`{"type":"object"}`), resource: capability.ResourceReference{Namespace: "kubernetes-deployment", Argument: "namespace"}},
	}
	for _, item := range specs {
		item := item
		handler := capability.HandlerFunc(func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			var decoded map[string]any
			decoder := json.NewDecoder(bytes.NewReader(arguments))
			decoder.UseNumber()
			if decoder.Decode(&decoded) != nil || !reflect.DeepEqual(decoded, fixtureArguments[item.name]) {
				return nil, fmt.Errorf("invalid %s fixture arguments", item.name)
			}
			if observe != nil {
				observe("start", item.name)
			}
			if err := waitContext(ctx, latencies[item.name]); err != nil {
				if observe != nil {
					observe("finish_error", item.name)
				}
				return nil, err
			}
			if observe != nil {
				observe("finish", item.name)
			}
			return append(json.RawMessage(nil), fixtureOutputs[item.name]...), nil
		})
		spec := capability.Spec{
			Name: item.name, Version: "pysolate.checkout-readiness-api.v1", Description: "Deterministic delayed checkout release-readiness read.",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.checkout-readiness-api.v1-" + item.method,
			InputSchema: item.input, OutputSchema: item.output, Python: &capability.PythonProjection{Module: item.module, Method: item.method, Arguments: item.arguments},
			ReadOnly: true, Idempotent: true,
			PreDispatch: &capability.PreDispatchContract{Resource: item.resource, Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1 << 20, CostUnits: 1},
		}
		if err := registry.Register(spec, grant, handler); err != nil {
			return nil, fmt.Errorf("register %s: %w", item.name, err)
		}
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 4})
}

func scaledDuration(nanos int64, scale float64) time.Duration {
	value := time.Duration(float64(nanos) * scale)
	if value < time.Millisecond {
		return time.Millisecond
	}
	return value
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
