package mechanismcampaign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

type CampaignConfig struct {
	ArtifactPath      string
	Fixture           agenttrajectory.Fixture
	WorkspaceRoot     string
	GenerationStep    time.Duration
	FinalizationDelay time.Duration
	EnableCOW         bool
	EnableColdIO      bool
	ColdPayloadBytes  int
}

type CampaignResult struct {
	Origin     OriginSharingResult
	Candidates CandidateStageResult
	Resume     ResumeStageResult
	Controls   ControlStageResult
	Events     []Event
	DurationNS uint64
}

func RunCampaign(ctx context.Context, config CampaignConfig) (CampaignResult, error) {
	if ctx == nil || config.ArtifactPath == "" || config.WorkspaceRoot == "" {
		return CampaignResult{}, errors.New("invalid mechanism campaign config")
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return CampaignResult{}, err
	}
	started := time.Now()
	originStarted := time.Now()
	origin, err := RunOriginSharingStage(ctx, OriginSharingConfig{
		ArtifactPath: config.ArtifactPath,
		StoreRoot:    filepath.Join(config.WorkspaceRoot, "origin-store"),
	})
	if err != nil {
		return CampaignResult{}, err
	}
	originOffset := int64(0)
	candidateOffset := time.Since(originStarted).Nanoseconds()
	candidates, err := RunCandidateStage(ctx, CandidateStageConfig{
		ArtifactPath: config.ArtifactPath, Fixture: config.Fixture,
		WorkspaceRoot:  filepath.Join(config.WorkspaceRoot, "candidates"),
		GenerationStep: config.GenerationStep, FinalizationDelay: config.FinalizationDelay, EnableCOW: config.EnableCOW,
		OriginBriefing: origin.Value,
	})
	if err != nil {
		return CampaignResult{}, err
	}
	if err := origin.RetainForMain(ctx); err != nil {
		return CampaignResult{}, err
	}
	resumeOffset := time.Since(originStarted).Nanoseconds()
	resumeStarted := time.Now()
	resume, err := RunResumeStage(ctx, ResumeStageConfig{
		ArtifactPath: config.ArtifactPath, Capsule: candidates.SelectedCapsule,
		PortableRoot:  candidates.SelectedRoot,
		WorkspaceRoot: filepath.Join(config.WorkspaceRoot, "resume"),
		EnableColdIO:  config.EnableColdIO, PayloadBytes: config.ColdPayloadBytes,
	})
	if err != nil {
		return CampaignResult{}, err
	}
	controlOffset := resumeOffset + time.Since(resumeStarted).Nanoseconds()
	controls, err := RunFailClosedControls(ctx, ControlStageConfig{
		ArtifactPath: config.ArtifactPath, Fixture: config.Fixture,
		WorkspaceRoot: filepath.Join(config.WorkspaceRoot, "controls"),
	})
	if err != nil {
		return CampaignResult{}, err
	}
	events := mergeCampaignEvents(
		stageEvents{offset: originOffset, events: origin.Events},
		stageEvents{offset: candidateOffset, events: candidates.Events},
		stageEvents{offset: resumeOffset, events: resume.Events},
		stageEvents{offset: controlOffset, events: controls.Events},
	)
	result := CampaignResult{
		Origin: origin, Candidates: candidates, Resume: resume, Controls: controls, Events: events,
		DurationNS: uint64(time.Since(started).Nanoseconds()),
	}
	if err := result.Validate(config.EnableCOW, config.EnableColdIO); err != nil {
		return CampaignResult{}, err
	}
	return result, nil
}

func (result CampaignResult) Validate(requireCOW, requireColdIO bool) error {
	if result.Origin.PhysicalComputes != 1 || len(result.Origin.LogicalDispositions) != 2 || result.Origin.Retained.Disposition == "" {
		return errors.New("origin sharing/retention did not close")
	}
	for _, candidateID := range []string{"brighton", "oxford"} {
		candidate, ok := result.Candidates.Candidates[candidateID]
		if !ok || candidate.ControllerSnapshot.PhysicalIssues != 3 || candidate.ControllerSnapshot.LogicalClaims != 3 ||
			candidate.ControllerSnapshot.Consumed != 3 || !candidate.ControllerSnapshot.SourceSealed {
			return errors.New("candidate prefix pre-dispatch did not close")
		}
		if requireCOW && !candidate.COWSelected {
			return errors.New("candidate COW was required but not selected")
		}
	}
	if result.Candidates.Candidates["brighton"].TotalCostGBP != 118.4 || result.Candidates.Candidates["oxford"].TotalCostGBP != 78 {
		return errors.New("candidate oracle mismatch")
	}
	if result.Resume.BoundRoot.IdentitySHA256 != result.Candidates.SelectedRoot.IdentitySHA256 ||
		result.Resume.ImportedInfo.WorkspaceSHA256 != result.Candidates.SelectedRoot.WorkspaceSHA256 ||
		result.Resume.ImportedInfo.TreeSHA256 != result.Candidates.SelectedInfo.TreeSHA256 {
		return errors.New("serialized resume identity mismatch")
	}
	if requireCOW && !requireColdIO && !result.Resume.COW.COWSelected {
		return errors.New("resumed Main COW was required but not selected")
	}
	if requireColdIO && (result.Resume.ColdIO.State == "" || result.Resume.ColdIO.Waits != 1 || result.Resume.ColdIO.Resumes != 1) {
		return errors.New("cold I/O continuation did not close")
	}
	if !result.Controls.ArgumentMismatchRejected || !result.Controls.SourceMismatchRejected || result.Controls.Snapshot.RejectedClaims != 1 {
		return errors.New("fail-closed controls did not close")
	}
	if len(result.Events) == 0 || result.DurationNS == 0 {
		return errors.New("campaign has no causal evidence")
	}
	return nil
}

type stageEvents struct {
	offset int64
	events []Event
}

func mergeCampaignEvents(stages ...stageEvents) []Event {
	var merged []Event
	for _, stage := range stages {
		for _, event := range stage.events {
			event.AtNS += stage.offset
			merged = append(merged, event)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].AtNS == merged[j].AtNS {
			return merged[i].Sequence < merged[j].Sequence
		}
		return merged[i].AtNS < merged[j].AtNS
	})
	for index := range merged {
		merged[index].Sequence = uint64(index + 1)
		merged[index].ID = fmt.Sprintf("event-%04d", merged[index].Sequence)
	}
	return merged
}
