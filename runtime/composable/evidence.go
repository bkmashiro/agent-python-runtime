// Package composable validates body-free evidence for the bounded composable
// runtime mechanisms. It verifies records, not private result bodies.
package composable

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
)

const EvidenceSchemaVersion = "pysolate.composable-evidence.v1"

var (
	ErrInvalidEvidence = errors.New("invalid composable runtime evidence")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	tokenPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

type Claim string

const (
	ClaimCacheReuse        Claim = "cache_reuse"
	ClaimCOWDeferred       Claim = "cow_deferred"
	ClaimFreshResume       Claim = "fresh_resume"
	ClaimPreparedSingleUse Claim = "prepared_single_use"
	ClaimRealChildFanout   Claim = "real_child_fanout"
)

type BranchEvidence struct {
	ChangedBytes      uint64 `json:"changed_bytes"`
	MaterializedBytes uint64 `json:"materialized_bytes"`
	MaxDepth          uint32 `json:"max_depth"`
	ReachableRoots    uint32 `json:"reachable_roots"`
	DiscardedRoots    uint32 `json:"discarded_roots"`
}

type ChildEvidence struct {
	Count     uint32                   `json:"count"`
	Completed uint32                   `json:"completed"`
	Timeline  []subagent.TimelineEvent `json:"timeline"`
}

type Evidence struct {
	SchemaVersion         string                          `json:"schema_version"`
	SourceCommit          string                          `json:"source_commit"`
	ArtifactSHA256        string                          `json:"artifact_sha256"`
	ParentWorkspaceSHA256 string                          `json:"parent_workspace_sha256"`
	SelectedRootSHA256    string                          `json:"selected_root_sha256"`
	Mechanisms            runtimeconfig.MechanismEvidence `json:"mechanisms"`
	Observations          []streaming.ObservationIdentity `json:"observations"`
	Branch                BranchEvidence                  `json:"branch"`
	Children              ChildEvidence                   `json:"children"`
	Functions             agentfunction.Stats             `json:"functions"`
	Flights               agentfunction.FlightStats       `json:"flights"`
	Workflow              workflow.Metrics                `json:"workflow"`
	GuestCreated          uint32                          `json:"guest_created"`
	GuestDestroyed        uint32                          `json:"guest_destroyed"`
	Prepared              wazeroengine.PreparedState      `json:"prepared"`
	COW                   wazeroengine.COWProbe           `json:"cow"`
	Claims                []Claim                         `json:"claims"`
}

func DecodeEvidence(raw []byte) (Evidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, ErrInvalidEvidence
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Evidence{}, ErrInvalidEvidence
	}
	return evidence, nil
}

func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != EvidenceSchemaVersion || !commitPattern.MatchString(evidence.SourceCommit) ||
		!digestPattern.MatchString(evidence.ArtifactSHA256) || !digestPattern.MatchString(evidence.ParentWorkspaceSHA256) ||
		!digestPattern.MatchString(evidence.SelectedRootSHA256) || evidence.Mechanisms.Validate() != nil ||
		evidence.GuestDestroyed > evidence.GuestCreated || evidence.Prepared.SchemaVersion != "pysolate.prepared-runtime.v1" ||
		evidence.COW.SchemaVersion != "pysolate.cow-probe.v1" || !tokenPattern.MatchString(evidence.COW.Platform) ||
		!sort.SliceIsSorted(evidence.Claims, func(i, j int) bool { return evidence.Claims[i] < evidence.Claims[j] }) ||
		!sort.StringsAreSorted(evidence.COW.Blockers) {
		return ErrInvalidEvidence
	}
	if (!evidence.Prepared.Selected && (evidence.Prepared.Ready || evidence.Prepared.PreparedRuns != 0 || evidence.Prepared.FreshFallbackRuns != 0 || evidence.Prepared.PrepareMS != 0)) ||
		(evidence.Prepared.Ready && evidence.Prepared.PreparedRuns != 0) ||
		(evidence.COW.COWSelected && (!evidence.COW.MemoryCOWCandidate || evidence.COW.Fallback || len(evidence.COW.Blockers) != 0)) ||
		(evidence.COW.COWSelected && evidence.Mechanisms.Disposition(runtimeconfig.MechanismMemoryCOW) != runtimeconfig.MechanismSelected) {
		return ErrInvalidEvidence
	}
	for index, claim := range evidence.Claims {
		if (index > 0 && evidence.Claims[index-1] == claim) || !validClaim(claim) {
			return ErrInvalidEvidence
		}
	}
	for _, identity := range evidence.Observations {
		if identity.Validate(true) != nil {
			return ErrInvalidEvidence
		}
	}
	if evidence.Children.Completed > evidence.Children.Count || len(evidence.Children.Timeline) != int(evidence.Children.Completed) {
		return ErrInvalidEvidence
	}
	for _, event := range evidence.Children.Timeline {
		if !tokenPattern.MatchString(event.ChildID) || !digestPattern.MatchString(event.DescriptorSHA256) ||
			!digestPattern.MatchString(event.ParentLineageSHA256) || !digestPattern.MatchString(event.ChildPlanSHA256) ||
			event.StartMS < 0 || event.EndMS < event.StartMS || (event.Outcome != "ok" && event.Outcome != "error" && event.Outcome != "cancelled") {
			return ErrInvalidEvidence
		}
	}
	allowedBlocker := map[string]bool{
		"compiled_module_unavailable": true, "linear_memory_not_fixed_private_candidate": true,
		"linux_memfd_private_mapping_unavailable": true, "module_instance_state_not_resettable": true,
		"static_non_memory_state_not_censused": true, "wasi_host_state_not_resettable": true,
	}
	for _, blocker := range evidence.COW.Blockers {
		if !allowedBlocker[blocker] {
			return ErrInvalidEvidence
		}
	}
	for _, claim := range evidence.Claims {
		if !evidence.supports(claim) {
			return ErrInvalidEvidence
		}
	}
	return nil
}

func validClaim(claim Claim) bool {
	switch claim {
	case ClaimCacheReuse, ClaimCOWDeferred, ClaimFreshResume, ClaimPreparedSingleUse, ClaimRealChildFanout:
		return true
	default:
		return false
	}
}

func (evidence Evidence) supports(claim Claim) bool {
	switch claim {
	case ClaimCacheReuse:
		return evidence.Mechanisms.Disposition(runtimeconfig.MechanismFunctionCache) == runtimeconfig.MechanismSelected && evidence.Functions.Hits > 0
	case ClaimCOWDeferred:
		return evidence.COW.Fallback && !evidence.COW.COWSelected && len(evidence.COW.Blockers) > 0
	case ClaimFreshResume:
		return evidence.Mechanisms.Disposition(runtimeconfig.MechanismFreshReevaluation) == runtimeconfig.MechanismSelected &&
			evidence.GuestCreated == evidence.GuestDestroyed && evidence.GuestCreated >= 2 && evidence.Workflow.Lookups > 0
	case ClaimPreparedSingleUse:
		return evidence.Mechanisms.Disposition(runtimeconfig.MechanismPreparedRuntime) == runtimeconfig.MechanismSelected &&
			evidence.Prepared.Selected && evidence.Prepared.PreparedRuns == 1
	case ClaimRealChildFanout:
		return evidence.Mechanisms.Disposition(runtimeconfig.MechanismChildFanout) == runtimeconfig.MechanismSelected &&
			evidence.Children.Count >= 2 && evidence.Children.Completed == evidence.Children.Count && evidence.GuestCreated == evidence.GuestDestroyed
	default:
		return false
	}
}
