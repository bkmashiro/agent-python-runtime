package durable

import (
	"context"
	"errors"
	"fmt"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

// CapabilityPolicy is the Host-owned durable policy attached after discovery.
// Provider metadata may inform this decision but cannot approve itself.
type CapabilityPolicy struct {
	// Version must change when execution-visible capability behavior or its
	// Python presentation changes.
	Version    string
	Recovery   RecoveryMode
	Lookup     LookupFunc
	Wait       WaitFunc
	Scheduling SchedulingClass
	Observer   ToolObserver
}

// CapabilityApprover turns provider metadata into an explicit Host decision.
type CapabilityApprover func(context.Context, pysolate.Capability) (CapabilityPolicy, error)

// BindCapability combines one discovered capability with Host-owned durable
// policy. NewRunner performs the final validation and freezes the declaration.
func BindCapability(capability pysolate.Capability, policy CapabilityPolicy) Tool {
	capability.Spec.InputSchema = append([]byte(nil), capability.Spec.InputSchema...)
	return Tool{
		Definition: capability,
		Name:       capability.Name,
		Version:    policy.Version,
		Recovery:   policy.Recovery,
		Lookup:     policy.Lookup,
		Wait:       policy.Wait,
		Scheduling: policy.Scheduling,
		Observer:   policy.Observer,
	}
}

// ToolsFromProviders discovers capabilities and requires the Host to approve a
// durable policy for every one. Partial catalogs are never returned.
func ToolsFromProviders(ctx context.Context, approve CapabilityApprover, providers ...pysolate.ToolProvider) ([]Tool, error) {
	if approve == nil {
		return nil, errors.New("durable capability policy is required")
	}
	capabilities, err := pysolate.DiscoverCapabilities(ctx, providers...)
	if err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(capabilities))
	for _, capability := range capabilities {
		policy, policyErr := approve(ctx, capability)
		if policyErr != nil {
			return nil, fmt.Errorf("approve capability %q: %w", capability.Name, policyErr)
		}
		if policy.Version == "" {
			return nil, fmt.Errorf("approve capability %q: version is required", capability.Name)
		}
		switch policy.Recovery {
		case RetrySafe, Idempotent, Lookup, Manual, WaitMode:
		default:
			return nil, fmt.Errorf("approve capability %q: recovery mode is required", capability.Name)
		}
		tools = append(tools, BindCapability(capability, policy))
	}
	return tools, nil
}
