package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

var exactCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const fixedGuestArtifactSourceCommit = "db756fd7b40d465072b5fb1b6f3867d29c5d8114"
const fixedGuestArtifactSHA256 = "sha256:d5706fbf113c7042a4484ad5713ee5baa8fe4788c33beb9b6223b0ff9f1201af"
const legacyGuestArtifactSourceCommit = "501daef99796c1af7cd7bab1e0ab712a199820b9"
const legacyGuestArtifactSHA256 = "sha256:a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b"

func main() {
	artifact := flag.String("artifact", "", "fixed verified Guest artifact")
	contract := flag.String("contract", "", "frozen experiment contract JSON")
	oracle := flag.String("oracle", "", "independent oracle JSON")
	laneConfig := flag.String("lane-config", "", "frozen lane config JSON")
	output := flag.String("output", "", "private evidence output JSON")
	flag.Parse()
	if *artifact == "" || *contract == "" || *oracle == "" || *laneConfig == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "fixed artifact, preregistration files and private output are required")
		os.Exit(2)
	}
	if err := run(context.Background(), *artifact, *contract, *oracle, *laneConfig, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, artifactPath, contractPath, oraclePath, laneConfigPath, output string) error {
	harnessCommit, err := currentHarnessSourceCommit()
	if err != nil {
		return err
	}
	contract, oracle, _, err := loadPreregistration(contractPath, oraclePath, laneConfigPath)
	if err != nil {
		return err
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	if err := verifyArtifactManifest(artifactPath, artifact); err != nil {
		return err
	}
	identityHandler := &timedFixtureHandler{delay: 1}
	identityPlan, err := buildFixturePlan(identityHandler)
	if err != nil {
		return err
	}
	specs := identityPlan.Specs()
	if len(specs) != 1 {
		return errors.New("source-prefix fixture plan is not singular")
	}
	specJSON, err := json.Marshal(specs[0])
	if err != nil {
		return err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, StagedObservation: true, PrivateWorkspace: true}
	profileJSON, err := json.Marshal(runConfig)
	if err != nil {
		return err
	}
	workspaceRoot, err := os.MkdirTemp("", ".pysolate-source-prefix-workspace-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspaceRoot)
	if err := os.Chmod(workspaceRoot, 0o700); err != nil {
		return err
	}
	manager, err := workspace.NewManager(workspaceRoot)
	if err != nil {
		return err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return err
	}
	environment := sourcePrefixEnvironment{artifact: artifact, manager: manager, base: base, config: runConfig}
	identities := workflowbench.SourcePrefixRuntimeIdentities{
		ArtifactSHA256: digestBytes(artifact), ArtifactSourceCommit: fixedGuestArtifactSourceCommit, HarnessSourceCommit: harnessCommit,
		ExecutionProfileSHA256: digestBytes(profileJSON), CapabilityPlanSHA256: identityPlan.Identity(),
		CapabilitySpecSHA256: digestBytes(specJSON), HandlerSHA256: digestBytes([]byte(fixtureHandlerContract)),
	}
	evidence, err := workflowbench.ExecuteSourcePrefixPairs(ctx, contract, identities, func(laneContext context.Context, pair, order uint32, treatment workflowbench.SourcePrefixTreatment) (workflowbench.SourcePrefixRow, error) {
		return environment.executeLane(laneContext, contract, contract.ExpectedResultSHA256, pair, order, treatment)
	})
	if err != nil {
		return err
	}
	if expectedSHA, err := canonicalResultSHA(oracle.ExpectedResult); err != nil || expectedSHA != contract.ExpectedResultSHA256 {
		return errors.New("independent oracle changed during execution")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	if _, err := workflowbench.DecodeSourcePrefixEvidence(encoded, contract); err != nil {
		return err
	}
	if err := atomicPrivateWrite(output, encoded); err != nil {
		return err
	}
	fmt.Printf("pairs=%d baseline_median_ns=%d streaming_median_ns=%d speedup_milli=%d supported=%t evidence_sha256=%s\n",
		contract.Repetitions, evidence.BaselineMedianNS, evidence.StreamingMedianNS, evidence.MedianSpeedupMilli, evidence.SpeedupSupported, digestBytes(encoded))
	return nil
}

func currentHarnessSourceCommit() (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("harness lacks Go build identity")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return resolveVCSIdentity(settings)
}

func resolveVCSIdentity(settings map[string]string) (string, error) {
	commit := settings["vcs.revision"]
	if !exactCommitPattern.MatchString(commit) || settings["vcs.modified"] != "false" {
		return "", errors.New("harness must be built from a clean exact VCS revision")
	}
	return commit, nil
}

func verifyArtifactManifest(artifactPath string, artifact []byte) error {
	if digestBytes(artifact) != fixedGuestArtifactSHA256 {
		return errors.New("artifact does not match fixed Guest anchor")
	}
	artifactInfo, err := os.Lstat(artifactPath)
	if err != nil || !artifactInfo.Mode().IsRegular() {
		return errors.New("artifact must be a regular file")
	}
	manifestPath := filepath.Join(filepath.Dir(artifactPath), "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return errors.New("artifact manifest must be a regular file")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest struct {
		Artifact struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"artifact"`
		Build struct {
			RepositoryCommit string `json:"repository_commit"`
		} `json:"build"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Build.RepositoryCommit != fixedGuestArtifactSourceCommit || manifest.Artifact.Filename != filepath.Base(artifactPath) || manifest.Artifact.SHA256 != digestBytes(artifact)[len("sha256:"):] {
		return errors.New("artifact manifest identity mismatch")
	}
	return nil
}

var _ capability.Handler = (*timedFixtureHandler)(nil)
