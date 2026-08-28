package passplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
)

var (
	ErrInvalidPlugin               = errors.New("invalid source pass plugin")
	ErrDuplicatePlugin             = errors.New("duplicate source pass plugin")
	ErrPluginDisabled              = errors.New("source pass plugin is disabled")
	ErrUnsupportedStage            = errors.New("source pass plugin does not implement its registered stage")
	ErrDirectOptimizationSelection = errors.New("optimization mechanisms must be selected through the bound pass catalog")
	ErrPassConflict                = errors.New("enabled optimization passes conflict")
)

type Plugin interface {
	Registration() passregistration.Registration
}

type PlanProjectionPlugin interface {
	Plugin
	Project(*capability.Plan) (string, error)
}

type RunBindingPlugin interface {
	Plugin
	Bind(string) (string, error)
}

type SourcePatchPlugin interface {
	Plugin
	Transform(context.Context, sourcepatch.Transformer, string) (sourcepatch.Patch, error)
}

type HostScheduledSourcePatchPlugin interface {
	SourcePatchPlugin
	HostScheduled() bool
}

type CapabilitySourcePatchPlugin interface {
	Plugin
	Transform(context.Context, sourcepatch.Transformer, string, []sourcepatch.CapabilityProjection) (sourcepatch.Patch, error)
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
	plugins      map[passregistration.Name]Plugin
	enabled      map[passregistration.Name]bool
	requirements map[passregistration.Name]runtimeconfig.MechanismSet
}

type UnifiedCatalogConfig struct {
	SemanticPreDispatchConfigSHA256 string
	PreparedNumpyLoadConfigSHA256   string
	PreparedPureRegionConfigSHA256  string
}

func NewDefaultUnifiedCatalog() (*Registry, error) {
	return NewUnifiedCatalog(UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: catalogConfigSHA256(passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion),
		PreparedNumpyLoadConfigSHA256:   catalogConfigSHA256(passregistration.PreparedNumpyLoad, passregistration.PreparedNumpyLoadVersion),
		PreparedPureRegionConfigSHA256:  catalogConfigSHA256(passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion),
	})
}

func NewDefaultEnabledCatalog(names ...passregistration.Name) (*Registry, error) {
	registry, err := NewDefaultUnifiedCatalog()
	if err != nil {
		return nil, err
	}
	return registry.Enable(names...)
}

func LowerDefaultRunConfig(config runtimeconfig.RunConfig, names ...passregistration.Name) (runtimeconfig.RunConfig, RuntimeSelection, error) {
	registry, err := NewDefaultEnabledCatalog(names...)
	if err != nil {
		return runtimeconfig.RunConfig{}, RuntimeSelection{}, err
	}
	return registry.ApplyRunConfig(config)
}

// NewUnifiedCatalog registers every retained optimization and historical source
// optimizer in one default-off static pass catalog. Runtime owners remain the
// lowering targets and keep all lifecycle state.
func NewUnifiedCatalog(config UnifiedCatalogConfig) (*Registry, error) {
	semantic, err := passregistration.SemanticPreDispatchDefinition().Register(
		passregistration.SemanticAnalyzerSHA256, config.SemanticPreDispatchConfigSHA256,
	)
	if err != nil {
		return nil, err
	}
	preparedPure, err := passregistration.PreparedPureRegionDefinition().Register(
		passregistration.SemanticAnalyzerSHA256, config.PreparedPureRegionConfigSHA256,
	)
	if err != nil {
		return nil, err
	}
	preparedNumpy, err := passregistration.PreparedNumpyLoadDefinition().Register(
		passregistration.SemanticAnalyzerSHA256, config.PreparedNumpyLoadConfigSHA256,
	)
	if err != nil {
		return nil, err
	}
	semanticAdapter, err := AdaptExisting(semantic)
	if err != nil {
		return nil, err
	}
	preparedPureAdapter, err := AdaptExisting(preparedPure)
	if err != nil {
		return nil, err
	}
	preparedNumpyAdapter, err := AdaptExisting(preparedNumpy)
	if err != nil {
		return nil, err
	}
	preparedValue, err := valueslot.NewPreparedValuePass()
	if err != nil {
		return nil, err
	}
	cse, err := sourcepatch.NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		return nil, err
	}
	fold, err := sourcepatch.NewPureScalarFold(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		return nil, err
	}
	plmCapabilities, err := sourcepatch.NewPLMCapabilityCalls(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		return nil, err
	}
	dataLocal, err := sourcepatch.NewDataLocalNumpySum(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		return nil, err
	}
	plugins := []Plugin{
		semanticAdapter, preparedPureAdapter, preparedNumpyAdapter, preparedValue,
		cse, fold, plmCapabilities, dataLocal,
	}
	for _, definition := range passregistration.RuntimeOptimizationDefinitions() {
		registration, registerErr := definition.Register("", runtimeOptimizationConfigSHA256(definition))
		if registerErr != nil {
			return nil, registerErr
		}
		adapter, adaptErr := AdaptExisting(registration)
		if adaptErr != nil {
			return nil, adaptErr
		}
		plugins = append(plugins, adapter)
	}
	registry, err := New(plugins...)
	if err != nil {
		return nil, err
	}
	registry.requirements = unifiedRequirements()
	return registry, nil
}

type RuntimeSelection struct {
	Mechanisms runtimeconfig.MechanismSet
	Passes     []passregistration.Name
}

const RuntimeSelectionEvidenceSchemaVersion = "pysolate.optimization-pass-selection.v2"

type RuntimeSelectionEvidence struct {
	SchemaVersion string                          `json:"schema_version"`
	Passes        []RuntimePassEvidence           `json:"passes"`
	Mechanisms    runtimeconfig.MechanismEvidence `json:"mechanisms"`
}

type RuntimePassEvidence struct {
	Name               passregistration.Name  `json:"name"`
	Version            string                 `json:"version"`
	Stage              passregistration.Stage `json:"stage"`
	RegistrationSHA256 string                 `json:"registration_sha256"`
}

func (registry *Registry) ResolveRuntime(base, available runtimeconfig.MechanismSet) (runtimeconfig.MechanismSet, RuntimeSelectionEvidence, error) {
	selection, err := registry.LowerMechanisms(base)
	if err != nil {
		return runtimeconfig.MechanismSet{}, RuntimeSelectionEvidence{}, err
	}
	resolved, mechanisms, err := runtimeconfig.ResolveMechanisms(selection.Mechanisms, available)
	if err != nil {
		return runtimeconfig.MechanismSet{}, RuntimeSelectionEvidence{}, err
	}
	passes := make([]RuntimePassEvidence, 0, len(selection.Passes))
	for _, name := range selection.Passes {
		registration := registry.plugins[name].Registration()
		passes = append(passes, RuntimePassEvidence{
			Name: name, Version: registration.Version(), Stage: registration.Stage(),
			RegistrationSHA256: registration.IdentitySHA256(),
		})
	}
	return resolved, RuntimeSelectionEvidence{
		SchemaVersion: RuntimeSelectionEvidenceSchemaVersion,
		Passes:        passes,
		Mechanisms:    mechanisms,
	}, nil
}

func (registry *Registry) LowerMechanisms(base runtimeconfig.MechanismSet) (RuntimeSelection, error) {
	if registry == nil {
		return RuntimeSelection{}, ErrInvalidPlugin
	}
	mechanisms := base
	passes := make([]passregistration.Name, 0, len(registry.enabled))
	for name, enabled := range registry.enabled {
		if !enabled {
			continue
		}
		passes = append(passes, name)
		mechanisms = mergeMechanisms(mechanisms, registry.requirements[name])
	}
	sort.Slice(passes, func(left, right int) bool { return passes[left] < passes[right] })
	if err := mechanisms.Validate(); err != nil {
		return RuntimeSelection{}, err
	}
	return RuntimeSelection{Mechanisms: mechanisms, Passes: passes}, nil
}

func (registry *Registry) ApplyRunConfig(config runtimeconfig.RunConfig) (runtimeconfig.RunConfig, RuntimeSelection, error) {
	if directOptimizationSelected(config.Mechanisms) {
		return runtimeconfig.RunConfig{}, RuntimeSelection{}, ErrDirectOptimizationSelection
	}
	selection, err := registry.LowerMechanisms(config.Mechanisms)
	if err != nil {
		return runtimeconfig.RunConfig{}, RuntimeSelection{}, err
	}
	config.Mechanisms = selection.Mechanisms
	return config, selection, nil
}

func directOptimizationSelected(mechanisms runtimeconfig.MechanismSet) bool {
	return mechanisms.Streaming || mechanisms.ChildFanout || mechanisms.FunctionCache || mechanisms.SingleFlight ||
		mechanisms.FreshReevaluation || mechanisms.PreparedRuntime || mechanisms.MemoryCOW || mechanisms.ColdIOContinuation ||
		mechanisms.SemanticPreDispatch || mechanisms.SemanticReuse || mechanisms.SplitPhaseCalls || mechanisms.ValueSlots
}

func PLMCapabilityProjections(plan *capability.Plan) []sourcepatch.CapabilityProjection {
	if plan == nil {
		return nil
	}
	projections := make([]sourcepatch.CapabilityProjection, 0)
	for _, spec := range plan.Specs() {
		if spec.PLM == nil || spec.PLM.PrepareEffect == capability.PrepareNone || spec.Python == nil {
			continue
		}
		projections = append(projections, sourcepatch.CapabilityProjection{
			Capability: spec.Name, Module: spec.Python.Module, Method: spec.Python.Method,
			Arguments: append([]string(nil), spec.Python.Arguments...), ResultField: spec.Python.ResultField,
		})
	}
	sort.Slice(projections, func(left, right int) bool {
		leftName := projections[left].Module + "." + projections[left].Method
		rightName := projections[right].Module + "." + projections[right].Method
		return leftName < rightName
	})
	return projections
}

func runtimeOptimizationConfigSHA256(definition passregistration.Definition) string {
	digest := sha256.Sum256([]byte(definition.Version() + "\x00" + string(definition.Name()) + "\x00runtime-lowering"))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func catalogConfigSHA256(name passregistration.Name, version string) string {
	digest := sha256.Sum256([]byte(string(name) + "\x00" + version))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func unifiedRequirements() map[passregistration.Name]runtimeconfig.MechanismSet {
	return map[passregistration.Name]runtimeconfig.MechanismSet{
		passregistration.SemanticPreDispatch:          {SemanticAnalysis: true, StagedObservation: true, SemanticPreDispatch: true},
		passregistration.PreparedPureRegion:           {SemanticAnalysis: true},
		passregistration.PreparedNumpyLoad:            {SemanticAnalysis: true, PreparedRuntime: true},
		passregistration.PreparedValueBinding:         {ValueSlots: true},
		sourcepatch.PureScalarCSEName:                 {SemanticAnalysis: true},
		sourcepatch.PureScalarFoldName:                {SemanticAnalysis: true},
		sourcepatch.PLMCapabilityCallsName:            {SplitPhaseCalls: true},
		sourcepatch.DataLocalNumpySumName:             {ValueSlots: true},
		passregistration.SourceStreamingExecution:     {Streaming: true, PrivateWorkspace: true},
		passregistration.StreamedChildFanout:          {Streaming: true, PrivateWorkspace: true, ImmutableBranches: true, ChildFanout: true},
		passregistration.AgentFunctionRetention:       {ImmutableBranches: true, FunctionCache: true},
		passregistration.AgentFunctionSingleFlight:    {SingleFlight: true},
		passregistration.FreshWorkflowReevaluation:    {ImmutableBranches: true, FunctionCache: true, FreshReevaluation: true},
		passregistration.PreparedRuntimeInstantiation: {PreparedRuntime: true},
		passregistration.PrivateMemoryCOW:             {PreparedRuntime: true, MemoryCOW: true},
		passregistration.ColdIOResidency:              {PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true},
		passregistration.SemanticWholeRunReuse:        {SemanticAnalysis: true, SingleFlight: true, SemanticReuse: true},
	}
}

func mergeMechanisms(left, right runtimeconfig.MechanismSet) runtimeconfig.MechanismSet {
	return runtimeconfig.MechanismSet{
		ApprovalSuspension:      left.ApprovalSuspension || right.ApprovalSuspension,
		Streaming:               left.Streaming || right.Streaming,
		StagedObservation:       left.StagedObservation || right.StagedObservation,
		PrivateWorkspace:        left.PrivateWorkspace || right.PrivateWorkspace,
		ProgrammaticToolCalling: left.ProgrammaticToolCalling || right.ProgrammaticToolCalling,
		ImmutableBranches:       left.ImmutableBranches || right.ImmutableBranches,
		ChildFanout:             left.ChildFanout || right.ChildFanout,
		FunctionCache:           left.FunctionCache || right.FunctionCache,
		SingleFlight:            left.SingleFlight || right.SingleFlight,
		FreshReevaluation:       left.FreshReevaluation || right.FreshReevaluation,
		PreparedRuntime:         left.PreparedRuntime || right.PreparedRuntime,
		MemoryCOW:               left.MemoryCOW || right.MemoryCOW,
		ColdIOContinuation:      left.ColdIOContinuation || right.ColdIOContinuation,
		SemanticAnalysis:        left.SemanticAnalysis || right.SemanticAnalysis,
		SemanticPreDispatch:     left.SemanticPreDispatch || right.SemanticPreDispatch,
		SemanticReuse:           left.SemanticReuse || right.SemanticReuse,
		SplitPhaseCalls:         left.SplitPhaseCalls || right.SplitPhaseCalls,
		ValueSlots:              left.ValueSlots || right.ValueSlots,
	}
}

func New(plugins ...Plugin) (*Registry, error) {
	registry := &Registry{
		plugins:      make(map[passregistration.Name]Plugin, len(plugins)),
		enabled:      make(map[passregistration.Name]bool, len(plugins)),
		requirements: make(map[passregistration.Name]runtimeconfig.MechanismSet),
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
		plugins:      make(map[passregistration.Name]Plugin, len(registry.plugins)),
		enabled:      make(map[passregistration.Name]bool, len(registry.plugins)),
		requirements: make(map[passregistration.Name]runtimeconfig.MechanismSet, len(registry.requirements)),
	}
	for name, plugin := range registry.plugins {
		configured.plugins[name] = plugin
		configured.enabled[name] = registry.enabled[name]
	}
	for name, requirements := range registry.requirements {
		configured.requirements[name] = requirements
	}
	for _, name := range names {
		if _, exists := configured.plugins[name]; !exists {
			return nil, ErrInvalidPlugin
		}
		configured.enabled[name] = true
	}
	if err := configured.validateEnabledSelection(); err != nil {
		return nil, err
	}
	return configured, nil
}

func (registry *Registry) validateEnabledSelection() error {
	var sourceMutation passregistration.Name
	mechanisms := runtimeconfig.MechanismSet{}
	for name, enabled := range registry.enabled {
		if !enabled {
			continue
		}
		plugin := registry.plugins[name]
		if plugin.Registration().Consumer() == passregistration.ExecutionPatch {
			if sourceMutation != "" {
				return ErrPassConflict
			}
			sourceMutation = name
		}
		mechanisms = mergeMechanisms(mechanisms, registry.requirements[name])
	}
	if err := mechanisms.Validate(); err != nil {
		return errors.Join(ErrPassConflict, err)
	}
	return nil
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

func (registry *Registry) ProjectPlan(name passregistration.Name, plan *capability.Plan) (string, error) {
	plugin, ok := registry.Lookup(name)
	if !ok {
		return "", ErrInvalidPlugin
	}
	if !registry.enabled[name] {
		return "", ErrPluginDisabled
	}
	projection, ok := plugin.(PlanProjectionPlugin)
	if !ok || plugin.Registration().Stage() != passregistration.StagePlanProjection {
		return "", ErrUnsupportedStage
	}
	return projection.Project(plan)
}

func (registry *Registry) BindRunValue(name passregistration.Name, slotID string) (string, error) {
	plugin, ok := registry.Lookup(name)
	if !ok {
		return "", ErrInvalidPlugin
	}
	if !registry.enabled[name] {
		return "", ErrPluginDisabled
	}
	binding, ok := plugin.(RunBindingPlugin)
	if !ok || plugin.Registration().Stage() != passregistration.StageRunBinding {
		return "", ErrUnsupportedStage
	}
	return binding.Bind(slotID)
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

// ExecuteCapabilityHostScheduled runs the plan-bound V1 capability-call pass.
// The projection manifest is Host-derived from the sealed capability Plan and
// is bound into the patch before the exact Guest validates and executes it.
func (registry *Registry) ExecuteCapabilityHostScheduled(
	ctx context.Context,
	name passregistration.Name,
	transformer sourcepatch.Transformer,
	runner HostScheduledSourcePatchRunner,
	request []byte,
	trustedPrepare string,
	projections []sourcepatch.CapabilityProjection,
) (Execution, error) {
	if runner == nil || len(projections) == 0 {
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
	capabilityPlugin, ok := plugin.(CapabilitySourcePatchPlugin)
	if !ok || !capabilityPlugin.HostScheduled() || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		return Execution{}, ErrUnsupportedStage
	}
	if !registry.enabled[name] {
		payload, runErr := runner.Run(ctx, request, trustedPrepare)
		return Execution{Payload: payload}, runErr
	}
	patch, passErr := capabilityPlugin.Transform(ctx, transformer, runRequest.Code, projections)
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
	if passErr != nil || !patch.Applied() {
		payload, runErr := runValueSlotBaseline(ctx, baselineFactory, request, trustedPrepare)
		return Execution{Payload: payload, Patch: patch, PassError: passErr}, runErr
	}
	if validationErr := patch.Validate(runRequest.Code, plugin.Registration()); validationErr != nil {
		payload, runErr := runValueSlotBaseline(ctx, baselineFactory, request, trustedPrepare)
		return Execution{Payload: payload, Patch: patch, PassError: validationErr}, runErr
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
