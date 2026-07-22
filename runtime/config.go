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
	CapabilityGrants map[string]CapabilityGrant
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
	return nil
}
