package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

var ErrMechanismBaseline = errors.New("invalid agentic mechanism baseline")

var pythonToolIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const mechanismBaselineStatus = "mechanism_only_not_model_evaluation"

type MechanismBaselineIdentity struct {
	RepositoryCommit      string
	GuestArtifactSHA256   string
	DatasetManifestSHA256 string
	DatasetPlanSHA256     string
}

type MechanismSurfaceResult struct {
	HostRoundTrips   int  `json:"host_round_trips"`
	PythonRuns       int  `json:"python_runs"`
	CapabilityCalls  int  `json:"capability_calls"`
	TracePassed      bool `json:"trace_passed"`
	FinalStatePassed bool `json:"final_state_passed"`
}

type MechanismTaskResult struct {
	TaskID        string                 `json:"task_id"`
	Archetype     string                 `json:"archetype"`
	ExpectedCalls int                    `json:"expected_calls"`
	Direct        MechanismSurfaceResult `json:"direct"`
	Python        MechanismSurfaceResult `json:"python"`
}

type MechanismSummary struct {
	TaskCount             int  `json:"task_count"`
	DirectHostRoundTrips  int  `json:"direct_host_round_trips"`
	PythonRuns            int  `json:"python_runs"`
	PythonCapabilityCalls int  `json:"python_capability_calls"`
	AllOraclesPassed      bool `json:"all_oracles_passed"`
}

type MechanismBaselineReport struct {
	SchemaVersion         string                `json:"schema_version"`
	Status                string                `json:"status"`
	AdmissionMode         string                `json:"admission_mode"`
	RepositoryCommit      string                `json:"repository_commit"`
	GuestArtifactSHA256   string                `json:"guest_artifact_sha256"`
	DatasetManifestSHA256 string                `json:"dataset_manifest_sha256"`
	DatasetPlanSHA256     string                `json:"dataset_plan_sha256"`
	Tasks                 []MechanismTaskResult `json:"tasks"`
	Summary               MechanismSummary      `json:"summary"`
	ProhibitedClaims      []string              `json:"prohibited_claims"`
}

func BuildScriptedOracleProgram(task Task) (string, int, error) {
	if task.Track != "stateful_local_tools" || len(task.Interaction.Turns) != 1 {
		return "", 0, ErrMechanismBaseline
	}
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" || len(oracle.Turns) != 1 || len(oracle.Turns[0]) == 0 {
		return "", 0, ErrMechanismBaseline
	}
	toolNames := make(map[string]struct{}, len(oracle.Turns[0]))
	for _, call := range oracle.Turns[0] {
		var arguments any
		if !pythonToolIdentifier.MatchString(call.Name) || len(call.Arguments) == 0 || decodeUseNumber(call.Arguments, &arguments) != nil {
			return "", 0, ErrMechanismBaseline
		}
		toolNames[call.Name] = struct{}{}
	}
	names := make([]string, 0, len(toolNames))
	for name := range toolNames {
		names = append(names, name)
	}
	sort.Strings(names)
	var program strings.Builder
	program.WriteString("import json\nfrom host_tools import ")
	program.WriteString(strings.Join(names, ", "))
	program.WriteByte('\n')
	for _, call := range oracle.Turns[0] {
		var arguments any
		if decodeUseNumber(call.Arguments, &arguments) != nil {
			return "", 0, ErrMechanismBaseline
		}
		canonicalArguments, err := json.Marshal(arguments)
		if err != nil {
			return "", 0, ErrMechanismBaseline
		}
		program.WriteString(call.Name)
		program.WriteString("(**json.loads(")
		program.WriteString(strconv.Quote(string(canonicalArguments)))
		program.WriteString("))\n")
	}
	program.WriteString("result = {\"status\": \"completed\"}\n")
	return program.String(), len(oracle.Turns[0]), nil
}

func RunMechanismBaseline(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, data *RoutingDataset, identity MechanismBaselineIdentity) (MechanismBaselineReport, error) {
	if len(wasm) < 8 || data == nil || len(data.Tasks) != 6 || !validLowerHex(identity.RepositoryCommit, 40) ||
		!validDigest(identity.GuestArtifactSHA256) || !validDigest(identity.DatasetManifestSHA256) || !validDigest(identity.DatasetPlanSHA256) {
		return MechanismBaselineReport{}, ErrMechanismBaseline
	}
	report := MechanismBaselineReport{
		SchemaVersion: "agentic-mechanism-baseline/v1", Status: mechanismBaselineStatus,
		AdmissionMode:    "internal_legacy_no_manifest",
		RepositoryCommit: identity.RepositoryCommit, GuestArtifactSHA256: identity.GuestArtifactSHA256,
		DatasetManifestSHA256: identity.DatasetManifestSHA256, DatasetPlanSHA256: identity.DatasetPlanSHA256,
		Tasks:            make([]MechanismTaskResult, 0, len(data.Tasks)),
		ProhibitedClaims: []string{"model_quality", "token_reduction", "latency_reduction", "computer_replacement_rate", "profile_qualified_placement", "decision_eligible"},
	}
	for _, task := range data.Tasks {
		program, expectedCalls, err := BuildScriptedOracleProgram(task)
		if err != nil {
			return MechanismBaselineReport{}, err
		}
		directRuntime, err := NewToolRuntime(task)
		if err != nil || directRuntime.SetTurn(0) != nil {
			return MechanismBaselineReport{}, ErrMechanismBaseline
		}
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return MechanismBaselineReport{}, ErrMechanismBaseline
		}
		for index, call := range oracle.Turns[0] {
			if _, err := directRuntime.InvokeDirect(ctx, "mechanism-direct-"+task.ID+fmt.Sprintf("-%d", index), fmt.Sprintf("call-%d", index), call.Name, call.Arguments); err != nil {
				return MechanismBaselineReport{}, err
			}
		}
		directScore, err := ScoreStateful(task, directRuntime.Trace(), directRuntime.FileSystem())
		if err != nil {
			return MechanismBaselineReport{}, err
		}

		pythonRuntime, err := NewToolRuntime(task)
		if err != nil || pythonRuntime.SetTurn(0) != nil {
			return MechanismBaselineReport{}, ErrMechanismBaseline
		}
		executor, err := NewWASIPythonExecutor(ctx, wasm, config, pythonRuntime)
		if err != nil {
			return MechanismBaselineReport{}, err
		}
		pythonResult, executeErr := executor.Execute(ctx, "mechanism-python-"+task.ID, program, uint32(expectedCalls))
		closeErr := executor.Close(context.Background())
		if executeErr != nil || closeErr != nil || !pythonResult.Success || int(pythonResult.CapabilityCalls) != expectedCalls {
			return MechanismBaselineReport{}, ErrMechanismBaseline
		}
		pythonScore, err := ScoreStateful(task, pythonRuntime.Trace(), pythonRuntime.FileSystem())
		if err != nil {
			return MechanismBaselineReport{}, err
		}
		taskResult := MechanismTaskResult{
			TaskID: task.ID, Archetype: data.Archetypes[task.ID], ExpectedCalls: expectedCalls,
			Direct: MechanismSurfaceResult{HostRoundTrips: expectedCalls, TracePassed: directScore.TracePassed, FinalStatePassed: directScore.FinalStatePassed},
			Python: MechanismSurfaceResult{PythonRuns: 1, CapabilityCalls: int(pythonResult.CapabilityCalls), TracePassed: pythonScore.TracePassed, FinalStatePassed: pythonScore.FinalStatePassed},
		}
		report.Tasks = append(report.Tasks, taskResult)
		report.Summary.DirectHostRoundTrips += expectedCalls
		report.Summary.PythonRuns++
		report.Summary.PythonCapabilityCalls += int(pythonResult.CapabilityCalls)
	}
	report.Summary.TaskCount = len(report.Tasks)
	report.Summary.AllOraclesPassed = true
	for _, task := range report.Tasks {
		if !task.Direct.TracePassed || !task.Direct.FinalStatePassed || !task.Python.TracePassed || !task.Python.FinalStatePassed {
			report.Summary.AllOraclesPassed = false
		}
	}
	if err := report.Validate(); err != nil {
		return MechanismBaselineReport{}, err
	}
	return report, nil
}

func (report MechanismBaselineReport) Validate() error {
	if report.SchemaVersion != "agentic-mechanism-baseline/v1" || report.Status != mechanismBaselineStatus || report.AdmissionMode != "internal_legacy_no_manifest" ||
		!validLowerHex(report.RepositoryCommit, 40) || !validDigest(report.GuestArtifactSHA256) || !validDigest(report.DatasetManifestSHA256) || !validDigest(report.DatasetPlanSHA256) ||
		len(report.Tasks) != 6 || len(report.ProhibitedClaims) != 6 {
		return ErrMechanismBaseline
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	summary := MechanismSummary{TaskCount: len(report.Tasks), AllOraclesPassed: true}
	for _, task := range report.Tasks {
		if !routingTaskIDPattern.MatchString(task.TaskID) || seen[task.TaskID] || !allowedRoutingArchetype(task.Archetype) || task.ExpectedCalls <= 0 ||
			task.Direct.HostRoundTrips != task.ExpectedCalls || task.Direct.PythonRuns != 0 || task.Direct.CapabilityCalls != 0 ||
			task.Python.HostRoundTrips != 0 || task.Python.PythonRuns != 1 || task.Python.CapabilityCalls != task.ExpectedCalls {
			return ErrMechanismBaseline
		}
		seen[task.TaskID] = true
		counts[task.Archetype]++
		summary.DirectHostRoundTrips += task.Direct.HostRoundTrips
		summary.PythonRuns += task.Python.PythonRuns
		summary.PythonCapabilityCalls += task.Python.CapabilityCalls
		if !task.Direct.TracePassed || !task.Direct.FinalStatePassed || !task.Python.TracePassed || !task.Python.FinalStatePassed {
			summary.AllOraclesPassed = false
		}
	}
	if counts["direct_favored"] != 2 || counts["python_favored"] != 2 || counts["boundary"] != 2 || summary != report.Summary {
		return ErrMechanismBaseline
	}
	wantClaims := []string{"model_quality", "token_reduction", "latency_reduction", "computer_replacement_rate", "profile_qualified_placement", "decision_eligible"}
	for i := range wantClaims {
		if report.ProhibitedClaims[i] != wantClaims[i] {
			return ErrMechanismBaseline
		}
	}
	return nil
}
