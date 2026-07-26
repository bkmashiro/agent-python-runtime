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
	"path/filepath"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type dependencies struct {
	executablePath func() (string, error)
	newAdapter     func() (provider.Adapter, error)
}

type artifactEntry struct {
	TrialID        string            `json:"trial_id"`
	TaskID         string            `json:"task_id"`
	Condition      agentic.Condition `json:"condition"`
	Replicate      uint32            `json:"replicate"`
	Digest         string            `json:"digest"`
	Path           string            `json:"path"`
	OutcomeSuccess bool              `json:"outcome_success"`
	StrictPass     bool              `json:"strict_pass"`
	ErrorCode      string            `json:"error_code,omitempty"`
}

type pilotSummary struct {
	Version                 string          `json:"version"`
	Mode                    string          `json:"mode"`
	Status                  string          `json:"status"`
	DecisionEligible        bool            `json:"decision_eligible"`
	PlanDigest              string          `json:"plan_digest"`
	ActivationDigest        string          `json:"activation_digest"`
	TreatmentID             string          `json:"treatment_id"`
	TreatmentDigest         string          `json:"treatment_digest"`
	Bounds                  executionBounds `json:"bounds"`
	TrialCount              uint32          `json:"trial_count"`
	OutcomeSuccessfulTrials uint32          `json:"outcome_successful_trials"`
	StrictPassedTrials      uint32          `json:"strict_passed_trials"`
	ProviderAttempts        uint32          `json:"provider_attempts"`
	ProviderCalls           uint32          `json:"provider_calls"`
	PythonAttempts          uint32          `json:"python_attempts"`
	PythonRuns              uint32          `json:"python_runs"`
	Usage                   provider.Usage  `json:"usage"`
	Artifacts               []artifactEntry `json:"artifacts"`
}

func (summary *pilotSummary) recordTrialMetrics(result agentic.TrialResult) error {
	if summary == nil || result.Metrics == nil {
		return errors.New("trial metrics are unavailable")
	}
	if result.Metrics.OutcomeSuccess {
		summary.OutcomeSuccessfulTrials++
	}
	if result.Metrics.StrictPass {
		summary.StrictPassedTrials++
	}
	return nil
}

var representativeCanaryTasks = []string{
	"bfcl-v4-stateful-local-tools-multi_turn_base_12",
	"bfcl-v4-stateless-function-calling-parallel_multiple_112",
}

type executionBounds struct {
	TrialCount          uint32 `json:"trial_count"`
	MaxProviderAttempts uint32 `json:"max_provider_attempts"`
	MaxInputTokens      uint64 `json:"max_input_tokens"`
	MaxOutputTokens     uint64 `json:"max_output_tokens"`
	MaxTotalTokens      uint64 `json:"max_total_tokens"`
	MaxToolCalls        uint32 `json:"max_tool_calls"`
	MaxPythonRuns       uint32 `json:"max_python_runs"`
}

type executionScope struct {
	Mode       string
	TaskIDs    []string
	Conditions []agentic.Condition
	Replicates []uint32
	Bounds     executionBounds
}

func run(ctx context.Context, args []string, deps dependencies) error {
	flags := flag.NewFlagSet("apyrun-agentic-pilot", flag.ContinueOnError)
	datasetRoot := flags.String("dataset", "", "agentic dataset root")
	planPath := flags.String("plan", "", "frozen development pilot plan")
	activationPath := flags.String("activation", "", "owner-approved activation")
	guestPath := flags.String("guest", "", "exact core Guest WASM artifact")
	outputRoot := flags.String("out", "", "new output directory")
	repositoryCommit := flags.String("repository-commit", "", "exact 40-hex source commit")
	canary := flags.Bool("canary", false, "run the fixed representative three-condition canary")
	diagnosticTask := flags.String("diagnostic-task", "", "run one authorized development task across all three conditions")
	diagnosticCondition := flags.String("diagnostic-condition", "", "restrict a diagnostic task to direct, python, or hybrid")
	treatmentPath := flags.String("treatment", "", "frozen development treatment; diagnostic tasks only")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *datasetRoot == "" || *planPath == "" || *activationPath == "" || *guestPath == "" || *outputRoot == "" || len(*repositoryCommit) != 40 {
		return errors.New("invalid arguments")
	}
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(*planPath, *datasetRoot)
	if err != nil {
		return err
	}
	treatment := agentic.BaselineTreatment()
	if *treatmentPath != "" {
		if *diagnosticTask == "" && !*canary {
			return agentic.ErrDevelopmentTreatment
		}
		treatment, err = agentic.LoadDevelopmentTreatment(*treatmentPath)
		if err != nil || treatment.ID == agentic.TreatmentBaselineV1 || !treatment.Implemented() {
			return agentic.ErrDevelopmentTreatment
		}
	}
	scope, err := selectExecutionScope(plan, dataset, *canary, *diagnosticTask, agentic.Condition(*diagnosticCondition))
	if err != nil {
		return err
	}
	scope, err = applyTreatmentBounds(plan, scope, treatment)
	if err != nil {
		return err
	}
	executable, err := deps.executablePath()
	if err != nil {
		return err
	}
	hostDigest, err := fileDigest(executable, 512*1024*1024)
	if err != nil {
		return fmt.Errorf("digest Host artifact: %w", err)
	}
	var activation agentic.PilotActivation
	if treatment.ID == agentic.TreatmentBaselineV1 {
		activation, err = agentic.LoadPilotActivation(*activationPath, plan, hostDigest)
	} else {
		activation, err = agentic.LoadPilotActivationForTreatment(*activationPath, plan, hostDigest, treatment)
	}
	if err != nil || activation.RepositoryCommit != *repositoryCommit || activation.ExecutionMode != scope.Mode {
		return agentic.ErrPilotActivation
	}
	guestBytes, err := readRegularBounded(*guestPath, 256*1024*1024)
	if err != nil {
		return fmt.Errorf("read Guest artifact: %w", err)
	}
	guestSum := sha256.Sum256(guestBytes)
	guestDigest := "sha256:" + hex.EncodeToString(guestSum[:])
	if activation.GuestArtifacts["core"] != guestDigest {
		return agentic.ErrPilotActivation
	}
	credential, exists := os.LookupEnv(plan.CredentialEnvName)
	if !exists || strings.TrimSpace(credential) == "" {
		return errors.New("provider credential is unavailable")
	}
	if info, statErr := os.Lstat(*outputRoot); statErr == nil || !errors.Is(statErr, os.ErrNotExist) || info != nil {
		return errors.New("output directory must not exist")
	}
	adapter, err := deps.newAdapter()
	if err != nil {
		return err
	}
	if err := os.Mkdir(*outputRoot, 0o700); err != nil {
		return err
	}
	trialsRoot := filepath.Join(*outputRoot, "trials")
	if err := os.Mkdir(trialsRoot, 0o700); err != nil {
		return err
	}
	debugRoot := ""
	if *diagnosticTask != "" {
		debugRoot = filepath.Join(*outputRoot, "debug")
		if err := os.Mkdir(debugRoot, 0o700); err != nil {
			return err
		}
	}
	tasks := make(map[string]agentic.Task)
	for _, task := range dataset.Tasks {
		tasks[task.ID] = task
	}
	summary := pilotSummary{
		Version: "agentic-development-pilot-result/v3", Mode: scope.Mode, Status: "complete", DecisionEligible: false,
		PlanDigest: plan.Digest, ActivationDigest: activation.Digest, TreatmentID: treatment.ID, TreatmentDigest: treatment.Digest,
		Bounds:    scope.Bounds,
		Artifacts: make([]artifactEntry, 0, scope.Bounds.TrialCount),
	}
	for _, taskID := range scope.TaskIDs {
		task, exists := tasks[taskID]
		if !exists {
			return agentic.ErrPilotPlan
		}
		for _, condition := range scope.Conditions {
			limits, err := plan.LimitsFor(task, condition)
			if err != nil {
				return err
			}
			for _, replicate := range scope.Replicates {
				if !plan.Authorizes(task.ID, condition, replicate) {
					return agentic.ErrPilotPlan
				}
				identity, err := activation.Identity(condition)
				if err != nil {
					return err
				}
				var factory agentic.PythonWorkflowFactory
				if condition != agentic.ConditionDirect {
					factory = func(tools *agentic.ToolRuntime) (agentic.PythonWorkflow, error) {
						return agentic.NewWASIPythonExecutor(ctx, guestBytes, plan.RuntimeConfig(), tools)
					}
				}
				var result agentic.TrialResult
				if debugRoot != "" {
					result, err = agentic.RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(ctx, adapter, task, condition, plan.Model, replicate, limits, identity, treatment, factory)
				} else {
					result, err = agentic.RunDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, plan.Model, replicate, limits, identity, factory)
				}
				if err != nil {
					return err
				}
				if debugRoot != "" {
					if err := writeRawDebug(filepath.Join(debugRoot, result.TrialID+".json"), result); err != nil {
						return err
					}
				}
				path := filepath.Join(trialsRoot, result.TrialID+".json")
				artifactDigest, err := agentic.WriteTrialArtifact(path, result)
				if err != nil {
					return err
				}
				summary.TrialCount++
				if err := summary.recordTrialMetrics(result); err != nil {
					return err
				}
				summary.ProviderAttempts += result.ProviderAttempts
				summary.ProviderCalls += result.ProviderCalls
				summary.PythonAttempts += result.PythonAttempts
				summary.PythonRuns += result.PythonRuns
				if summary.Usage.InputTokens, err = addBounded(summary.Usage.InputTokens, result.Usage.InputTokens, scope.Bounds.MaxInputTokens); err != nil {
					return err
				}
				if summary.Usage.OutputTokens, err = addBounded(summary.Usage.OutputTokens, result.Usage.OutputTokens, scope.Bounds.MaxOutputTokens); err != nil {
					return err
				}
				if summary.Usage.TotalTokens, err = addBounded(summary.Usage.TotalTokens, result.Usage.TotalTokens, scope.Bounds.MaxTotalTokens); err != nil {
					return err
				}
				summary.Artifacts = append(summary.Artifacts, artifactEntry{
					TrialID: result.TrialID, TaskID: task.ID, Condition: condition, Replicate: replicate,
					Digest: artifactDigest, Path: filepath.ToSlash(filepath.Join("trials", result.TrialID+".json")),
					OutcomeSuccess: result.Metrics.OutcomeSuccess, StrictPass: result.Metrics.StrictPass,
					ErrorCode: result.ErrorCode,
				})
				if abortPilot(result.ErrorCode) {
					summary.Status = "aborted"
					return fmt.Errorf("pilot aborted after trial %s: %s", result.TrialID, result.ErrorCode)
				}
			}
		}
	}
	if summary.TrialCount != scope.Bounds.TrialCount || summary.ProviderAttempts > scope.Bounds.MaxProviderAttempts || summary.PythonAttempts > scope.Bounds.MaxPythonRuns {
		return agentic.ErrPilotPlan
	}
	summaryBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	summaryBytes = append(summaryBytes, '\n')
	summaryPath := filepath.Join(*outputRoot, "manifest.json")
	if err := writeExclusive(summaryPath, summaryBytes); err != nil {
		return err
	}
	manifestSum := sha256.Sum256(summaryBytes)
	_, err = fmt.Fprintf(os.Stdout, `{"status":"complete","mode":%q,"trial_count":%d,"manifest_digest":"sha256:%s"}`+"\n", summary.Mode, summary.TrialCount, hex.EncodeToString(manifestSum[:]))
	return err
}

func selectExecutionScope(plan agentic.DevelopmentPilotPlan, dataset *agentic.Dataset, canary bool, diagnosticTask string, diagnosticCondition agentic.Condition) (executionScope, error) {
	if (canary && diagnosticTask != "") || (diagnosticCondition != "" && (canary || diagnosticTask == "")) {
		return executionScope{}, agentic.ErrPilotPlan
	}
	full := executionScope{
		Mode: "pilot", TaskIDs: append([]string(nil), plan.TaskIDs...),
		Conditions: append([]agentic.Condition(nil), plan.Conditions...), Replicates: append([]uint32(nil), plan.Replicates...),
		Bounds: executionBounds{
			TrialCount: plan.GlobalBounds.TrialCount, MaxProviderAttempts: plan.GlobalBounds.MaxProviderAttempts,
			MaxInputTokens: plan.GlobalBounds.MaxInputTokens, MaxOutputTokens: plan.GlobalBounds.MaxOutputTokens,
			MaxTotalTokens: plan.GlobalBounds.MaxTotalTokens, MaxToolCalls: plan.GlobalBounds.MaxToolCalls,
			MaxPythonRuns: plan.GlobalBounds.MaxPythonRuns,
		},
	}
	if !canary && diagnosticTask == "" {
		return full, nil
	}
	taskIDs := representativeCanaryTasks
	conditions := plan.Conditions
	if diagnosticTask != "" {
		taskIDs = []string{diagnosticTask}
		if diagnosticCondition != "" {
			conditions = []agentic.Condition{diagnosticCondition}
		}
	}
	return selectCanaryScope(plan, dataset, taskIDs, conditions)
}

func applyTreatmentBounds(plan agentic.DevelopmentPilotPlan, scope executionScope, treatment agentic.DevelopmentTreatment) (executionScope, error) {
	pythonCapableConditions := uint64(0)
	hybridConditions := uint64(0)
	for _, condition := range scope.Conditions {
		if condition == agentic.ConditionPython || condition == agentic.ConditionHybrid {
			pythonCapableConditions++
		}
		if condition == agentic.ConditionHybrid {
			hybridConditions++
		}
	}
	trialsPerCondition := uint64(len(scope.TaskIDs)) * uint64(len(scope.Replicates))
	additionalPython := pythonCapableConditions * trialsPerCondition * uint64(treatment.MaxPythonRepairsPerTrial)
	additionalProvider := additionalPython + hybridConditions*trialsPerCondition*uint64(treatment.MaxRouterCallsPerHybridTrial)
	if additionalProvider == 0 && additionalPython == 0 {
		return scope, nil
	}
	if additionalPython > uint64(^uint32(0))-uint64(scope.Bounds.MaxPythonRuns) || additionalProvider > uint64(^uint32(0))-uint64(scope.Bounds.MaxProviderAttempts) ||
		additionalProvider > (^uint64(0)-scope.Bounds.MaxInputTokens)/plan.PerTrial.MaxInputTokensPerAttempt ||
		additionalProvider > (^uint64(0)-scope.Bounds.MaxOutputTokens)/plan.PerTrial.MaxOutputTokensPerAttempt ||
		additionalProvider > (^uint64(0)-scope.Bounds.MaxTotalTokens)/(plan.PerTrial.MaxInputTokensPerAttempt+plan.PerTrial.MaxOutputTokensPerAttempt) {
		return executionScope{}, agentic.ErrPilotPlan
	}
	scope.Bounds.MaxPythonRuns += uint32(additionalPython)
	scope.Bounds.MaxProviderAttempts += uint32(additionalProvider)
	scope.Bounds.MaxInputTokens += additionalProvider * plan.PerTrial.MaxInputTokensPerAttempt
	scope.Bounds.MaxOutputTokens += additionalProvider * plan.PerTrial.MaxOutputTokensPerAttempt
	scope.Bounds.MaxTotalTokens += additionalProvider * (plan.PerTrial.MaxInputTokensPerAttempt + plan.PerTrial.MaxOutputTokensPerAttempt)
	if scope.Bounds.MaxPythonRuns > plan.GlobalBounds.MaxPythonRuns || scope.Bounds.MaxProviderAttempts > plan.GlobalBounds.MaxProviderAttempts ||
		scope.Bounds.MaxInputTokens > plan.GlobalBounds.MaxInputTokens || scope.Bounds.MaxOutputTokens > plan.GlobalBounds.MaxOutputTokens || scope.Bounds.MaxTotalTokens > plan.GlobalBounds.MaxTotalTokens {
		return executionScope{}, agentic.ErrPilotPlan
	}
	return scope, nil
}

func selectCanaryScope(plan agentic.DevelopmentPilotPlan, dataset *agentic.Dataset, taskIDs []string, conditions []agentic.Condition) (executionScope, error) {
	if dataset == nil || len(conditions) == 0 || len(conditions) > len(plan.Conditions) {
		return executionScope{}, agentic.ErrPilotPlan
	}
	wanted := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		for _, condition := range conditions {
			if !plan.Authorizes(taskID, condition, 0) {
				return executionScope{}, agentic.ErrPilotPlan
			}
		}
		wanted[taskID] = struct{}{}
	}
	turns := uint64(0)
	attempts := uint64(0)
	found := 0
	for _, task := range dataset.Tasks {
		if _, exists := wanted[task.ID]; exists && task.Split == "dev" {
			turns += uint64(len(task.Interaction.Turns))
			for _, condition := range conditions {
				conditionAttempts, err := plan.ProviderAttemptsFor(task, condition)
				if err != nil || conditionAttempts > ^uint64(0)-attempts {
					return executionScope{}, agentic.ErrPilotPlan
				}
				attempts += conditionAttempts
			}
			found++
		}
	}
	if found != len(taskIDs) || turns == 0 || turns > 64 {
		return executionScope{}, agentic.ErrPilotPlan
	}
	trialCount := uint64(len(taskIDs) * len(conditions))
	pythonConditionCount := uint64(0)
	for _, condition := range conditions {
		if condition != agentic.ConditionDirect {
			pythonConditionCount++
		}
	}
	return executionScope{
		Mode: "canary", TaskIDs: append([]string(nil), taskIDs...),
		Conditions: append([]agentic.Condition(nil), conditions...), Replicates: []uint32{0},
		Bounds: executionBounds{
			TrialCount: uint32(trialCount), MaxProviderAttempts: uint32(attempts),
			MaxInputTokens:  attempts * plan.PerTrial.MaxInputTokensPerAttempt,
			MaxOutputTokens: attempts * plan.PerTrial.MaxOutputTokensPerAttempt,
			MaxTotalTokens:  attempts * (plan.PerTrial.MaxInputTokensPerAttempt + plan.PerTrial.MaxOutputTokensPerAttempt),
			MaxToolCalls:    uint32(trialCount) * plan.PerTrial.MaxToolCalls,
			MaxPythonRuns:   uint32(turns * pythonConditionCount),
		},
	}, nil
}

func abortPilot(code string) bool {
	switch code {
	case "usage_missing", "provider_identity_mismatch", "provider_output_limit_exceeded", "provider_budget_exceeded", "provider_timeout", "cancelled", "provider_or_protocol_failure", "python_engine_failure", "python_trace_mismatch", "direct_host_call_failed", "invalid_tool_observation":
		return true
	default:
		return false
	}
}

func addBounded(left, right, maximum uint64) (uint64, error) {
	if right > ^uint64(0)-left || left+right > maximum {
		return 0, errors.New("global token bound exceeded")
	}
	return left + right, nil
}

func fileDigest(path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return "", errors.New("artifact is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func readRegularBounded(path string, maximum int64) ([]byte, error) {
	if _, err := fileDigest(path, maximum); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func writeExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func writeRawDebug(path string, result agentic.TrialResult) error {
	if result.RawDebug == nil || result.TrialID == "" || result.TaskID == "" {
		return errors.New("raw debug is unavailable")
	}
	document := struct {
		Version   string                 `json:"version"`
		TrialID   string                 `json:"trial_id"`
		TaskID    string                 `json:"task_id"`
		Condition agentic.Condition      `json:"condition"`
		Replicate uint32                 `json:"replicate"`
		Debug     *agentic.TrialRawDebug `json:"debug"`
	}{"agentic-private-raw-debug/v1", result.TrialID, result.TaskID, result.Condition, result.Replicate, result.RawDebug}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(path, append(content, '\n'))
}

func main() {
	deps := dependencies{executablePath: os.Executable, newAdapter: func() (provider.Adapter, error) { return provider.NewLinkAPIResponses() }}
	if err := run(context.Background(), os.Args[1:], deps); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
