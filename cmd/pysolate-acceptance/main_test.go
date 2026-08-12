package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigRequiresExecutableAndArtifact(t *testing.T) {
	if err := validateConfig(config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	artifact := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(artifact, []byte("wasm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateConfig(config{Artifact: artifact, APYRun: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing apyrun accepted")
	}
	apyrun := filepath.Join(t.TempDir(), "apyrun")
	if err := os.WriteFile(apyrun, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{"manifest.json", "import-inventory.json", "import-qualification.json"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(artifact), sidecar), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateConfig(config{Artifact: artifact, APYRun: apyrun}); err == nil {
		t.Fatal("missing evidence directory accepted")
	}
}

func TestReportIsBoundedCanonicalAndValid(t *testing.T) {
	report := acceptanceReport{
		SchemaVersion:  "pysolate.playback-acceptance.v1",
		ArtifactSHA256: digest('a'), ExecutionProfileSHA256: digest('b'),
		Bundle:     bundleReport{Identity: digest('c'), Path: "/tmp/bundle.json", Mode: 0o600, Entries: 1},
		SourceHits: 1, LiveStatus: "ok", PlaybackStatus: "ok",
		LiveResultSHA256: digest('d'), PlaybackResultSHA256: digest('d'),
		LiveWorkspaceSHA256: digest('e'), PlaybackWorkspaceSHA256: digest('e'),
		PrivacyForbiddenMatches: []string{}, NetworkDisabledPlayback: true,
	}
	encoded, err := encodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxReportBytes || encoded[len(encoded)-1] != '\n' {
		t.Fatalf("invalid encoded report length/framing: %d", len(encoded))
	}
	var decoded acceptanceReport
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != report.SchemaVersion || decoded.PrivacyForbiddenMatches == nil {
		t.Fatalf("report round trip mismatch: %+v", decoded)
	}
	report.SourceHits = 2
	if _, err := encodeReport(report); err == nil {
		t.Fatal("report with playback network hit accepted")
	}
}

func TestPublishReportIsProtectedAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := publishReport(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	if err := publishReport(path, []byte(`{"ok":false}`)); err == nil {
		t.Fatal("existing report overwritten")
	}
}

func digest(character byte) string {
	value := make([]byte, 71)
	copy(value, "sha256:")
	for index := 7; index < len(value); index++ {
		value[index] = character
	}
	return string(value)
}
