package playback

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const (
	SchemaVersion   = "pysolate.playback-bundle.v1"
	MaxEncodedBytes = 16 << 20
	maxBundleBytes  = MaxEncodedBytes
	maxEntries      = 256
	maxJSONNodes    = 65536
)

type Metadata struct {
	CapabilityPlanSHA256   string
	RequestSHA256          string
	ArtifactSHA256         string
	ExecutionProfileSHA256 string
	ExpectedStatus         string
	ExpectedResultSHA256   string
	InitialWorkspaceSHA256 string
	FinalWorkspaceSHA256   string
	Grants                 []capability.GrantBinding
}

type Bundle struct {
	SchemaVersion          string                       `json:"schema_version"`
	Identity               string                       `json:"bundle_sha256"`
	CapabilityPlanSHA256   string                       `json:"capability_plan_sha256"`
	RequestSHA256          string                       `json:"request_sha256"`
	ArtifactSHA256         string                       `json:"artifact_sha256"`
	ExecutionProfileSHA256 string                       `json:"execution_profile_sha256"`
	ExpectedStatus         string                       `json:"expected_status"`
	ExpectedResultSHA256   string                       `json:"expected_result_sha256"`
	InitialWorkspaceSHA256 string                       `json:"initial_workspace_sha256,omitempty"`
	FinalWorkspaceSHA256   string                       `json:"final_workspace_sha256,omitempty"`
	Grants                 []capability.GrantBinding    `json:"grants"`
	Entries                []capability.TranscriptEntry `json:"entries"`
}

type identityDocument struct {
	SchemaVersion          string                       `json:"schema_version"`
	CapabilityPlanSHA256   string                       `json:"capability_plan_sha256"`
	RequestSHA256          string                       `json:"request_sha256"`
	ArtifactSHA256         string                       `json:"artifact_sha256"`
	ExecutionProfileSHA256 string                       `json:"execution_profile_sha256"`
	ExpectedStatus         string                       `json:"expected_status"`
	ExpectedResultSHA256   string                       `json:"expected_result_sha256"`
	InitialWorkspaceSHA256 string                       `json:"initial_workspace_sha256,omitempty"`
	FinalWorkspaceSHA256   string                       `json:"final_workspace_sha256,omitempty"`
	Grants                 []capability.GrantBinding    `json:"grants"`
	Entries                []capability.TranscriptEntry `json:"entries"`
}

func CanonicalSHA256(raw []byte) (string, error) {
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return "", err
	}
	return SHA256(canonical), nil
}

func SHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func New(metadata Metadata, entries []capability.TranscriptEntry) (Bundle, error) {
	bundle := Bundle{
		SchemaVersion: SchemaVersion, CapabilityPlanSHA256: metadata.CapabilityPlanSHA256,
		RequestSHA256: metadata.RequestSHA256, ArtifactSHA256: metadata.ArtifactSHA256,
		ExecutionProfileSHA256: metadata.ExecutionProfileSHA256, ExpectedStatus: metadata.ExpectedStatus,
		ExpectedResultSHA256:   metadata.ExpectedResultSHA256,
		InitialWorkspaceSHA256: metadata.InitialWorkspaceSHA256, FinalWorkspaceSHA256: metadata.FinalWorkspaceSHA256,
		Grants: append([]capability.GrantBinding(nil), metadata.Grants...), Entries: cloneEntries(entries),
	}
	if err := normalizeAndValidate(&bundle); err != nil {
		return Bundle{}, err
	}
	identity, err := computeIdentity(bundle)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Identity = identity
	return bundle, nil
}

func Encode(value Bundle) ([]byte, error) {
	bundle := cloneBundle(value)
	if err := normalizeAndValidate(&bundle); err != nil {
		return nil, err
	}
	identity, err := computeIdentity(bundle)
	if err != nil {
		return nil, err
	}
	if value.Identity != "" && value.Identity != identity {
		return nil, errors.New("playback bundle identity mismatch")
	}
	bundle.Identity = identity
	encoded, err := json.Marshal(bundle)
	if err != nil || len(encoded) > maxBundleBytes {
		return nil, errors.New("encode playback bundle")
	}
	return encoded, nil
}

func Decode(raw []byte) (Bundle, error) {
	if len(raw) == 0 || len(raw) > maxBundleBytes || !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return Bundle{}, errors.New("invalid playback bundle JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, errors.New("decode playback bundle")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Bundle{}, errors.New("playback bundle contains trailing JSON")
	}
	originalIdentity := bundle.Identity
	bundle.Identity = ""
	encoded, err := Encode(bundle)
	if err != nil {
		return Bundle{}, err
	}
	var canonical Bundle
	if err := json.Unmarshal(encoded, &canonical); err != nil || canonical.Identity != originalIdentity || !bytes.Equal(encoded, raw) {
		return Bundle{}, errors.New("playback bundle is non-canonical or tampered")
	}
	return canonical, nil
}

func normalizeAndValidate(bundle *Bundle) error {
	if bundle == nil || bundle.SchemaVersion != SchemaVersion || !validDigest(bundle.CapabilityPlanSHA256) ||
		!validDigest(bundle.RequestSHA256) || !validDigest(bundle.ArtifactSHA256) || !validDigest(bundle.ExecutionProfileSHA256) ||
		(bundle.ExpectedStatus != "ok" && bundle.ExpectedStatus != "error") || !validDigest(bundle.ExpectedResultSHA256) ||
		len(bundle.Grants) == 0 || len(bundle.Entries) > maxEntries ||
		(bundle.InitialWorkspaceSHA256 == "") != (bundle.FinalWorkspaceSHA256 == "") {
		return errors.New("invalid playback bundle metadata")
	}
	if bundle.InitialWorkspaceSHA256 != "" && (!validDigest(bundle.InitialWorkspaceSHA256) || !validDigest(bundle.FinalWorkspaceSHA256)) {
		return errors.New("invalid playback workspace identities")
	}
	sort.Slice(bundle.Grants, func(left, right int) bool { return bundle.Grants[left].Capability < bundle.Grants[right].Capability })
	for index, grant := range bundle.Grants {
		if !validName(grant.Capability) || !validDigest(grant.PolicySHA256) || (index > 0 && bundle.Grants[index-1].Capability == grant.Capability) {
			return errors.New("invalid playback grant binding")
		}
	}
	sort.Slice(bundle.Entries, func(left, right int) bool {
		return bundle.Entries[left].OperationIndex < bundle.Entries[right].OperationIndex
	})
	seenOperations := make(map[uint32]struct{}, len(bundle.Entries))
	for index := range bundle.Entries {
		entry := &bundle.Entries[index]
		if _, duplicate := seenOperations[entry.OperationIndex]; duplicate || !validName(entry.Capability) {
			return errors.New("invalid playback operation")
		}
		seenOperations[entry.OperationIndex] = struct{}{}
		arguments, err := canonicalJSON(entry.Arguments)
		if err != nil || SHA256(arguments) != entry.ArgumentsSHA256 {
			return errors.New("invalid playback arguments")
		}
		result, err := canonicalJSON(entry.Result)
		if err != nil || SHA256(result) != entry.ResultSHA256 {
			return errors.New("invalid playback result")
		}
		entry.Arguments = arguments
		entry.Result = result
		if !validEvidence(entry.Evidence) {
			return errors.New("invalid playback transport evidence")
		}
	}
	return nil
}

func computeIdentity(bundle Bundle) (string, error) {
	document := identityDocument{
		SchemaVersion: bundle.SchemaVersion, CapabilityPlanSHA256: bundle.CapabilityPlanSHA256,
		RequestSHA256: bundle.RequestSHA256, ArtifactSHA256: bundle.ArtifactSHA256,
		ExecutionProfileSHA256: bundle.ExecutionProfileSHA256, ExpectedStatus: bundle.ExpectedStatus,
		ExpectedResultSHA256:   bundle.ExpectedResultSHA256,
		InitialWorkspaceSHA256: bundle.InitialWorkspaceSHA256, FinalWorkspaceSHA256: bundle.FinalWorkspaceSHA256,
		Grants: bundle.Grants, Entries: bundle.Entries,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	return SHA256(encoded), nil
}

func cloneBundle(value Bundle) Bundle {
	value.Grants = append([]capability.GrantBinding(nil), value.Grants...)
	value.Entries = cloneEntries(value.Entries)
	return value
}

func cloneEntries(entries []capability.TranscriptEntry) []capability.TranscriptEntry {
	cloned := make([]capability.TranscriptEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Arguments = append(json.RawMessage(nil), entry.Arguments...)
		cloned[index].Result = append(json.RawMessage(nil), entry.Result...)
	}
	return cloned
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 || !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return nil, errors.New("invalid canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("JSON contains trailing data")
	}
	return json.Marshal(document)
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func consumeJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxJSONNodes {
		return errors.New("JSON is too complex")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("invalid JSON object")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate JSON key")
			}
			seen[key] = struct{}{}
			if err := consumeJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func validName(value string) bool {
	if len(value) == 0 || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func validEvidence(evidence capability.TransportEvidence) bool {
	if len(evidence.MediaType) == 0 || len(evidence.MediaType) > 128 || evidence.BodyBytes > 1<<20 || !validDigest(evidence.BodySHA256) {
		return false
	}
	return (evidence.Kind == "http" && evidence.Status >= 100 && evidence.Status <= 599) ||
		(evidence.Kind == "branch_override" && evidence.Status == 200 && evidence.MediaType == "application/json")
}
