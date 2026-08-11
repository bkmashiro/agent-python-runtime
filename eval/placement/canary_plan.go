package placement

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

var ErrDevelopmentCanaryPlan = errors.New("invalid placement development canary plan")

type DevelopmentCanaryPlan struct {
	SchemaVersion             string              `json:"schema_version"`
	Status                    string              `json:"status"`
	SourceCommit              string              `json:"source_commit"`
	CorpusDatasetID           string              `json:"corpus_dataset_id"`
	CorpusManifestSHA256      string              `json:"corpus_manifest_sha256"`
	IdentityLockSHA256        string              `json:"identity_lock_sha256"`
	TreatmentSource           string              `json:"treatment_source"`
	TreatmentSourceSHA256     string              `json:"treatment_source_sha256"`
	ProfilePolicySource       string              `json:"profile_policy_source"`
	ProfilePolicySourceSHA256 string              `json:"profile_policy_source_sha256"`
	ProviderProtocol          string              `json:"provider_protocol"`
	SelectionPolicy           string              `json:"selection_policy"`
	Arms                      []string            `json:"arms"`
	Phases                    []string            `json:"phases"`
	ReplicatesPerArm          uint32              `json:"replicates_per_arm"`
	Tasks                     []CanaryTask        `json:"tasks"`
	FailurePolicy             CanaryFailurePolicy `json:"failure_policy"`
	Budgets                   CanaryBudgets       `json:"budgets"`
}

type CanaryTask struct {
	ID                string            `json:"id"`
	SHA256            string            `json:"sha256"`
	Stratum           string            `json:"stratum"`
	Role              string            `json:"role"`
	ExpectedAdmission map[string]string `json:"expected_admission"`
}

type CanaryFailurePolicy struct {
	ModelProgramIsTerminalTrial    bool   `json:"model_program_is_terminal_trial"`
	SingleModelProgramFailureStops bool   `json:"single_model_program_failure_stops"`
	PerTaskPromptTuning            bool   `json:"per_task_prompt_tuning"`
	InfrastructureRepairLimit      uint32 `json:"infrastructure_repair_limit"`
}

type CanaryBudgets struct {
	PlannedCells         uint32 `json:"planned_cells"`
	MaxCLITokens         uint64 `json:"max_cli_tokens"`
	MaxContinuousMinutes uint32 `json:"max_continuous_minutes"`
}

func LoadDevelopmentCanaryPlan(corpusRoot, planPath string) (DevelopmentCanaryPlan, error) {
	info, err := os.Lstat(planPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxManifest {
		return DevelopmentCanaryPlan{}, ErrDevelopmentCanaryPlan
	}
	data, err := os.ReadFile(planPath)
	if err != nil || int64(len(data)) != info.Size() {
		return DevelopmentCanaryPlan{}, ErrDevelopmentCanaryPlan
	}
	var plan DevelopmentCanaryPlan
	if decodeStrict(data, &plan) != nil {
		return DevelopmentCanaryPlan{}, ErrDevelopmentCanaryPlan
	}
	corpus, err := Load(corpusRoot)
	if err != nil || validateDevelopmentCanaryPlan(corpusRoot, corpus, plan) != nil {
		return DevelopmentCanaryPlan{}, ErrDevelopmentCanaryPlan
	}
	return plan, nil
}

func validateDevelopmentCanaryPlan(root string, corpus *Corpus, plan DevelopmentCanaryPlan) error {
	if corpus == nil || plan.SchemaVersion != "placement-development-canary-plan/v1" ||
		plan.Status != "preregistered_pre_canary" || plan.SourceCommit != "01e95272ebce599e9e2b5513c38f3fa4c6878885" ||
		plan.CorpusDatasetID != corpus.Manifest.DatasetID || plan.CorpusManifestSHA256 != "sha256:f756770de51bda5f9dbf78ece677a25bda7cbdd427a66e48409af0cb6db2116a" ||
		plan.ProviderProtocol != "codex-jsonl-code-proposal-v2" || plan.SelectionPolicy == "" ||
		!reflect.DeepEqual(plan.Arms, []string{"direct", "pysolate", "computer"}) ||
		!reflect.DeepEqual(plan.Phases, []string{"scripted_parity", "model"}) || plan.ReplicatesPerArm != 1 ||
		len(plan.Tasks) != 6 || !plan.FailurePolicy.ModelProgramIsTerminalTrial ||
		plan.FailurePolicy.SingleModelProgramFailureStops || plan.FailurePolicy.PerTaskPromptTuning ||
		plan.FailurePolicy.InfrastructureRepairLimit != 1 || plan.Budgets.PlannedCells != 18 ||
		plan.Budgets.MaxCLITokens != 2_500_000 || plan.Budgets.MaxContinuousMinutes != 120 {
		return ErrDevelopmentCanaryPlan
	}
	if digestRegular(filepath.Join(root, "identity-lock.json")) != plan.IdentityLockSHA256 {
		return ErrDevelopmentCanaryPlan
	}
	repositoryRoot := filepath.Clean(filepath.Join(root, "..", "..", "..", ".."))
	if plan.TreatmentSource != "cmd/apyrun-placement-program-canary/main.go" ||
		digestRegular(filepath.Join(repositoryRoot, filepath.FromSlash(plan.TreatmentSource))) != plan.TreatmentSourceSHA256 ||
		plan.ProfilePolicySource != "eval/placement/stdlib_profile.go" ||
		digestRegular(filepath.Join(repositoryRoot, filepath.FromSlash(plan.ProfilePolicySource))) != plan.ProfilePolicySourceSHA256 {
		return ErrDevelopmentCanaryPlan
	}
	manifestByID := make(map[string]ManifestTask, len(corpus.Manifest.Tasks))
	taskByID := make(map[string]Task, len(corpus.Tasks))
	for index, entry := range corpus.Manifest.Tasks {
		manifestByID[entry.ID] = entry
		taskByID[entry.ID] = corpus.Tasks[index]
	}
	seenIDs, seenRoles := map[string]bool{}, map[string]bool{}
	strata := map[string]int{}
	for _, selected := range plan.Tasks {
		entry, entryOK := manifestByID[selected.ID]
		task, taskOK := taskByID[selected.ID]
		if !entryOK || !taskOK || seenIDs[selected.ID] || seenRoles[selected.Role] || selected.Role == "" ||
			entry.Split != "development" || task.Split != "development" || !task.ModelVisible ||
			selected.SHA256 != entry.SHA256 || selected.Stratum != entry.Stratum || len(selected.ExpectedAdmission) != 3 {
			return ErrDevelopmentCanaryPlan
		}
		seenIDs[selected.ID], seenRoles[selected.Role] = true, true
		strata[selected.Stratum]++
		for _, arm := range plan.Arms {
			if selected.ExpectedAdmission[arm] != task.Admission[arm].Status {
				return ErrDevelopmentCanaryPlan
			}
		}
	}
	if !reflect.DeepEqual(strata, map[string]int{
		"direct_favored": 1, "pysolate_favored": 2, "mixed_capability": 1,
		"computer_favored": 1, "boundary": 1,
	}) {
		return ErrDevelopmentCanaryPlan
	}
	return nil
}

func digestRegular(path string) string {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) != info.Size() {
		return ""
	}
	return digest(data)
}
