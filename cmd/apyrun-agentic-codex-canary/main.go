package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const maxArtifactBytes = 256 * 1024 * 1024
const codexSparkModel = "gpt-5.3-codex-spark"

var canaryLimits = agentic.TrialLimits{
	MaxProviderCalls:       8,
	MaxToolCalls:           32,
	MaxPythonRuns:          4,
	MaxInputTokens:         500_000,
	MaxOutputTokens:        20_000,
	MaxTotalTokens:         520_000,
	MaxOutputTokensPerCall: 4_096,
}

type dependencies struct {
	readFile       func(string) ([]byte, error)
	newAdapter     func(string, string, string, time.Duration) (provider.Adapter, error)
	newPythonWasi  func(context.Context, []byte, runtimeconfig.RunConfig, *agentic.ToolRuntime) (agentic.PythonWorkflow, error)
	runTrial       func(context.Context, provider.Adapter, agentic.Task, agentic.Condition, string, uint32, agentic.TrialLimits, agentic.ExecutionIdentity, agentic.DevelopmentTreatment, agentic.PythonWorkflowFactory) (agentic.TrialResult, error)
	codexVersion   func(context.Context, string, time.Duration) (string, error)
	now            func() time.Time
	repositoryRoot func() (string, error)
	workdir        func() (string, error)
}

func productionDependencies() dependencies {
	return dependencies{
		readFile: os.ReadFile,
		newAdapter: func(executablePath, model, workdir string, timeout time.Duration) (provider.Adapter, error) {
			return provider.NewCodexCLIAdapter(executablePath, model, workdir, timeout)
		},
		newPythonWasi: func(ctx context.Context, guest []byte, config runtimeconfig.RunConfig, tools *agentic.ToolRuntime) (agentic.PythonWorkflow, error) {
			return agentic.NewWASIPythonExecutor(ctx, guest, config, tools)
		},
		runTrial:     agentic.RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment,
		codexVersion: probeCodexVersion,
		now:          time.Now,
		repositoryRoot: func() (string, error) {
			return os.Executable()
		},
		workdir: os.Getwd,
	}
}

func main() {
	if err := execute(context.Background(), os.Args[1:], productionDependencies()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, deps dependencies) error {
	digest, err := run(ctx, args, deps)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, digest)
	return err
}

func run(ctx context.Context, args []string, deps dependencies) (string, error) {
	if ctx == nil {
		return "", errors.New("missing context")
	}
	if deps.readFile == nil {
		deps.readFile = os.ReadFile
	}
	if deps.newAdapter == nil {
		deps.newAdapter = func(executablePath, model, workdir string, timeout time.Duration) (provider.Adapter, error) {
			return provider.NewCodexCLIAdapter(executablePath, model, workdir, timeout)
		}
	}
	if deps.newPythonWasi == nil {
		deps.newPythonWasi = func(ctx context.Context, guest []byte, config runtimeconfig.RunConfig, tools *agentic.ToolRuntime) (agentic.PythonWorkflow, error) {
			return agentic.NewWASIPythonExecutor(ctx, guest, config, tools)
		}
	}
	if deps.runTrial == nil {
		deps.runTrial = agentic.RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment
	}
	if deps.codexVersion == nil {
		deps.codexVersion = probeCodexVersion
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.repositoryRoot == nil {
		deps.repositoryRoot = os.Executable
	}
	if deps.workdir == nil {
		deps.workdir = os.Getwd
	}

	flags := flag.NewFlagSet("apyrun-agentic-codex-canary", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	codexPath := flags.String("codex", "", "Codex CLI executable path")
	model := flags.String("model", codexSparkModel, "exact provider model")
	datasetRoot := flags.String("dataset", "", "routing diagnostic dataset root")
	guestPath := flags.String("guest", "", "exact core Guest WASM artifact")
	repoCommit := flags.String("repository-commit", "", "exact source commit")
	taskID := flags.String("task", "rd-001", "routing task ID")
	conditionValue := flags.String("condition", string(agentic.ConditionDirect), "direct|python|hybrid")
	treatmentPath := flags.String("treatment", "", "optional frozen development treatment; defaults to baseline-v1")
	replicate := flags.Uint("replicate", 0, "trial replicate")
	out := flags.String("out", "", "new artifact path")
	timeoutText := flags.String("timeout", "180s", "Codex timeout")

	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", errors.New("invalid CLI arguments")
	}

	timeout, err := time.ParseDuration(*timeoutText)
	if err != nil || timeout <= 0 {
		return "", errors.New("invalid timeout")
	}
	if *codexPath == "" || *datasetRoot == "" || *guestPath == "" || *repoCommit == "" || *out == "" {
		return "", errors.New("missing required argument")
	}
	if *replicate > 1000 {
		return "", errors.New("replicate exceeds limit")
	}
	if err := validateLowerHex(*repoCommit); err != nil || len(*repoCommit) != 40 {
		return "", errors.New("invalid repository-commit")
	}
	condition := agentic.Condition(*conditionValue)
	if !isSupportedCondition(condition) {
		return "", errors.New("invalid condition")
	}
	treatment := agentic.BaselineTreatment()
	if *treatmentPath != "" {
		treatment, err = agentic.LoadDevelopmentTreatment(*treatmentPath)
		if err != nil {
			return "", err
		}
	}
	if err := validateCanaryLimits(canaryLimits); err != nil {
		return "", err
	}
	if err := requireNewArtifactPath(*out); err != nil {
		return "", err
	}

	dataset, err := agentic.LoadRoutingDataset(*datasetRoot)
	if err != nil {
		return "", err
	}
	task := findTask(dataset.Tasks, *taskID)
	if task == nil {
		return "", errors.New("task absent")
	}

	hostPath, err := deps.repositoryRoot()
	if err != nil {
		return "", err
	}
	hostPath, err = filepath.Abs(hostPath)
	if err != nil {
		return "", err
	}
	hostDigest, err := digestFile(hostPath)
	if err != nil {
		return "", err
	}
	hostWorkdir, err := deps.workdir()
	if err != nil {
		return "", err
	}
	hostWorkdir, err = filepath.Abs(hostWorkdir)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(hostWorkdir); statErr != nil || !info.IsDir() {
		return "", errors.New("invalid working directory")
	}
	guest, err := deps.readFile(*guestPath)
	if err != nil {
		return "", err
	}
	guestDigest, err := digestBytes(guest)
	if err != nil {
		return "", err
	}

	canonicalModel, err := canonicalizeModel(*model)
	if err != nil {
		return "", err
	}
	resolvedCodex, err := exec.LookPath(*codexPath)
	if err != nil {
		return "", err
	}
	resolvedCodex, err = filepath.EvalSymlinks(resolvedCodex)
	if err != nil {
		return "", err
	}
	resolvedCodex, err = filepath.Abs(resolvedCodex)
	if err != nil {
		return "", err
	}
	codexDigest, err := digestFile(resolvedCodex)
	if err != nil {
		return "", err
	}
	catalogVersion, err := deps.codexVersion(ctx, resolvedCodex, timeout)
	if err != nil {
		return "", err
	}
	catalogDigest, err := digestCatalogBinding(canonicalModel, catalogVersion, codexDigest)
	if err != nil {
		return "", err
	}
	identityGuestDigest := ""
	if condition != agentic.ConditionDirect {
		identityGuestDigest = guestDigest
	}
	identity, err := buildExecutionIdentity(*repoCommit, hostDigest, dataset.Plan.DatasetManifestDigest, catalogDigest, identityGuestDigest, deps.now(), condition)
	if err != nil {
		return "", err
	}

	adapter, err := deps.newAdapter(resolvedCodex, canonicalModel, hostWorkdir, timeout)
	if err != nil {
		return "", err
	}

	var factory agentic.PythonWorkflowFactory
	if condition != agentic.ConditionDirect {
		factory = func(tools *agentic.ToolRuntime) (agentic.PythonWorkflow, error) {
			return deps.newPythonWasi(ctx, guest, runtimeconfig.DefaultRunConfig(), tools)
		}
	}

	result, err := deps.runTrial(ctx, adapter, *task, condition, canonicalModel, uint32(*replicate), canaryLimits, identity, treatment, factory)
	if err != nil {
		return "", err
	}
	artifactDigest, err := agentic.WriteTrialArtifact(*out, result)
	if err != nil {
		return "", err
	}
	return artifactDigest, nil
}

func findTask(tasks []agentic.Task, taskID string) *agentic.Task {
	for index := range tasks {
		if tasks[index].ID == taskID {
			return &tasks[index]
		}
	}
	return nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return digestReader(file)
}

func digestBytes(value []byte) (string, error) {
	if len(value) == 0 || len(value) > maxArtifactBytes {
		return "", errors.New("invalid artifact")
	}
	hash := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func digestReader(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxArtifactBytes {
		return "", errors.New("invalid artifact")
	}
	hash := sha256.New()
	buf := make([]byte, 128*1024)
	remaining := info.Size()
	for remaining > 0 {
		toRead := len(buf)
		if int64(toRead) > remaining {
			toRead = int(remaining)
		}
		n, err := file.Read(buf[:toRead])
		if n > 0 {
			if _, writeErr := hash.Write(buf[:n]); writeErr != nil {
				return "", writeErr
			}
			remaining -= int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	if remaining != 0 {
		return "", errors.New("artifact read changed")
	}
	sum := hash.Sum(nil)
	return "sha256:" + hex.EncodeToString(sum), nil
}

func digestCatalogBinding(model, codexVersion, codexDigest string) (string, error) {
	if model == "" || codexVersion == "" || !isDigest(codexDigest) {
		return "", errors.New("invalid catalog identity")
	}
	raw, err := json.Marshal(struct {
		Catalog          string `json:"catalog"`
		Model            string `json:"model"`
		Protocol         string `json:"protocol"`
		Reasoning        string `json:"reasoning"`
		Sandbox          string `json:"sandbox"`
		ExecutableSHA256 string `json:"executable_sha256"`
	}{
		Catalog:          codexVersion,
		Model:            model,
		Protocol:         provider.CodexCLIProtocol,
		Reasoning:        "xhigh",
		Sandbox:          "read-only",
		ExecutableSHA256: codexDigest,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalizeModel(value string) (string, error) {
	if value != codexSparkModel {
		return "", errors.New("unsupported model")
	}
	return value, nil
}

func buildExecutionIdentity(repositoryCommit, hostArtifactDigest, datasetManifestDigest, providerCatalogDigest, guestArtifactDigest string, observedAt time.Time, condition agentic.Condition) (agentic.ExecutionIdentity, error) {
	if !isSupportedCondition(condition) {
		return agentic.ExecutionIdentity{}, errors.New("invalid condition")
	}
	if observedAt.IsZero() {
		return agentic.ExecutionIdentity{}, errors.New("invalid observed_at")
	}
	if err := validateLowerHex(repositoryCommit); err != nil {
		return agentic.ExecutionIdentity{}, errors.New("invalid identity inputs")
	}
	if !isDigest(hostArtifactDigest) || !isDigest(datasetManifestDigest) || !isDigest(providerCatalogDigest) {
		return agentic.ExecutionIdentity{}, errors.New("invalid identity digest")
	}
	if observedAt.Location() != time.UTC {
		observedAt = observedAt.UTC()
	}
	identity := agentic.ExecutionIdentity{
		RepositoryCommit:          repositoryCommit,
		HostArtifactDigest:        hostArtifactDigest,
		DatasetManifestDigest:     datasetManifestDigest,
		ProviderCatalogDigest:     providerCatalogDigest,
		ProviderCatalogObservedAt: observedAt.UTC().Format(time.RFC3339),
	}
	if condition != agentic.ConditionDirect {
		identity.GuestProfile = "core"
		identity.GuestArtifactDigest = guestArtifactDigest
	}
	if !isDigest(identity.ProviderCatalogDigest) || !isDigest(identity.HostArtifactDigest) || !isDigest(identity.DatasetManifestDigest) || !isDigest(identity.ProviderCatalogDigest) {
		return agentic.ExecutionIdentity{}, errors.New("invalid identity digest")
	}
	if _, err := time.Parse(time.RFC3339, identity.ProviderCatalogObservedAt); err != nil {
		return agentic.ExecutionIdentity{}, errors.New("invalid observed_at")
	}
	if condition == agentic.ConditionDirect && (identity.GuestArtifactDigest != "" || identity.GuestProfile != "") {
		return agentic.ExecutionIdentity{}, errors.New("direct identity includes guest")
	}
	if condition != agentic.ConditionDirect {
		if identity.GuestProfile != "core" || !isDigest(identity.GuestArtifactDigest) {
			return agentic.ExecutionIdentity{}, errors.New("invalid guest identity")
		}
	}
	return identity, nil
}

func isSupportedCondition(condition agentic.Condition) bool {
	switch condition {
	case agentic.ConditionDirect, agentic.ConditionPython, agentic.ConditionHybrid:
		return true
	default:
		return false
	}
}

func validateCanaryLimits(limits agentic.TrialLimits) error {
	if limits.MaxProviderCalls != 8 || limits.MaxToolCalls != 32 || limits.MaxPythonRuns != 4 ||
		limits.MaxInputTokens != 500_000 || limits.MaxOutputTokens != 20_000 || limits.MaxTotalTokens != 520_000 ||
		limits.MaxOutputTokensPerCall != 4_096 {
		return errors.New("invalid canary limits")
	}
	if limits.MaxPythonRuns > limits.MaxToolCalls || limits.MaxProviderCalls == 0 || limits.MaxToolCalls == 0 || limits.MaxInputTokens == 0 ||
		limits.MaxOutputTokens == 0 || limits.MaxTotalTokens == 0 || limits.MaxOutputTokensPerCall == 0 {
		return errors.New("invalid canary limits")
	}
	return nil
}

func validateLowerHex(value string) error {
	if len(value) != 40 {
		return errors.New("invalid commit length")
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return errors.New("invalid commit format")
		}
	}
	return nil
}

func isDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "sha256:") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func requireNewArtifactPath(out string) error {
	if info, err := os.Lstat(out); err == nil {
		if info.Mode()&os.ModeType == 0 {
			return errors.New("output path already exists")
		}
		return errors.New("output path is not a regular file")
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(out)
	if parent == "." {
		return nil
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if !parentInfo.Mode().IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid output parent")
	}
	return nil
}

func probeCodexVersion(ctx context.Context, executable string, timeout time.Duration) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, executable, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", errors.New("empty codex version")
	}
	return version, nil
}
