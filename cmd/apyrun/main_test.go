package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperatorConfigOnlyAcceptsPoCResourcePolicy(t *testing.T) {
	config, err := decodeOperatorConfig([]byte(`{"timeout_ms":50,"max_request_bytes":2048}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Timeout != 50*time.Millisecond || resolved.MaxRequestBytes != 2048 {
		t.Fatalf("unexpected config: %#v", resolved)
	}
	if _, err := decodeOperatorConfig([]byte(`{"prepared_capacity":1}`)); err == nil {
		t.Fatal("removed lifecycle policy was accepted")
	}
	if _, err := decodeOperatorConfig([]byte(`{"transaction_journal_path":"/tmp/journal"}`)); err == nil {
		t.Fatal("removed transaction journal was accepted")
	}
}

func TestOperatorConfigAcceptsOneHostOwnedMountedWorkspace(t *testing.T) {
	root := t.TempDir()
	encoded, err := json.Marshal(map[string]any{
		"workspace": map[string]any{
			"source_directory": root,
			"output_capsule":   filepath.Join(root, "state.pwc"),
			"disposition":      "export_on_success",
			"limits": map[string]any{
				"max_files": 16, "max_bytes": 1024, "max_file_bytes": 512, "max_depth": 4,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeOperatorConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.resolve(); err != nil {
		t.Fatal(err)
	}
	limits, err := config.Workspace.resolveLimits()
	if err != nil || limits.MaxFiles != 16 || limits.MaxBytes != 1024 || limits.MaxFileBytes != 512 || limits.MaxDepth != 4 {
		t.Fatalf("limits=%+v err=%v", limits, err)
	}
}

func TestOperatorConfigRejectsAmbiguousOrAgentStyleWorkspaceAuthority(t *testing.T) {
	root := t.TempDir()
	cases := []map[string]any{
		{"workspace_files": map[string]string{"a": "b"}, "workspace": map[string]any{}},
		{"max_tool_calls": 1, "workspace": map[string]any{}},
		{"workspace": map[string]any{"source_directory": root, "input_capsule": filepath.Join(root, "in.pwc"), "disposition": "discard"}},
		{"workspace": map[string]any{"source_directory": "relative", "disposition": "discard"}},
		{"workspace": map[string]any{"input_capsule": root + "/../" + filepath.Base(root) + "/in.pwc", "disposition": "discard"}},
		{"workspace": map[string]any{"output_capsule": "relative", "disposition": "export_on_response"}},
		{"workspace": map[string]any{"output_capsule": filepath.Join(root, "out.pwc")}},
		{"workspace": map[string]any{"disposition": "export_on_success"}},
		{"workspace": map[string]any{"disposition": "discard", "output_capsule": filepath.Join(root, "out.pwc")}},
		{"workspace": map[string]any{"disposition": "export_sometimes", "output_capsule": filepath.Join(root, "out.pwc")}},
		{"workspace": map[string]any{"disposition": "discard", "limits": map[string]any{"max_files": 0, "max_bytes": 1, "max_file_bytes": 1, "max_depth": 1}}},
	}
	for index, value := range cases {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		config, err := decodeOperatorConfig(encoded)
		if err != nil {
			t.Fatalf("case %d decode: %v", index, err)
		}
		if _, err := config.resolve(); err == nil {
			t.Fatalf("case %d was accepted: %s", index, encoded)
		}
	}
}

func TestOperatorConfigRejectsAmbiguousSourceAndPlaybackJSON(t *testing.T) {
	cases := []string{
		`{"information_sources":null}`,
		`{"playback":null}`,
		`{"information_sources":{"demo_catalog":{"endpoint":"http://127.0.0.1/a","endpoint":"http://127.0.0.1/b","timeout_ms":1000,"max_response_bytes":4096}}}`,
		`{"playback":{"mode":"capture","mode":"playback","output_bundle":"/tmp/out"}}`,
	}
	for _, raw := range cases {
		if _, err := decodeOperatorConfig([]byte(raw)); err == nil {
			t.Fatalf("ambiguous config accepted: %s", raw)
		}
	}
}

func TestPlaybackConfigRequiresHostExpectedBundleIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := (&playbackConfig{Mode: "playback", InputBundle: path}).validate(); err == nil {
		t.Fatal("playback without expected bundle identity accepted")
	}
	valid := "sha256:" + strings.Repeat("a", 64)
	if err := (&playbackConfig{Mode: "playback", InputBundle: path, ExpectedBundleSHA256: valid}).validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&playbackConfig{Mode: "capture", OutputBundle: path, ExpectedBundleSHA256: valid}).validate(); err == nil {
		t.Fatal("capture accepted playback-only expected identity")
	}
}

func TestOperatorConfigAllowsCuratedSourceWithMountedWorkspace(t *testing.T) {
	config, err := decodeOperatorConfig([]byte(`{"workspace":{"disposition":"discard"},"information_sources":{"demo_catalog":{"endpoint":"http://127.0.0.1:8080/catalog","timeout_ms":500,"max_response_bytes":4096}},"max_tool_calls":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.resolve(); err != nil {
		t.Fatal(err)
	}
	policy, err := config.InformationSources.DemoCatalog.resolve()
	if err != nil || policy.Endpoint != "http://127.0.0.1:8080/catalog" || policy.Timeout != 500*time.Millisecond || policy.MaxResponseBytes != 4096 {
		t.Fatalf("policy=%#v err=%v", policy, err)
	}
}

func TestOperatorConfigRejectsGenericOrInvalidSourceAuthority(t *testing.T) {
	for _, payload := range []string{
		`{"information_sources":{"http":{"url":"https://example.test"}}}`,
		`{"information_sources":{"demo_catalog":{"endpoint":"https://user:pass@example.test/catalog","timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"demo_catalog":{"endpoint":"https://example.test/catalog","timeout_ms":0,"max_response_bytes":4096}}}`,
		`{"information_sources":{"demo_catalog":{"endpoint":"https://example.test/catalog","timeout_ms":500,"max_response_bytes":0}}}`,
		`{"max_tool_calls":2}`,
	} {
		config, err := decodeOperatorConfig([]byte(payload))
		if err == nil {
			_, err = config.resolve()
		}
		if err == nil {
			t.Fatalf("source authority was accepted: %s", payload)
		}
	}
}

func TestExecuteRequiresArtifact(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(nil, strings.NewReader("{}"), &stdout, &stderr, dependencies{})
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
