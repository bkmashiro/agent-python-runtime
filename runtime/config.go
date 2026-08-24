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

// ColdIOPolicy is Host-owned Experimental timing policy for one same-slot
// capability wait. A zero PageOutAfter selects MADV_COLD only.
type ColdIOPolicy struct {
	ColdAfter    time.Duration
	PageOutAfter time.Duration
}

// RunConfig is Host-owned authority and resource policy. It is never decoded
// from RunRequest JSON.
type RunConfig struct {
	Timeout          time.Duration
	MaxRequestBytes  uint32
	MaxResponseBytes uint32
	MemoryLimitPages uint32
	// ProgramSurface controls direct/programmatic presentation independently of
	// execution placement. Programmatic and both require the explicit mechanism.
	ProgramSurface ProgramSurfaceMode
	// ExecutionProfile is Host-owned artifact/import admission policy. A nil
	// profile preserves legacy requests but rejects any explicit compatibility
	// declaration.
	ExecutionProfile *ExecutionProfile
	// DeterministicVerification is an Experimental/Partial Host profile. It
	// controls the WASI clocks/random source for an exact artifact and rejects
	// unsupported workload classes before Guest execution.
	DeterministicVerification *DeterministicVerificationProfile
	CapabilityGrants          map[string]CapabilityGrant
	// ColdIO is required exactly when continuation-preserving cold I/O is selected.
	// It is Host policy and is never accepted from Guest input.
	ColdIO *ColdIOPolicy
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
		ProgramSurface:   ProgramSurfaceDirect,
		CapabilityGrants: map[string]CapabilityGrant{},
	}
}

func validateColdIOPolicy(policy ColdIOPolicy, timeout time.Duration) error {
	if policy.ColdAfter <= 0 || policy.ColdAfter >= timeout {
		return errors.New("cold I/O threshold must be inside the Run timeout")
	}
	if policy.PageOutAfter < 0 {
		return errors.New("cold I/O pageout threshold cannot be negative")
	}
	if policy.PageOutAfter != 0 && (policy.PageOutAfter <= policy.ColdAfter || policy.PageOutAfter >= timeout) {
		return errors.New("cold I/O pageout threshold must follow cold and precede timeout")
	}
	return nil
}

func (config RunConfig) Validate() error {
	if err := config.Mechanisms.Validate(); err != nil {
		return err
	}
	switch config.ProgramSurface {
	case ProgramSurfaceDirect:
		if config.Mechanisms.ProgrammaticToolCalling {
			return errors.New("direct program surface cannot select programmatic tool calling")
		}
	case ProgramSurfaceProgrammatic, ProgramSurfaceBoth:
		if !config.Mechanisms.ProgrammaticToolCalling {
			return errors.New("programmatic surface requires programmatic tool calling")
		}
		if config.Mechanisms.SplitPhaseCalls || config.Mechanisms.ValueSlots {
			return errors.New("Host source passes require the direct program surface")
		}
	default:
		return errors.New("invalid program surface")
	}
	if config.Mechanisms.ColdIOContinuation {
		if config.ColdIO == nil {
			return errors.New("cold I/O policy is required")
		}
		if err := validateColdIOPolicy(*config.ColdIO, config.Timeout); err != nil {
			return err
		}
	} else if config.ColdIO != nil {
		return errors.New("cold I/O policy requires the cold continuation mechanism")
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
