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
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const exitEscalationRequired = 3

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
	if operator.Deterministic != nil {
		if runConfig.ExecutionProfile == nil || runConfig.ExecutionProfile.ArtifactSHA256() == "" {
			writeDiagnostic(stderr, "deterministic verification requires a verified artifact profile")
			return 2
		}
		deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(runConfig.ExecutionProfile.ArtifactSHA256(), operator.Deterministic.RandomSeed)
		if err != nil {
			writeDiagnostic(stderr, "invalid deterministic verification profile")
			return 2
		}
		runConfig.DeterministicVerification = &deterministic
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
	var playbackBundle *playback.Bundle
	var branchManifest *playback.BranchManifest
	if operator.Playback != nil && (operator.Playback.Mode == "playback" || operator.Playback.Mode == "branch") {
		bundleFile, openErr := os.Open(operator.Playback.InputBundle)
		if openErr != nil {
			writeDiagnostic(stderr, "open playback bundle")
			return 2
		}
		bundleBytes, readErr := io.ReadAll(io.LimitReader(bundleFile, playback.MaxEncodedBytes+1))
		closeErr := bundleFile.Close()
		if readErr != nil || closeErr != nil || len(bundleBytes) > playback.MaxEncodedBytes {
			writeDiagnostic(stderr, "read playback bundle")
			return 2
		}
		decodedBundle, decodeErr := playback.Decode(bundleBytes)
		if decodeErr != nil || decodedBundle.Identity != operator.Playback.ExpectedBundleSHA256 {
			writeDiagnostic(stderr, "validate playback bundle identity")
			return 2
		}
		playbackBundle = &decodedBundle
		if operator.Playback.Mode == "branch" {
			manifestFile, openErr := os.Open(operator.Playback.InputBranchManifest)
			if openErr != nil {
				writeDiagnostic(stderr, "open playback branch manifest")
				return 2
			}
			manifestBytes, readErr := io.ReadAll(io.LimitReader(manifestFile, playback.MaxBranchBytes+1))
			closeErr := manifestFile.Close()
			if readErr != nil || closeErr != nil || len(manifestBytes) > playback.MaxBranchBytes {
				writeDiagnostic(stderr, "read playback branch manifest")
				return 2
			}
			decodedManifest, decodeErr := playback.DecodeBranchManifest(manifestBytes)
			if decodeErr != nil || decodedManifest.Identity != operator.Playback.ExpectedBranchSHA256 {
				writeDiagnostic(stderr, "validate playback branch manifest identity")
				return 2
			}
			branchManifest = &decodedManifest
		}
	}
	var capabilityPlan *capability.Plan
	var runBroker *capability.Broker
	hasHostTools := operator.WorkspaceFiles != nil || operator.InformationSources != nil
	if hasHostTools {
		registry := capability.NewRegistry()
		if operator.WorkspaceFiles != nil {
			workspaceTool, err := capability.NewWorkspace(operator.WorkspaceFiles)
			if err != nil {
				writeDiagnostic(stderr, "invalid workspace_files")
				return 2
			}
			if err := capability.RegisterWorkspaceTools(registry, workspaceTool); err != nil {
				writeDiagnostic(stderr, "initialize workspace tools")
				return 1
			}
		}
		offlineSources := playbackBundle != nil && (branchManifest == nil || branchManifest.SuffixMode != playback.BranchLiveSuffix)
		if operator.InformationSources != nil && operator.InformationSources.DemoCatalog != nil {
			policy, err := operator.InformationSources.DemoCatalog.resolve()
			if err != nil {
				writeDiagnostic(stderr, "invalid demo_catalog source policy")
				return 2
			}
			if offlineSources {
				spec, grant, definitionErr := capability.DemoCatalogDefinition(policy)
				if definitionErr != nil || registry.Register(spec, grant, capability.NewPlaybackHandler()) != nil {
					writeDiagnostic(stderr, "initialize offline demo_catalog source")
					return 1
				}
			} else if err := capability.RegisterDemoCatalog(registry, policy); err != nil {
				writeDiagnostic(stderr, "initialize demo_catalog source")
				return 1
			}
		}
		if operator.InformationSources != nil && operator.InformationSources.BenchmarkManifest != nil {
			policy, err := operator.InformationSources.BenchmarkManifest.resolve()
			if err != nil {
				writeDiagnostic(stderr, "invalid benchmark_manifest source policy")
				return 2
			}
			if offlineSources {
				if err := capability.RegisterBenchmarkManifestPlayback(registry, policy); err != nil {
					writeDiagnostic(stderr, "initialize offline benchmark_manifest source")
					return 1
				}
			} else if err := capability.RegisterBenchmarkManifest(registry, policy); err != nil {
				writeDiagnostic(stderr, "initialize benchmark_manifest source")
				return 1
			}
		}
		maxCalls := operator.MaxToolCalls
		if maxCalls == 0 {
			maxCalls = 8
		}
		capabilityPlan, err = registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
		if err != nil {
			writeDiagnostic(stderr, "seal capability plan")
			return 1
		}
		factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
			brokerConfig := capability.Config{RunIdentity: runIdentity, Plan: capabilityPlan}
			if branchManifest != nil {
				prefix := append([]capability.TranscriptEntry(nil), playbackBundle.Entries[:branchManifest.ForkOperation]...)
				brokerConfig.Branch = &capability.BranchConfig{
					ForkOperation: branchManifest.ForkOperation, PrefixEntries: prefix,
					Mode: capability.BranchMode(branchManifest.SuffixMode), SuffixEntries: branchManifest.SuffixEntries,
				}
			} else if playbackBundle != nil {
				brokerConfig.Playback = &capability.PlaybackConfig{Entries: playbackBundle.Entries}
			}
			broker, err := capability.NewBroker(brokerConfig)
			if err == nil {
				runBroker = broker
			}
			return broker, err
		}
		trustedPrepare = capabilityPlan.PythonPrelude()
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
	if playbackBundle != nil {
		var admissionErr error
		if branchManifest != nil {
			admissionErr = validateBranchAdmission(*playbackBundle, *branchManifest, capabilityPlan, runConfig, wasm, requestSHA256, workspaceBinding)
		} else {
			admissionErr = validatePlaybackAdmission(*playbackBundle, capabilityPlan, runConfig, wasm, requestSHA256, workspaceBinding)
		}
		if admissionErr != nil {
			writeDiagnostic(stderr, admissionErr.Error())
			return 2
		}
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
	var stagedPlayback *stagedPlaybackBundle
	var decodedResponse runtimeconfig.RunResponse
	needsDecodedResponse := workspaceBinding != nil || operator.Playback != nil
	if needsDecodedResponse {
		decodedResponse, err = runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, response)
		if err != nil {
			writeDiagnostic(stderr, "validate Host run response")
			return 1
		}
	}
	if workspaceBinding != nil {
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
		if decodedResponse, err = runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, response); err != nil {
			writeDiagnostic(stderr, "validate workspace disposition response")
			return 1
		}
	}
	if uint64(len(response)) > uint64(runConfig.MaxResponseBytes) {
		writeDiagnostic(stderr, "response exceeds configured bounds")
		return 1
	}
	if playbackBundle != nil && branchManifest == nil {
		if err := validatePlaybackOutcome(*playbackBundle, decodedResponse); err != nil {
			writeDiagnostic(stderr, err.Error())
			return 1
		}
	}
	if operator.Playback != nil && (operator.Playback.Mode == "capture" || operator.Playback.Mode == "branch") {
		if capabilityPlan == nil || runBroker == nil {
			writeDiagnostic(stderr, "capture broker is unavailable")
			return 1
		}
		profileSHA256, profileErr := executionProfileSHA256(runConfig)
		resultSHA256, resultErr := playback.CanonicalSHA256(decodedResponse.Result)
		if profileErr != nil || resultErr != nil {
			writeDiagnostic(stderr, "bind playback identities")
			return 1
		}
		metadata := playback.Metadata{
			CapabilityPlanSHA256: capabilityPlan.Identity(), RequestSHA256: requestSHA256,
			ArtifactSHA256: playback.SHA256(wasm), ExecutionProfileSHA256: profileSHA256,
			ExpectedStatus: string(decodedResponse.Status), ExpectedResultSHA256: resultSHA256, Grants: capabilityPlan.Grants(),
		}
		if decodedResponse.WorkspaceReceipt != nil {
			metadata.InitialWorkspaceSHA256 = decodedResponse.WorkspaceReceipt.InitialWorkspaceSHA256
			metadata.FinalWorkspaceSHA256 = decodedResponse.WorkspaceReceipt.FinalWorkspaceSHA256
		}
		transcript := runBroker.SnapshotTranscript()
		if uint32(len(transcript)) != runBroker.Calls() {
			writeDiagnostic(stderr, "capture transcript is incomplete")
			return 1
		}
		bundle, bundleErr := playback.New(metadata, transcript)
		if bundleErr != nil {
			writeDiagnostic(stderr, "author playback bundle")
			return 1
		}
		stagedPlayback, err = stagePlaybackBundle(operator.Playback.OutputBundle, bundle)
		if err != nil {
			writeDiagnostic(stderr, err.Error())
			return 1
		}
		defer stagedPlayback.discard()
	}
	if stagedCapsule != nil {
		if err := stagedCapsule.publish(); err != nil {
			writeDiagnostic(stderr, err.Error())
			return 1
		}
	}
	if stagedPlayback != nil {
		if err := stagedPlayback.publish(); err != nil {
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
