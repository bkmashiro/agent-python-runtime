// Package engine defines the runtime-neutral Host/guest execution boundary.
package engine

import (
	"context"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

// Runner executes requests against one compiled guest artifact. Every Run uses
// a fresh guest instance; lifecycle strategies are deliberately outside the PoC.
type Runner interface {
	Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error)
	Close(ctx context.Context) error
	Properties() Properties
}

type Factory interface {
	Name() string
	New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (Runner, error)
}

var ErrInvalidProperties = errors.New("invalid engine properties")

// Properties reports the backend and optional Host-bound artifact profile.
type Properties struct {
	Backend            string
	ExecutionProfileID string
	AllowedImports     []string
	AvailableImports   []string
	QualifiedImports   []string
	ArtifactSHA256     string
	ManifestSHA256     string
}

func (properties Properties) Validate() error {
	if properties.Backend == "" {
		return fmt.Errorf("%w: backend name is empty", ErrInvalidProperties)
	}
	if (properties.ExecutionProfileID == "") != (len(properties.AllowedImports) == 0) {
		return fmt.Errorf("%w: execution profile identity and imports must be present together", ErrInvalidProperties)
	}
	if properties.ExecutionProfileID == "" {
		if properties.ArtifactSHA256 != "" || properties.ManifestSHA256 != "" || len(properties.AvailableImports) != 0 || len(properties.QualifiedImports) != 0 {
			return fmt.Errorf("%w: artifact identity requires an execution profile", ErrInvalidProperties)
		}
		return nil
	}
	profile, err := runtimeconfig.NewExecutionProfile(properties.ExecutionProfileID, properties.AllowedImports)
	if err != nil {
		return fmt.Errorf("%w: execution profile is invalid", ErrInvalidProperties)
	}
	if properties.ArtifactSHA256 == "" {
		if properties.ManifestSHA256 != "" || len(properties.AvailableImports) != 0 || len(properties.QualifiedImports) != 0 {
			return fmt.Errorf("%w: incomplete artifact identity", ErrInvalidProperties)
		}
		return nil
	}
	_, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: properties.ExecutionProfileID, ArtifactSHA256: properties.ArtifactSHA256,
		ManifestSHA256: properties.ManifestSHA256, ImportRoots: properties.AvailableImports,
		QualifiedImportRoots: properties.QualifiedImports,
	})
	if err != nil {
		return fmt.Errorf("%w: artifact-bound execution profile is invalid", ErrInvalidProperties)
	}
	return nil
}

func (properties Properties) ExecutionProfile() *runtimeconfig.ExecutionProfile {
	if properties.ExecutionProfileID == "" || len(properties.AllowedImports) == 0 {
		return nil
	}
	profile, err := runtimeconfig.NewExecutionProfile(properties.ExecutionProfileID, append([]string(nil), properties.AllowedImports...))
	if err != nil {
		return nil
	}
	if properties.ArtifactSHA256 != "" {
		profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
			ProfileID: properties.ExecutionProfileID, ArtifactSHA256: properties.ArtifactSHA256,
			ManifestSHA256: properties.ManifestSHA256, ImportRoots: append([]string(nil), properties.AvailableImports...),
			QualifiedImportRoots: append([]string(nil), properties.QualifiedImports...),
		})
		if err != nil {
			return nil
		}
	}
	return &profile
}
