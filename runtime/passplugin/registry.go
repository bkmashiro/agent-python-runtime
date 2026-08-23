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
}

func New(plugins ...Plugin) (*Registry, error) {
	registry := &Registry{plugins: make(map[passregistration.Name]Plugin, len(plugins))}
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
	patchPlugin, ok := plugin.(SourcePatchPlugin)
	if !ok || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return sourcepatch.Patch{}, ErrUnsupportedStage
	}
	return patchPlugin.Transform(ctx, transformer, source)
}
