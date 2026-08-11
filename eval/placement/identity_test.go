package placement

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type identityLock struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Pysolate      struct {
		StartCommit string `json:"start_commit"`
		Guest       struct {
			SourceCommit   string `json:"source_commit"`
			ArtifactSHA256 string `json:"artifact_sha256"`
			ManifestSHA256 string `json:"manifest_sha256"`
		} `json:"guest"`
		Policy struct {
			ID               string   `json:"id"`
			ArtifactProfile  string   `json:"artifact_profile"`
			ImportDerivation string   `json:"import_derivation"`
			AllowedImports   []string `json:"allowed_imports"`
			Workspace        string   `json:"workspace_authority"`
			Outbound         string   `json:"outbound"`
			Process          string   `json:"process"`
			PackageInstall   string   `json:"package_install"`
		} `json:"policy"`
	} `json:"pysolate"`
	Provider struct {
		Model           string `json:"model"`
		Reasoning       string `json:"reasoning"`
		CodexCLIVersion string `json:"codex_cli_version"`
		Protocol        string `json:"protocol"`
	} `json:"provider"`
	Computer struct {
		Repository        string `json:"repository"`
		Tag               string `json:"tag"`
		Commit            string `json:"commit"`
		Tree              string `json:"tree"`
		ArchiveSHA256     string `json:"archive_sha256"`
		PackageLockSHA256 string `json:"package_lock_sha256"`
		WranglerVersion   string `json:"wrangler_version"`
		WorkerdVersion    string `json:"workerd_version"`
		HarnessSHA256     string `json:"harness_sha256"`
		PrimaryBackend    string `json:"primary_backend"`
		GlobalOutbound    string `json:"global_outbound"`
		ExecutionMode     string `json:"execution_mode"`
	} `json:"computer"`
	CI struct {
		Policy    string `json:"policy"`
		Workflows []struct {
			Path    string   `json:"path"`
			Trigger []string `json:"triggers"`
		} `json:"workflows"`
	} `json:"ci"`
}

func TestPlacementIdentityLock(t *testing.T) {
	path := filepath.Join("..", "agentic", "placement", "v1", "identity-lock.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock identityLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		t.Fatalf("decode identity lock: %v", err)
	}
	if lock.SchemaVersion != "placement-identity-lock/v2" || lock.Status != "development_refrozen_pre_decision" {
		t.Fatalf("identity lock not frozen: %+v", lock)
	}
	for label, value := range map[string]string{
		"pysolate start":   lock.Pysolate.StartCommit,
		"guest source":     lock.Pysolate.Guest.SourceCommit,
		"guest artifact":   lock.Pysolate.Guest.ArtifactSHA256,
		"guest manifest":   lock.Pysolate.Guest.ManifestSHA256,
		"computer commit":  lock.Computer.Commit,
		"computer tree":    lock.Computer.Tree,
		"computer archive": lock.Computer.ArchiveSHA256,
		"computer lock":    lock.Computer.PackageLockSHA256,
		"computer harness": lock.Computer.HarnessSHA256,
	} {
		if (label == "pysolate start" || label == "guest source" || label == "computer commit" || label == "computer tree") && len(value) == 40 {
			continue
		}
		if !validDigest(value) {
			t.Fatalf("invalid %s identity %q", label, value)
		}
	}
	if lock.Provider.Model != "gpt-5.3-codex-spark" || lock.Provider.Reasoning != "xhigh" ||
		lock.Provider.CodexCLIVersion != "0.146.0" || lock.Provider.Protocol != "codex-jsonl-code-proposal-v2" {
		t.Fatalf("provider identity drift: %+v", lock.Provider)
	}
	if lock.Pysolate.Policy.ID != AgentStdlibWorkspacePolicyID || lock.Pysolate.Policy.ArtifactProfile != "base" ||
		lock.Pysolate.Policy.ImportDerivation != "host-static-preamble-v1" ||
		!reflect.DeepEqual(lock.Pysolate.Policy.AllowedImports, AgentStdlibWorkspaceImports()) ||
		lock.Pysolate.Policy.Workspace != "typed-host-tools-only" || lock.Pysolate.Policy.Outbound != "null" ||
		lock.Pysolate.Policy.Process != "null" || lock.Pysolate.Policy.PackageInstall != "null" {
		t.Fatalf("Pysolate policy identity drift: %+v", lock.Pysolate.Policy)
	}
	if lock.Computer.Repository != "https://github.com/cloudflare/computer" || lock.Computer.Tag != "v0.1.1" ||
		lock.Computer.PrimaryBackend != "worker-javascript" || lock.Computer.GlobalOutbound != "null" ||
		lock.Computer.ExecutionMode != "wrangler-local" || lock.Computer.WranglerVersion != "4.115.0" || lock.Computer.WorkerdVersion != "1.20260722.1" {
		t.Fatalf("computer identity drift: %+v", lock.Computer)
	}
	if lock.CI.Policy != "manual_only_do_not_run" || len(lock.CI.Workflows) != 4 {
		t.Fatalf("CI policy drift: %+v", lock.CI)
	}
	for _, workflow := range lock.CI.Workflows {
		if len(workflow.Trigger) != 1 || workflow.Trigger[0] != "workflow_dispatch" {
			t.Fatalf("workflow %s has non-manual trigger %v", workflow.Path, workflow.Trigger)
		}
	}
}
