package runtime

import (
	"errors"
	"time"
)

const (
	hardMaxTimeout       = 5 * time.Minute
	hardMaxRequestBytes  = 16 * 1024 * 1024
	hardMaxResponseBytes = 16 * 1024 * 1024
	hardMaxMemoryPages   = 16384 // 1 GiB in 64 KiB WebAssembly pages.
)

// RunConfig is Host-owned authority and resource policy. It is never decoded
// from RunRequest JSON.
type RunConfig struct {
	Timeout          time.Duration
	MaxRequestBytes  uint32
	MaxResponseBytes uint32
	MemoryLimitPages uint32
	// ExecutionProfile is Host-owned artifact/import admission policy. A nil
	// profile preserves legacy requests but rejects any explicit compatibility
	// declaration.
	ExecutionProfile *ExecutionProfile
	// DeterministicVerification is an Experimental/Partial Host profile. It
	// controls the WASI clocks/random source for an exact artifact and rejects
	// unsupported workload classes before Guest execution.
	DeterministicVerification *DeterministicVerificationProfile
	CapabilityGrants          map[string]CapabilityGrant
	// Mechanisms is an internal Host-owned optional mechanism set. Its zero value
	// preserves ordinary fresh execution and does not alter capability grants.
	Mechanisms MechanismSet
}

// CapabilityGrant is deliberately opaque until a typed capability is added.
// The Host, never generated code, constructs grants.
type CapabilityGrant struct {
	Name string
}

func DefaultRunConfig() RunConfig {
	return RunConfig{
		Timeout:          20 * time.Second,
		MaxRequestBytes:  1024 * 1024,
		MaxResponseBytes: 1024 * 1024,
		MemoryLimitPages: 8192,
		CapabilityGrants: map[string]CapabilityGrant{},
	}
}

func (config RunConfig) Validate() error {
	if err := config.Mechanisms.Validate(); err != nil {
		return err
	}
	if config.Timeout <= 0 || config.Timeout > hardMaxTimeout {
		return errors.New("timeout must be positive and at most five minutes")
	}
	if config.MaxRequestBytes == 0 || config.MaxRequestBytes > hardMaxRequestBytes {
		return errors.New("max request bytes is outside the hard bound")
	}
	if config.MaxResponseBytes == 0 || config.MaxResponseBytes > hardMaxResponseBytes {
		return errors.New("max response bytes is outside the hard bound")
	}
	if config.MemoryLimitPages == 0 || config.MemoryLimitPages > hardMaxMemoryPages {
		return errors.New("memory page limit is outside the hard bound")
	}
	if config.ExecutionProfile != nil && config.ExecutionProfile.Validate() != nil {
		return errors.New("execution profile is invalid")
	}
	if config.DeterministicVerification != nil {
		profile := config.DeterministicVerification
		if profile.Validate() != nil || config.ExecutionProfile == nil || config.ExecutionProfile.Validate() != nil ||
			config.ExecutionProfile.ArtifactSHA256() == "" || config.ExecutionProfile.ArtifactSHA256() != profile.ArtifactSHA256() {
			return ErrDeterministicVerificationAdmission
		}
	}
	return nil
}
