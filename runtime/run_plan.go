package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

const maxRunPlanModules = 1024

var (
	ErrInvalidSourceValidationEvidence = errors.New("invalid source validation evidence")
	ErrInvalidFrozenRunPlan            = errors.New("invalid frozen RunPlan")
)

type RunPlanState string

const RunPlanFrozen RunPlanState = "frozen"

type sourceValidationDocument struct {
	SchemaVersion       uint32   `json:"schema_version"`
	Validator           string   `json:"validator"`
	Status              string   `json:"status"`
	SourceSHA256        string   `json:"source_sha256"`
	Profile             string   `json:"profile"`
	DeclaredImportRoots []string `json:"declared_import_roots"`
	ASTImportRoots      []string `json:"ast_import_roots"`
	BytecodeChecked     bool     `json:"bytecode_checked"`
	BaselineModules     []string `json:"baseline_modules"`
	EntryClosureModules []string `json:"entry_closure_modules"`
	SealedModules       []string `json:"sealed_modules"`
}

type SourceValidationEvidence struct {
	document       sourceValidationDocument
	evidenceSHA256 string
}

func DecodeSourceValidationEvidence(data []byte) (SourceValidationEvidence, error) {
	if len(data) == 0 || len(data) > maxSourceCompatibilityBytes || rejectDuplicateBoundedJSON(data) != nil {
		return SourceValidationEvidence{}, ErrInvalidSourceValidationEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document sourceValidationDocument
	if decoder.Decode(&document) != nil {
		return SourceValidationEvidence{}, ErrInvalidSourceValidationEvidence
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SourceValidationEvidence{}, ErrInvalidSourceValidationEvidence
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return SourceValidationEvidence{}, ErrInvalidSourceValidationEvidence
	}
	digest := sha256.Sum256(canonical)
	result := SourceValidationEvidence{document: cloneSourceValidationDocument(document), evidenceSHA256: "sha256:" + hex.EncodeToString(digest[:])}
	if result.Validate() != nil {
		return SourceValidationEvidence{}, ErrInvalidSourceValidationEvidence
	}
	return result, nil
}

func (evidence SourceValidationEvidence) Validate() error {
	document := evidence.document
	if document.SchemaVersion != 1 || document.Validator != "exact-guest-static-imports-v1" || document.Status != "ready" ||
		!document.BytecodeChecked || !validProfileID(document.Profile) || !validProfileDigest(document.SourceSHA256) ||
		!sortedUniqueImportRoots(document.DeclaredImportRoots) || !sortedUniqueImportRoots(document.ASTImportRoots) ||
		!equalStrings(document.DeclaredImportRoots, document.ASTImportRoots) || !validModuleSet(document.BaselineModules) ||
		!validModuleSet(document.EntryClosureModules) || !validModuleSet(document.SealedModules) ||
		len(document.BaselineModules)+len(document.EntryClosureModules) != len(document.SealedModules) {
		return ErrInvalidSourceValidationEvidence
	}
	if intersectsSorted(document.BaselineModules, document.EntryClosureModules) ||
		!equalStrings(mergeSorted(document.BaselineModules, document.EntryClosureModules), document.SealedModules) {
		return ErrInvalidSourceValidationEvidence
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return ErrInvalidSourceValidationEvidence
	}
	digest := sha256.Sum256(canonical)
	if evidence.evidenceSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		return ErrInvalidSourceValidationEvidence
	}
	return nil
}

func (evidence SourceValidationEvidence) MarshalJSON() ([]byte, error) {
	return json.Marshal(evidence.document)
}
func (evidence SourceValidationEvidence) EvidenceSHA256() string { return evidence.evidenceSHA256 }
func (evidence SourceValidationEvidence) SourceSHA256() string   { return evidence.document.SourceSHA256 }
func (evidence SourceValidationEvidence) ProfileID() string      { return evidence.document.Profile }
func (evidence SourceValidationEvidence) DeclaredImportRoots() []string {
	return cloneStrings(evidence.document.DeclaredImportRoots)
}
func (evidence SourceValidationEvidence) ASTImportRoots() []string {
	return cloneStrings(evidence.document.ASTImportRoots)
}
func (evidence SourceValidationEvidence) BaselineModules() []string {
	return cloneStrings(evidence.document.BaselineModules)
}
func (evidence SourceValidationEvidence) EntryClosureModules() []string {
	return cloneStrings(evidence.document.EntryClosureModules)
}
func (evidence SourceValidationEvidence) SealedModules() []string {
	return cloneStrings(evidence.document.SealedModules)
}

type frozenRunPlanDocument struct {
	SchemaVersion                  uint32       `json:"schema_version"`
	State                          RunPlanState `json:"state"`
	RequestSHA256                  string       `json:"request_sha256"`
	SourceSHA256                   string       `json:"source_sha256"`
	Profile                        string       `json:"profile"`
	ArtifactSHA256                 string       `json:"artifact_sha256"`
	ManifestSHA256                 string       `json:"manifest_sha256"`
	CompatibilityEvidenceSHA256    string       `json:"compatibility_evidence_sha256"`
	SourceValidationEvidenceSHA256 string       `json:"source_validation_evidence_sha256"`
	ImportPolicySHA256             string       `json:"import_policy_sha256"`
	DeclaredImportRoots            []string     `json:"declared_import_roots"`
	ASTImportRoots                 []string     `json:"ast_import_roots"`
	BaselineModules                []string     `json:"baseline_modules"`
	EntryClosureModules            []string     `json:"entry_closure_modules"`
	SealedModules                  []string     `json:"sealed_modules"`
	BytecodeChecked                bool         `json:"bytecode_checked"`
	PlanSHA256                     string       `json:"plan_sha256,omitempty"`
}

type FrozenRunPlan struct{ document frozenRunPlanDocument }

func NewFrozenRunPlan(rawRequest []byte, request RunRequest, compatibility CompatibilityResult, validation SourceValidationEvidence) (FrozenRunPlan, error) {
	if len(rawRequest) == 0 || compatibility.Validate() != nil || compatibility.Status() != SourceCompatible ||
		compatibility.ArtifactSHA256() == "" || validation.Validate() != nil {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	requestDigest := sha256.Sum256(rawRequest)
	sourceDigest := sha256.Sum256([]byte(request.Code))
	sourceSHA256 := "sha256:" + hex.EncodeToString(sourceDigest[:])
	if request.Compatibility == nil || request.Compatibility.Profile != compatibility.ProfileID() ||
		compatibility.ProfileID() != validation.ProfileID() || sourceSHA256 != compatibility.SourceSHA256() ||
		sourceSHA256 != validation.SourceSHA256() || !equalStrings(compatibility.DeclaredImports(), validation.DeclaredImportRoots()) ||
		!equalStrings(validation.DeclaredImportRoots(), validation.ASTImportRoots()) {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	policyCanonical, _ := json.Marshal(validation.SealedModules())
	policyDigest := sha256.Sum256(append([]byte("agent-python-runtime-import-policy-v1\x00"), policyCanonical...))
	document := frozenRunPlanDocument{
		SchemaVersion: 1, State: RunPlanFrozen, RequestSHA256: "sha256:" + hex.EncodeToString(requestDigest[:]), SourceSHA256: sourceSHA256,
		Profile: compatibility.ProfileID(), ArtifactSHA256: compatibility.ArtifactSHA256(), ManifestSHA256: compatibility.ManifestSHA256(),
		CompatibilityEvidenceSHA256: compatibility.EvidenceSHA256(), SourceValidationEvidenceSHA256: validation.EvidenceSHA256(),
		ImportPolicySHA256: "sha256:" + hex.EncodeToString(policyDigest[:]), DeclaredImportRoots: compatibility.DeclaredImports(),
		ASTImportRoots: validation.ASTImportRoots(), BaselineModules: validation.BaselineModules(),
		EntryClosureModules: validation.EntryClosureModules(), SealedModules: validation.SealedModules(), BytecodeChecked: true,
	}
	plan := FrozenRunPlan{document: document}
	plan.document.PlanSHA256 = plan.digest()
	if plan.Validate() != nil {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	return plan, nil
}

func DecodeFrozenRunPlan(data []byte) (FrozenRunPlan, error) {
	if len(data) == 0 || len(data) > maxSourceCompatibilityBytes || rejectDuplicateBoundedJSON(data) != nil {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document frozenRunPlanDocument
	if decoder.Decode(&document) != nil {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	plan := FrozenRunPlan{document: cloneFrozenRunPlanDocument(document)}
	if plan.Validate() != nil {
		return FrozenRunPlan{}, ErrInvalidFrozenRunPlan
	}
	return plan, nil
}

func (plan FrozenRunPlan) Validate() error {
	document := plan.document
	if document.SchemaVersion != 1 || document.State != RunPlanFrozen || !validProfileID(document.Profile) ||
		!validProfileDigest(document.RequestSHA256) || !validProfileDigest(document.SourceSHA256) ||
		!validProfileDigest(document.ArtifactSHA256) || !validProfileDigest(document.ManifestSHA256) ||
		!validProfileDigest(document.CompatibilityEvidenceSHA256) || !validProfileDigest(document.SourceValidationEvidenceSHA256) ||
		!validProfileDigest(document.ImportPolicySHA256) || !validProfileDigest(document.PlanSHA256) || !document.BytecodeChecked ||
		!sortedUniqueImportRoots(document.DeclaredImportRoots) || !equalStrings(document.DeclaredImportRoots, document.ASTImportRoots) ||
		!validModuleSet(document.BaselineModules) || !validModuleSet(document.EntryClosureModules) || !validModuleSet(document.SealedModules) ||
		intersectsSorted(document.BaselineModules, document.EntryClosureModules) ||
		!equalStrings(mergeSorted(document.BaselineModules, document.EntryClosureModules), document.SealedModules) {
		return ErrInvalidFrozenRunPlan
	}
	policyCanonical, _ := json.Marshal(document.SealedModules)
	policyDigest := sha256.Sum256(append([]byte("agent-python-runtime-import-policy-v1\x00"), policyCanonical...))
	if document.ImportPolicySHA256 != "sha256:"+hex.EncodeToString(policyDigest[:]) || document.PlanSHA256 != plan.digest() {
		return ErrInvalidFrozenRunPlan
	}
	return nil
}

func (plan FrozenRunPlan) digest() string {
	document := cloneFrozenRunPlanDocument(plan.document)
	document.PlanSHA256 = ""
	canonical, _ := json.Marshal(document)
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (plan FrozenRunPlan) MarshalJSON() ([]byte, error) { return json.Marshal(plan.document) }
func (plan *FrozenRunPlan) UnmarshalJSON(data []byte) error {
	if plan == nil || len(data) == 0 || len(data) > maxSourceCompatibilityBytes || rejectDuplicateBoundedJSON(data) != nil {
		return ErrInvalidFrozenRunPlan
	}
	var document frozenRunPlanDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil {
		return ErrInvalidFrozenRunPlan
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidFrozenRunPlan
	}
	candidate := FrozenRunPlan{document: cloneFrozenRunPlanDocument(document)}
	if candidate.Validate() != nil {
		return ErrInvalidFrozenRunPlan
	}
	*plan = candidate
	return nil
}
func (plan FrozenRunPlan) State() RunPlanState  { return plan.document.State }
func (plan FrozenRunPlan) PlanSHA256() string   { return plan.document.PlanSHA256 }
func (plan FrozenRunPlan) SourceSHA256() string { return plan.document.SourceSHA256 }
func (plan FrozenRunPlan) ProfileID() string    { return plan.document.Profile }
func (plan FrozenRunPlan) CompatibilityEvidenceSHA256() string {
	return plan.document.CompatibilityEvidenceSHA256
}
func (plan FrozenRunPlan) SourceValidationEvidenceSHA256() string {
	return plan.document.SourceValidationEvidenceSHA256
}
func (plan FrozenRunPlan) EntryClosureModules() []string {
	return cloneStrings(plan.document.EntryClosureModules)
}
func (plan FrozenRunPlan) SealedModules() []string { return cloneStrings(plan.document.SealedModules) }

func cloneSourceValidationDocument(value sourceValidationDocument) sourceValidationDocument {
	value.DeclaredImportRoots = cloneStrings(value.DeclaredImportRoots)
	value.ASTImportRoots = cloneStrings(value.ASTImportRoots)
	value.BaselineModules = cloneStrings(value.BaselineModules)
	value.EntryClosureModules = cloneStrings(value.EntryClosureModules)
	value.SealedModules = cloneStrings(value.SealedModules)
	return value
}

func cloneFrozenRunPlanDocument(value frozenRunPlanDocument) frozenRunPlanDocument {
	value.DeclaredImportRoots = cloneStrings(value.DeclaredImportRoots)
	value.ASTImportRoots = cloneStrings(value.ASTImportRoots)
	value.BaselineModules = cloneStrings(value.BaselineModules)
	value.EntryClosureModules = cloneStrings(value.EntryClosureModules)
	value.SealedModules = cloneStrings(value.SealedModules)
	return value
}

func validModuleSet(values []string) bool {
	if len(values) > maxRunPlanModules || !sort.StringsAreSorted(values) || hasAdjacentDuplicate(values) {
		return false
	}
	for _, value := range values {
		if value == "" || len(value) > 256 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
			return false
		}
		for _, segment := range strings.Split(value, ".") {
			if !validImportName(segment) {
				return false
			}
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
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

func intersectsSorted(left, right []string) bool {
	for i, j := 0, 0; i < len(left) && j < len(right); {
		if left[i] == right[j] {
			return true
		}
		if left[i] < right[j] {
			i++
		} else {
			j++
		}
	}
	return false
}

func mergeSorted(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		if left[i] < right[j] {
			result = append(result, left[i])
			i++
		} else {
			result = append(result, right[j])
			j++
		}
	}
	result = append(result, left[i:]...)
	result = append(result, right[j:]...)
	return result
}
