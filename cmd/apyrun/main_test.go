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

func TestBranchPlaybackConfigRequiresProtectedParentManifestAndChildOutput(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.json")
	manifest := filepath.Join(root, "branch.json")
	child := filepath.Join(root, "child.json")
	digest := "sha256:" + strings.Repeat("a", 64)
	valid := playbackConfig{
		Mode: "branch", InputBundle: parent, ExpectedBundleSHA256: digest,
		InputBranchManifest: manifest, ExpectedBranchSHA256: digest, OutputBundle: child,
	}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*playbackConfig){
		func(value *playbackConfig) { value.InputBranchManifest = "" },
		func(value *playbackConfig) { value.ExpectedBranchSHA256 = "" },
		func(value *playbackConfig) { value.OutputBundle = value.InputBundle },
		func(value *playbackConfig) { value.ExpectedBundleSHA256 = "sha256:ABC" },
	} {
		candidate := valid
		mutate(&candidate)
		if err := candidate.validate(); err == nil {
			t.Fatalf("invalid branch config accepted: %+v", candidate)
		}
	}
}

func TestOperatorDeterministicVerificationIsHostOwnedAndRequiresArtifactProfile(t *testing.T) {
	valid := []byte(`{"execution_profile":{"id":"base","allowed_imports":["datetime","sys"]},"deterministic_verification":{"status":"experimental_partial","random_seed":"study-1"}}`)
	config, err := decodeOperatorConfig(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.resolve(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		[]byte(`{"deterministic_verification":{"status":"experimental_partial","random_seed":"study-1"}}`),
		[]byte(`{"execution_profile":{"id":"base","allowed_imports":["sys"]},"deterministic_verification":{"status":"current","random_seed":"study-1"}}`),
		[]byte(`{"execution_profile":{"id":"base","allowed_imports":["sys"]},"deterministic_verification":{"status":"experimental_partial","random_seed":"bad seed"}}`),
		[]byte(`{"execution_profile":{"id":"base","allowed_imports":["sys"]},"deterministic_verification":{"status":"experimental_partial","random_seed":"study","clock":1}}`),
	} {
		candidate, err := decodeOperatorConfig(raw)
		if err == nil {
			_, err = candidate.resolve()
		}
		if err == nil {
			t.Fatalf("invalid deterministic config accepted: %s", raw)
		}
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

func TestOperatorConfigAllowsBenchmarkManifestAloneOrWithDemoCatalog(t *testing.T) {
	benchmarkOnly, err := decodeOperatorConfig([]byte(`{"information_sources":{"benchmark_manifest":{"endpoint":"http://127.0.0.1:8080/manifest?track=stable","timeout_ms":750,"max_response_bytes":32768}},"max_tool_calls":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkOnly.resolve(); err != nil {
		t.Fatal(err)
	}
	policy, err := benchmarkOnly.InformationSources.BenchmarkManifest.resolve()
	if err != nil || policy.Endpoint != "http://127.0.0.1:8080/manifest?track=stable" || policy.Timeout != 750*time.Millisecond || policy.MaxResponseBytes != 32768 {
		t.Fatalf("policy=%#v err=%v", policy, err)
	}

	both, err := decodeOperatorConfig([]byte(`{"information_sources":{"demo_catalog":{"endpoint":"http://127.0.0.1:8080/catalog","timeout_ms":500,"max_response_bytes":4096},"benchmark_manifest":{"endpoint":"http://127.0.0.1:8081/manifest","timeout_ms":750,"max_response_bytes":32768}},"max_tool_calls":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := both.resolve(); err != nil || both.InformationSources.DemoCatalog == nil || both.InformationSources.BenchmarkManifest == nil {
		t.Fatalf("multi-source config=%#v err=%v", both, err)
	}
}

func TestOperatorConfigRejectsBenchmarkManifestTransportControlsAndInvalidPolicy(t *testing.T) {
	for _, payload := range []string{
		`{"information_sources":{"benchmark_manifest":{"url":"https://agent.invalid/manifest","endpoint":"https://source.test/manifest","timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"https://source.test/manifest","method":"POST","timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"https://source.test/manifest","headers":{"Authorization":"secret"},"timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"https://user:pass@source.test/manifest","timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"file:///tmp/manifest","timeout_ms":500,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"https://source.test/manifest","timeout_ms":0,"max_response_bytes":4096}}}`,
		`{"information_sources":{"benchmark_manifest":{"endpoint":"https://source.test/manifest","timeout_ms":500,"max_response_bytes":0}}}`,
	} {
		config, err := decodeOperatorConfig([]byte(payload))
		if err == nil {
			_, err = config.resolve()
		}
		if err == nil {
			t.Fatalf("benchmark source authority was accepted: %s", payload)
		}
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
