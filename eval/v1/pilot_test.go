package v1_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	evalv1 "github.com/bkmashiro/agent-python-runtime/eval/v1"
)

func TestDevelopmentPilotSemanticBinding(t *testing.T) {
	experiment := []byte(`{"schema_version":"evaluation-experiment/v1","status":"frozen","dev_scenarios":["dev_a","dev_b"],"conditions":["direct-only","python-only","hybrid"],"repeats_per_scenario_condition":2,"provider":{"protocol":"openai-responses","max_output_tokens":100}}`)
	hashed := sha256.Sum256(experiment)
	digest := "sha256:" + hex.EncodeToString(hashed[:])
	pilot := map[string]any{
		"schema_version": "evaluation-development-pilot/v1", "pilot_id": "pilot", "status": "frozen", "split": "dev",
		"experiment_digest": digest, "credential_env_name": "LINKAPI_API_KEY", "trial_count": 12,
		"max_total_provider_calls": 12, "max_total_output_tokens": 1200,
		"transport_retry_policy": map[string]any{"automatic_retries": 0, "reason": "unverified_provider_idempotency"},
		"decision_eligible":      false, "conclusion": "development_only",
	}
	pilotJSON, _ := json.Marshal(pilot)
	if err := evalv1.ValidateDevelopmentPilot(experiment, pilotJSON); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"digest drift": func(value map[string]any) {
			value["experiment_digest"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"evaluation claim":  func(value map[string]any) { value["decision_eligible"] = true },
		"missing trial":     func(value map[string]any) { value["trial_count"] = 11 },
		"token underbudget": func(value map[string]any) { value["max_total_output_tokens"] = 1199 },
		"automatic retry":   func(value map[string]any) { value["transport_retry_policy"].(map[string]any)["automatic_retries"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			copyValue := map[string]any{}
			for key, value := range pilot {
				copyValue[key] = value
			}
			copyValue["transport_retry_policy"] = map[string]any{"automatic_retries": 0, "reason": "unverified_provider_idempotency"}
			mutate(copyValue)
			encoded, _ := json.Marshal(copyValue)
			if err := evalv1.ValidateDevelopmentPilot(experiment, encoded); err == nil {
				t.Fatal("invalid pilot binding accepted")
			}
		})
	}
}
