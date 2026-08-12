package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestAgentSourceGetsProfileAndWorkspaceToolsFromHost(t *testing.T) {
	artifact := guestArtifact(t)
	manifest := filepath.Join(filepath.Dir(artifact), "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Skip("distribution manifest is not available beside the Guest artifact")
	}
	binary := apyrunBinary(t)
	config := filepath.Join(t.TempDir(), "host.json")
	configData := []byte(`{"execution_profile":{"id":"base","allowed_imports":["csv"]},"workspace_files":{"metrics.csv":"name,value\na,2\nb,3\n"},"max_tool_calls":4}`)
	if err := os.WriteFile(config, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"agent-poc","code":"import csv\nrows=list(csv.reader(workspace.read_text('metrics.csv').splitlines()))\ntotal=sum(int(row[1]) for row in rows[1:])\nwrite_text('summary.txt', str(total))\nresult={'total':total,'files':workspace.list_files(),'summary':read_text('summary.txt')}","inputs":{}}`)
	command := exec.Command(binary, "-artifact", artifact, "-manifest", manifest, "-config", config)
	command.Stdin = bytes.NewReader(request)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run apyrun: %v\n%s", err, output)
	}
	var response guestResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(map[string]any)
	if response.Status != "ok" || !ok || result["total"] != float64(5) || result["summary"] != "5" || len(response.Receipts) != 4 {
		t.Fatalf("response=%+v", response)
	}
	if len(response.CapabilityPlanSHA256) != 71 || !bytes.HasPrefix([]byte(response.CapabilityPlanSHA256), []byte("sha256:")) {
		t.Fatalf("missing capability plan identity: %+v", response)
	}
	for _, receipt := range response.Receipts {
		if receipt["capability_plan_sha256"] != response.CapabilityPlanSHA256 {
			t.Fatalf("receipt plan mismatch: response=%+v receipt=%+v", response, receipt)
		}
	}

	zeroCallRequest := []byte(`{"run_id":"agent-poc-zero","code":"result={'ok':True}","inputs":{}}`)
	zeroCallCommand := exec.Command(binary, "-artifact", artifact, "-manifest", manifest, "-config", config)
	zeroCallCommand.Stdin = bytes.NewReader(zeroCallRequest)
	zeroCallOutput, err := zeroCallCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("run zero-call apyrun: %v\n%s", err, zeroCallOutput)
	}
	var zeroCallResponse guestResponse
	if err := json.Unmarshal(zeroCallOutput, &zeroCallResponse); err != nil {
		t.Fatal(err)
	}
	if zeroCallResponse.Status != "ok" || len(zeroCallResponse.Receipts) != 0 || zeroCallResponse.CapabilityPlanSHA256 != response.CapabilityPlanSHA256 {
		t.Fatalf("zero-call capability plan evidence=%+v want_plan=%s", zeroCallResponse, response.CapabilityPlanSHA256)
	}
}

func TestCuratedSourceCoexistsWithMountedWorkspace(t *testing.T) {
	artifact := guestArtifact(t)
	manifest := filepath.Join(filepath.Dir(artifact), "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Skip("distribution manifest is not available beside the Guest artifact")
	}
	var hits atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/catalog" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[{"id":"a","title":"Alpha","score":2},{"id":"b","title":"Beta","score":3}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	bundlePath := filepath.Join(t.TempDir(), "source.playback.json")
	configPath := filepath.Join(t.TempDir(), "host.json")
	config, err := json.Marshal(map[string]any{
		"workspace": map[string]any{"source_directory": root, "disposition": "discard"},
		"information_sources": map[string]any{"demo_catalog": map[string]any{
			"endpoint": server.URL + "/catalog", "timeout_ms": 1000, "max_response_bytes": 8192,
		}},
		"max_tool_calls": 2,
		"playback":       map[string]any{"mode": "capture", "output_bundle": bundlePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"source-mounted-workspace","code":"items=sources.demo_catalog()\ntitles=[item['title'] for item in items]\nwith open('/workspace/summary.txt','w',encoding='utf-8') as handle:\n    handle.write('|'.join(titles))\nwith open('/workspace/summary.txt',encoding='utf-8') as handle:\n    summary=handle.read()\nresult={'titles':titles,'summary':summary}","inputs":{}}`)
	command := exec.Command(apyrunBinary(t), "-artifact", artifact, "-config", configPath)
	command.Stdin = bytes.NewReader(request)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run curated source: %v\n%s", err, output)
	}
	var response guestResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(map[string]any)
	if response.Status != "ok" || !ok || result["summary"] != "Alpha|Beta" || hits.Load() != 1 || len(response.Receipts) != 1 {
		t.Fatalf("response=%+v hits=%d", response, hits.Load())
	}
	if response.Receipts[0]["capability"] != "sources.demo_catalog" || response.WorkspaceReceipt["disposition"] != "discarded" {
		t.Fatalf("missing source/workspace evidence: %+v", response)
	}
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	bundleInfo, err := os.Stat(bundlePath)
	if err != nil || bundleInfo.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode=%v err=%v", bundleInfo, err)
	}
	bundle, err := playback.Decode(bundleBytes)
	if err != nil || len(bundle.Entries) != 1 || bundle.Entries[0].Capability != "sources.demo_catalog" || bundle.Entries[0].Evidence.Status != 200 {
		t.Fatalf("bundle=%+v err=%v", bundle, err)
	}
	for _, forbidden := range []string{server.URL, "source-mounted-workspace", "/workspace/summary.txt", "Alpha|Beta", `\"summary\"`} {
		if bytes.Contains(bundleBytes, []byte(forbidden)) {
			t.Fatalf("bundle leaked forbidden run material %q", forbidden)
		}
	}

	server.Close()
	playbackRoot := t.TempDir()
	playbackConfigPath := filepath.Join(t.TempDir(), "host-playback.json")
	playbackConfig, err := json.Marshal(map[string]any{
		"workspace": map[string]any{"source_directory": playbackRoot, "disposition": "discard"},
		"information_sources": map[string]any{"demo_catalog": map[string]any{
			"endpoint": server.URL + "/catalog", "timeout_ms": 1000, "max_response_bytes": 8192,
		}},
		"max_tool_calls": 2,
		"playback":       map[string]any{"mode": "playback", "input_bundle": bundlePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playbackConfigPath, playbackConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	playbackCommand := exec.Command(apyrunBinary(t), "-artifact", artifact, "-config", playbackConfigPath)
	playbackCommand.Stdin = bytes.NewReader(request)
	playbackOutput, err := playbackCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("run offline playback: %v\n%s", err, playbackOutput)
	}
	var playbackResponse guestResponse
	if err := json.Unmarshal(playbackOutput, &playbackResponse); err != nil {
		t.Fatal(err)
	}
	liveResult, _ := json.Marshal(response.Result)
	offlineResult, _ := json.Marshal(playbackResponse.Result)
	liveResultSHA, _ := playback.CanonicalSHA256(liveResult)
	offlineResultSHA, _ := playback.CanonicalSHA256(offlineResult)
	if hits.Load() != 1 || playbackResponse.Status != "ok" || liveResultSHA != offlineResultSHA ||
		response.WorkspaceReceipt["final_workspace_sha256"] != playbackResponse.WorkspaceReceipt["final_workspace_sha256"] {
		t.Fatalf("live=%+v playback=%+v hits=%d", response, playbackResponse, hits.Load())
	}

	tamperCases := []struct {
		name   string
		mutate func(*playback.Bundle)
		tail   bool
	}{
		{name: "plan", mutate: func(value *playback.Bundle) { value.CapabilityPlanSHA256 = playback.SHA256([]byte("different-plan")) }},
		{name: "grant", mutate: func(value *playback.Bundle) {
			value.Grants[0].PolicySHA256 = playback.SHA256([]byte("different-grant"))
		}},
		{name: "request", mutate: func(value *playback.Bundle) { value.RequestSHA256 = playback.SHA256([]byte("different-request")) }},
		{name: "operation", mutate: func(value *playback.Bundle) { value.Entries[0].OperationIndex = 1 }},
		{name: "capability", mutate: func(value *playback.Bundle) { value.Entries[0].Capability = "sources.other_catalog" }},
		{name: "arguments", mutate: func(value *playback.Bundle) {
			value.Entries[0].Arguments = json.RawMessage(`{"unused":true}`)
			value.Entries[0].ArgumentsSHA256 = playback.SHA256(value.Entries[0].Arguments)
		}},
		{name: "result", mutate: func(value *playback.Bundle) {
			value.Entries[0].Result = json.RawMessage(`{"items":[{"id":"a","score":2,"title":"Changed"}]}`)
			value.Entries[0].ResultSHA256 = playback.SHA256(value.Entries[0].Result)
		}},
		{name: "extra", mutate: func(value *playback.Bundle) {
			extra := value.Entries[0]
			extra.OperationIndex = 1
			value.Entries = append(value.Entries, extra)
		}},
		{name: "missing", mutate: func(value *playback.Bundle) { value.Entries = nil }},
		{name: "tail", tail: true},
	}
	for _, testCase := range tamperCases {
		t.Run("tamper-"+testCase.name, func(t *testing.T) {
			cleanBytes, err := playback.Encode(bundle)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := playback.Decode(cleanBytes)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.mutate != nil {
				testCase.mutate(&candidate)
				candidate.Identity = ""
				cleanBytes, err = playback.Encode(candidate)
				if err != nil {
					t.Fatal(err)
				}
			}
			if testCase.tail {
				cleanBytes = append(cleanBytes, []byte("tail")...)
			}
			tamperedPath := filepath.Join(t.TempDir(), "tampered.playback.json")
			if err := os.WriteFile(tamperedPath, cleanBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			tamperedConfigPath := filepath.Join(t.TempDir(), "host.json")
			tamperedConfig, _ := json.Marshal(map[string]any{
				"workspace": map[string]any{"source_directory": t.TempDir(), "disposition": "discard"},
				"information_sources": map[string]any{"demo_catalog": map[string]any{
					"endpoint": server.URL + "/catalog", "timeout_ms": 1000, "max_response_bytes": 8192,
				}},
				"max_tool_calls": 2,
				"playback":       map[string]any{"mode": "playback", "input_bundle": tamperedPath},
			})
			if err := os.WriteFile(tamperedConfigPath, tamperedConfig, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(apyrunBinary(t), "-artifact", artifact, "-config", tamperedConfigPath)
			command.Stdin = bytes.NewReader(request)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("tampered bundle accepted: %s", output)
			}
			if hits.Load() != 1 {
				t.Fatalf("tamper attempted network: hits=%d", hits.Load())
			}
		})
	}
}
