package passplugin

import (
	"context"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

var (
	ErrInvalidPlugin    = errors.New("invalid source pass plugin")
	ErrDuplicatePlugin  = errors.New("duplicate source pass plugin")
	ErrPluginDisabled   = errors.New("source pass plugin is disabled")
	ErrUnsupportedStage = errors.New("source pass plugin does not implement its registered stage")
)

type Plugin interface {
	Registration() passregistration.Registration
}

type SourcePatchPlugin interface {
	Plugin
	Transform(context.Context, sourcepatch.Transformer, string) (sourcepatch.Patch, error)
}

type ExistingAdapter struct {
	registration passregistration.Registration
}

func AdaptExisting(registration passregistration.Registration) (ExistingAdapter, error) {
	if registration.IdentitySHA256() == "" {
		return ExistingAdapter{}, ErrInvalidPlugin
	}
	return ExistingAdapter{registration: registration}, nil
}

func (adapter ExistingAdapter) Registration() passregistration.Registration {
	return adapter.registration
}

type Registry struct {
	plugins map[passregistration.Name]Plugin
	enabled map[passregistration.Name]bool
}

func New(plugins ...Plugin) (*Registry, error) {
	registry := &Registry{
		plugins: make(map[passregistration.Name]Plugin, len(plugins)),
		enabled: make(map[passregistration.Name]bool, len(plugins)),
	}
	for _, plugin := range plugins {
		if plugin == nil {
			return nil, ErrInvalidPlugin
		}
		registration := plugin.Registration()
		if registration.IdentitySHA256() == "" {
			return nil, ErrInvalidPlugin
		}
		if _, exists := registry.plugins[registration.Name()]; exists {
			return nil, ErrDuplicatePlugin
		}
		registry.plugins[registration.Name()] = plugin
	}
	return registry, nil
}

// Enable returns a configured registry copy. New registries are all-off.
func (registry *Registry) Enable(names ...passregistration.Name) (*Registry, error) {
	if registry == nil {
		return nil, ErrInvalidPlugin
	}
	configured := &Registry{
		plugins: make(map[passregistration.Name]Plugin, len(registry.plugins)),
		enabled: make(map[passregistration.Name]bool, len(registry.plugins)),
	}
	for name, plugin := range registry.plugins {
		configured.plugins[name] = plugin
	}
	for _, name := range names {
		if _, exists := configured.plugins[name]; !exists {
			return nil, ErrInvalidPlugin
		}
		configured.enabled[name] = true
	}
	return configured, nil
}

func (registry *Registry) Lookup(name passregistration.Name) (Plugin, bool) {
	if registry == nil {
		return nil, false
	}
	plugin, ok := registry.plugins[name]
	return plugin, ok
}

func (registry *Registry) Transform(ctx context.Context, name passregistration.Name, transformer sourcepatch.Transformer, source string) (sourcepatch.Patch, error) {
	plugin, ok := registry.Lookup(name)
	if !ok {
		return sourcepatch.Patch{}, ErrInvalidPlugin
	}
	if !registry.enabled[name] {
		return sourcepatch.Patch{}, ErrPluginDisabled
	}
	patchPlugin, ok := plugin.(SourcePatchPlugin)
	if !ok || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return sourcepatch.Patch{}, ErrUnsupportedStage
	}
	return patchPlugin.Transform(ctx, transformer, source)
}
