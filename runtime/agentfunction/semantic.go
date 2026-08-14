package agentfunction

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

// QualifiedGuestInvocation is an opaque Host-minted proof that one exact
// whole-Run semantic region passed the static reuse contract.
type QualifiedGuestInvocation struct {
	invocation Invocation
}

func NewQualifiedGuestInvocation(invocation Invocation, analysis semantic.Analysis, plan semantic.Plan, request []byte) (QualifiedGuestInvocation, error) {
	if err := invocation.Validate(); err != nil || invocation.Admission != Cacheable ||
		invocation.SemanticAnalysisSHA256 != "" || invocation.SemanticPlanSHA256 != "" ||
		invocation.SemanticAnalyzerSHA256 != "" || invocation.SemanticRegionID != "" ||
		invocation.SemanticRequestContractSHA256 != "" {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	if err := analysis.Validate(); err != nil || plan.Validate() != nil || len(plan.Regions) != 1 {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	analysisIdentity, _, err := analysis.Identity()
	if err != nil {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	planIdentity, _, err := plan.Identity()
	if err != nil {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	planAnalysisIdentity, _, err := plan.Analysis.Identity()
	if err != nil || planAnalysisIdentity != analysisIdentity {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	requestContract, err := GuestRequestContractSHA256(request)
	if err != nil {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	region := plan.Regions[0]
	if plan.Analysis.SourceSHA256 != invocation.FunctionSourceSHA256 ||
		plan.Analysis.ArtifactSHA256 != invocation.ArtifactSHA256 ||
		plan.Analysis.ExecutionProfileSHA256 != invocation.ExecutionProfileSHA256 ||
		plan.Analysis.ImportClosureSHA256 != invocation.ImportClosureSHA256 ||
		region.Kind != semantic.RegionWholeRun || region.FunctionID != "" ||
		region.ASTSHA256 != analysis.ASTSHA256 || !region.Reusable() ||
		!semanticDependenciesMatchInvocation(region.Dependencies, invocation) {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	invocation.SemanticAnalysisSHA256 = analysisIdentity
	invocation.SemanticPlanSHA256 = planIdentity
	invocation.SemanticAnalyzerSHA256 = analysis.AnalyzerSHA256
	invocation.SemanticRegionID = region.ID
	invocation.SemanticRequestContractSHA256 = requestContract
	if err := invocation.Validate(); err != nil {
		return QualifiedGuestInvocation{}, ErrGuestQualification
	}
	return QualifiedGuestInvocation{invocation: invocation}, nil
}

func (qualified QualifiedGuestInvocation) Identity() (string, []byte, error) {
	return qualified.invocation.Identity()
}

func SemanticWholeRunDependencies(invocation Invocation) ([]semantic.Dependency, error) {
	if err := invocation.Validate(); err != nil {
		return nil, ErrGuestQualification
	}
	inputDigest := sha256.Sum256(invocation.CanonicalInputs)
	dependencies := []semantic.Dependency{{Kind: semantic.DependencyCanonicalInputs, IdentitySHA256: fmt.Sprintf("sha256:%x", inputDigest[:])}}
	for _, root := range invocation.ImmutableRootSHA256 {
		dependencies = append(dependencies, semantic.Dependency{Kind: semantic.DependencyImmutableRoot, IdentitySHA256: root})
	}
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].Kind != dependencies[j].Kind {
			return dependencies[i].Kind < dependencies[j].Kind
		}
		return dependencies[i].IdentitySHA256 < dependencies[j].IdentitySHA256
	})
	return dependencies, nil
}

func semanticDependenciesMatchInvocation(actual []semantic.Dependency, invocation Invocation) bool {
	expected, err := SemanticWholeRunDependencies(invocation)
	if err != nil || len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
