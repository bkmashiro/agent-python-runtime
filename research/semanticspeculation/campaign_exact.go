package semanticspeculation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

var Phase3PrivacyIdentity = syntheticDigest([]byte(`{"coalescing":"forbidden","privacy":"exact_partition"}`))

type ExactGuestCampaignConfig struct {
	Artifact        []byte
	Manifest        []byte
	ImportInventory []byte
	RunConfig       runtimeconfig.RunConfig
	WorkspaceRoot   string
	PhysicalDelay   time.Duration
}

type ExactGuestCampaign struct {
	config   ExactGuestCampaignConfig
	bindings TrialBindings
}

type campaignProviderTracker struct {
	attempts, successes, cancelled       atomic.Uint32
	resultBytes, costUnits, elapsedNanos atomic.Uint64
}

func NewExactGuestCampaign(config ExactGuestCampaignConfig) (*ExactGuestCampaign, error) {
	if len(config.Artifact) == 0 || len(config.Manifest) == 0 || len(config.ImportInventory) == 0 ||
		config.RunConfig.ExecutionProfile == nil || config.WorkspaceRoot == "" || config.PhysicalDelay != 250*time.Millisecond {
		return nil, errors.New("invalid exact Guest campaign config")
	}
	artifactSHA := digestBytes(config.Artifact)
	if config.RunConfig.ExecutionProfile.ArtifactSHA256() != artifactSHA ||
		config.RunConfig.ExecutionProfile.ManifestSHA256() != digestBytes(config.Manifest) {
		return nil, errors.New("exact Guest campaign artifact/profile mismatch")
	}
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config.RunConfig)
	if err != nil {
		return nil, err
	}
	plan, err := NewPhase3CampaignPlan(capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	if err != nil {
		return nil, err
	}
	return &ExactGuestCampaign{config: config, bindings: TrialBindings{
		ArtifactSHA256: artifactSHA, ManifestSHA256: digestBytes(config.Manifest), ImportInventorySHA256: digestBytes(config.ImportInventory),
		ExecutionProfileSHA256: profileSHA, CapabilityPlanSHA256: plan.Identity(), PrivacySHA256: Phase3PrivacyIdentity,
	}}, nil
}

func (campaign *ExactGuestCampaign) Bindings() TrialBindings {
	if campaign == nil {
		return TrialBindings{}
	}
	return campaign.bindings
}

func NewPhase3CampaignPlan(handler capability.Handler) (*capability.Plan, error) {
	if handler == nil {
		return nil, errors.New("phase 3 campaign handler is nil")
	}
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"eager-comparator"}`))
	if err != nil {
		return nil, err
	}
	registry := capability.NewRegistry()
	spec := capability.Spec{
		Name: "fixture.eager_time", Version: "fixture.eager-time.v1", Description: "Comparator external read.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		ReadOnly: true, Idempotent: true, HandlerIdentity: "fixture-eager-time-handler-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "time", Method: "read", Arguments: []string{"value"}, ResultField: "value"},
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "fixture", Argument: "value"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
			Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
}

func (campaign *ExactGuestCampaign) RunCoordinate(ctx context.Context, fixture SyntheticCase, trialIndex uint32) (MatchedCaseEvidence, error) {
	if campaign == nil || ctx == nil || !isFrozenPhase3Case(fixture) || trialIndex == 0 || trialIndex > 5 {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCampaign
	}
	factory := func(treatment string, _ uint32) (ScheduledTreatment, error) {
		tracker := &campaignProviderTracker{}
		plan, err := NewPhase3CampaignPlan(capability.HandlerFunc(func(callCtx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			started := time.Now()
			defer func() { tracker.elapsedNanos.Add(uint64(time.Since(started))) }()
			tracker.attempts.Add(1)
			timer := time.NewTimer(campaign.config.PhysicalDelay)
			defer timer.Stop()
			select {
			case <-callCtx.Done():
				tracker.cancelled.Add(1)
				return nil, callCtx.Err()
			case <-timer.C:
			}
			response := json.RawMessage(`{"value":"weather"}`)
			tracker.successes.Add(1)
			tracker.resultBytes.Add(uint64(len(response)))
			tracker.costUnits.Add(1)
			return response, nil
		}))
		if err != nil {
			return nil, err
		}
		if plan.Identity() != campaign.bindings.CapabilityPlanSHA256 {
			return nil, errors.New("phase 3 campaign plan identity drift")
		}
		runID := campaignOpaqueRunID(fixture.ID, trialIndex, treatment)
		observation := func() ProviderObservation {
			return ProviderObservation{
				Attempts: tracker.attempts.Load(), ResultBytes: tracker.resultBytes.Load(), CostUnits: tracker.costUnits.Load(), ElapsedNanos: tracker.elapsedNanos.Load(),
				Dispositions: PhysicalDispositions{Consumed: tracker.successes.Load(), Cancelled: tracker.cancelled.Load()},
			}
		}
		brokerFactory := func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		}
		workspaceRoot := filepath.Join(campaign.config.WorkspaceRoot, runID)
		switch treatment {
		case "serial_whole_file":
			config := campaign.config.RunConfig
			config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
			return NewSerialGuestTreatment(SerialGuestTreatmentConfig{
				Artifact: campaign.config.Artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory,
				ProviderObservation: observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID,
			})
		case "eager_style_gate":
			config := campaign.config.RunConfig
			return NewEagerGuestTreatment(EagerGuestTreatmentConfig{
				Artifact: campaign.config.Artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory,
				AllowedImportRoots: campaign.config.RunConfig.ExecutionProfile.AllowedImports(), ProviderObservation: observation,
				RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID,
			})
		case "semantic_pre_dispatch":
			return NewSemanticPreDispatchTreatment(SemanticPreDispatchTreatmentConfig{
				Artifact: campaign.config.Artifact, RunConfig: campaign.config.RunConfig, Plan: plan, ProviderObservation: observation,
				ImportClosureSHA256: campaign.bindings.ImportInventorySHA256, PhysicalReadBudget: 1,
				RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID,
			})
		default:
			return nil, fmt.Errorf("unknown achieved treatment %q", treatment)
		}
	}
	result, err := RunMatchedCaseCampaign(ctx, fixture, trialIndex, campaign.bindings, factory, func(serial TrialRecord) (uint64, error) {
		elapsed := trialElapsedNanos(serial)
		if fixture.ExpectedLogicalCalls > 0 {
			latency := uint64(campaign.config.PhysicalDelay)
			if elapsed <= latency {
				return 0, ErrInvalidMatchedCampaign
			}
			return elapsed - latency, nil
		}
		return elapsed, nil
	})
	if err != nil {
		return MatchedCaseEvidence{}, err
	}
	return SealMatchedCaseEvidence(result)
}

func campaignOpaqueRunID(caseID string, trialIndex uint32, treatment string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d\x00%s", phase3ShuffleSeed, caseID, trialIndex, treatment)))
	return "phase3-" + hex.EncodeToString(digest[:12])
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
