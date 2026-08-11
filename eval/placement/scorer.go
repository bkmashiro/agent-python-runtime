package placement

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
)

var ErrTrialResult = errors.New("invalid placement trial result")

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type SemanticCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ObservedAdmission struct {
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	BeforeProvider bool   `json:"before_provider"`
}

type ExecutionEvidence struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

type UsageEvidence struct {
	ProviderCalls uint32 `json:"provider_calls"`
	InputTokens   uint64 `json:"input_tokens,omitempty"`
	OutputTokens  uint64 `json:"output_tokens,omitempty"`
	TotalTokens   uint64 `json:"total_tokens"`
	ToolCalls     uint32 `json:"tool_calls,omitempty"`
}

type LifecycleEvidence struct {
	WallTimeMillis uint64 `json:"wall_time_millis"`
	StartCount     uint32 `json:"start_count"`
	WorkspaceBytes uint64 `json:"workspace_bytes,omitempty"`
}

type TrialResult struct {
	SchemaVersion         string            `json:"schema_version"`
	TrialID               string            `json:"trial_id"`
	TaskID                string            `json:"task_id"`
	TaskSHA256            string            `json:"task_sha256"`
	Arm                   string            `json:"arm"`
	Mode                  string            `json:"mode"`
	Replicate             uint32            `json:"replicate"`
	SourceCommit          string            `json:"source_commit"`
	TreatmentSHA256       string            `json:"treatment_sha256"`
	RuntimeIdentitySHA256 string            `json:"runtime_identity_sha256"`
	Admission             ObservedAdmission `json:"admission"`
	Execution             ExecutionEvidence `json:"execution"`
	ObservedFinalState    json.RawMessage   `json:"observed_final_state,omitempty"`
	ObservedEffects       []SemanticCall    `json:"observed_effects,omitempty"`
	Usage                 UsageEvidence     `json:"usage"`
	Lifecycle             LifecycleEvidence `json:"lifecycle"`
}

type ScoreResult struct {
	Pass              bool   `json:"pass"`
	ExpectedRejection bool   `json:"expected_rejection"`
	AdmissionPass     bool   `json:"admission_pass"`
	ExecutionPass     bool   `json:"execution_pass"`
	FinalStatePass    bool   `json:"final_state_pass"`
	EffectPass        bool   `json:"effect_pass"`
	FailureLayer      string `json:"failure_layer,omitempty"`
}

func ValidateTrialResult(result TrialResult) error {
	if result.SchemaVersion != "placement-trial-result/v1" || result.TrialID == "" || result.TaskID == "" ||
		!validDigest(result.TaskSHA256) || !commitPattern.MatchString(result.SourceCommit) ||
		!validDigest(result.TreatmentSHA256) || !validDigest(result.RuntimeIdentitySHA256) ||
		(result.Arm != "direct" && result.Arm != "pysolate" && result.Arm != "computer") ||
		(result.Mode != "scripted" && result.Mode != "model") || result.Replicate == 0 ||
		(result.Admission.Status != "admitted" && result.Admission.Status != "rejected") || result.Admission.Reason == "" ||
		(result.Execution.Status != "completed" && result.Execution.Status != "failed" && result.Execution.Status != "not_started") {
		return ErrTrialResult
	}
	if result.Mode == "model" && result.Admission.Status == "admitted" &&
		(result.Usage.ProviderCalls == 0 || result.Usage.TotalTokens == 0) {
		return ErrTrialResult
	}
	if result.Admission.Status == "admitted" && result.Execution.Status == "completed" && len(bytes.TrimSpace(result.ObservedFinalState)) == 0 {
		return ErrTrialResult
	}
	if result.Lifecycle.StartCount > 64 {
		return ErrTrialResult
	}
	return nil
}

func Score(task Task, result TrialResult) (ScoreResult, error) {
	if result.TaskID != task.ID {
		return ScoreResult{}, ErrTrialResult
	}
	expected, ok := task.Admission[result.Arm]
	if !ok {
		return ScoreResult{}, ErrTrialResult
	}
	if expected.Status == "rejected" {
		pass := result.Admission.Status == "rejected" && result.Admission.BeforeProvider &&
			result.Usage.ProviderCalls == 0 && len(result.ObservedEffects) == 0 && result.Execution.Status == "not_started"
		score := ScoreResult{Pass: pass, ExpectedRejection: true, AdmissionPass: pass, ExecutionPass: pass, FinalStatePass: pass, EffectPass: pass}
		if !pass {
			score.FailureLayer = "admission"
		}
		return score, nil
	}
	if result.Admission.Status != "admitted" {
		return ScoreResult{FailureLayer: "admission"}, nil
	}
	score := ScoreResult{AdmissionPass: true}
	if result.Execution.Status != "completed" {
		score.FailureLayer = "execution"
		return score, nil
	}
	score.ExecutionPass = true
	finalPass, err := canonicalEqual(task.Oracle.FinalState, result.ObservedFinalState)
	if err != nil {
		return ScoreResult{}, ErrTrialResult
	}
	score.FinalStatePass = finalPass
	if !finalPass {
		score.FailureLayer = "oracle_final_state"
		return score, nil
	}
	effectPass, err := scoreEffects(task.Oracle.EffectContract, result.ObservedEffects)
	if err != nil {
		return ScoreResult{}, ErrTrialResult
	}
	score.EffectPass = effectPass
	if !effectPass {
		score.FailureLayer = "oracle_effect"
		return score, nil
	}
	score.Pass = true
	return score, nil
}

type exactEffectContract struct {
	Kind             string          `json:"kind"`
	Required         []SemanticCall  `json:"required"`
	Forbidden        []string        `json:"forbidden"`
	OrderingEdges    [][]int         `json:"ordering_edges"`
	RequiredStatus   string          `json:"required_status"`
	ForbiddenEffects []string        `json:"forbidden_effects"`
	Oracle           json.RawMessage `json:"oracle"`
}

func scoreEffects(raw json.RawMessage, observed []SemanticCall) (bool, error) {
	var contract exactEffectContract
	if decodeStrict(raw, &contract) != nil || contract.Kind == "" {
		return false, ErrTrialResult
	}
	switch contract.Kind {
	case "exact_semantic_calls":
		if len(contract.Required) != len(observed) {
			return false, nil
		}
		for index := range contract.Required {
			if contract.Required[index].Name != observed[index].Name {
				return false, nil
			}
			equal, err := canonicalEqual(contract.Required[index].Arguments, observed[index].Arguments)
			if err != nil || !equal {
				return false, err
			}
		}
		for _, edge := range contract.OrderingEdges {
			if len(edge) != 2 || edge[0] < 0 || edge[1] < 0 || edge[0] >= len(observed) || edge[1] >= len(observed) || edge[0] >= edge[1] {
				return false, ErrTrialResult
			}
		}
		return true, nil
	case "bfcl_expected_calls":
		return scoreBFCL(contract.Oracle, observed)
	case "admission_rejection":
		return len(observed) == 0 && contract.RequiredStatus == "rejected", nil
	default:
		return false, ErrTrialResult
	}
}

type bfclOracle struct {
	Kind  string            `json:"kind"`
	Turns []json.RawMessage `json:"turns"`
}

type bfclExpected struct {
	name    string
	exact   json.RawMessage
	options map[string][]json.RawMessage
}

func scoreBFCL(raw json.RawMessage, observed []SemanticCall) (bool, error) {
	var oracle bfclOracle
	if decodeStrict(raw, &oracle) != nil || oracle.Kind != "expected_call_trace" {
		return false, ErrTrialResult
	}
	expected := make([]bfclExpected, 0)
	for _, turn := range oracle.Turns {
		trimmed := bytes.TrimSpace(turn)
		if len(trimmed) == 0 {
			return false, ErrTrialResult
		}
		if trimmed[0] == '[' {
			var calls []SemanticCall
			if decodeStrict(turn, &calls) != nil {
				return false, ErrTrialResult
			}
			for _, call := range calls {
				expected = append(expected, bfclExpected{name: call.Name, exact: call.Arguments})
			}
			continue
		}
		var alternatives map[string]map[string][]json.RawMessage
		if decodeStrict(turn, &alternatives) != nil || len(alternatives) == 0 {
			return false, ErrTrialResult
		}
		names := make([]string, 0, len(alternatives))
		for name := range alternatives {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			expected = append(expected, bfclExpected{name: name, options: alternatives[name]})
		}
	}
	if len(expected) != len(observed) {
		return false, nil
	}
	for index, want := range expected {
		got := observed[index]
		if got.Name != want.name {
			return false, nil
		}
		if want.exact != nil {
			equal, err := canonicalEqual(want.exact, got.Arguments)
			if err != nil || !equal {
				return false, err
			}
			continue
		}
		var actual map[string]json.RawMessage
		if decodeStrict(got.Arguments, &actual) != nil || len(actual) != len(want.options) {
			return false, nil
		}
		for key, options := range want.options {
			value, ok := actual[key]
			if !ok {
				return false, nil
			}
			matched := false
			for _, option := range options {
				equal, err := canonicalEqual(option, value)
				if err != nil {
					return false, err
				}
				matched = matched || equal
			}
			if !matched {
				return false, nil
			}
		}
	}
	return true, nil
}

func canonicalEqual(left, right json.RawMessage) (bool, error) {
	canonical := func(raw json.RawMessage) ([]byte, error) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if decoder.Decode(&struct{}{}) == nil {
			return nil, ErrTrialResult
		}
		return json.Marshal(value)
	}
	leftCanonical, err := canonical(left)
	if err != nil {
		return false, err
	}
	rightCanonical, err := canonical(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftCanonical, rightCanonical), nil
}
