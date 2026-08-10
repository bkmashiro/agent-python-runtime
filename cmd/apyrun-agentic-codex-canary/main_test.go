package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

var canaryCommandErrOutput = func(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestRunRejectsUnknownTask(t *testing.T) {
	datasetRoot := routingDatasetRoot(t)
	codexPath, guestPath, outPath := testFiles(t)
	deps := productionDependencies()
	deps.newAdapter = func(string, string, string, time.Duration) (provider.Adapter, error) {
		return fakeAdapter{}, nil
	}
	deps.codexVersion = func(_ context.Context, _ string, _ time.Duration) (string, error) { return "v1", nil }
	deps.repositoryRoot = func() (string, error) { return codexPath, nil }

	err := runError(context.Background(), []string{
		"-codex", codexPath, "-model", "gpt-5.3-codex-spark", "-dataset", datasetRoot,
		"-guest", guestPath, "-repository-commit", strings.Repeat("a", 40),
		"-task", "rd-999", "-condition", "direct", "-provider-observed-at", "2026-08-11T00:30:00Z", "-out", outPath,
	}, deps)
	if err == nil || !strings.Contains(canaryCommandErrOutput(err), "task absent") {
		t.Fatalf("expected missing task error, got %v", err)
	}
}

func TestRunRejectsInvalidCondition(t *testing.T) {
	datasetRoot := routingDatasetRoot(t)
	codexPath, guestPath, outPath := testFiles(t)
	deps := productionDependencies()
	deps.newAdapter = func(string, string, string, time.Duration) (provider.Adapter, error) {
		return fakeAdapter{}, nil
	}
	deps.codexVersion = func(_ context.Context, _ string, _ time.Duration) (string, error) { return "v1", nil }
	deps.repositoryRoot = func() (string, error) { return codexPath, nil }

	err := runError(context.Background(), []string{
		"-codex", codexPath, "-model", "gpt-5.3-codex-spark", "-dataset", datasetRoot,
		"-guest", guestPath, "-repository-commit", strings.Repeat("b", 40),
		"-task", "rd-001", "-condition", "invalid", "-provider-observed-at", "2026-08-11T00:30:00Z", "-out", outPath,
	}, deps)
	if err == nil || !strings.Contains(canaryCommandErrOutput(err), "invalid condition") {
		t.Fatalf("expected invalid condition error, got %v", err)
	}
}

func TestExecutionIdentityOmitsGuestForDirectCondition(t *testing.T) {
	identity, err := buildExecutionIdentity(
		strings.Repeat("c", 40),
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		"sha256:"+strings.Repeat("d", 64),
		"",
		time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		agentic.ConditionDirect,
	)
	if err != nil {
		t.Fatalf("buildExecutionIdentity error: %v", err)
	}
	if identity.GuestProfile != "" || identity.GuestArtifactDigest != "" {
		t.Fatalf("direct identity leaked guest fields: %#v", identity)
	}
}

func TestRunRejectsExistingOutputBeforeAdapter(t *testing.T) {
	datasetRoot := routingDatasetRoot(t)
	codexPath, guestPath, outPath := testFiles(t)
	if err := os.WriteFile(outPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := productionDependencies()
	adapterCalled := false
	deps.newAdapter = func(string, string, string, time.Duration) (provider.Adapter, error) {
		adapterCalled = true
		return fakeAdapter{}, nil
	}
	deps.repositoryRoot = func() (string, error) { return codexPath, nil }

	err := runError(context.Background(), []string{
		"-codex", codexPath, "-model", codexSparkModel, "-dataset", datasetRoot,
		"-guest", guestPath, "-repository-commit", strings.Repeat("d", 40),
		"-task", "rd-001", "-condition", "direct", "-provider-observed-at", "2026-08-11T00:30:00Z", "-out", outPath,
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "output path already exists") || adapterCalled {
		t.Fatalf("err=%v adapter_called=%v", err, adapterCalled)
	}
}

func TestExecuteFlagsUsesDefaultsAndRejectsMissingArguments(t *testing.T) {
	codexPath, guestPath, outPath := testFiles(t)
	deps := productionDependencies()
	deps.newAdapter = func(string, string, string, time.Duration) (provider.Adapter, error) { return fakeAdapter{}, nil }
	deps.repositoryRoot = func() (string, error) { return codexPath, nil }
	deps.codexVersion = func(_ context.Context, _ string, _ time.Duration) (string, error) { return "v1", nil }
	deps.runTrial = func(_ context.Context, _ provider.Adapter, _ agentic.Task, _ agentic.Condition, _ string, _ uint32, _ agentic.TrialLimits, _ agentic.ExecutionIdentity, _ agentic.DevelopmentTreatment, _ agentic.PythonWorkflowFactory) (agentic.TrialResult, error) {
		return agentic.TrialResult{}, errors.New("trial should not execute in missing args")
	}
	if _, err := run(context.Background(), []string{"-codex", codexPath, "-guest", guestPath, "-repository-commit", strings.Repeat("e", 40), "-provider-observed-at", "2026-08-11T00:30:00Z", "-out", outPath}, deps); err == nil {
		t.Fatalf("expected dataset argument requirement error")
	}
}

func runError(ctx context.Context, args []string, deps dependencies) error {
	_, err := run(ctx, args, deps)
	if err == flag.ErrHelp {
		return err
	}
	return err
}

func testFiles(t *testing.T) (string, string, string) {
	t.Helper()
	codex := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\necho codex version 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(guest, []byte("guest"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.json")
	return codex, guest, out
}

func routingDatasetRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve routing dataset root")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "eval", "agentic", "routing", "v1")
}

type fakeAdapter struct{}

func (fakeAdapter) Protocol() string { return provider.CodexCLIProtocol }
func (fakeAdapter) Exchange(_ context.Context, _ provider.Request) (provider.Response, error) {
	return provider.Response{}, errors.New("not used")
}
