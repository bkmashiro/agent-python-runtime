package composable_test

import (
	"bytes"
	"encoding/json"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/composable"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
)

func TestEvidenceValidatesBodyFreeMechanismClaims(t *testing.T) {
	modes := selectedModes(t)
	evidence := composable.Evidence{
		SchemaVersion:  composable.EvidenceSchemaVersion,
		SourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: digest('a'), ParentWorkspaceSHA256: digest('b'), SelectedRootSHA256: digest('c'),
		Mechanisms:   modes,
		Observations: []streaming.ObservationIdentity{observationIdentity()},
		Branch:       composable.BranchEvidence{ChangedBytes: 10, MaterializedBytes: 20, MaxDepth: 1, ReachableRoots: 1, DiscardedRoots: 1},
		Children: composable.ChildEvidence{Count: 2, Completed: 2, Timeline: []subagent.TimelineEvent{
			{ChildID: "left", DescriptorSHA256: digest('d'), ParentLineageSHA256: digest('e'), ChildPlanSHA256: digest('1'), Outcome: "ok", StartMS: 0, EndMS: 1},
			{ChildID: "right", DescriptorSHA256: digest('f'), ParentLineageSHA256: digest('e'), ChildPlanSHA256: digest('1'), Outcome: "ok", StartMS: 0, EndMS: 2},
		}},
		Functions:    agentfunction.Stats{Hits: 1, Misses: 1, Writes: 1, StoredBytes: 12},
		Flights:      agentfunction.FlightStats{Leaders: 1, Waiters: 1},
		Workflow:     workflow.Metrics{GuestInstances: 1, Lookups: 2, RetainedStateBytes: 20, EvaluationMS: 1},
		GuestCreated: 4, GuestDestroyed: 4,
		Prepared: wazeroengine.PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1", Selected: true, PreparedRuns: 1, FreshFallbackRuns: 1, PrepareMS: 1},
		COW:      wazeroengine.COWProbe{SchemaVersion: "pysolate.cow-probe.v1", Platform: "linux", PreparedCompatible: true, MemoryCount: 1, MemoryFixed: true, MemoryCOWCandidate: true, Fallback: true, Blockers: []string{"module_instance_state_not_resettable", "static_non_memory_state_not_censused", "wasi_host_state_not_resettable"}},
		Claims:   []composable.Claim{composable.ClaimCacheReuse, composable.ClaimCOWDeferred, composable.ClaimFreshResume, composable.ClaimPreparedSingleUse, composable.ClaimRealChildFanout},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := composable.DecodeEvidence(raw)
	if err != nil || decoded.Validate() != nil {
		t.Fatalf("decode err=%v evidence=%+v", err, decoded)
	}
}

func TestEvidenceRejectsUnknownFieldsSubstitutionAndUnsupportedClaims(t *testing.T) {
	evidence := composable.Evidence{SchemaVersion: composable.EvidenceSchemaVersion}
	raw, _ := json.Marshal(evidence)
	raw = bytes.Replace(raw, []byte(`{"schema_version"`), []byte(`{"private_body":"secret","schema_version"`), 1)
	if _, err := composable.DecodeEvidence(raw); err == nil {
		t.Fatal("accepted unknown private body")
	}

	valid := composable.Evidence{
		SchemaVersion: composable.EvidenceSchemaVersion, SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: digest('a'), ParentWorkspaceSHA256: digest('b'), SelectedRootSHA256: digest('c'),
		Mechanisms: selectedModes(t), GuestCreated: 1, GuestDestroyed: 1,
		Prepared: wazeroengine.PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1"},
		COW:      wazeroengine.COWProbe{SchemaVersion: "pysolate.cow-probe.v1", Platform: "darwin", Fallback: true, Blockers: []string{"host/path/leak"}},
		Claims:   []composable.Claim{composable.ClaimRealChildFanout},
	}
	if err := valid.Validate(); err == nil {
		t.Fatal("accepted unsupported claim/private blocker")
	}
}

func selectedModes(t *testing.T) runtimeconfig.MechanismEvidence {
	t.Helper()
	set := runtimeconfig.MechanismSet{Streaming: true, StagedObservation: true, PrivateWorkspace: true, ImmutableBranches: true, ChildFanout: true, FunctionCache: true, SingleFlight: true, FreshReevaluation: true, PreparedRuntime: true}
	_, evidence, err := runtimeconfig.ResolveMechanisms(set, set)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func observationIdentity() streaming.ObservationIdentity {
	return streaming.ObservationIdentity{
		SchemaVersion: streaming.ObservationIdentitySchemaVersion,
		StreamEpoch:   "stream-1", WorkflowEpoch: "workflow-1", SourceSHA256: digest('1'), SuiteSHA256: digest('2'), SuiteRange: streaming.ByteRange{Start: 0, End: 10},
		DynamicOccurrence: 1, ArgumentsSHA256: digest('3'), Capability: "fixture.read", SpecSHA256: digest('4'),
		HandlerIdentity: "fixture-handler-v1", PlanSHA256: digest('5'), GrantPolicySHA256: digest('6'),
		FreshnessEpoch: "freshness-1", ExpiryEpoch: "expiry-1", PrivacyPartition: "project-private", ParentLineageSHA256: digest('9'),
	}
}

func digest(character byte) string {
	value := make([]byte, 71)
	copy(value, "sha256:")
	for index := 7; index < len(value); index++ {
		value[index] = character
	}
	return string(value)
}
