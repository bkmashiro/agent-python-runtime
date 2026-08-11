package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/placement"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const model = "gpt-5.3-codex-spark"

var limits = agentic.TrialLimits{
	MaxProviderCalls: 1, MaxToolCalls: 1, MaxPythonRuns: 1,
	MaxInputTokens: 500_000, MaxOutputTokens: 65_536, MaxTotalTokens: 565_536,
	MaxOutputTokensPerCall: 8_192,
}

type programProposal struct {
	Code    string   `json:"code"`
	Imports []string `json:"imports"`
}

type computerProposal struct {
	Source string `json:"source"`
}

type canaryResult struct {
	SchemaVersion    string                     `json:"schema_version"`
	TaskID           string                     `json:"task_id"`
	Arm              string                     `json:"arm"`
	Model            string                     `json:"model"`
	TreatmentSHA256  string                     `json:"treatment_sha256"`
	RepositoryCommit string                     `json:"repository_commit"`
	ProviderCalls    uint32                     `json:"provider_calls"`
	Usage            provider.Usage             `json:"usage"`
	Exchange         []agentic.ExchangeEvidence `json:"exchange"`
	ProposalSHA256   string                     `json:"proposal_sha256"`
	Execution        map[string]any             `json:"execution"`
	Score            map[string]any             `json:"score"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("apyrun-placement-program-canary", flag.ContinueOnError)
	flags.SetOutput(bytes.NewBuffer(nil))
	arm := flags.String("arm", "", "pysolate|computer")
	datasetRoot := flags.String("dataset", "", "routing diagnostic dataset")
	taskID := flags.String("task", "rd-003", "fixed development diagnostic task")
	codexPath := flags.String("codex", "", "Codex CLI")
	workdir := flags.String("workdir", "", "read-only model working directory")
	repositoryCommit := flags.String("repository-commit", "", "exact repository commit")
	guestDir := flags.String("guest-dir", "", "verified Guest bundle")
	computerCheckout := flags.String("computer-checkout", "", "pinned Cloudflare Computer checkout")
	computerAdapter := flags.String("computer-adapter", "", "Computer adapter script")
	pythonPath := flags.String("python", "/Users/yuzhe/.local/bin/python3.11", "acceptance Python")
	out := flags.String("out", "", "new result path")
	timeoutText := flags.String("timeout", "180s", "provider timeout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (*arm != "pysolate" && *arm != "computer") ||
		*datasetRoot == "" || *codexPath == "" || *workdir == "" || *repositoryCommit == "" || *out == "" || *taskID != "rd-003" {
		return errors.New("invalid placement canary arguments")
	}
	if *arm == "pysolate" && *guestDir == "" {
		return errors.New("missing Guest bundle")
	}
	if *arm == "computer" && (*computerCheckout == "" || *computerAdapter == "") {
		return errors.New("missing Computer comparator")
	}
	timeout, err := time.ParseDuration(*timeoutText)
	if err != nil || timeout <= 0 || len(*repositoryCommit) != 40 {
		return errors.New("invalid placement canary identity")
	}
	if err := requireNewFile(*out); err != nil {
		return err
	}
	dataset, err := agentic.LoadRoutingDataset(*datasetRoot)
	if err != nil {
		return err
	}
	var task *agentic.Task
	for index := range dataset.Tasks {
		if dataset.Tasks[index].ID == *taskID {
			task = &dataset.Tasks[index]
			break
		}
	}
	if task == nil || task.Split != "dev" || task.Track != "stateful_local_tools" {
		return errors.New("canary task unavailable")
	}
	adapter, err := provider.NewCodexCLIAdapter(*codexPath, model, *workdir, timeout)
	if err != nil {
		return err
	}
	session, err := agentic.NewResponsesSession(adapter, model, limits)
	if err != nil {
		return err
	}
	instructions, submissionTool := treatment(*arm, *task)
	parsed, err := session.Exchange(ctx,
		[]any{
			map[string]any{"role": "developer", "content": instructions},
			map[string]any{"role": "user", "content": taskText(*task)},
		},
		[]map[string]any{submissionTool}, "required", false,
		map[string]string{submissionTool["name"].(string): submissionTool["name"].(string)},
	)
	if err != nil || parsed.HasMessage || parsed.Refused || len(parsed.Calls) != 1 {
		return errors.New("provider did not return exactly one program proposal")
	}
	result := canaryResult{
		SchemaVersion: "placement-program-canary/v1", TaskID: task.ID, Arm: *arm, Model: model,
		TreatmentSHA256: digest([]byte(instructions)), RepositoryCommit: *repositoryCommit,
		ProviderCalls: session.ProviderCalls(), Usage: session.Usage(), Exchange: session.Evidence(),
	}
	if *arm == "pysolate" {
		var proposal programProposal
		if decodeStrict(parsed.Calls[0].Arguments, &proposal) != nil || validatePythonProposal(proposal) != nil {
			return errors.New("invalid Python proposal")
		}
		result.ProposalSHA256 = digest([]byte(proposal.Code))
		execution, score, executeErr := runPysolate(ctx, *task, proposal, *guestDir)
		if executeErr != nil {
			return executeErr
		}
		result.Execution, result.Score = execution, score
	} else {
		var proposal computerProposal
		if decodeStrict(parsed.Calls[0].Arguments, &proposal) != nil || validateComputerProposal(proposal) != nil {
			return errors.New("invalid JavaScript proposal")
		}
		result.ProposalSHA256 = digest([]byte(proposal.Source))
		execution, score, executeErr := runComputer(ctx, proposal, *computerCheckout, *computerAdapter, *pythonPath)
		if executeErr != nil {
			return executeErr
		}
		result.Execution, result.Score = execution, score
	}
	return writeNewJSON(*out, result)
}

func treatment(arm string, task agentic.Task) (string, map[string]any) {
	var toolDescriptions []map[string]any
	for _, tool := range task.Tools {
		var parameters any
		_ = json.Unmarshal(tool.Parameters, &parameters)
		toolDescriptions = append(toolDescriptions, map[string]any{"name": tool.Name, "parameters": parameters, "response": json.RawMessage(tool.Response)})
	}
	toolJSON, _ := json.Marshal(toolDescriptions)
	if arm == "pysolate" {
		instructions := `Generate exactly one bounded static Python program for a profile-qualified Pysolate Guest. Return it only through submit_python. The listed task tools are already trusted, prebound Python functions with the same names and keyword arguments; call them directly. Do not import host_tools. Imports, if any, must be consecutive static absolute imports at the top and imports must list their exact top-level roots. Do not use exec, eval, __import__, importlib, reflection, shell, subprocess, files outside the tools, network, credentials, retry, fallback, or markdown fences. Read data inside the program, perform the requested transformation, and set result to a JSON object. Respect the user-requested tool sequence and do not add redundant effects. Tool contracts: ` + string(toolJSON)
		return instructions, map[string]any{
			"type": "function", "name": "submit_python", "description": "Submit one static bounded Python program.", "strict": false,
			"parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"code", "imports"}, "properties": map[string]any{
				"code": map[string]any{"type": "string"}, "imports": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 16},
			}},
		}
	}
	instructions := `Generate exactly one ECMAScript module for the pinned Cloudflare Computer Worker JavaScript backend and return it only through submit_javascript. The module must default-export an async function. The isolated workspace root is /workspace. The initial file is /workspace/alex/Documents/metrics.csv. Read it at runtime with node:fs/promises, perform the requested transformation, and write /workspace/alex/Documents/high_value_rows.txt. No network, credentials, process, shell, package install, fallback, retry, dynamic code evaluation, markdown fences, or additional files. Return a small JSON-compatible status value.`
	return instructions, map[string]any{
		"type": "function", "name": "submit_javascript", "description": "Submit one bounded Worker JavaScript module.", "strict": false,
		"parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"source"}, "properties": map[string]any{"source": map[string]any{"type": "string"}}},
	}
}

func taskText(task agentic.Task) string {
	var parts []string
	for _, turn := range task.Interaction.Turns {
		var messages []struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(turn, &messages) == nil {
			for _, message := range messages {
				parts = append(parts, message.Content)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func runPysolate(ctx context.Context, task agentic.Task, proposal programProposal, guestDir string) (map[string]any, map[string]any, error) {
	bundle, err := placement.LoadGuestBundle(guestDir, []string{"json"}, placement.GuestIdentityExpectation{
		ArtifactSHA256: "sha256:4078dbcec0307e5636c86b84523b8349a557db115bfac7569ff5d003b08ceadb",
		ManifestSHA256: "sha256:b6baa6f5adb27263ef586faed897cde42c1815b4ce7c415333696800b3bbb6a6",
	})
	if err != nil {
		return nil, nil, err
	}
	tools, err := agentic.NewToolRuntime(task)
	if err != nil {
		return nil, nil, err
	}
	if err := tools.SetTurn(0); err != nil {
		return nil, nil, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bundle.Profile
	executor, err := agentic.NewWASIPythonExecutor(ctx, bundle.WASM, config, tools)
	if err != nil {
		return nil, nil, err
	}
	defer executor.Close(context.Background())
	started := time.Now()
	runResult, err := executor.ExecuteProfileQualified(ctx, "placement-canary-pysolate", proposal.Code, "base", proposal.Imports, 8)
	wall := time.Since(started)
	if err != nil || !runResult.Success {
		return nil, nil, errors.New("profile-qualified Pysolate execution failed")
	}
	score, err := agentic.ScoreStateful(task, tools.Trace(), tools.FileSystem())
	if err != nil {
		return nil, nil, err
	}
	execution := map[string]any{
		"status": "completed", "wall_ns": wall.Nanoseconds(), "backend": runResult.Backend, "reset_mode": runResult.ResetMode,
		"profile": "base", "artifact_sha256": bundle.Identity.ArtifactSHA256, "manifest_sha256": bundle.Identity.ManifestSHA256,
		"capability_calls": runResult.CapabilityCalls, "run_plan_bound": bytes.Contains(runResult.RawResponse, []byte(`"run_plan"`)),
	}
	return execution, map[string]any{"strict_effect_passed": score.TracePassed, "final_state_passed": score.FinalStatePassed, "passed": score.TracePassed && score.FinalStatePassed}, nil
}

func runComputer(ctx context.Context, proposal computerProposal, checkout, adapterPath, pythonPath string) (map[string]any, map[string]any, error) {
	request := map[string]any{
		"schema_version": "cloudflare-computer-local-trial/v1", "workspace_id": "placement-model-canary",
		"files":  map[string]string{"alex/Documents/metrics.csv": "alpha,5\nbeta,2\ngamma,9\n"},
		"source": proposal.Source, "input": map[string]any{}, "output_files": []string{"alex/Documents/high_value_rows.txt"}, "tool_fixture": nil,
	}
	directory, err := os.MkdirTemp("", "placement-computer-model-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(directory)
	requestPath := filepath.Join(directory, "request.json")
	resultPath := filepath.Join(directory, "result.json")
	if err := os.WriteFile(requestPath, mustJSON(request), 0o600); err != nil {
		return nil, nil, err
	}
	command := exec.CommandContext(ctx, pythonPath, adapterPath, "run", "--checkout", checkout, "--request", requestPath, "--result", resultPath, "--port", "8794")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("Computer adapter failed: %s", strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(resultPath)
	if err != nil || len(data) > 1<<20 {
		return nil, nil, errors.New("invalid Computer result")
	}
	var result map[string]any
	if decodeStrict(data, &result) != nil {
		return nil, nil, errors.New("invalid Computer result")
	}
	outputs, ok := result["output_files"].(map[string]any)
	value, valueOK := outputs["alex/Documents/high_value_rows.txt"].(string)
	passed := ok && valueOK && value == "alpha,gamma"
	execution := map[string]any{
		"status": nestedString(result, "execution", "status"), "exit_code": nestedNumber(result, "execution", "exitCode"),
		"identity": result["identity"], "lifecycle": result["lifecycle"], "request_sha256": result["request_sha256"],
	}
	return execution, map[string]any{"strict_effect_passed": false, "effect_status": "native_workspace_trace_not_semantically_comparable", "final_state_passed": passed, "passed": passed}, nil
}

func validatePythonProposal(proposal programProposal) error {
	if !validSource(proposal.Code) || proposal.Imports == nil || len(proposal.Imports) > 16 {
		return errors.New("invalid")
	}
	seen := map[string]bool{}
	for _, item := range proposal.Imports {
		if item != "json" || seen[item] {
			return errors.New("invalid")
		}
		seen[item] = true
	}
	return nil
}
func validateComputerProposal(proposal computerProposal) error {
	if !validSource(proposal.Source) {
		return errors.New("invalid")
	}
	return nil
}
func validSource(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && len(value) <= 64*1024 && !strings.ContainsRune(value, 0)
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing JSON")
	}
	return nil
}
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
func requireNewFile(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("invalid output")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("output exists")
	}
	return nil
}
func writeNewJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
func nestedString(value map[string]any, object, field string) string {
	child, _ := value[object].(map[string]any)
	result, _ := child[field].(string)
	return result
}
func nestedNumber(value map[string]any, object, field string) float64 {
	child, _ := value[object].(map[string]any)
	result, _ := child[field].(float64)
	return result
}
