package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const exitEscalationRequired = 3

const workspaceToolPrelude = `
import json as _host_json
import _agent_runtime_host as _host_bridge
_host_call_sequence = 0

def _workspace_call(capability, arguments):
    global _host_call_sequence
    _host_call_sequence += 1
    request = {
        "call_id": "workspace-" + str(_host_call_sequence),
        "capability": capability,
        "arguments": arguments,
    }
    response = _host_json.loads(_host_bridge.call(_host_json.dumps(request, separators=(",", ":"))))
    if response["status"] != "ok":
        raise RuntimeError(response["error"]["message"])
    return response["result"]

def read_text(path):
    return _workspace_call("workspace.read_text", {"path": path})["content"]

def write_text(path, content):
    return _workspace_call("workspace.write_text", {"path": path, "content": content})["written"]

def list_files():
    return _workspace_call("workspace.list_files", {})["files"]
`

type dependencies struct {
	readFile func(string) ([]byte, error)
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, dependencies{readFile: os.ReadFile}))
}

func execute(args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("apyrun", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	artifactPath := flags.String("artifact", "", "path to the verified WASI guest artifact")
	manifestPath := flags.String("manifest", "", "path to the artifact distribution manifest")
	configPath := flags.String("config", "", "path to Host-owned operator config")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *artifactPath == "" {
		writeDiagnostic(stderr, "usage: apyrun -artifact <guest.wasm> [-manifest <manifest.json> -config <host.json>]")
		return 2
	}
	if deps.readFile == nil {
		deps.readFile = os.ReadFile
	}
	configData := []byte("{}")
	if *configPath != "" {
		var err error
		configData, err = deps.readFile(*configPath)
		if err != nil {
			writeDiagnostic(stderr, "read operator config")
			return 2
		}
	}
	operator, err := decodeOperatorConfig(configData)
	if err != nil {
		writeDiagnostic(stderr, err.Error())
		return 2
	}
	runConfig, err := operator.resolve()
	if err != nil {
		writeDiagnostic(stderr, err.Error())
		return 2
	}
	request, err := io.ReadAll(io.LimitReader(stdin, int64(runConfig.MaxRequestBytes)+1))
	if err != nil || uint64(len(request)) > uint64(runConfig.MaxRequestBytes) {
		writeDiagnostic(stderr, "RunRequest exceeds configured bounds")
		return 2
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		writeDiagnostic(stderr, "invalid RunRequest")
		return 2
	}
	if err := runtimeconfig.AdmitRunRequirements(decodedRequest); err != nil {
		return emitUnsupportedOutcome(request, err, runConfig.MaxResponseBytes, stdout, stderr)
	}
	requestData, err := runtimeconfig.EncodeRunRequest(decodedRequest)
	if err != nil {
		writeDiagnostic(stderr, "encode Host-bound request failed")
		return 2
	}
	if (runConfig.ExecutionProfile != nil) != (*manifestPath != "") {
		writeDiagnostic(stderr, "execution profile and artifact manifest must be configured together")
		return 2
	}
	wasm, err := deps.readFile(*artifactPath)
	if err != nil {
		writeDiagnostic(stderr, "read guest artifact")
		return 2
	}
	if runConfig.ExecutionProfile != nil {
		bound, err := bindArtifactProfile(deps.readFile, *artifactPath, *manifestPath, wasm, *runConfig.ExecutionProfile)
		if err != nil {
			writeDiagnostic(stderr, err.Error())
			return 2
		}
		runConfig.ExecutionProfile = &bound
	}
	if runConfig.ExecutionProfile != nil {
		boundRequest, err := runtimeconfig.BindAgentSource(decodedRequest, runConfig.ExecutionProfile)
		if err != nil {
			writeDiagnostic(stderr, "execution profile source comparison failed")
			return 2
		}
		decodedRequest = boundRequest
		requestData, err = runtimeconfig.EncodeRunRequest(decodedRequest)
		if err != nil {
			writeDiagnostic(stderr, "encode Host-bound request failed")
			return 2
		}
	}
	if uint64(len(requestData)) > uint64(runConfig.MaxRequestBytes) {
		writeDiagnostic(stderr, "Host-bound RunRequest exceeds configured bounds")
		return 2
	}
	ctx := context.Background()
	factory := wazeroengine.Factory{}
	trustedPrepare := ""
	digest := sha256.Sum256(requestData)
	requestSHA256 := fmt.Sprintf("sha256:%x", digest[:])
	runIdentity := fmt.Sprintf("host-%x", digest[:8])
	if operator.WorkspaceFiles != nil {
		workspaceTool, err := capability.NewWorkspace(operator.WorkspaceFiles)
		if err != nil {
			writeDiagnostic(stderr, "invalid workspace_files")
			return 2
		}
		registry := capability.NewRegistry()
		if err := capability.RegisterWorkspaceTools(registry, workspaceTool); err != nil {
			writeDiagnostic(stderr, "initialize workspace tools")
			return 1
		}
		maxCalls := operator.MaxToolCalls
		if maxCalls == 0 {
			maxCalls = 8
		}
		capabilityPlan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
		if err != nil {
			writeDiagnostic(stderr, "seal capability plan")
			return 1
		}
		factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: runIdentity, Plan: capabilityPlan})
		}
		trustedPrepare = workspaceToolPrelude
	}
	workspaceBinding, err := prepareMountedWorkspace(operator.Workspace)
	if err != nil {
		writeDiagnostic(stderr, err.Error())
		return 2
	}
	if workspaceBinding != nil {
		defer workspaceBinding.close()
		factory.WorkspaceManager = workspaceBinding.manager
		factory.WorkspaceRef = workspaceBinding.ref
		factory.WorkspaceOwner = runIdentity
	}
	runner, err := factory.New(ctx, wasm, runConfig)
	if err != nil {
		writeDiagnostic(stderr, "initialize execution backend")
		return 1
	}
	runnerClosed := false
	defer func() {
		if !runnerClosed {
			_ = runner.Close(context.Background())
		}
	}()
	response, runErr := runner.Run(ctx, requestData, trustedPrepare)
	closeErr := runner.Close(context.Background())
	if closeErr != nil {
		writeDiagnostic(stderr, "close execution backend")
		return 1
	}
	runnerClosed = true
	if runErr != nil {
		if _, outcomeErr := runtimeconfig.NewUnsupportedOutcome(request, runErr); outcomeErr == nil {
			return emitUnsupportedOutcome(request, runErr, runConfig.MaxResponseBytes, stdout, stderr)
		}
		writeDiagnostic(stderr, "execute guest")
		return 1
	}
	var stagedCapsule *stagedWorkspaceCapsule
	if workspaceBinding != nil {
		decodedResponse, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, response)
		if err != nil {
			writeDiagnostic(stderr, "validate Host run response")
			return 1
		}
		receipt, staged, err := workspaceBinding.prepareDisposition(decodedResponse.Status, requestSHA256)
		if err != nil {
			writeDiagnostic(stderr, err.Error())
			return 1
		}
		stagedCapsule = staged
		if stagedCapsule != nil {
			defer stagedCapsule.discard()
		}
		decodedResponse.WorkspaceReceipt = &receipt
		response, err = json.Marshal(decodedResponse)
		if err != nil {
			writeDiagnostic(stderr, "encode workspace disposition response")
			return 1
		}
		if _, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, response); err != nil {
			writeDiagnostic(stderr, "validate workspace disposition response")
			return 1
		}
	}
	if uint64(len(response)) > uint64(runConfig.MaxResponseBytes) {
		writeDiagnostic(stderr, "response exceeds configured bounds")
		return 1
	}
	if stagedCapsule != nil {
		if err := stagedCapsule.publish(); err != nil {
			writeDiagnostic(stderr, err.Error())
			return 1
		}
	}
	if _, err := stdout.Write(append(response, '\n')); err != nil {
		writeDiagnostic(stderr, "write response")
		return 1
	}
	return 0
}

func bindArtifactProfile(readFile func(string) ([]byte, error), artifactPath, manifestPath string, wasm []byte, profile runtimeconfig.ExecutionProfile) (runtimeconfig.ExecutionProfile, error) {
	manifest, err := readFile(manifestPath)
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, fmt.Errorf("read execution profile manifest")
	}
	inventory, qualification, err := readManifestCompanions(readFile, manifestPath, manifest)
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), wasm, manifest, inventory, qualification)
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, fmt.Errorf("verify execution profile artifact")
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, fmt.Errorf("execution profile artifact mismatch")
	}
	return bound, nil
}

func readManifestCompanions(readFile func(string) ([]byte, error), manifestPath string, manifest []byte) ([]byte, []byte, error) {
	directory := filepath.Dir(manifestPath)
	inventoryName, required, err := runtimeconfig.DistributionImportInventoryFilename(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect execution profile manifest")
	}
	var inventory []byte
	if required {
		inventory, err = readFile(filepath.Join(directory, inventoryName))
		if err != nil {
			return nil, nil, fmt.Errorf("read execution profile import inventory")
		}
	}
	qualificationName, required, err := runtimeconfig.DistributionImportQualificationFilename(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect execution profile manifest")
	}
	var qualification []byte
	if required {
		qualification, err = readFile(filepath.Join(directory, qualificationName))
		if err != nil {
			return nil, nil, fmt.Errorf("read execution profile import qualification")
		}
	}
	return inventory, qualification, nil
}

func emitUnsupportedOutcome(request []byte, runErr error, maximum uint32, stdout, stderr io.Writer) int {
	outcome, err := runtimeconfig.NewUnsupportedOutcome(request, runErr)
	if err != nil {
		writeDiagnostic(stderr, "classify unsupported outcome")
		return 1
	}
	encoded, err := json.Marshal(outcome)
	if err != nil || uint64(len(encoded)) > uint64(maximum) {
		writeDiagnostic(stderr, "encode unsupported outcome")
		return 1
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		writeDiagnostic(stderr, "write unsupported outcome")
		return 1
	}
	return exitEscalationRequired
}

func writeDiagnostic(writer io.Writer, message string) {
	if len(message) > 512 {
		message = message[:512]
	}
	_, _ = fmt.Fprintln(writer, message)
}
