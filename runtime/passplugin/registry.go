package passplugin

import (
	"context"
	"errors"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
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

type HostScheduledSourcePatchPlugin interface {
	SourcePatchPlugin
	HostScheduled() bool
}

type ValueSlotSourcePatchPlugin interface {
	SourcePatchPlugin
	ValueSlotBound() bool
}

type SourcePatchRunner interface {
	Run(context.Context, []byte, string) ([]byte, error)
	RunSourcePatchDerived(context.Context, []byte, sourcepatch.Patch, passregistration.Registration) ([]byte, error)
}

type HostScheduledSourcePatchRunner interface {
	Run(context.Context, []byte, string) ([]byte, error)
	RunHostScheduledSourcePatchDerived(context.Context, []byte, sourcepatch.Patch, passregistration.Registration) ([]byte, error)
}

type ValueSlotSourcePatchRunner interface {
	Run(context.Context, []byte, string) ([]byte, error)
	RunValueSlotSourcePatchDerived(context.Context, []byte, sourcepatch.Patch, passregistration.Registration) (ValueSlotRun, error)
	Close(context.Context) error
}

type ValueSlotSourcePatchRunnerFactory func(context.Context) (ValueSlotSourcePatchRunner, error)

type ValueSlotRun struct {
	Payload []byte
	Applied bool
}

type Execution struct {
	Payload   []byte
	Patch     sourcepatch.Patch
	Applied   bool
	PassError error
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

// Execute runs one enabled source-patch plugin. Disabled, inapplicable or failed
// transforms execute the unchanged original request because no Agent execution or
// authority-bearing work has started at that point.
func (registry *Registry) Execute(ctx context.Context, name passregistration.Name, transformer sourcepatch.Transformer, runner SourcePatchRunner, request []byte) (Execution, error) {
	if runner == nil {
		return Execution{}, ErrInvalidPlugin
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return Execution{}, err
	}
	plugin, exists := registry.Lookup(name)
	if !exists {
		return Execution{}, ErrInvalidPlugin
	}
	patchPlugin, sourcePatch := plugin.(SourcePatchPlugin)
	if !sourcePatch || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return Execution{}, ErrUnsupportedStage
	}
	if scheduled, ok := plugin.(HostScheduledSourcePatchPlugin); ok && scheduled.HostScheduled() {
		return Execution{}, ErrUnsupportedStage
	}
	if slotBound, ok := plugin.(ValueSlotSourcePatchPlugin); ok && slotBound.ValueSlotBound() {
		return Execution{}, ErrUnsupportedStage
	}
	if !registry.enabled[name] {
		payload, runErr := runner.Run(ctx, request, "")
		return Execution{Payload: payload}, runErr
	}
	patch, passErr := patchPlugin.Transform(ctx, transformer, runRequest.Code)
	if passErr != nil || !patch.Applied() {
		payload, runErr := runner.Run(ctx, request, "")
		return Execution{Payload: payload, Patch: patch, PassError: passErr}, runErr
	}
	payload, runErr := runner.RunSourcePatchDerived(ctx, request, patch, plugin.Registration())
	return Execution{Payload: payload, Patch: patch, Applied: runErr == nil}, runErr
}

// ExecuteHostScheduled runs one explicitly Host-scheduled source patch. The
// separate entry point prevents an effect-owning pass from entering the
// authority-free RunSourcePatchDerived seam.
func (registry *Registry) ExecuteHostScheduled(ctx context.Context, name passregistration.Name, transformer sourcepatch.Transformer, runner HostScheduledSourcePatchRunner, request []byte, trustedPrepare string) (Execution, error) {
	if runner == nil {
		return Execution{}, ErrInvalidPlugin
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return Execution{}, err
	}
	plugin, exists := registry.Lookup(name)
	if !exists {
		return Execution{}, ErrInvalidPlugin
	}
	patchPlugin, sourcePatch := plugin.(SourcePatchPlugin)
	scheduled, hostScheduled := plugin.(HostScheduledSourcePatchPlugin)
	if !sourcePatch || !hostScheduled || !scheduled.HostScheduled() || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return Execution{}, ErrUnsupportedStage
	}
	if !registry.enabled[name] {
		payload, runErr := runner.Run(ctx, request, trustedPrepare)
		return Execution{Payload: payload}, runErr
	}
	patch, passErr := patchPlugin.Transform(ctx, transformer, runRequest.Code)
	if passErr != nil || !patch.Applied() {
		payload, runErr := runner.Run(ctx, request, trustedPrepare)
		return Execution{Payload: payload, Patch: patch, PassError: passErr}, runErr
	}
	payload, runErr := runner.RunHostScheduledSourcePatchDerived(ctx, request, patch, plugin.Registration())
	return Execution{Payload: payload, Patch: patch, Applied: runErr == nil}, runErr
}

// ExecuteValueSlot selects and validates one fixed value-slot patch before it
// invokes the selected runner factory. Rejection and producer failure construct
// only an ordinary runner and execute the unchanged source.
func (registry *Registry) ExecuteValueSlot(
	ctx context.Context,
	name passregistration.Name,
	transformer sourcepatch.Transformer,
	baselineFactory ValueSlotSourcePatchRunnerFactory,
	selectedFactory ValueSlotSourcePatchRunnerFactory,
	request []byte,
	trustedPrepare string,
) (Execution, error) {
	if transformer == nil || baselineFactory == nil || selectedFactory == nil {
		return Execution{}, ErrInvalidPlugin
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return Execution{}, err
	}
	plugin, exists := registry.Lookup(name)
	if !exists {
		return Execution{}, ErrInvalidPlugin
	}
	slotPlugin, valueSlotBound := plugin.(ValueSlotSourcePatchPlugin)
	if !valueSlotBound || !slotPlugin.ValueSlotBound() || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return Execution{}, ErrUnsupportedStage
	}
	if !registry.enabled[name] {
		payload, runErr := runValueSlotBaseline(ctx, baselineFactory, request, trustedPrepare)
		return Execution{Payload: payload}, runErr
	}
	patch, passErr := slotPlugin.Transform(ctx, transformer, runRequest.Code)
	if passErr != nil || !patch.Applied() || patch.Validate(runRequest.Code, plugin.Registration()) != nil {
		payload, runErr := runValueSlotBaseline(ctx, baselineFactory, request, trustedPrepare)
		return Execution{Payload: payload, Patch: patch, PassError: passErr}, runErr
	}
	runner, factoryErr := selectedFactory(ctx)
	if factoryErr != nil || runner == nil {
		if factoryErr == nil {
			factoryErr = ErrInvalidPlugin
		}
		payload, runErr := runValueSlotBaseline(ctx, baselineFactory, request, trustedPrepare)
		return Execution{Payload: payload, Patch: patch, PassError: factoryErr}, runErr
	}
	result, runErr := runner.RunValueSlotSourcePatchDerived(ctx, request, patch, plugin.Registration())
	closeErr := runner.Close(ctx)
	runErr = errors.Join(runErr, closeErr)
	return Execution{Payload: result.Payload, Patch: patch, Applied: result.Applied && runErr == nil}, runErr
}

func runValueSlotBaseline(ctx context.Context, factory ValueSlotSourcePatchRunnerFactory, request []byte, trustedPrepare string) ([]byte, error) {
	runner, err := factory(ctx)
	if err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, ErrInvalidPlugin
	}
	payload, runErr := runner.Run(ctx, request, trustedPrepare)
	return payload, errors.Join(runErr, runner.Close(ctx))
}
