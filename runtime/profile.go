package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxDeclaredImports = 64

var (
	ErrInvalidCompatibilityDeclaration = errors.New("invalid compatibility declaration")
	ErrInvalidExecutionProfile         = errors.New("invalid execution profile")
	ErrExecutionProfileUnsupported     = errors.New("execution profile unsupported")
)

// CompatibilityDeclaration is untrusted compatibility metadata. It can only
// narrow admission; it cannot install packages, select an artifact, or grant a
// Host capability. Imports are explicit declarations, not source-code proof.
type CompatibilityDeclaration struct {
	Profile string   `json:"profile"`
	Imports []string `json:"imports"`
}

// ExecutionProfile is Host-owned admission policy for one named artifact
// profile. Construction freezes the declared import roots. The runtime does
// not yet prove that this policy was derived from the artifact manifest.
type ExecutionProfile struct {
	id             string
	allowedImports map[string]struct{}
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
	return nil
}

func (profile ExecutionProfile) ID() string { return profile.id }

func (profile ExecutionProfile) AllowedImports() []string {
	imports := make([]string, 0, len(profile.allowedImports))
	for module := range profile.allowedImports {
		imports = append(imports, module)
	}
	sort.Strings(imports)
	return imports
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
		if !validImportName(module) {
			return ErrInvalidCompatibilityDeclaration
		}
		if _, duplicate := seen[module]; duplicate {
			return ErrInvalidCompatibilityDeclaration
		}
		seen[module] = struct{}{}
	}
	return nil
}

// AdmitRunCompatibility compares an untrusted declaration with one Host-bound
// artifact profile. A missing declaration preserves the v1 request path; when a
// declaration is present, absence of Host profile policy fails closed.
func AdmitRunCompatibility(request RunRequest, profile *ExecutionProfile) error {
	if request.Compatibility == nil {
		return nil
	}
	if err := ValidateCompatibilityDeclaration(request.Compatibility); err != nil {
		return err
	}
	if profile == nil || profile.Validate() != nil || profile.id != request.Compatibility.Profile {
		return &ExecutionProfileUnsupportedError{RequestedProfile: request.Compatibility.Profile}
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
		return &ExecutionProfileUnsupportedError{RequestedProfile: request.Compatibility.Profile, UnsupportedImports: unsupported}
	}
	return nil
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
