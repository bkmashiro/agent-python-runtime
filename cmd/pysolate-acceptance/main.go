package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync/atomic"

	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const maxReportBytes = 64 << 10

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type config struct {
	Artifact    string
	APYRun      string
	EvidenceDir string
	Output      string
}

type bundleReport struct {
	Identity string      `json:"identity"`
	Path     string      `json:"path"`
	Mode     os.FileMode `json:"mode"`
	Entries  int         `json:"entries"`
}

type acceptanceReport struct {
	SchemaVersion           string       `json:"schema_version"`
	ArtifactSHA256          string       `json:"artifact_sha256"`
	ExecutionProfileSHA256  string       `json:"execution_profile_sha256"`
	Bundle                  bundleReport `json:"bundle"`
	SourceHits              uint32       `json:"source_hits"`
	LiveStatus              string       `json:"live_status"`
	PlaybackStatus          string       `json:"playback_status"`
	LiveResultSHA256        string       `json:"live_result_sha256"`
	PlaybackResultSHA256    string       `json:"playback_result_sha256"`
	LiveWorkspaceSHA256     string       `json:"live_workspace_sha256"`
	PlaybackWorkspaceSHA256 string       `json:"playback_workspace_sha256"`
	PrivacyForbiddenMatches []string     `json:"privacy_forbidden_matches"`
	NetworkDisabledPlayback bool         `json:"network_disabled_playback"`
}

type runResponse struct {
	Status           string          `json:"status"`
	Result           json.RawMessage `json:"result"`
	WorkspaceReceipt *struct {
		FinalWorkspaceSHA256 string `json:"final_workspace_sha256"`
	} `json:"workspace_receipt"`
}

func main() {
	var value config
	flag.StringVar(&value.Artifact, "artifact", "", "absolute path to the qualified CPython/WASI artifact")
	flag.StringVar(&value.APYRun, "apyrun", "", "absolute path to the apyrun executable")
	flag.StringVar(&value.EvidenceDir, "evidence-dir", "", "existing protected directory for the no-overwrite Playback Bundle")
	flag.StringVar(&value.Output, "output", "", "optional absolute report output path (0600, no overwrite)")
	flag.Parse()
	if err := validateConfig(value); err != nil {
		fmt.Fprintln(os.Stderr, "acceptance unavailable:", err)
		os.Exit(2)
	}
	report, err := runAcceptance(value)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance failed:", err)
		os.Exit(1)
	}
	encoded, err := encodeReport(report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode acceptance report:", err)
		os.Exit(1)
	}
	if value.Output != "" {
		if err := publishReport(value.Output, encoded); err != nil {
			fmt.Fprintln(os.Stderr, "publish acceptance report:", err)
			os.Exit(1)
		}
	}
	_, _ = os.Stdout.Write(encoded)
}

func validateConfig(value config) error {
	if !filepath.IsAbs(value.Artifact) || !filepath.IsAbs(value.APYRun) || !filepath.IsAbs(value.EvidenceDir) || (value.Output != "" && !filepath.IsAbs(value.Output)) {
		return errors.New("artifact, apyrun, evidence directory and optional output must be absolute paths")
	}
	if info, err := os.Stat(value.Artifact); err != nil || !info.Mode().IsRegular() {
		return errors.New("real Guest artifact is unavailable")
	}
	if info, err := os.Stat(value.APYRun); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("apyrun executable is unavailable")
	}
	for _, sidecar := range []string{"manifest.json", "import-inventory.json", "import-qualification.json"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(value.Artifact), sidecar)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("qualified Guest sidecar %s is unavailable", sidecar)
		}
	}
	if info, err := os.Stat(value.EvidenceDir); err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("evidence directory is unavailable or accessible by group/other")
	}
	return nil
}

func runAcceptance(value config) (acceptanceReport, error) {
	working, err := os.MkdirTemp("", "pysolate-acceptance-*")
	if err != nil {
		return acceptanceReport{}, err
	}
	defer os.RemoveAll(working)
	bundlePath := filepath.Join(value.EvidenceDir, "curated-source.playback.json")
	var hits atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/catalog" {
			http.Error(response, "unexpected request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = response.Write([]byte(`{"items":[{"id":"a","title":"Alpha","score":2},{"id":"b","title":"Beta","score":3}]}`))
	}))
	request := []byte(`{"run_id":"acceptance-live","code":"items=sources.demo_catalog()\ntitles=[item['title'] for item in items]\nwith open('/workspace/summary.txt','w',encoding='utf-8') as handle:\n    handle.write('|'.join(titles))\nresult={'titles':titles}","inputs":{}}`)
	liveRoot := filepath.Join(working, "live-workspace")
	if err := os.Mkdir(liveRoot, 0o700); err != nil {
		server.Close()
		return acceptanceReport{}, err
	}
	liveConfig := map[string]any{
		"program_surface":     "programmatic",
		"workspace":           map[string]any{"source_directory": liveRoot, "disposition": "discard"},
		"information_sources": map[string]any{"demo_catalog": map[string]any{"endpoint": server.URL + "/catalog", "timeout_ms": 1000, "max_response_bytes": 8192}},
		"max_tool_calls":      2,
		"playback":            map[string]any{"mode": "capture", "output_bundle": bundlePath},
	}
	live, err := runAPYRun(value, filepath.Join(working, "live.json"), liveConfig, request)
	if err != nil {
		server.Close()
		return acceptanceReport{}, err
	}
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		server.Close()
		return acceptanceReport{}, err
	}
	bundle, err := playback.Decode(bundleBytes)
	if err != nil {
		server.Close()
		return acceptanceReport{}, err
	}
	bundleInfo, err := os.Stat(bundlePath)
	if err != nil {
		server.Close()
		return acceptanceReport{}, err
	}
	serverURL := server.URL
	server.Close()

	playbackRoot := filepath.Join(working, "playback-workspace")
	if err := os.Mkdir(playbackRoot, 0o700); err != nil {
		return acceptanceReport{}, err
	}
	playbackConfig := map[string]any{
		"program_surface":     "programmatic",
		"workspace":           map[string]any{"source_directory": playbackRoot, "disposition": "discard"},
		"information_sources": map[string]any{"demo_catalog": map[string]any{"endpoint": serverURL + "/catalog", "timeout_ms": 1000, "max_response_bytes": 8192}},
		"max_tool_calls":      2,
		"playback":            map[string]any{"mode": "playback", "input_bundle": bundlePath, "expected_bundle_sha256": bundle.Identity},
	}
	offlineRequest := bytes.Replace(request, []byte("acceptance-live"), []byte("acceptance-live"), 1)
	offline, err := runAPYRun(value, filepath.Join(working, "playback.json"), playbackConfig, offlineRequest)
	if err != nil {
		return acceptanceReport{}, err
	}
	liveResult, err := playback.CanonicalSHA256(live.Result)
	if err != nil {
		return acceptanceReport{}, err
	}
	offlineResult, err := playback.CanonicalSHA256(offline.Result)
	if err != nil {
		return acceptanceReport{}, err
	}
	forbidden := []string{serverURL, "acceptance-live", "/workspace/summary.txt", "Alpha|Beta", `"titles"`}
	matches := make([]string, 0)
	for _, candidate := range forbidden {
		if bytes.Contains(bundleBytes, []byte(candidate)) {
			matches = append(matches, candidate)
		}
	}
	artifactBytes, err := os.ReadFile(value.Artifact)
	if err != nil {
		return acceptanceReport{}, err
	}
	report := acceptanceReport{
		SchemaVersion:  "pysolate.playback-acceptance.v1",
		ArtifactSHA256: sha256Identity(artifactBytes), ExecutionProfileSHA256: bundle.ExecutionProfileSHA256,
		Bundle:     bundleReport{Identity: bundle.Identity, Path: bundlePath, Mode: bundleInfo.Mode().Perm(), Entries: len(bundle.Entries)},
		SourceHits: hits.Load(), LiveStatus: live.Status, PlaybackStatus: offline.Status,
		LiveResultSHA256: liveResult, PlaybackResultSHA256: offlineResult,
		LiveWorkspaceSHA256: workspaceIdentity(live), PlaybackWorkspaceSHA256: workspaceIdentity(offline),
		PrivacyForbiddenMatches: matches, NetworkDisabledPlayback: hits.Load() == 1,
	}
	if err := validateReport(report); err != nil {
		return acceptanceReport{}, err
	}
	return report, nil
}

func runAPYRun(value config, configPath string, hostConfig map[string]any, request []byte) (runResponse, error) {
	encoded, err := json.Marshal(hostConfig)
	if err != nil {
		return runResponse{}, err
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		return runResponse{}, err
	}
	command := exec.Command(value.APYRun, "-artifact", value.Artifact, "-config", configPath)
	command.Stdin = bytes.NewReader(request)
	output, err := command.CombinedOutput()
	if err != nil {
		return runResponse{}, fmt.Errorf("apyrun: %w: %s", err, output)
	}
	var response runResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return runResponse{}, fmt.Errorf("decode apyrun response: %w", err)
	}
	return response, nil
}

func workspaceIdentity(response runResponse) string {
	if response.WorkspaceReceipt == nil {
		return ""
	}
	return response.WorkspaceReceipt.FinalWorkspaceSHA256
}

func validateReport(report acceptanceReport) error {
	if report.SchemaVersion != "pysolate.playback-acceptance.v1" || !digestPattern.MatchString(report.ArtifactSHA256) ||
		!digestPattern.MatchString(report.ExecutionProfileSHA256) || !digestPattern.MatchString(report.Bundle.Identity) || report.Bundle.Mode != 0o600 ||
		report.Bundle.Entries != 1 || report.SourceHits != 1 || report.LiveStatus != "ok" || report.PlaybackStatus != "ok" ||
		!digestPattern.MatchString(report.LiveResultSHA256) || report.LiveResultSHA256 != report.PlaybackResultSHA256 ||
		!digestPattern.MatchString(report.LiveWorkspaceSHA256) || report.LiveWorkspaceSHA256 != report.PlaybackWorkspaceSHA256 ||
		len(report.PrivacyForbiddenMatches) != 0 || !report.NetworkDisabledPlayback {
		return errors.New("acceptance invariants failed")
	}
	return nil
}

func encodeReport(report acceptanceReport) ([]byte, error) {
	if err := validateReport(report); err != nil {
		return nil, err
	}
	if report.PrivacyForbiddenMatches == nil {
		report.PrivacyForbiddenMatches = []string{}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxReportBytes {
		return nil, errors.New("acceptance report exceeds limit")
	}
	return encoded, nil
}

func publishReport(path string, encoded []byte) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("invalid report path")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func sha256Identity(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
