package evaluationworkloads

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/workloads"
)

func Corpus(definitions []workloads.Workload) (evaluation.Corpus, error) {
	if len(definitions) != 3 {
		return evaluation.Corpus{}, evaluation.ErrInvalid
	}
	rows := make([]evaluation.Workload, len(definitions))
	for i, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return evaluation.Corpus{}, evaluation.ErrInvalid
		}
		family, treatments, capabilityNames, err := familyAndTreatments(definition.ID)
		if err != nil {
			return evaluation.Corpus{}, err
		}
		capabilities := make([]evaluation.CapabilityRequirement, len(capabilityNames))
		for j, capability := range capabilityNames {
			capabilities[j] = evaluation.CapabilityRequirement{Name: capability, EffectClass: evaluation.EffectExternalRead, Playback: evaluation.PlaybackCaptured}
		}
		oracle := evaluation.Oracle{Kind: evaluation.OracleResultOnly, ExpectedResultSHA256: definition.ExpectedResultSHA256, ExpectedCapabilityCalls: definition.ExpectedCapabilityCalls}
		if len(definition.ExpectedWorkspace) > 0 {
			oracle.Kind = evaluation.OracleResultAndWorkspace
			oracle.ExpectedWorkspaceSHA256, err = workspaceIdentity(definition.ExpectedWorkspace)
			if err != nil {
				return evaluation.Corpus{}, evaluation.ErrInvalid
			}
		}
		rows[i] = evaluation.Workload{ID: definition.ID, Version: 1, Family: family, CodeSHA256: definition.CodeSHA256, InputSHA256: definition.InputSHA256, WorkspaceSeedSHA256: definition.WorkspaceSeedSHA256, RequiredCapabilities: capabilities, Treatments: treatments, Oracle: oracle}
	}
	corpus := evaluation.Corpus{SchemaVersion: evaluation.CorpusSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, Workloads: rows}
	if _, _, err := evaluation.EncodeCorpus(corpus); err != nil {
		return evaluation.Corpus{}, err
	}
	return corpus, nil
}

func familyAndTreatments(id string) (evaluation.Family, []evaluation.Treatment, []string, error) {
	switch id {
	case "structured-source-v1":
		return evaluation.FamilyStructuredSource, []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch}, []string{"sources.benchmark_manifest", "sources.demo_catalog"}, nil
	case "stateful-local-v1":
		return evaluation.FamilyStatefulLocal, []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay}, []string{}, nil
	case "bounded-planning-v1":
		return evaluation.FamilyBoundedPlanning, []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch, evaluation.TreatmentDeterministicVerify}, []string{"sources.demo_catalog"}, nil
	default:
		return "", nil, nil, evaluation.ErrInvalid
	}
}

func workspaceIdentity(entries []workloads.WorkspaceEntry) (string, error) {
	payload, err := json.Marshal(struct {
		Domain  string                     `json:"domain"`
		Entries []workloads.WorkspaceEntry `json:"entries"`
	}{Domain: "pysolate.workload-workspace-oracle.v1", Entries: entries})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}
