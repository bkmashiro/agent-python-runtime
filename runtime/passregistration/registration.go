package passregistration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

type Name string
type Consumer string
type Stage string

const (
	SourceRegistrationSchemaVersion       = "pysolate.semantic-pass-registration.v2"
	AnalyzerFreeRegistrationSchemaVersion = "pysolate.stage-aware-pass-registration.v3"

	SemanticPreDispatch          Name = "semantic_pre_dispatch"
	PreparedPureRegion           Name = "prepared_pure_region"
	PreparedNumpyLoad            Name = "prepared_numpy_load"
	PreparedValueBinding         Name = "prepared_value_binding"
	ChildFanoutExecution         Name = "child_fanout_execution"
	AgentFunctionRetention       Name = "agent_function_retention"
	AgentFunctionSingleFlight    Name = "agent_function_singleflight"
	FreshWorkflowReevaluation    Name = "fresh_workflow_reevaluation"
	PreparedRuntimeInstantiation Name = "prepared_runtime_instantiation"
	PrivateMemoryCOW             Name = "private_memory_cow"
	ColdIOResidency              Name = "cold_io_residency"
	SemanticWholeRunReuse        Name = "semantic_whole_run_reuse"

	SemanticPreDispatchVersion          = "pysolate.semantic-pre-dispatch-pass.v0"
	PreparedPureRegionVersion           = "pysolate.prepared-pure-region-pass.v1"
	PreparedNumpyLoadVersion            = "pysolate.prepared-numpy-load-pass.v1"
	PreparedValueBindingVersion         = "pysolate.prepared-value-binding-pass.v1"
	ChildFanoutExecutionVersion         = "pysolate.child-fanout-pass.v1"
	AgentFunctionRetentionVersion       = "pysolate.agent-function-retention-pass.v1"
	AgentFunctionSingleFlightVersion    = "pysolate.agent-function-singleflight-pass.v1"
	FreshWorkflowReevaluationVersion    = "pysolate.fresh-workflow-reevaluation-pass.v1"
	PreparedRuntimeInstantiationVersion = "pysolate.prepared-runtime-instantiation-pass.v1"
	PrivateMemoryCOWVersion             = "pysolate.private-memory-cow-pass.v1"
	ColdIOResidencyVersion              = "pysolate.cold-io-residency-pass.v1"
	SemanticWholeRunReuseVersion        = "pysolate.semantic-whole-run-reuse-pass.v1"
	SemanticAnalyzerSHA256              = "sha256:9ed43801b84228c031ba1c3df35dbeab924f1de6d43bb41836b9be894b7be94e"

	OverlayOnly       Consumer = "overlay_only"
	ExecutionPatch    Consumer = "execution_patch"
	PlanProjection    Consumer = "plan_projection"
	RunBinding        Consumer = "run_binding"
	MechanismLowering Consumer = "mechanism_lowering"

	StagePlanProjection     Stage = "plan_projection"
	StagePrefixOverlay      Stage = "prefix_overlay"
	StageHybridPreparePatch Stage = "hybrid_prepare_patch"
	StageWholeProgramPatch  Stage = "whole_program_patch"
	StageMultiProgramPatch  Stage = "multi_program_patch"
	StageRunBinding         Stage = "run_binding"
	StageRuntimeLowering    Stage = "runtime_lowering"
)

var (
	ErrInvalid    = errors.New("invalid pass registration")
	ErrDuplicate  = errors.New("duplicate pass registration")
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Definition struct {
	name     Name
	version  string
	stage    Stage
	consumer Consumer
}

func Define(name Name, version string, stage Stage, consumer Consumer) (Definition, error) {
	if !namePattern.MatchString(string(name)) || version == "" || len(version) > 128 || !validStageForConsumer(stage, consumer) {
		return Definition{}, ErrInvalid
	}
	return Definition{
		name: name, version: version, stage: stage, consumer: consumer,
	}, nil
}

func SemanticPreDispatchDefinition() Definition {
	value, _ := Define(SemanticPreDispatch, SemanticPreDispatchVersion, StagePrefixOverlay, OverlayOnly)
	return value
}

func PreparedPureRegionDefinition() Definition {
	value, _ := Define(PreparedPureRegion, PreparedPureRegionVersion, StageWholeProgramPatch, ExecutionPatch)
	return value
}

func PreparedNumpyLoadDefinition() Definition {
	value, _ := Define(PreparedNumpyLoad, PreparedNumpyLoadVersion, StageHybridPreparePatch, ExecutionPatch)
	return value
}

func PreparedValueBindingDefinition() Definition {
	value, _ := Define(PreparedValueBinding, PreparedValueBindingVersion, StageRunBinding, RunBinding)
	return value
}

func RuntimeOptimizationDefinitions() []Definition {
	specs := []struct {
		name    Name
		version string
	}{
		{ChildFanoutExecution, ChildFanoutExecutionVersion},
		{AgentFunctionRetention, AgentFunctionRetentionVersion},
		{AgentFunctionSingleFlight, AgentFunctionSingleFlightVersion},
		{FreshWorkflowReevaluation, FreshWorkflowReevaluationVersion},
		{PreparedRuntimeInstantiation, PreparedRuntimeInstantiationVersion},
		{PrivateMemoryCOW, PrivateMemoryCOWVersion},
		{ColdIOResidency, ColdIOResidencyVersion},
		{SemanticWholeRunReuse, SemanticWholeRunReuseVersion},
	}
	definitions := make([]Definition, 0, len(specs))
	for _, spec := range specs {
		definition, _ := Define(spec.name, spec.version, StageRuntimeLowering, MechanismLowering)
		definitions = append(definitions, definition)
	}
	return definitions
}

func (definition Definition) Name() Name         { return definition.name }
func (definition Definition) Version() string    { return definition.version }
func (definition Definition) Stage() Stage       { return definition.stage }
func (definition Definition) Consumer() Consumer { return definition.consumer }

func (definition Definition) Register(analyzerSHA256, configSHA256 string) (Registration, error) {
	if !validAnalyzerIdentity(definition.consumer, analyzerSHA256) || !digestPattern.MatchString(configSHA256) ||
		!validStageForConsumer(definition.stage, definition.consumer) || definition.name == "" {
		return Registration{}, ErrInvalid
	}
	schemaVersion := SourceRegistrationSchemaVersion
	if definition.consumer == PlanProjection || definition.consumer == RunBinding || definition.consumer == MechanismLowering {
		schemaVersion = AnalyzerFreeRegistrationSchemaVersion
	}
	value := identity{
		SchemaVersion: schemaVersion, Name: definition.name, Version: definition.version,
		Stage: definition.stage, AnalyzerSHA256: analyzerSHA256, ConfigSHA256: configSHA256,
		Consumer: definition.consumer,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Registration{}, ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return Registration{
		name: definition.name, version: definition.version, stage: definition.stage,
		analyzerSHA256: analyzerSHA256, configSHA256: configSHA256,
		consumer:       definition.consumer,
		identitySHA256: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func validStageForConsumer(stage Stage, consumer Consumer) bool {
	switch consumer {
	case OverlayOnly:
		return stage == StagePrefixOverlay
	case ExecutionPatch:
		return stage == StageHybridPreparePatch || stage == StageWholeProgramPatch || stage == StageMultiProgramPatch
	case PlanProjection:
		return stage == StagePlanProjection
	case RunBinding:
		return stage == StageRunBinding
	case MechanismLowering:
		return stage == StageRuntimeLowering
	default:
		return false
	}
}

func validAnalyzerIdentity(consumer Consumer, analyzerSHA256 string) bool {
	if consumer == PlanProjection || consumer == RunBinding || consumer == MechanismLowering {
		return analyzerSHA256 == ""
	}
	return digestPattern.MatchString(analyzerSHA256)
}

type Registration struct {
	name           Name
	version        string
	stage          Stage
	analyzerSHA256 string
	configSHA256   string
	consumer       Consumer
	identitySHA256 string
}

type identity struct {
	SchemaVersion  string   `json:"schema_version"`
	Name           Name     `json:"name"`
	Version        string   `json:"version"`
	Stage          Stage    `json:"stage"`
	AnalyzerSHA256 string   `json:"analyzer_sha256"`
	ConfigSHA256   string   `json:"config_sha256"`
	Consumer       Consumer `json:"consumer"`
}

// New preserves the original built-in constructor. New pass implementations use
// Define(...).Register(...) and do not require edits to this switch.
func New(name Name, version, analyzerSHA256, configSHA256 string, consumer Consumer) (Registration, error) {
	var definition Definition
	switch {
	case name == SemanticPreDispatch && version == SemanticPreDispatchVersion:
		definition = SemanticPreDispatchDefinition()
	case name == PreparedPureRegion && version == PreparedPureRegionVersion:
		definition = PreparedPureRegionDefinition()
	case name == PreparedNumpyLoad && version == PreparedNumpyLoadVersion:
		definition = PreparedNumpyLoadDefinition()
	case name == PreparedValueBinding && version == PreparedValueBindingVersion:
		definition = PreparedValueBindingDefinition()
	default:
		return Registration{}, ErrInvalid
	}
	if definition.consumer != consumer {
		return Registration{}, ErrInvalid
	}
	return definition.Register(analyzerSHA256, configSHA256)
}

func (registration Registration) Name() Name             { return registration.name }
func (registration Registration) Version() string        { return registration.version }
func (registration Registration) Stage() Stage           { return registration.stage }
func (registration Registration) AnalyzerSHA256() string { return registration.analyzerSHA256 }
func (registration Registration) ConfigSHA256() string   { return registration.configSHA256 }
func (registration Registration) Consumer() Consumer     { return registration.consumer }
func (registration Registration) IdentitySHA256() string { return registration.identitySHA256 }

type Registry struct {
	registrations map[Name]Registration
}

func NewRegistry(registrations ...Registration) (Registry, error) {
	registry := Registry{registrations: make(map[Name]Registration, len(registrations))}
	for _, registration := range registrations {
		if registration.identitySHA256 == "" {
			return Registry{}, ErrInvalid
		}
		if _, exists := registry.registrations[registration.name]; exists {
			return Registry{}, ErrDuplicate
		}
		registry.registrations[registration.name] = registration
	}
	return registry, nil
}

func (registry Registry) Lookup(name Name) (Registration, bool) {
	registration, ok := registry.registrations[name]
	return registration, ok
}
