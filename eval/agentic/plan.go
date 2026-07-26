package agentic

import (
	"errors"
	"os"
	"sort"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

var ErrPilotPlan = errors.New("invalid agentic development pilot plan")

type DevelopmentPilotPlan struct {
	SchemaVersion         string `json:"schema_version"`
	Status                string `json:"status"`
	Split                 string `json:"split"`
	Model                 string `json:"model"`
	DecisionEligible      bool   `json:"decision_eligible"`
	DatasetManifestDigest string `json:"dataset_manifest_digest"`
	CredentialEnvName     string `json:"credential_env_name"`
	TransportRetryPolicy  struct {
		AutomaticRetries uint32 `json:"automatic_retries"`
		Reason           string `json:"reason"`
	} `json:"transport_retry_policy"`
	CostGate struct {
		Status             string `json:"status"`
		ActivationRequired bool   `json:"activation_required"`
	} `json:"cost_gate"`
	Conditions    []Condition `json:"conditions"`
	Replicates    []uint32    `json:"replicates"`
	GuestProfiles []string    `json:"guest_profiles"`
	TaskIDs       []string    `json:"task_ids"`
	PerTrial      struct {
		MaxProviderAttemptsPerTurn uint32 `json:"max_provider_attempts_per_turn"`
		MaxToolCalls               uint32 `json:"max_tool_calls"`
		MaxPythonRunsPerTurn       uint32 `json:"max_python_runs_per_turn"`
		MaxInputTokensPerAttempt   uint64 `json:"max_input_tokens_per_provider_attempt"`
		MaxOutputTokensPerAttempt  uint64 `json:"max_output_tokens_per_provider_attempt"`
	} `json:"per_trial"`
	RunConfig struct {
		TimeoutSeconds   uint32 `json:"timeout_seconds"`
		MaxRequestBytes  uint32 `json:"max_request_bytes"`
		MaxResponseBytes uint32 `json:"max_response_bytes"`
		MemoryLimitPages uint32 `json:"memory_limit_pages"`
	} `json:"run_config"`
	GlobalBounds struct {
		TrialCount          uint32 `json:"trial_count"`
		MaxProviderAttempts uint32 `json:"max_provider_attempts"`
		MaxInputTokens      uint64 `json:"max_input_tokens"`
		MaxOutputTokens     uint64 `json:"max_output_tokens"`
		MaxTotalTokens      uint64 `json:"max_total_tokens"`
		MaxToolCalls        uint32 `json:"max_tool_calls"`
		MaxPythonRuns       uint32 `json:"max_python_runs"`
	} `json:"global_bounds"`
	Digest string `json:"-"`
}

func LoadDevelopmentPilotPlan(path, datasetRoot string) (DevelopmentPilotPlan, *Dataset, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 64*1024 {
		return DevelopmentPilotPlan{}, nil, ErrPilotPlan
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DevelopmentPilotPlan{}, nil, ErrPilotPlan
	}
	var plan DevelopmentPilotPlan
	if decodeStrict(data, &plan) != nil {
		return DevelopmentPilotPlan{}, nil, ErrPilotPlan
	}
	dataset, err := Load(datasetRoot)
	if err != nil {
		return DevelopmentPilotPlan{}, nil, err
	}
	manifest, err := os.ReadFile(datasetRoot + string(os.PathSeparator) + "manifest.json")
	if err != nil || digest(manifest) != plan.DatasetManifestDigest {
		return DevelopmentPilotPlan{}, nil, ErrPilotPlan
	}
	plan.Digest = digest(data)
	if validateDevelopmentPlan(plan, dataset) != nil {
		return DevelopmentPilotPlan{}, nil, ErrPilotPlan
	}
	return plan, dataset, nil
}

func validateDevelopmentPlan(plan DevelopmentPilotPlan, dataset *Dataset) error {
	if dataset == nil || plan.SchemaVersion != "agentic-development-pilot-plan/v1" || plan.Status != "frozen" || plan.Split != "dev" ||
		plan.Model != developmentModel || plan.DecisionEligible || !validDigest(plan.DatasetManifestDigest) ||
		plan.CredentialEnvName != "LINKAPI_API_KEY" || plan.TransportRetryPolicy.AutomaticRetries != 0 ||
		plan.TransportRetryPolicy.Reason != "unverified_provider_idempotency" || plan.CostGate.Status != "awaiting_owner_approval" || !plan.CostGate.ActivationRequired ||
		len(plan.Conditions) != 3 || plan.Conditions[0] != ConditionDirect || plan.Conditions[1] != ConditionPython || plan.Conditions[2] != ConditionHybrid ||
		len(plan.Replicates) != 1 || plan.Replicates[0] != 0 || len(plan.GuestProfiles) != 1 || plan.GuestProfiles[0] != "core" ||
		plan.PerTrial.MaxProviderAttemptsPerTurn != 1 || plan.PerTrial.MaxToolCalls == 0 || plan.PerTrial.MaxToolCalls > maxFunctionCalls ||
		plan.PerTrial.MaxPythonRunsPerTurn != 1 || plan.PerTrial.MaxInputTokensPerAttempt == 0 ||
		plan.PerTrial.MaxOutputTokensPerAttempt == 0 || plan.PerTrial.MaxOutputTokensPerAttempt > maxDirectOutputTokens {
		return ErrPilotPlan
	}
	config := plan.RuntimeConfig()
	if config.Validate() != nil {
		return ErrPilotPlan
	}
	devTasks := make(map[string]Task)
	for _, task := range dataset.Tasks {
		if task.Split == "dev" {
			devTasks[task.ID] = task
		}
	}
	if len(devTasks) == 0 || len(plan.TaskIDs) != len(devTasks) {
		return ErrPilotPlan
	}
	ids := append([]string(nil), plan.TaskIDs...)
	if !sort.StringsAreSorted(ids) {
		return ErrPilotPlan
	}
	turnsPerCondition := uint64(0)
	for index, id := range ids {
		if index > 0 && id == ids[index-1] {
			return ErrPilotPlan
		}
		task, exists := devTasks[id]
		if !exists {
			return ErrPilotPlan
		}
		turnsPerCondition += uint64(len(task.Interaction.Turns))
	}
	trialCount := uint64(len(ids) * len(plan.Conditions) * len(plan.Replicates))
	providerAttempts := turnsPerCondition * uint64(len(plan.Conditions)) * uint64(len(plan.Replicates))
	maxInput, inputOK := checkedMultiply(providerAttempts, plan.PerTrial.MaxInputTokensPerAttempt)
	maxOutput, outputOK := checkedMultiply(providerAttempts, plan.PerTrial.MaxOutputTokensPerAttempt)
	maxTotal, totalOK := checkedAdd(maxInput, maxOutput)
	maxToolCalls, toolOK := checkedMultiply(trialCount, uint64(plan.PerTrial.MaxToolCalls))
	maxPythonRuns := turnsPerCondition * 2 * uint64(len(plan.Replicates))
	if !inputOK || !outputOK || !totalOK || !toolOK || trialCount > uint64(^uint32(0)) || providerAttempts > uint64(^uint32(0)) || maxToolCalls > uint64(^uint32(0)) || maxPythonRuns > uint64(^uint32(0)) ||
		plan.GlobalBounds.TrialCount != uint32(trialCount) || plan.GlobalBounds.MaxProviderAttempts != uint32(providerAttempts) ||
		plan.GlobalBounds.MaxInputTokens != maxInput || plan.GlobalBounds.MaxOutputTokens != maxOutput || plan.GlobalBounds.MaxTotalTokens != maxTotal ||
		plan.GlobalBounds.MaxToolCalls != uint32(maxToolCalls) || plan.GlobalBounds.MaxPythonRuns != uint32(maxPythonRuns) {
		return ErrPilotPlan
	}
	return nil
}

func (plan DevelopmentPilotPlan) LimitsFor(task Task) (TrialLimits, error) {
	if task.Split != "dev" || !containsString(plan.TaskIDs, task.ID) {
		return TrialLimits{}, ErrPilotPlan
	}
	attempts := uint64(len(task.Interaction.Turns))
	input, inputOK := checkedMultiply(attempts, plan.PerTrial.MaxInputTokensPerAttempt)
	output, outputOK := checkedMultiply(attempts, plan.PerTrial.MaxOutputTokensPerAttempt)
	total, totalOK := checkedAdd(input, output)
	if !inputOK || !outputOK || !totalOK || attempts == 0 || attempts > 64 {
		return TrialLimits{}, ErrPilotPlan
	}
	limits := TrialLimits{
		MaxProviderCalls: uint32(attempts), MaxToolCalls: plan.PerTrial.MaxToolCalls,
		MaxPythonRuns:  uint32(attempts) * plan.PerTrial.MaxPythonRunsPerTurn,
		MaxInputTokens: input, MaxOutputTokens: output, MaxTotalTokens: total,
		MaxOutputTokensPerCall: plan.PerTrial.MaxOutputTokensPerAttempt,
	}
	if !limits.valid() {
		return TrialLimits{}, ErrPilotPlan
	}
	return limits, nil
}

func (plan DevelopmentPilotPlan) RuntimeConfig() runtimeconfig.RunConfig {
	return runtimeconfig.RunConfig{
		Timeout:         time.Duration(plan.RunConfig.TimeoutSeconds) * time.Second,
		MaxRequestBytes: plan.RunConfig.MaxRequestBytes, MaxResponseBytes: plan.RunConfig.MaxResponseBytes,
		MemoryLimitPages: plan.RunConfig.MemoryLimitPages, CapabilityGrants: map[string]runtimeconfig.CapabilityGrant{},
	}
}

func (plan DevelopmentPilotPlan) Authorizes(taskID string, condition Condition, replicate uint32) bool {
	return containsString(plan.TaskIDs, taskID) && containsCondition(plan.Conditions, condition) && containsUint32(plan.Replicates, replicate)
}

func checkedMultiply(left, right uint64) (uint64, bool) {
	if left != 0 && right > ^uint64(0)/left {
		return 0, false
	}
	return left * right, true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsCondition(values []Condition, target Condition) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsUint32(values []uint32, target uint32) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
