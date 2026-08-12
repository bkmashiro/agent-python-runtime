// Package operator provides bounded semantic inspection and branch execution
// helpers outside Runtime core. It owns no Agent conversation or provider
// parsing and uses only protected Runtime artifacts supplied by the Host.
package operator

import (
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const (
	InspectSchemaVersion = "pysolate.research-inspect.v1"
	CompareSchemaVersion = "pysolate.research-compare.v1"
	DAGSchemaVersion     = "pysolate.research-branch-dag.v1"
)

type BundleSummary struct {
	SchemaVersion        string   `json:"schema_version"`
	BundleSHA256         string   `json:"bundle_sha256"`
	Status               string   `json:"status"`
	SourceCalls          uint32   `json:"source_calls"`
	Capabilities         []string `json:"capabilities"`
	CallsTruncated       bool     `json:"calls_truncated"`
	HasWorkspace         bool     `json:"has_workspace"`
	ResultSHA256         string   `json:"result_sha256"`
	CapabilityPlanSHA256 string   `json:"capability_plan_sha256"`
	ArtifactSHA256       string   `json:"artifact_sha256"`
}

func InspectBundle(bundle playback.Bundle, maximumCalls uint32) BundleSummary {
	if maximumCalls == 0 || maximumCalls > 256 {
		maximumCalls = 64
	}
	capabilities := make([]string, 0, min(int(maximumCalls), len(bundle.Entries)))
	for index, entry := range bundle.Entries {
		if uint32(index) >= maximumCalls {
			break
		}
		capabilities = append(capabilities, entry.Capability)
	}
	return BundleSummary{
		SchemaVersion: InspectSchemaVersion, BundleSHA256: bundle.Identity, Status: bundle.ExpectedStatus,
		SourceCalls: uint32(len(bundle.Entries)), Capabilities: capabilities, CallsTruncated: uint32(len(bundle.Entries)) > maximumCalls,
		HasWorkspace: bundle.InitialWorkspaceSHA256 != "", ResultSHA256: bundle.ExpectedResultSHA256,
		CapabilityPlanSHA256: bundle.CapabilityPlanSHA256, ArtifactSHA256: bundle.ArtifactSHA256,
	}
}

type BundleComparison struct {
	SchemaVersion           string `json:"schema_version"`
	LeftBundleSHA256        string `json:"left_bundle_sha256"`
	RightBundleSHA256       string `json:"right_bundle_sha256"`
	SameStatus              bool   `json:"same_status"`
	SameResult              bool   `json:"same_result"`
	SamePlan                bool   `json:"same_plan"`
	SameArtifact            bool   `json:"same_artifact"`
	SameInitialWorkspace    bool   `json:"same_initial_workspace"`
	SameFinalWorkspace      bool   `json:"same_final_workspace"`
	CallDifferences         uint32 `json:"call_differences"`
	ComparedCalls           uint32 `json:"compared_calls"`
	CallComparisonTruncated bool   `json:"call_comparison_truncated"`
}

func CompareBundles(left, right playback.Bundle, maximumCalls uint32) BundleComparison {
	if maximumCalls == 0 || maximumCalls > 256 {
		maximumCalls = 64
	}
	maximumLength := len(left.Entries)
	if len(right.Entries) > maximumLength {
		maximumLength = len(right.Entries)
	}
	limit := maximumLength
	if limit > int(maximumCalls) {
		limit = int(maximumCalls)
	}
	var differences uint32
	for index := 0; index < limit; index++ {
		if index >= len(left.Entries) || index >= len(right.Entries) || !sameEntry(left.Entries[index], right.Entries[index]) {
			differences++
		}
	}
	return BundleComparison{
		SchemaVersion: CompareSchemaVersion, LeftBundleSHA256: left.Identity, RightBundleSHA256: right.Identity,
		SameStatus: left.ExpectedStatus == right.ExpectedStatus, SameResult: left.ExpectedResultSHA256 == right.ExpectedResultSHA256,
		SamePlan: left.CapabilityPlanSHA256 == right.CapabilityPlanSHA256, SameArtifact: left.ArtifactSHA256 == right.ArtifactSHA256,
		SameInitialWorkspace: left.InitialWorkspaceSHA256 == right.InitialWorkspaceSHA256,
		SameFinalWorkspace:   left.FinalWorkspaceSHA256 == right.FinalWorkspaceSHA256,
		CallDifferences:      differences, ComparedCalls: uint32(limit), CallComparisonTruncated: maximumLength > limit,
	}
}

func sameEntry(left, right capability.TranscriptEntry) bool {
	return left.OperationIndex == right.OperationIndex && left.Capability == right.Capability &&
		left.ArgumentsSHA256 == right.ArgumentsSHA256 && left.ResultSHA256 == right.ResultSHA256 &&
		left.Evidence.Kind == right.Evidence.Kind && left.Evidence.BodySHA256 == right.Evidence.BodySHA256
}

type ChildRelation struct {
	Manifest playback.BranchManifest
	Child    playback.Bundle
}

type DAGNode struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	ResultSHA256 string `json:"result_sha256"`
}

type DAGEdge struct {
	ParentBundleSHA256 string `json:"parent_bundle_sha256"`
	ChildBundleSHA256  string `json:"child_bundle_sha256"`
	BranchSHA256       string `json:"branch_sha256"`
	ForkOperation      uint32 `json:"fork_operation"`
	PrefixSHA256       string `json:"prefix_sha256"`
	SuffixMode         string `json:"suffix_mode"`
}

type BranchDAG struct {
	SchemaVersion string    `json:"schema_version"`
	Nodes         []DAGNode `json:"nodes"`
	Edges         []DAGEdge `json:"edges"`
	Truncated     bool      `json:"truncated"`
}

func ExportBranchDAG(parent playback.Bundle, children []ChildRelation, maximumNodes uint32) (BranchDAG, error) {
	if parent.Identity == "" || maximumNodes < 1 || maximumNodes > 4096 {
		return BranchDAG{}, fmt.Errorf("invalid bounded DAG request")
	}
	dag := BranchDAG{SchemaVersion: DAGSchemaVersion}
	dag.Nodes = append(dag.Nodes, DAGNode{ID: parent.Identity, Kind: "parent", Status: parent.ExpectedStatus, ResultSHA256: parent.ExpectedResultSHA256})
	sorted := append([]ChildRelation(nil), children...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Manifest.Identity < sorted[j].Manifest.Identity })
	for _, relation := range sorted {
		if uint32(len(dag.Nodes)) >= maximumNodes {
			dag.Truncated = true
			break
		}
		if relation.Child.Identity == "" || relation.Manifest.Identity == "" || relation.Manifest.ValidateParent(parent) != nil {
			return BranchDAG{}, fmt.Errorf("invalid child branch relation")
		}
		if err := validateChildRelation(parent, relation); err != nil {
			return BranchDAG{}, err
		}
		dag.Nodes = append(dag.Nodes, DAGNode{ID: relation.Child.Identity, Kind: "child", Status: relation.Child.ExpectedStatus, ResultSHA256: relation.Child.ExpectedResultSHA256})
		dag.Edges = append(dag.Edges, DAGEdge{
			ParentBundleSHA256: parent.Identity, ChildBundleSHA256: relation.Child.Identity,
			BranchSHA256: relation.Manifest.Identity, ForkOperation: relation.Manifest.ForkOperation,
			PrefixSHA256: relation.Manifest.PrefixSHA256, SuffixMode: string(relation.Manifest.SuffixMode),
		})
	}
	return dag, nil
}

func validateChildRelation(parent playback.Bundle, relation ChildRelation) error {
	child := relation.Child
	manifest := relation.Manifest
	if child.RequestSHA256 != manifest.RequestSHA256 || child.ArtifactSHA256 != manifest.ArtifactSHA256 ||
		child.ExecutionProfileSHA256 != manifest.ExecutionProfileSHA256 || child.InitialWorkspaceSHA256 != manifest.InitialWorkspaceSHA256 ||
		child.CapabilityPlanSHA256 != manifest.ChildCapabilityPlanSHA256 || !sameGrantBindings(child.Grants, manifest.ChildGrants) ||
		len(child.Entries) < int(manifest.ForkOperation) {
		return fmt.Errorf("child Bundle does not match branch admission or lineage")
	}
	for index := uint32(0); index < manifest.ForkOperation; index++ {
		if !sameEntry(parent.Entries[index], child.Entries[index]) {
			return fmt.Errorf("child Bundle does not preserve branch prefix")
		}
	}
	if manifest.SuffixMode != playback.BranchLiveSuffix {
		expected, err := manifest.PlaybackEntries(parent)
		if err != nil || len(expected) != len(child.Entries) {
			return fmt.Errorf("child Bundle does not match recorded branch tape")
		}
		for index := range expected {
			if !sameEntry(expected[index], child.Entries[index]) {
				return fmt.Errorf("child Bundle does not match recorded branch tape")
			}
		}
	}
	return nil
}

func sameGrantBindings(left, right []capability.GrantBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
