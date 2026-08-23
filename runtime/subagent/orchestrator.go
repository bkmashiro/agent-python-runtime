// Package subagent coordinates bounded pre-seal child work without granting
// publication authority to child executors.
package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const DescriptorSchemaVersion = "pysolate.subagent-descriptor.v1"

var (
	ErrInvalidDescriptor    = errors.New("invalid subagent descriptor")
	ErrInvalidOrchestrator  = errors.New("invalid subagent orchestrator")
	ErrFanoutBudget         = errors.New("subagent fanout budget exceeded")
	ErrOrchestratorTerminal = errors.New("subagent orchestrator is terminal")
	ErrOrchestratorAwaited  = errors.New("subagent orchestrator fanout is already awaited")
	ErrChildExecution       = errors.New("subagent child execution failed")
	ErrAuthorityWidening    = errors.New("subagent authority widening rejected")
	ErrDelegationBudget     = errors.New("subagent delegation call budget exceeded")
)

var descriptorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var descriptorDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Descriptor struct {
	SchemaVersion          string `json:"schema_version"`
	ChildID                string `json:"child_id"`
	ParentStreamEpoch      string `json:"parent_stream_epoch"`
	ParentLineageSHA256    string `json:"parent_lineage_sha256"`
	SourceOccurrence       string `json:"source_occurrence"`
	SourceSHA256           string `json:"source_sha256"`
	InputsSHA256           string `json:"inputs_sha256"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ChildPlanSHA256        string `json:"child_plan_sha256"`
	PrivacyPartition       string `json:"privacy_partition"`
	Depth                  uint32 `json:"depth"`
}

func (descriptor Descriptor) Validate() error {
	if descriptor.SchemaVersion != DescriptorSchemaVersion ||
		!descriptorName.MatchString(descriptor.ChildID) ||
		!descriptorName.MatchString(descriptor.ParentStreamEpoch) ||
		!descriptorName.MatchString(descriptor.SourceOccurrence) ||
		!descriptorName.MatchString(descriptor.PrivacyPartition) || descriptor.Depth == 0 {
		return ErrInvalidDescriptor
	}
	for _, digest := range []string{
		descriptor.ParentLineageSHA256, descriptor.SourceSHA256, descriptor.InputsSHA256,
		descriptor.ArtifactSHA256, descriptor.ExecutionProfileSHA256, descriptor.ChildPlanSHA256,
	} {
		if !descriptorDigest.MatchString(digest) {
			return ErrInvalidDescriptor
		}
	}
	return nil
}

func (descriptor Descriptor) Identity() (string, []byte, error) {
	if err := descriptor.Validate(); err != nil {
		return "", nil, err
	}
	document, err := json.Marshal(descriptor)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:]), document, nil
}

type Invocation struct {
	Descriptor   Descriptor
	WorkspaceRef workspace.Ref
}

type Executor interface {
	Execute(context.Context, Invocation) error
}

type ExecutorFunc func(context.Context, Invocation) error

func (function ExecutorFunc) Execute(ctx context.Context, invocation Invocation) error {
	return function(ctx, invocation)
}

type Config struct {
	Manager               *workspace.Manager
	ParentRef             workspace.Ref
	ParentWorkspaceSHA256 string
	ParentLineage         string
	MaxFanout             uint32
	MaxDepth              uint32
	ParentPlan            *capability.Plan
	ChildPlans            map[string]*capability.Plan
	MaxDelegatedCalls     uint32
	Executor              Executor
}

type child struct {
	descriptor    Descriptor
	descriptorSHA string
	branch        *workspace.Branch
	cancel        context.CancelFunc
	done          chan struct{}
	err           error
	startedMS     float64
	endedMS       float64
	reservedCalls uint32
}

type Orchestrator struct {
	config        Config
	parentDepth   uint32
	started       time.Time
	mu            sync.Mutex
	children      map[string]*child
	terminal      bool
	awaited       bool
	ready         bool
	reservedCalls uint32
}

func New(config Config) (*Orchestrator, error) {
	if config.Manager == nil || config.Executor == nil || config.ParentRef == "" ||
		!descriptorDigest.MatchString(config.ParentWorkspaceSHA256) ||
		!descriptorDigest.MatchString(config.ParentLineage) || config.MaxFanout == 0 || config.MaxDepth == 0 {
		return nil, ErrInvalidOrchestrator
	}
	if config.ParentPlan != nil || config.ChildPlans != nil || config.MaxDelegatedCalls != 0 {
		if config.ParentPlan == nil || len(config.ChildPlans) == 0 || config.MaxDelegatedCalls == 0 || config.MaxDelegatedCalls > config.ParentPlan.MaxCalls() {
			return nil, ErrInvalidOrchestrator
		}
		for identity, plan := range config.ChildPlans {
			if plan == nil || identity != plan.Identity() {
				return nil, ErrInvalidOrchestrator
			}
		}
	}
	info, err := config.Manager.Inspect(config.ParentRef)
	if err != nil || info.WorkspaceSHA256 != config.ParentWorkspaceSHA256 {
		return nil, ErrInvalidOrchestrator
	}
	lineage, parentDepth, err := config.Manager.PortableIdentity(config.ParentRef)
	if err != nil || lineage != config.ParentLineage || parentDepth >= config.MaxDepth {
		return nil, ErrInvalidOrchestrator
	}
	return &Orchestrator{config: config, parentDepth: parentDepth, started: time.Now(), children: make(map[string]*child)}, nil
}

func (orchestrator *Orchestrator) Stage(ctx context.Context, descriptor Descriptor) error {
	if orchestrator == nil || ctx == nil || descriptor.Validate() != nil ||
		descriptor.ParentLineageSHA256 != orchestrator.config.ParentLineage || descriptor.Depth != orchestrator.parentDepth+1 || descriptor.Depth > orchestrator.config.MaxDepth {
		return ErrInvalidDescriptor
	}
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	if orchestrator.terminal {
		return ErrOrchestratorTerminal
	}
	if orchestrator.awaited {
		return ErrOrchestratorAwaited
	}
	if uint32(len(orchestrator.children)) >= orchestrator.config.MaxFanout {
		return ErrFanoutBudget
	}
	if _, duplicate := orchestrator.children[descriptor.ChildID]; duplicate {
		return ErrInvalidDescriptor
	}
	var reservedCalls uint32
	if orchestrator.config.ParentPlan != nil {
		childPlan := orchestrator.config.ChildPlans[descriptor.ChildPlanSHA256]
		decision := capability.CompareDelegation(orchestrator.config.ParentPlan, childPlan)
		if !decision.Allowed {
			return errors.Join(ErrAuthorityWidening, errors.New(string(decision.Reason)))
		}
		reservedCalls = decision.ReservedCalls
		if reservedCalls > orchestrator.config.MaxDelegatedCalls-orchestrator.reservedCalls {
			return ErrDelegationBudget
		}
	}
	descriptorSHA, _, err := descriptor.Identity()
	if err != nil {
		return ErrInvalidDescriptor
	}
	branch, err := orchestrator.config.Manager.ForkBranch(orchestrator.config.ParentRef, orchestrator.config.ParentWorkspaceSHA256)
	if err != nil {
		return err
	}
	childContext, cancel := context.WithCancel(ctx)
	work := &child{
		descriptor: descriptor, descriptorSHA: descriptorSHA, branch: branch, cancel: cancel,
		done: make(chan struct{}), startedMS: elapsedMS(orchestrator.started), reservedCalls: reservedCalls,
	}
	orchestrator.children[descriptor.ChildID] = work
	orchestrator.reservedCalls += reservedCalls
	go func() {
		defer func() {
			work.endedMS = elapsedMS(orchestrator.started)
			close(work.done)
		}()
		work.err = orchestrator.config.Executor.Execute(childContext, Invocation{Descriptor: descriptor, WorkspaceRef: branch.Ref()})
	}()
	return nil
}

type TimelineEvent struct {
	ChildID             string  `json:"child_id"`
	DescriptorSHA256    string  `json:"descriptor_sha256"`
	ParentLineageSHA256 string  `json:"parent_lineage_sha256"`
	ChildPlanSHA256     string  `json:"child_plan_sha256"`
	StartMS             float64 `json:"start_ms"`
	EndMS               float64 `json:"end_ms"`
	Outcome             string  `json:"outcome"`
}

type JoinResult struct {
	SelectedChildID   string
	SelectedRoot      workspace.Root
	ChildCount        uint32
	Completed         uint32
	DiscardedRefs     []workspace.Ref
	Timeline          []TimelineEvent
	ChangedBytes      uint64
	MaterializedBytes uint64
	MaxBranchDepth    uint32
	ReachableRoots    uint32
	DiscardedRoots    uint32
	ReservedCalls     uint32
}

type AwaitResult struct {
	ChildCount uint32
	Completed  uint32
	Timeline   []TimelineEvent
}

// Await freezes fanout and waits for every staged child without selecting or
// publishing a branch. The caller may inspect observed outputs before Seal.
func (orchestrator *Orchestrator) Await(ctx context.Context) (AwaitResult, error) {
	if orchestrator == nil || ctx == nil {
		return AwaitResult{}, ErrInvalidOrchestrator
	}
	orchestrator.mu.Lock()
	if orchestrator.terminal {
		orchestrator.mu.Unlock()
		return AwaitResult{}, ErrOrchestratorTerminal
	}
	if orchestrator.awaited || len(orchestrator.children) == 0 {
		orchestrator.mu.Unlock()
		return AwaitResult{}, ErrOrchestratorAwaited
	}
	orchestrator.awaited = true
	children := orderedChildren(orchestrator.children)
	orchestrator.mu.Unlock()
	if err := waitChildren(ctx, children); err != nil {
		return AwaitResult{}, errors.Join(err, orchestrator.failAwait(children))
	}
	for _, work := range children {
		if work.err != nil {
			return AwaitResult{}, errors.Join(ErrChildExecution, work.err, orchestrator.failAwait(children))
		}
	}
	orchestrator.mu.Lock()
	orchestrator.ready = true
	orchestrator.mu.Unlock()
	return AwaitResult{ChildCount: uint32(len(children)), Completed: uint32(len(children)), Timeline: timeline(children)}, nil
}

func (orchestrator *Orchestrator) failAwait(children []*child) error {
	orchestrator.mu.Lock()
	orchestrator.terminal = true
	orchestrator.mu.Unlock()
	return orchestrator.cancelAndDiscard(children)
}

func (orchestrator *Orchestrator) Seal(ctx context.Context, selectedChildID string) (JoinResult, error) {
	children, err := orchestrator.beginTerminal(selectedChildID)
	if err != nil {
		return JoinResult{}, err
	}
	if err := waitChildren(ctx, children); err != nil {
		return JoinResult{}, errors.Join(err, orchestrator.cancelAndDiscard(children))
	}
	for _, work := range children {
		if work.err != nil {
			return JoinResult{}, errors.Join(ErrChildExecution, work.err, orchestrator.cancelAndDiscard(children))
		}
	}
	roots := make(map[string]workspace.Root, len(children))
	for _, work := range children {
		root, sealErr := work.branch.Seal(orchestrator.config.ParentWorkspaceSHA256)
		if sealErr != nil {
			return JoinResult{}, errors.Join(sealErr, destroyRoots(orchestrator.config.Manager, roots), orchestrator.cancelAndDiscard(children))
		}
		roots[work.descriptor.ChildID] = root
	}
	selected := roots[selectedChildID]
	result := JoinResult{
		SelectedChildID: selectedChildID, SelectedRoot: selected,
		ChildCount: uint32(len(children)), Completed: uint32(len(children)), Timeline: timeline(children),
		ReachableRoots: 1, ReservedCalls: orchestrator.reservedCalls,
	}
	for _, root := range roots {
		result.ChangedBytes += root.ChangedBytes
		if root.Depth > result.MaxBranchDepth {
			result.MaxBranchDepth = root.Depth
		}
		info, inspectErr := orchestrator.config.Manager.Inspect(root.Ref())
		if inspectErr != nil {
			return JoinResult{}, errors.Join(inspectErr, destroyRoots(orchestrator.config.Manager, roots))
		}
		result.MaterializedBytes += info.TotalBytes
	}
	for childID, root := range roots {
		if childID == selectedChildID {
			continue
		}
		if err := orchestrator.config.Manager.Destroy(root.Ref()); err != nil {
			return JoinResult{}, errors.Join(err, destroyRoots(orchestrator.config.Manager, roots))
		}
		result.DiscardedRefs = append(result.DiscardedRefs, root.Ref())
	}
	result.DiscardedRoots = uint32(len(result.DiscardedRefs))
	sort.Slice(result.DiscardedRefs, func(left, right int) bool { return result.DiscardedRefs[left] < result.DiscardedRefs[right] })
	return result, nil
}

func (orchestrator *Orchestrator) beginTerminal(selectedChildID string) ([]*child, error) {
	if orchestrator == nil || !descriptorName.MatchString(selectedChildID) {
		return nil, ErrInvalidDescriptor
	}
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	if orchestrator.terminal {
		return nil, ErrOrchestratorTerminal
	}
	if orchestrator.awaited && !orchestrator.ready {
		return nil, ErrOrchestratorAwaited
	}
	if _, exists := orchestrator.children[selectedChildID]; !exists {
		return nil, ErrInvalidDescriptor
	}
	orchestrator.terminal = true
	return orderedChildren(orchestrator.children), nil
}

type ParentDisposition string

const (
	ParentInvalid   ParentDisposition = "invalid"
	ParentCancelled ParentDisposition = "cancelled"
	ParentTimeout   ParentDisposition = "timeout"
)

func (orchestrator *Orchestrator) Abort(ctx context.Context, disposition ParentDisposition) error {
	if orchestrator == nil || ctx == nil || (disposition != ParentInvalid && disposition != ParentCancelled && disposition != ParentTimeout) {
		return ErrInvalidOrchestrator
	}
	orchestrator.mu.Lock()
	if orchestrator.terminal {
		orchestrator.mu.Unlock()
		return ErrOrchestratorTerminal
	}
	orchestrator.terminal = true
	children := orderedChildren(orchestrator.children)
	orchestrator.mu.Unlock()
	return orchestrator.cancelAndDiscard(children)
}

func (orchestrator *Orchestrator) PrivateRefs() []workspace.Ref {
	if orchestrator == nil {
		return nil
	}
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	refs := make([]workspace.Ref, 0, len(orchestrator.children))
	for _, work := range orchestrator.children {
		refs = append(refs, work.branch.Ref())
	}
	sort.Slice(refs, func(left, right int) bool { return refs[left] < refs[right] })
	return refs
}

func orderedChildren(children map[string]*child) []*child {
	ids := make([]string, 0, len(children))
	for id := range children {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ordered := make([]*child, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, children[id])
	}
	return ordered
}

func waitChildren(ctx context.Context, children []*child) error {
	for _, work := range children {
		select {
		case <-work.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (orchestrator *Orchestrator) cancelAndDiscard(children []*child) error {
	for _, work := range children {
		work.cancel()
	}
	var cleanupErrors []error
	for _, work := range children {
		<-work.done
		cleanupErrors = append(cleanupErrors, work.branch.Discard())
	}
	return errors.Join(cleanupErrors...)
}

func destroyRoots(manager *workspace.Manager, roots map[string]workspace.Root) error {
	var cleanupErrors []error
	for _, root := range roots {
		cleanupErrors = append(cleanupErrors, manager.Destroy(root.Ref()))
	}
	return errors.Join(cleanupErrors...)
}

func elapsedMS(started time.Time) float64 {
	return max(0, float64(time.Since(started).Nanoseconds())/1e6)
}

func timeline(children []*child) []TimelineEvent {
	events := make([]TimelineEvent, 0, len(children))
	for _, work := range children {
		outcome := "ok"
		if work.err != nil {
			outcome = "error"
		}
		events = append(events, TimelineEvent{
			ChildID: work.descriptor.ChildID, DescriptorSHA256: work.descriptorSHA,
			ParentLineageSHA256: work.descriptor.ParentLineageSHA256,
			ChildPlanSHA256:     work.descriptor.ChildPlanSHA256,
			StartMS:             work.startedMS, EndMS: work.endedMS, Outcome: outcome,
		})
	}
	return events
}
