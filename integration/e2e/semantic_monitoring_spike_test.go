package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestRealGuestSysMonitoringCallPositionAndResultParity(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	code := strings.Join([]string{
		"import sys, dis",
		"tool_id = 3",
		"captured = []",
		"sys.monitoring.use_tool_id(tool_id, 'pysolate-call-probe')",
		"def target(value):",
		"    return value + 1",
		"def program():",
		"    answer = target(41)",
		"    return answer",
		"def callback(code, offset, callable_obj, arg0):",
		"    if callable_obj is target:",
		"        instruction = next(item for item in dis.get_instructions(code) if item.offset == offset)",
		"        position = instruction.positions",
		"        captured.append({'offset': offset, 'line': position.lineno, 'end_line': position.end_lineno, 'column': position.col_offset, 'end_column': position.end_col_offset})",
		"sys.monitoring.register_callback(tool_id, sys.monitoring.events.CALL, callback)",
		"sys.monitoring.set_local_events(tool_id, program.__code__, sys.monitoring.events.CALL)",
		"value = program()",
		"sys.monitoring.set_local_events(tool_id, program.__code__, 0)",
		"sys.monitoring.free_tool_id(tool_id)",
		"result = {'value': value, 'captured': captured, 'python': sys.version}",
	}, "\n") + "\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "monitoring-call-position", Code: code, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	var result struct {
		Value    int    `json:"value"`
		Python   string `json:"python"`
		Captured []struct {
			Offset    int `json:"offset"`
			Line      int `json:"line"`
			EndLine   int `json:"end_line"`
			Column    int `json:"column"`
			EndColumn int `json:"end_column"`
		} `json:"captured"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != 42 || !strings.HasPrefix(result.Python, "3.14.0 ") || len(result.Captured) != 1 {
		t.Fatalf("result=%+v payload=%s", result, payload)
	}
	position := result.Captured[0]
	if position.Offset != 14 || position.Line != 8 || position.EndLine != 8 || position.Column != 13 || position.EndColumn != 23 {
		t.Fatalf("unexpected CALL position: %+v", position)
	}
	t.Logf("monitoring CALL position: python=%q offset=%d span=%d:%d-%d:%d", result.Python, position.Offset, position.Line, position.Column, position.EndLine, position.EndColumn)
}

func TestRealGuestSysMonitoringBoundedOverheadProbe(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	code := strings.Join([]string{
		"import sys, time, statistics",
		"tool_id = 3",
		"sys.monitoring.use_tool_id(tool_id, 'pysolate-overhead-probe')",
		"call_events = 0",
		"line_events = 0",
		"def target(value):",
		"    return value + 1",
		"def program(n):",
		"    total = 0",
		"    for value in range(n):",
		"        total += target(value)",
		"    return total",
		"def call_callback(code, offset, callable_obj, arg0):",
		"    global call_events",
		"    call_events += 1",
		"def line_callback(code, line):",
		"    global line_events",
		"    line_events += 1",
		"sys.monitoring.register_callback(tool_id, sys.monitoring.events.CALL, call_callback)",
		"sys.monitoring.register_callback(tool_id, sys.monitoring.events.LINE, line_callback)",
		"baseline = []",
		"call_monitored = []",
		"line_monitored = []",
		"samples = 9",
		"iterations = 10000",
		"for _ in range(samples):",
		"    sys.monitoring.set_local_events(tool_id, program.__code__, 0)",
		"    started = time.perf_counter_ns()",
		"    value = program(iterations)",
		"    baseline.append(time.perf_counter_ns() - started)",
		"    sys.monitoring.set_local_events(tool_id, program.__code__, sys.monitoring.events.CALL)",
		"    started = time.perf_counter_ns()",
		"    monitored_value = program(iterations)",
		"    call_monitored.append(time.perf_counter_ns() - started)",
		"for _ in range(samples):",
		"    sys.monitoring.set_local_events(tool_id, program.__code__, sys.monitoring.events.LINE)",
		"    started = time.perf_counter_ns()",
		"    line_value = program(iterations)",
		"    line_monitored.append(time.perf_counter_ns() - started)",
		"sys.monitoring.set_local_events(tool_id, program.__code__, 0)",
		"sys.monitoring.free_tool_id(tool_id)",
		"baseline_median = int(statistics.median(baseline))",
		"call_median = int(statistics.median(call_monitored))",
		"line_median = int(statistics.median(line_monitored))",
		"result = {'value': value, 'monitored_value': monitored_value, 'line_value': line_value, 'samples': samples, 'iterations': iterations, 'baseline_ns_median': baseline_median, 'call_ns_median': call_median, 'line_ns_median': line_median, 'call_ratio': call_median / baseline_median, 'line_ratio': line_median / baseline_median, 'call_events': call_events, 'line_events': line_events}",
	}, "\n") + "\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "monitoring-overhead", Code: code, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	var result struct {
		Value            int     `json:"value"`
		MonitoredValue   int     `json:"monitored_value"`
		LineValue        int     `json:"line_value"`
		Samples          int     `json:"samples"`
		Iterations       int     `json:"iterations"`
		BaselineNSMedian int64   `json:"baseline_ns_median"`
		CallNSMedian     int64   `json:"call_ns_median"`
		LineNSMedian     int64   `json:"line_ns_median"`
		CallEvents       int64   `json:"call_events"`
		LineEvents       int64   `json:"line_events"`
		CallRatio        float64 `json:"call_ratio"`
		LineRatio        float64 `json:"line_ratio"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	expected := 50005000
	if result.Value != expected || result.MonitoredValue != expected || result.LineValue != expected || result.Samples != 9 || result.Iterations != 10000 ||
		result.BaselineNSMedian <= 0 || result.CallNSMedian <= 0 || result.LineNSMedian <= 0 || result.CallRatio <= 0 || result.LineRatio <= 0 ||
		result.CallEvents == 0 || result.LineEvents == 0 {
		t.Fatalf("invalid overhead probe: %+v payload=%s", result, payload)
	}
	t.Logf("monitoring overhead: baseline=%dns call=%dns ratio=%.3f line=%dns ratio=%.3f call_events=%d line_events=%d",
		result.BaselineNSMedian, result.CallNSMedian, result.CallRatio, result.LineNSMedian, result.LineRatio, result.CallEvents, result.LineEvents)
}
