package playback

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const (
	BranchSchemaVersion = "pysolate.playback-branch.v1"
	MaxBranchBytes      = 8 << 20
)

type BranchSuffixMode string

const (
	BranchOverride       BranchSuffixMode = "override"
	BranchRecordedSuffix BranchSuffixMode = "recorded_suffix"
	BranchLiveSuffix     BranchSuffixMode = "live_suffix"
)

type BranchMetadata struct {
	ParentBundleSHA256        string
	ForkOperation             uint32
	RequestSHA256             string
	ArtifactSHA256            string
	ExecutionProfileSHA256    string
	InitialWorkspaceSHA256    string
	ChildCapabilityPlanSHA256 string
	ChildGrants               []capability.GrantBinding
	SuffixMode                BranchSuffixMode
}

type BranchManifest struct {
	SchemaVersion             string                       `json:"schema_version"`
	Identity                  string                       `json:"branch_sha256"`
	ParentBundleSHA256        string                       `json:"parent_bundle_sha256"`
	ForkOperation             uint32                       `json:"fork_operation"`
	PrefixSHA256              string                       `json:"prefix_sha256"`
	RequestSHA256             string                       `json:"request_sha256"`
	ArtifactSHA256            string                       `json:"artifact_sha256"`
	ExecutionProfileSHA256    string                       `json:"execution_profile_sha256"`
	InitialWorkspaceSHA256    string                       `json:"initial_workspace_sha256,omitempty"`
	ChildCapabilityPlanSHA256 string                       `json:"child_capability_plan_sha256"`
	ChildGrants               []capability.GrantBinding    `json:"child_grants"`
	SuffixMode                BranchSuffixMode             `json:"suffix_mode"`
	SuffixEntries             []capability.TranscriptEntry `json:"suffix_entries"`
}

type branchIdentityDocument struct {
	SchemaVersion             string                       `json:"schema_version"`
	ParentBundleSHA256        string                       `json:"parent_bundle_sha256"`
	ForkOperation             uint32                       `json:"fork_operation"`
	PrefixSHA256              string                       `json:"prefix_sha256"`
	RequestSHA256             string                       `json:"request_sha256"`
	ArtifactSHA256            string                       `json:"artifact_sha256"`
	ExecutionProfileSHA256    string                       `json:"execution_profile_sha256"`
	InitialWorkspaceSHA256    string                       `json:"initial_workspace_sha256,omitempty"`
	ChildCapabilityPlanSHA256 string                       `json:"child_capability_plan_sha256"`
	ChildGrants               []capability.GrantBinding    `json:"child_grants"`
	SuffixMode                BranchSuffixMode             `json:"suffix_mode"`
	SuffixEntries             []capability.TranscriptEntry `json:"suffix_entries"`
}

func NewBranchManifest(metadata BranchMetadata, parent Bundle, suffix []capability.TranscriptEntry) (BranchManifest, error) {
	if _, err := Encode(parent); err != nil {
		return BranchManifest{}, errors.New("branch parent is invalid")
	}
	manifest := BranchManifest{
		SchemaVersion: BranchSchemaVersion, ParentBundleSHA256: metadata.ParentBundleSHA256,
		ForkOperation: metadata.ForkOperation, RequestSHA256: metadata.RequestSHA256,
		ArtifactSHA256: metadata.ArtifactSHA256, ExecutionProfileSHA256: metadata.ExecutionProfileSHA256,
		InitialWorkspaceSHA256: metadata.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: metadata.ChildCapabilityPlanSHA256,
		ChildGrants: append([]capability.GrantBinding(nil), metadata.ChildGrants...), SuffixMode: metadata.SuffixMode,
		SuffixEntries: cloneEntries(suffix),
	}
	if parent.Identity == "" || metadata.ParentBundleSHA256 != parent.Identity || metadata.ForkOperation >= uint32(len(parent.Entries)) ||
		metadata.RequestSHA256 != parent.RequestSHA256 || metadata.ArtifactSHA256 != parent.ArtifactSHA256 ||
		metadata.ExecutionProfileSHA256 != parent.ExecutionProfileSHA256 || metadata.InitialWorkspaceSHA256 != parent.InitialWorkspaceSHA256 {
		return BranchManifest{}, errors.New("branch parent admission mismatch")
	}
	manifest.PrefixSHA256 = branchPrefixSHA256(parent, metadata.ForkOperation)
	if err := normalizeAndValidateBranch(&manifest); err != nil {
		return BranchManifest{}, err
	}
	manifest.Identity = branchIdentity(manifest)
	return manifest, nil
}

func EncodeBranchManifest(value BranchManifest) ([]byte, error) {
	manifest := cloneBranchManifest(value)
	if err := normalizeAndValidateBranch(&manifest); err != nil {
		return nil, err
	}
	identity := branchIdentity(manifest)
	if value.Identity != "" && value.Identity != identity {
		return nil, errors.New("branch manifest identity mismatch")
	}
	manifest.Identity = identity
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded) > MaxBranchBytes {
		return nil, errors.New("encode branch manifest")
	}
	return encoded, nil
}

func DecodeBranchManifest(raw []byte) (BranchManifest, error) {
	if len(raw) == 0 || len(raw) > MaxBranchBytes || !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return BranchManifest{}, errors.New("invalid branch manifest JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest BranchManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BranchManifest{}, errors.New("decode branch manifest")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BranchManifest{}, errors.New("branch manifest contains trailing JSON")
	}
	original := manifest.Identity
	manifest.Identity = ""
	encoded, err := EncodeBranchManifest(manifest)
	if err != nil {
		return BranchManifest{}, err
	}
	var canonical BranchManifest
	if json.Unmarshal(encoded, &canonical) != nil || canonical.Identity != original || !bytes.Equal(encoded, raw) {
		return BranchManifest{}, errors.New("branch manifest is non-canonical or tampered")
	}
	return canonical, nil
}

// Validate verifies the complete self-identity and canonical semantic content
// of an in-memory manifest. DecodeBranchManifest already performs this check
// for files, while this method keeps callers of the Go API from accidentally
// trusting a struct that was mutated after construction.
func (manifest BranchManifest) Validate() error {
	if manifest.Identity == "" {
		return errors.New("branch manifest identity is missing")
	}
	if _, err := EncodeBranchManifest(manifest); err != nil {
		return err
	}
	return nil
}

func (manifest BranchManifest) ValidateParent(parent Bundle) error {
	if manifest.Validate() != nil {
		return errors.New("branch manifest is invalid")
	}
	if _, err := Encode(parent); err != nil {
		return errors.New("branch parent is invalid")
	}
	if parent.Identity == "" || manifest.ParentBundleSHA256 != parent.Identity || manifest.ForkOperation >= uint32(len(parent.Entries)) ||
		manifest.PrefixSHA256 != branchPrefixSHA256(parent, manifest.ForkOperation) ||
		manifest.RequestSHA256 != parent.RequestSHA256 || manifest.ArtifactSHA256 != parent.ArtifactSHA256 ||
		manifest.ExecutionProfileSHA256 != parent.ExecutionProfileSHA256 || manifest.InitialWorkspaceSHA256 != parent.InitialWorkspaceSHA256 {
		return errors.New("branch parent admission mismatch")
	}
	return nil
}

func (manifest BranchManifest) PlaybackEntries(parent Bundle) ([]capability.TranscriptEntry, error) {
	if err := manifest.ValidateParent(parent); err != nil {
		return nil, err
	}
	entries := cloneEntries(parent.Entries[:manifest.ForkOperation])
	entries = append(entries, cloneEntries(manifest.SuffixEntries)...)
	return entries, nil
}

func normalizeAndValidateBranch(manifest *BranchManifest) error {
	if manifest == nil || manifest.SchemaVersion != BranchSchemaVersion || !validDigest(manifest.ParentBundleSHA256) ||
		!validDigest(manifest.PrefixSHA256) || !validDigest(manifest.RequestSHA256) || !validDigest(manifest.ArtifactSHA256) ||
		!validDigest(manifest.ExecutionProfileSHA256) || !validDigest(manifest.ChildCapabilityPlanSHA256) ||
		(manifest.InitialWorkspaceSHA256 != "" && !validDigest(manifest.InitialWorkspaceSHA256)) || len(manifest.ChildGrants) == 0 ||
		len(manifest.SuffixEntries) > maxEntries {
		return errors.New("invalid branch manifest metadata")
	}
	switch manifest.SuffixMode {
	case BranchOverride, BranchRecordedSuffix:
		if len(manifest.SuffixEntries) == 0 {
			return errors.New("recorded branch suffix is empty")
		}
	case BranchLiveSuffix:
		if len(manifest.SuffixEntries) != 0 {
			return errors.New("live branch suffix cannot contain a recorded tape")
		}
	default:
		return errors.New("invalid branch suffix mode")
	}
	sort.Slice(manifest.ChildGrants, func(i, j int) bool { return manifest.ChildGrants[i].Capability < manifest.ChildGrants[j].Capability })
	for index, grant := range manifest.ChildGrants {
		if !validName(grant.Capability) || !validDigest(grant.PolicySHA256) || (index > 0 && manifest.ChildGrants[index-1].Capability == grant.Capability) {
			return errors.New("invalid branch grant")
		}
	}
	for index := range manifest.SuffixEntries {
		entry := &manifest.SuffixEntries[index]
		expectedOperation := manifest.ForkOperation + uint32(index)
		if entry.OperationIndex != expectedOperation || !validName(entry.Capability) {
			return errors.New("invalid branch suffix operation")
		}
		arguments, err := canonicalJSON(entry.Arguments)
		if err != nil || SHA256(arguments) != entry.ArgumentsSHA256 {
			return errors.New("invalid branch suffix arguments")
		}
		result, err := canonicalJSON(entry.Result)
		if err != nil || SHA256(result) != entry.ResultSHA256 || len(result) > 1<<20 {
			return errors.New("invalid branch suffix result")
		}
		if !validBranchEvidence(entry.Evidence, manifest.SuffixMode) {
			return errors.New("invalid branch suffix evidence")
		}
		entry.Arguments, entry.Result = arguments, result
	}
	return nil
}

func validBranchEvidence(evidence capability.TransportEvidence, mode BranchSuffixMode) bool {
	if mode == BranchRecordedSuffix {
		return validEvidence(evidence)
	}
	return mode == BranchOverride && evidence.Kind == "branch_override" && evidence.Status == 200 &&
		evidence.MediaType == "application/json" && evidence.BodyBytes <= 1<<20 && validDigest(evidence.BodySHA256)
}

func branchPrefixSHA256(parent Bundle, fork uint32) string {
	prefix := struct {
		SchemaVersion      string                       `json:"schema_version"`
		ParentBundleSHA256 string                       `json:"parent_bundle_sha256"`
		ForkOperation      uint32                       `json:"fork_operation"`
		Entries            []capability.TranscriptEntry `json:"entries"`
	}{"pysolate.playback-branch-prefix.v1", parent.Identity, fork, cloneEntries(parent.Entries[:fork])}
	encoded, _ := json.Marshal(prefix)
	return SHA256(encoded)
}

func branchIdentity(manifest BranchManifest) string {
	document := branchIdentityDocument{
		SchemaVersion: manifest.SchemaVersion, ParentBundleSHA256: manifest.ParentBundleSHA256,
		ForkOperation: manifest.ForkOperation, PrefixSHA256: manifest.PrefixSHA256, RequestSHA256: manifest.RequestSHA256,
		ArtifactSHA256: manifest.ArtifactSHA256, ExecutionProfileSHA256: manifest.ExecutionProfileSHA256,
		InitialWorkspaceSHA256: manifest.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: manifest.ChildCapabilityPlanSHA256,
		ChildGrants: manifest.ChildGrants, SuffixMode: manifest.SuffixMode, SuffixEntries: manifest.SuffixEntries,
	}
	encoded, _ := json.Marshal(document)
	return SHA256(encoded)
}

func cloneBranchManifest(value BranchManifest) BranchManifest {
	value.ChildGrants = append([]capability.GrantBinding(nil), value.ChildGrants...)
	value.SuffixEntries = cloneEntries(value.SuffixEntries)
	return value
}
