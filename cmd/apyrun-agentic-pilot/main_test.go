package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestRunRejectsGuestDigestBeforeAdapterOrOutput(t *testing.T) {
	root := repositoryRoot(t)
	dataset := filepath.Join(root, "eval", "agentic", "v1")
	planPath := filepath.Join(dataset, "development-pilot-plan.json")
	plan, _, err := agentic.LoadDevelopmentPilotPlan(planPath, dataset)
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "host")
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if os.WriteFile(host, []byte("host-binary"), 0o700) != nil || os.WriteFile(guest, []byte("guest-one"), 0o600) != nil {
		t.Fatal("write artifacts")
	}
	hostDigest, _ := fileDigest(host, 1024)
	activationDocument := map[string]any{
		"schema_version": "agentic-pilot-activation/v1", "status": "approved", "plan_digest": plan.Digest,
		"repository_commit": strings.Repeat("a", 40), "host_artifact_digest": hostDigest,
		"dataset_manifest_digest": plan.DatasetManifestDigest,
		"guest_artifacts":         map[string]any{"core": "sha256:" + strings.Repeat("c", 64)},
		"maximum_spend":           map[string]any{"currency": "USD", "decimal": "5.00"},
		"approved_by":             "owner", "approved_at": "2026-07-26T12:00:00Z",
	}
	activationBytes, _ := json.Marshal(activationDocument)
	activation := filepath.Join(t.TempDir(), "activation.json")
	if os.WriteFile(activation, activationBytes, 0o600) != nil {
		t.Fatal("write activation")
	}
	adapterCalled := false
	deps := dependencies{
		executablePath: func() (string, error) { return host, nil },
		newAdapter:     func() (provider.Adapter, error) { adapterCalled = true; return nil, errors.New("must not be called") },
	}
	out := filepath.Join(t.TempDir(), "out")
	err = run(context.Background(), []string{
		"--dataset", dataset, "--plan", planPath, "--activation", activation, "--guest", guest,
		"--out", out, "--repository-commit", strings.Repeat("a", 40),
	}, deps)
	if !errors.Is(err, agentic.ErrPilotActivation) || adapterCalled {
		t.Fatalf("err=%v adapter_called=%v", err, adapterCalled)
	}
	if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output created before gate: %v", statErr)
	}
}
