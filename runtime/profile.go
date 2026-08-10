package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	maxDeclaredImports  = 64
	maxAvailableImports = 1024
)

var (
	ErrInvalidCompatibilityDeclaration   = errors.New("invalid compatibility declaration")
	ErrInvalidExecutionProfile           = errors.New("invalid execution profile")
	ErrExecutionProfileUnsupported       = errors.New("execution profile unsupported")
	ErrExecutionProfileArtifactMismatch  = errors.New("execution profile artifact mismatch")
	ErrExecutionProfileImportUnavailable = errors.New("execution profile import unavailable in artifact")
)

// CompatibilityDeclaration is untrusted compatibility metadata. It can only
// narrow admission; it cannot install packages, select an artifact, or grant a
// Host capability. Imports are explicit declarations, not source-code proof.
type CompatibilityDeclaration struct {
	Profile string   `json:"profile"`
	Imports []string `json:"imports"`
}

// ExecutionProfile is Host-owned admission policy for one named artifact
// profile. Construction freezes declared import roots; BindVerifiedArtifact can
// additionally freeze verified profile/artifact/manifest identity. A schema-v4
// artifact binding freezes both target-Guest discoverability and separately
// executed import qualification evidence.
type ExecutionProfile struct {
	id               string
	allowedImports   map[string]struct{}
	artifactSHA256   string
	manifestSHA256   string
	availableImports map[string]struct{}
	qualifiedImports map[string]struct{}
}

// ExecutionProfileUnsupportedError contains bounded Host-authored rejection
// details. It is never synthesized from Guest exception text.
type ExecutionProfileUnsupportedError struct {
	RequestedProfile   string
	UnsupportedImports []string
}

func (failure *ExecutionProfileUnsupportedError) Error() string {
	if failure == nil {
		return ErrExecutionProfileUnsupported.Error()
	}
	return fmt.Sprintf("%s (%d unsupported imports)", ErrExecutionProfileUnsupported, len(failure.UnsupportedImports))
}

func (failure *ExecutionProfileUnsupportedError) Unwrap() error {
	return ErrExecutionProfileUnsupported
}

func NewExecutionProfile(id string, allowedImports []string) (ExecutionProfile, error) {
	if !validProfileID(id) || len(allowedImports) == 0 || len(allowedImports) > maxDeclaredImports {
		return ExecutionProfile{}, ErrInvalidExecutionProfile
	}
	allowed := make(map[string]struct{}, len(allowedImports))
	for _, module := range allowedImports {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ExecutionProfile{}, ErrInvalidExecutionProfile
		}
		if _, duplicate := allowed[module]; duplicate {
			return ExecutionProfile{}, ErrInvalidExecutionProfile
		}
		allowed[module] = struct{}{}
	}
	return ExecutionProfile{id: id, allowedImports: allowed}, nil
}

func (profile ExecutionProfile) Validate() error {
	if !validProfileID(profile.id) || len(profile.allowedImports) == 0 || len(profile.allowedImports) > maxDeclaredImports {
		return ErrInvalidExecutionProfile
	}
	for module := range profile.allowedImports {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ErrInvalidExecutionProfile
		}
	}
	bound := profile.artifactSHA256 != ""
	if (profile.artifactSHA256 == "") != (profile.manifestSHA256 == "") ||
		(bound && (!validProfileDigest(profile.artifactSHA256) || !validProfileDigest(profile.manifestSHA256))) ||
		bound != (len(profile.availableImports) > 0) || bound != (len(profile.qualifiedImports) > 0) ||
		len(profile.availableImports) > maxAvailableImports || len(profile.qualifiedImports) > maxDeclaredImports {
		return ErrInvalidExecutionProfile
	}
	for module := range profile.availableImports {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ErrInvalidExecutionProfile
		}
	}
	for module := range profile.qualifiedImports {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ErrInvalidExecutionProfile
		}
		if _, ok := profile.availableImports[module]; !ok {
			return ErrInvalidExecutionProfile
		}
	}
	if bound {
		for module := range profile.allowedImports {
			if _, ok := profile.qualifiedImports[module]; !ok {
				return ErrInvalidExecutionProfile
			}
		}
	}
	return nil
}

func (profile ExecutionProfile) ID() string { return profile.id }

func (profile ExecutionProfile) ArtifactSHA256() string { return profile.artifactSHA256 }

func (profile ExecutionProfile) ManifestSHA256() string { return profile.manifestSHA256 }

func (profile ExecutionProfile) AllowedImports() []string {
	imports := make([]string, 0, len(profile.allowedImports))
	for module := range profile.allowedImports {
		imports = append(imports, module)
	}
	sort.Strings(imports)
	return imports
}

func (profile ExecutionProfile) AvailableImports() []string {
	imports := make([]string, 0, len(profile.availableImports))
	for module := range profile.availableImports {
		imports = append(imports, module)
	}
	sort.Strings(imports)
	return imports
}

func (profile ExecutionProfile) QualifiedImports() []string {
	imports := make([]string, 0, len(profile.qualifiedImports))
	for module := range profile.qualifiedImports {
		imports = append(imports, module)
	}
	sort.Strings(imports)
	return imports
}

func (profile ExecutionProfile) BindVerifiedArtifact(identity VerifiedArtifactIdentity) (ExecutionProfile, error) {
	if profile.Validate() != nil || identity.ProfileID != profile.id || !validProfileDigest(identity.ArtifactSHA256) || !validProfileDigest(identity.ManifestSHA256) {
		return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
	}
	if profile.artifactSHA256 != "" && (profile.artifactSHA256 != identity.ArtifactSHA256 || profile.manifestSHA256 != identity.ManifestSHA256) {
		return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
	}
	if len(identity.ImportRoots) == 0 || len(identity.ImportRoots) > maxAvailableImports ||
		len(identity.QualifiedImportRoots) == 0 || len(identity.QualifiedImportRoots) > maxDeclaredImports {
		return ExecutionProfile{}, ErrExecutionProfileImportUnavailable
	}
	available := make(map[string]struct{}, len(identity.ImportRoots))
	for _, module := range identity.ImportRoots {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
		}
		if _, duplicate := available[module]; duplicate {
			return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
		}
		available[module] = struct{}{}
	}
	qualified := make(map[string]struct{}, len(identity.QualifiedImportRoots))
	for _, module := range identity.QualifiedImportRoots {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
		}
		if _, duplicate := qualified[module]; duplicate {
			return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
		}
		if _, ok := available[module]; !ok {
			return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
		}
		qualified[module] = struct{}{}
	}
	for module := range profile.allowedImports {
		if _, ok := qualified[module]; !ok {
			return ExecutionProfile{}, ErrExecutionProfileImportUnavailable
		}
	}
	if profile.artifactSHA256 != "" && (!sameImportSet(profile.availableImports, available) || !sameImportSet(profile.qualifiedImports, qualified)) {
		return ExecutionProfile{}, ErrExecutionProfileArtifactMismatch
	}
	bound := profile
	bound.allowedImports = make(map[string]struct{}, len(profile.allowedImports))
	for module := range profile.allowedImports {
		bound.allowedImports[module] = struct{}{}
	}
	bound.availableImports = available
	bound.qualifiedImports = qualified
	bound.artifactSHA256 = identity.ArtifactSHA256
	bound.manifestSHA256 = identity.ManifestSHA256
	return bound, nil
}

func sameImportSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for module := range left {
		if _, ok := right[module]; !ok {
			return false
		}
	}
	return true
}

func validProfileDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func ValidateCompatibilityDeclaration(declaration *CompatibilityDeclaration) error {
	if declaration == nil {
		return nil
	}
	if !validProfileID(declaration.Profile) || declaration.Imports == nil || len(declaration.Imports) > maxDeclaredImports {
		return ErrInvalidCompatibilityDeclaration
	}
	seen := make(map[string]struct{}, len(declaration.Imports))
	for _, module := range declaration.Imports {
		if !validImportName(module) || strings.Contains(module, ".") {
			return ErrInvalidCompatibilityDeclaration
		}
		if _, duplicate := seen[module]; duplicate {
			return ErrInvalidCompatibilityDeclaration
		}
		seen[module] = struct{}{}
	}
	return nil
}

// EvaluateRunCompatibility compares the caller declaration and obvious static
// source imports with one Host profile. The returned immutable result is suitable
// as later RunPlan evidence; it never grants imports or claims syntax validity.
func EvaluateRunCompatibility(request RunRequest, profile *ExecutionProfile) (CompatibilityResult, error) {
	if request.Compatibility == nil {
		return CompatibilityResult{}, nil
	}
	if err := ValidateCompatibilityDeclaration(request.Compatibility); err != nil {
		return CompatibilityResult{}, err
	}
	if profile == nil || profile.Validate() != nil || profile.id != request.Compatibility.Profile {
		return CompatibilityResult{}, &ExecutionProfileUnsupportedError{RequestedProfile: request.Compatibility.Profile}
	}
	unsupported := make([]string, 0)
	for _, module := range request.Compatibility.Imports {
		root := strings.SplitN(module, ".", 2)[0]
		if _, ok := profile.allowedImports[root]; !ok {
			unsupported = append(unsupported, module)
		}
	}
	if len(unsupported) != 0 {
		sort.Strings(unsupported)
		return CompatibilityResult{}, &ExecutionProfileUnsupportedError{RequestedProfile: request.Compatibility.Profile, UnsupportedImports: unsupported}
	}
	result := CompareSourceCompatibility(request, *profile)
	if result.Status() != SourceCompatible {
		return result, &SourceCompatibilityError{Result: result}
	}
	return result, nil
}

// AdmitRunCompatibility compares an untrusted declaration with one Host-bound
// artifact profile. A missing declaration preserves the v1 request path; when a
// declaration is present, absence of Host profile policy fails closed.
func AdmitRunCompatibility(request RunRequest, profile *ExecutionProfile) error {
	_, err := EvaluateRunCompatibility(request, profile)
	return err
}

func validProfileID(id string) bool {
	return id == "base" || id == "numpy-core"
}

func validImportName(module string) bool {
	if module == "" || len(module) > 128 || strings.HasPrefix(module, ".") || strings.HasSuffix(module, ".") {
		return false
	}
	for _, segment := range strings.Split(module, ".") {
		if segment == "" {
			return false
		}
		for index, character := range segment {
			if index == 0 {
				if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '_' {
					return false
				}
				continue
			}
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '_' {
				return false
			}
		}
	}
	return true
}
