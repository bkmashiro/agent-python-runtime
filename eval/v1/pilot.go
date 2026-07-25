package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type developmentPilot struct {
	SchemaVersion        string `json:"schema_version"`
	PilotID              string `json:"pilot_id"`
	Status               string `json:"status"`
	Split                string `json:"split"`
	ExperimentDigest     string `json:"experiment_digest"`
	TrialCount           uint64 `json:"trial_count"`
	MaxProviderCalls     uint64 `json:"max_total_provider_calls"`
	MaxOutputTokens      uint64 `json:"max_total_output_tokens"`
	DecisionEligible     bool   `json:"decision_eligible"`
	Conclusion           string `json:"conclusion"`
	CredentialEnvName    string `json:"credential_env_name"`
	TransportRetryPolicy struct {
		AutomaticRetries uint64 `json:"automatic_retries"`
		Reason           string `json:"reason"`
	} `json:"transport_retry_policy"`
}

type pilotExperiment struct {
	SchemaVersion string   `json:"schema_version"`
	Status        string   `json:"status"`
	DevScenarios  []string `json:"dev_scenarios"`
	Conditions    []string `json:"conditions"`
	Repeats       uint64   `json:"repeats_per_scenario_condition"`
	Provider      struct {
		Protocol        string `json:"protocol"`
		MaxOutputTokens uint64 `json:"max_output_tokens"`
	} `json:"provider"`
}

func ValidateDevelopmentPilot(experimentJSON, pilotJSON []byte) error {
	var experiment pilotExperiment
	var pilot developmentPilot
	if json.Unmarshal(experimentJSON, &experiment) != nil || json.Unmarshal(pilotJSON, &pilot) != nil {
		return errors.New("development pilot documents are invalid")
	}
	if experiment.SchemaVersion != "evaluation-experiment/v1" || experiment.Status != "frozen" || experiment.Provider.Protocol != "openai-responses" || len(experiment.DevScenarios) == 0 || len(experiment.Conditions) != 3 || experiment.Repeats == 0 || experiment.Provider.MaxOutputTokens == 0 ||
		pilot.SchemaVersion != "evaluation-development-pilot/v1" || pilot.Status != "frozen" || pilot.Split != "dev" || pilot.DecisionEligible || pilot.Conclusion != "development_only" || pilot.CredentialEnvName != "LINKAPI_API_KEY" || pilot.TransportRetryPolicy.AutomaticRetries != 0 || pilot.TransportRetryPolicy.Reason != "unverified_provider_idempotency" || pilot.ExperimentDigest != digestDocument(experimentJSON) {
		return errors.New("development pilot binding is invalid")
	}
	conditionSet := map[string]bool{}
	for _, condition := range experiment.Conditions {
		conditionSet[condition] = true
	}
	if len(conditionSet) != 3 || !conditionSet["direct-only"] || !conditionSet["python-only"] || !conditionSet["hybrid"] {
		return errors.New("development pilot conditions are invalid")
	}
	if uint64(len(experiment.DevScenarios)) > ^uint64(0)/3/experiment.Repeats {
		return errors.New("development pilot trial count overflows")
	}
	expectedTrials := uint64(len(experiment.DevScenarios)) * 3 * experiment.Repeats
	if pilot.TrialCount != expectedTrials || pilot.MaxProviderCalls < expectedTrials || experiment.Provider.MaxOutputTokens > ^uint64(0)/expectedTrials || pilot.MaxOutputTokens < expectedTrials*experiment.Provider.MaxOutputTokens {
		return errors.New("development pilot budgets do not cover preregistered trials")
	}
	return nil
}

func digestDocument(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
