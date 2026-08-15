package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const summarySchemaVersion = "pysolate.transparent-campaign-run-summary.v2"

type hostSummary struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	Kernel    string `json:"kernel"`
}

type runSummary struct {
	SchemaVersion        string            `json:"schema_version"`
	ArtifactSHA256       string            `json:"artifact_sha256"`
	ArtifactSourceCommit string            `json:"artifact_source_commit"`
	CampaignSourceCommit string            `json:"campaign_source_commit"`
	ManifestSHA256       string            `json:"manifest_sha256"`
	Host                 hostSummary       `json:"host"`
	Repetitions          int               `json:"repetitions"`
	Runs                 []runSummaryEntry `json:"runs"`
}

type runSummaryEntry struct {
	Repetition          int                             `json:"repetition"`
	Treatment           workflowbench.CampaignTreatment `json:"treatment"`
	EvidenceFile        string                          `json:"evidence_file"`
	EvidenceSHA256      string                          `json:"evidence_sha256"`
	PhysicalExecutions  uint32                          `json:"physical_executions"`
	WallNS              int64                           `json:"wall_ns"`
	ProcessCPUNS        uint64                          `json:"process_cpu_ns,omitempty"`
	ProcessCPUAvailable bool                            `json:"process_cpu_available"`
}

func main() {
	artifactPath := flag.String("artifact", "", "path to the verified Guest WASM artifact")
	artifactSourceCommit := flag.String("artifact-source-commit", "", "source commit recorded by the artifact build")
	campaignSourceCommit := flag.String("campaign-source-commit", "", "source commit for the campaign adapter")
	output := flag.String("output", "", "new private output directory")
	repetitions := flag.Int("repetitions", 1, "balanced baseline/qualified repetitions")
	flag.Parse()
	if err := run(context.Background(), *artifactPath, *artifactSourceCommit, *campaignSourceCommit, *output, *repetitions); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, artifactPath, artifactSourceCommit, campaignSourceCommit, output string, repetitions int) error {
	if ctx == nil || artifactPath == "" || artifactSourceCommit == "" || campaignSourceCommit == "" || output == "" || repetitions < 1 || repetitions > 20 || !filepath.IsAbs(artifactPath) || !filepath.IsAbs(output) {
		return errors.New("artifact, artifact source commit, absolute output and 1..20 repetitions are required")
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	manifest, err := workflowbench.CanonicalTransparentCampaign()
	if err != nil {
		return err
	}
	plans, err := workflowbench.CanonicalCampaignPlans()
	if err != nil {
		return err
	}
	executor, err := workflowbench.NewCampaignGuestExecutor(workflowbench.CampaignGuestExecutorConfig{Artifact: artifact, Plans: plans})
	if err != nil {
		return err
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return fmt.Errorf("create private evidence directory: %w", err)
	}
	manifestPath := filepath.Join(output, "manifest.json")
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}
	manifestSHA256, err := fileSHA256(manifestPath)
	if err != nil {
		return err
	}
	host, err := currentHostSummary(ctx)
	if err != nil {
		return err
	}
	summary := runSummary{
		SchemaVersion: summarySchemaVersion, ArtifactSHA256: executor.ArtifactSHA256(),
		ArtifactSourceCommit: artifactSourceCommit, CampaignSourceCommit: campaignSourceCommit,
		ManifestSHA256: manifestSHA256, Host: host, Repetitions: repetitions,
	}
	for repetition := 0; repetition < repetitions; repetition++ {
		for _, treatment := range workflowbench.CampaignTreatmentOrder(repetition) {
			evidence, err := executeTreatment(ctx, output, executor, plans, manifest, treatment)
			if err != nil {
				return fmt.Errorf("repetition %d treatment %s: %w", repetition, treatment, err)
			}
			filename := evidenceFilename(repetition, treatment)
			evidencePath := filepath.Join(output, filename)
			if err := writeJSON(evidencePath, evidence); err != nil {
				return err
			}
			evidenceSHA256, err := fileSHA256(evidencePath)
			if err != nil {
				return err
			}
			summary.Runs = append(summary.Runs, runSummaryEntry{
				Repetition: repetition, Treatment: treatment, EvidenceFile: filename, EvidenceSHA256: evidenceSHA256,
				PhysicalExecutions: evidence.PhysicalExecutions, WallNS: evidence.WallNS,
				ProcessCPUNS: evidence.ProcessCPUNS, ProcessCPUAvailable: evidence.ProcessCPUAvailable,
			})
		}
	}
	return writeJSON(filepath.Join(output, "summary.json"), summary)
}

func executeTreatment(ctx context.Context, output string, executor *workflowbench.CampaignGuestExecutor, plans map[string]*capability.Plan, manifest workflowbench.CampaignManifest, treatment workflowbench.CampaignTreatment) (workflowbench.CampaignEvidence, error) {
	workspaceBase, err := os.MkdirTemp(output, ".workspace-")
	if err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	defer os.RemoveAll(workspaceBase)
	if err := os.Chmod(workspaceBase, 0o700); err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	manager, err := workspace.NewManager(workspaceBase)
	if err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	cacheDirectory, err := os.MkdirTemp(output, ".cache-")
	if err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	defer os.RemoveAll(cacheDirectory)
	adapter, err := workflowbench.NewRuntimeCampaignAdapter(workflowbench.RuntimeCampaignAdapterConfig{
		Guest: executor, Plans: plans, WorkspaceManager: manager, BaseWorkspaceRef: base,
		ArtifactSHA256: executor.ArtifactSHA256(), ExecutionProfileSHA256: executor.ExecutionProfileSHA256(), CacheDirectory: cacheDirectory,
	})
	if err != nil {
		return workflowbench.CampaignEvidence{}, err
	}
	evidence, runErr := workflowbench.RunTransparentCampaign(ctx, manifest, treatment, adapter)
	closeErr := adapter.Close(context.Background())
	return evidence, errors.Join(runErr, closeErr)
}

func currentHostSummary(ctx context.Context) (hostSummary, error) {
	command := exec.CommandContext(ctx, "uname", "-srv")
	output, err := command.Output()
	if err != nil {
		return hostSummary{}, fmt.Errorf("record host kernel: %w", err)
	}
	kernel := strings.TrimSpace(string(output))
	if kernel == "" {
		return hostSummary{}, errors.New("host kernel identity is empty")
	}
	return hostSummary{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), Kernel: kernel}, nil
}

func fileSHA256(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

func evidenceFilename(repetition int, treatment workflowbench.CampaignTreatment) string {
	return fmt.Sprintf("rep-%02d-%s.json", repetition, treatment)
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
