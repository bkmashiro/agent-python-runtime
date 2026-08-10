// Package engine defines the runtime-neutral Host/guest execution boundary.
// Concrete WebAssembly runtimes live in child adapter packages.
package engine

import (
	"context"
	"errors"
	"fmt"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

// Runner executes requests against one compiled guest artifact.
type Runner interface {
	Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error)
	Close(ctx context.Context) error
	Properties() Properties
}

// Factory constructs a Runner without exposing backend-specific module, memory,
// linker, or store types to callers.
type Factory interface {
	Name() string
	New(ctx context.Context, wasm []byte, config runtime.RunConfig) (Runner, error)
}

// ResetMode identifies the isolation mechanism a Runner actually applies.
type ResetMode string

// ExecutionStrategy identifies the Host-requested and actually active
// lifecycle strategy separately from its reset semantics.
type ExecutionStrategy string

const (
	// ResetModeFreshInstance destroys the request instance and instantiates a new
	// one for the next Run. It is the portable fail-closed baseline.
	ResetModeFreshInstance ResetMode = "fresh-instance"
	// ResetModePreparedRestore is reserved for a backend that restores a proven
	// prepared boundary, including every artifact state class it can mutate.
	ResetModePreparedRestore ResetMode = "prepared-restore"

	StrategyFreshInstance       ExecutionStrategy = "fresh-instance"
	StrategySingleUsePrepared   ExecutionStrategy = "single-use-preinitialized"
	StrategyCOWReadySingleUse   ExecutionStrategy = "cow-ready-single-use"
	StrategyCOWFullRemapRestore ExecutionStrategy = "cow-full-remap-restore"
	StrategyCOWLocality         ExecutionStrategy = "cow-locality-optimized"
	StrategyCOWAdaptiveReset    ExecutionStrategy = "cow-adaptive-reset"
)

var ErrInvalidProperties = errors.New("invalid engine properties")

// Properties records observable backend behavior, not aspirational support.
type Properties struct {
	Backend            string
	ResetMode          ResetMode
	RequestedStrategy  ExecutionStrategy
	ActiveStrategy     ExecutionStrategy
	Fallback           bool
	FallbackReason     string
	ExecutionProfileID string
	AllowedImports     []string
	AvailableImports   []string
	ArtifactSHA256     string
	ManifestSHA256     string
}

func (properties Properties) Validate() error {
	if properties.Backend == "" {
		return fmt.Errorf("%w: backend name is empty", ErrInvalidProperties)
	}
	if !validStrategy(properties.RequestedStrategy) || !validStrategy(properties.ActiveStrategy) {
		return fmt.Errorf("%w: unknown or incomplete execution strategy", ErrInvalidProperties)
	}
	if properties.Fallback {
		if properties.RequestedStrategy == properties.ActiveStrategy || properties.FallbackReason == "" {
			return fmt.Errorf("%w: fallback must change strategy and record a reason", ErrInvalidProperties)
		}
	} else if properties.RequestedStrategy != properties.ActiveStrategy || properties.FallbackReason != "" {
		return fmt.Errorf("%w: strategy drift requires an explicit fallback", ErrInvalidProperties)
	}
	if (properties.ExecutionProfileID == "") != (len(properties.AllowedImports) == 0) {
		return fmt.Errorf("%w: execution profile identity and imports must be present together", ErrInvalidProperties)
	}
	if properties.ExecutionProfileID != "" {
		profile, err := runtime.NewExecutionProfile(properties.ExecutionProfileID, properties.AllowedImports)
		if err != nil {
			return fmt.Errorf("%w: execution profile is invalid", ErrInvalidProperties)
		}
		if (properties.ArtifactSHA256 == "") != (properties.ManifestSHA256 == "") ||
			(properties.ArtifactSHA256 == "") != (len(properties.AvailableImports) == 0) {
			return fmt.Errorf("%w: artifact, manifest, and import inventory must be present together", ErrInvalidProperties)
		}
		if properties.ArtifactSHA256 != "" {
			if _, err := profile.BindVerifiedArtifact(runtime.VerifiedArtifactIdentity{ProfileID: properties.ExecutionProfileID, ArtifactSHA256: properties.ArtifactSHA256, ManifestSHA256: properties.ManifestSHA256, ImportRoots: properties.AvailableImports}); err != nil {
				return fmt.Errorf("%w: artifact-bound execution profile is invalid", ErrInvalidProperties)
			}
		}
	} else if properties.ArtifactSHA256 != "" || properties.ManifestSHA256 != "" || len(properties.AvailableImports) != 0 {
		return fmt.Errorf("%w: artifact identity requires an execution profile", ErrInvalidProperties)
	}
	wantResetMode := resetModeForStrategy(properties.ActiveStrategy)
	if wantResetMode == "" || properties.ResetMode != wantResetMode {
		return fmt.Errorf("%w: unknown reset mode %q", ErrInvalidProperties, properties.ResetMode)
	}
	return nil
}

// ExecutionProfile reconstructs a defensive Host profile from observable
// runner properties. A nil result means this runner has no profile binding.
func (properties Properties) ExecutionProfile() *runtime.ExecutionProfile {
	if properties.ExecutionProfileID == "" || len(properties.AllowedImports) == 0 {
		return nil
	}
	profile, err := runtime.NewExecutionProfile(properties.ExecutionProfileID, append([]string(nil), properties.AllowedImports...))
	if err != nil {
		return nil
	}
	if properties.ArtifactSHA256 != "" {
		profile, err = profile.BindVerifiedArtifact(runtime.VerifiedArtifactIdentity{
			ProfileID: properties.ExecutionProfileID, ArtifactSHA256: properties.ArtifactSHA256,
			ManifestSHA256: properties.ManifestSHA256, ImportRoots: append([]string(nil), properties.AvailableImports...),
		})
		if err != nil {
			return nil
		}
	}
	return &profile
}

func validStrategy(strategy ExecutionStrategy) bool {
	switch strategy {
	case StrategyFreshInstance, StrategySingleUsePrepared, StrategyCOWReadySingleUse,
		StrategyCOWFullRemapRestore, StrategyCOWLocality, StrategyCOWAdaptiveReset:
		return true
	default:
		return false
	}
}

func resetModeForStrategy(strategy ExecutionStrategy) ResetMode {
	switch strategy {
	case StrategyFreshInstance, StrategySingleUsePrepared, StrategyCOWReadySingleUse:
		return ResetModeFreshInstance
	case StrategyCOWFullRemapRestore, StrategyCOWLocality, StrategyCOWAdaptiveReset:
		return ResetModePreparedRestore
	default:
		return ""
	}
}
