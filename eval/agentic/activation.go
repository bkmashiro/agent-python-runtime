package agentic

import (
	"errors"
	"os"
	"time"
)

var ErrPilotActivation = errors.New("invalid agentic pilot activation")

type PilotActivation struct {
	SchemaVersion             string            `json:"schema_version"`
	Status                    string            `json:"status"`
	ExecutionMode             string            `json:"execution_mode"`
	PlanDigest                string            `json:"plan_digest"`
	TreatmentDigest           string            `json:"treatment_digest,omitempty"`
	RepositoryCommit          string            `json:"repository_commit"`
	HostArtifactDigest        string            `json:"host_artifact_digest"`
	DatasetManifestDigest     string            `json:"dataset_manifest_digest"`
	ProviderCatalogDigest     string            `json:"provider_catalog_digest"`
	ProviderCatalogObservedAt string            `json:"provider_catalog_observed_at"`
	GuestArtifacts            map[string]string `json:"guest_artifacts"`
	ApprovedBy                string            `json:"approved_by"`
	ApprovedAt                string            `json:"approved_at"`
	Digest                    string            `json:"-"`
}

func LoadPilotActivation(path string, plan DevelopmentPilotPlan, hostArtifactDigest string) (PilotActivation, error) {
	return loadPilotActivation(path, plan, hostArtifactDigest, "")
}

func LoadPilotActivationForTreatment(path string, plan DevelopmentPilotPlan, hostArtifactDigest string, treatment DevelopmentTreatment) (PilotActivation, error) {
	if !treatment.valid() || !validDigest(treatment.Digest) {
		return PilotActivation{}, ErrPilotActivation
	}
	return loadPilotActivation(path, plan, hostArtifactDigest, treatment.Digest)
}

func loadPilotActivation(path string, plan DevelopmentPilotPlan, hostArtifactDigest, treatmentDigest string) (PilotActivation, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 32*1024 {
		return PilotActivation{}, ErrPilotActivation
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PilotActivation{}, ErrPilotActivation
	}
	var activation PilotActivation
	if decodeStrict(data, &activation) != nil {
		return PilotActivation{}, ErrPilotActivation
	}
	approvedAt, timeErr := time.Parse(time.RFC3339, activation.ApprovedAt)
	catalogObservedAt, catalogTimeErr := time.Parse(time.RFC3339, activation.ProviderCatalogObservedAt)
	legacy := activation.SchemaVersion == "agentic-pilot-activation/v1" && activation.TreatmentDigest == "" && treatmentDigest == ""
	treatmentBound := activation.SchemaVersion == "agentic-pilot-activation/v2" && activation.ExecutionMode == "canary" &&
		validDigest(treatmentDigest) && activation.TreatmentDigest == treatmentDigest
	if (!legacy && !treatmentBound) || activation.Status != "approved" ||
		(activation.ExecutionMode != "canary" && activation.ExecutionMode != "pilot") ||
		activation.PlanDigest != plan.Digest || !validLowerHex(activation.RepositoryCommit, 40) ||
		activation.HostArtifactDigest != hostArtifactDigest || activation.DatasetManifestDigest != plan.DatasetManifestDigest ||
		!validDigest(activation.ProviderCatalogDigest) || catalogTimeErr != nil || catalogObservedAt.Location() != time.UTC ||
		len(activation.GuestArtifacts) != 1 || !validDigest(activation.GuestArtifacts["core"]) ||
		activation.ApprovedBy != "owner" || timeErr != nil || approvedAt.Location() != time.UTC {
		return PilotActivation{}, ErrPilotActivation
	}
	activation.Digest = digest(data)
	return activation, nil
}

func (activation PilotActivation) Identity(condition Condition) (ExecutionIdentity, error) {
	identity := ExecutionIdentity{
		RepositoryCommit: activation.RepositoryCommit, HostArtifactDigest: activation.HostArtifactDigest,
		DatasetManifestDigest: activation.DatasetManifestDigest,
		ProviderCatalogDigest: activation.ProviderCatalogDigest, ProviderCatalogObservedAt: activation.ProviderCatalogObservedAt,
	}
	if condition != ConditionDirect {
		identity.GuestProfile = "core"
		identity.GuestArtifactDigest = activation.GuestArtifacts["core"]
	}
	if !validExecutionIdentity(identity, condition) {
		return ExecutionIdentity{}, ErrPilotActivation
	}
	return identity, nil
}
