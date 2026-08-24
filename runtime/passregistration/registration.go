package passregistration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
)

type Name string
type Consumer string
type Binding string
type Stage string

const (
	SourceRegistrationSchemaVersion       = "pysolate.semantic-pass-registration.v2"
	AnalyzerFreeRegistrationSchemaVersion = "pysolate.stage-aware-pass-registration.v3"

	SemanticPreDispatch        Name = "semantic_pre_dispatch"
	PreparedPureRegion         Name = "prepared_pure_region"
	PreparedNumpyLoad          Name = "prepared_numpy_load"
	CapabilityFutureProjection Name = "capability_future_projection"
	PreparedValueBinding       Name = "prepared_value_binding"

	SemanticPreDispatchVersion        = "pysolate.semantic-pre-dispatch-pass.v0"
	PreparedPureRegionVersion         = "pysolate.prepared-pure-region-pass.v1"
	PreparedNumpyLoadVersion          = "pysolate.prepared-numpy-load-pass.v1"
	CapabilityFutureProjectionVersion = "pysolate.capability-future-projection-pass.v1"
	PreparedValueBindingVersion       = "pysolate.prepared-value-binding-pass.v1"
	SemanticAnalyzerSHA256            = "sha256:9ed43801b84228c031ba1c3df35dbeab924f1de6d43bb41836b9be894b7be94e"

	OverlayOnly    Consumer = "overlay_only"
	ExecutionPatch Consumer = "execution_patch"
	PlanProjection Consumer = "plan_projection"
	RunBinding     Consumer = "run_binding"

	StagePlanProjection     Stage = "plan_projection"
	StagePrefixOverlay      Stage = "prefix_overlay"
	StageHybridPreparePatch Stage = "hybrid_prepare_patch"
	StageWholeProgramPatch  Stage = "whole_program_patch"
	StageMultiProgramPatch  Stage = "multi_program_patch"
	StageRunBinding         Stage = "run_binding"

	SourceSHA256           Binding = "source_sha256"
	ASTSHA256              Binding = "ast_sha256"
	AnalysisSHA256         Binding = "analysis_sha256"
	AnalyzerSHA256         Binding = "analyzer_sha256"
	ExecutionProfileSHA256 Binding = "execution_profile_sha256"
	ImportClosureSHA256    Binding = "import_closure_sha256"
	CapabilityPlanSHA256   Binding = "capability_plan_sha256"
	PassConfigSHA256       Binding = "pass_config_sha256"
	OccurrenceID           Binding = "occurrence_id"
	RegionID               Binding = "region_id"
	RunIdentitySHA256      Binding = "run_identity_sha256"
	FinalSourceSHA256      Binding = "final_source_sha256"
)

var (
	ErrInvalid    = errors.New("invalid semantic pass registration")
	ErrDuplicate  = errors.New("duplicate semantic pass registration")
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

var overlayBindings = []Binding{
	SourceSHA256, ASTSHA256, AnalysisSHA256, AnalyzerSHA256,
	ExecutionProfileSHA256, ImportClosureSHA256, CapabilityPlanSHA256,
	PassConfigSHA256, OccurrenceID,
}

var patchBindings = []Binding{
	SourceSHA256, ASTSHA256, AnalysisSHA256, AnalyzerSHA256,
	ExecutionProfileSHA256, ImportClosureSHA256, CapabilityPlanSHA256,
	PassConfigSHA256, RegionID, FinalSourceSHA256,
}

var projectionBindings = []Binding{CapabilityPlanSHA256, PassConfigSHA256}
var runBindings = []Binding{RegionID, RunIdentitySHA256, PassConfigSHA256}

func OverlayBindings() []Binding    { return append([]Binding(nil), overlayBindings...) }
func PatchBindings() []Binding      { return append([]Binding(nil), patchBindings...) }
func ProjectionBindings() []Binding { return append([]Binding(nil), projectionBindings...) }
func RunBindings() []Binding        { return append([]Binding(nil), runBindings...) }

type Definition struct {
	name             Name
	version          string
	stage            Stage
	consumer         Consumer
	requiredBindings []Binding
}

func Define(name Name, version string, stage Stage, consumer Consumer, bindings []Binding) (Definition, error) {
	if !namePattern.MatchString(string(name)) || version == "" || len(version) > 128 || !validStageForConsumer(stage, consumer) {
		return Definition{}, ErrInvalid
	}
	if !reflect.DeepEqual(bindings, bindingsForConsumer(consumer)) {
		return Definition{}, ErrInvalid
	}
	return Definition{
		name: name, version: version, stage: stage, consumer: consumer,
		requiredBindings: append([]Binding(nil), bindings...),
	}, nil
}

func SemanticPreDispatchDefinition() Definition {
	value, _ := Define(SemanticPreDispatch, SemanticPreDispatchVersion, StagePrefixOverlay, OverlayOnly, OverlayBindings())
	return value
}

func PreparedPureRegionDefinition() Definition {
	value, _ := Define(PreparedPureRegion, PreparedPureRegionVersion, StageWholeProgramPatch, ExecutionPatch, PatchBindings())
	return value
}

func PreparedNumpyLoadDefinition() Definition {
	value, _ := Define(PreparedNumpyLoad, PreparedNumpyLoadVersion, StageHybridPreparePatch, ExecutionPatch, PatchBindings())
	return value
}

func CapabilityFutureProjectionDefinition() Definition {
	value, _ := Define(CapabilityFutureProjection, CapabilityFutureProjectionVersion, StagePlanProjection, PlanProjection, ProjectionBindings())
	return value
}

func PreparedValueBindingDefinition() Definition {
	value, _ := Define(PreparedValueBinding, PreparedValueBindingVersion, StageRunBinding, RunBinding, RunBindings())
	return value
}

func (definition Definition) Name() Name         { return definition.name }
func (definition Definition) Version() string    { return definition.version }
func (definition Definition) Stage() Stage       { return definition.stage }
func (definition Definition) Consumer() Consumer { return definition.consumer }
func (definition Definition) RequiredBindings() []Binding {
	return append([]Binding(nil), definition.requiredBindings...)
}

func (definition Definition) Register(analyzerSHA256, configSHA256 string) (Registration, error) {
	if !validAnalyzerIdentity(definition.consumer, analyzerSHA256) || !digestPattern.MatchString(configSHA256) ||
		!validStageForConsumer(definition.stage, definition.consumer) || definition.name == "" {
		return Registration{}, ErrInvalid
	}
	bindings := append([]Binding(nil), definition.requiredBindings...)
	schemaVersion := SourceRegistrationSchemaVersion
	if definition.consumer == PlanProjection || definition.consumer == RunBinding {
		schemaVersion = AnalyzerFreeRegistrationSchemaVersion
	}
	value := identity{
		SchemaVersion: schemaVersion, Name: definition.name, Version: definition.version,
		Stage: definition.stage, AnalyzerSHA256: analyzerSHA256, ConfigSHA256: configSHA256,
		Consumer: definition.consumer, RequiredBindings: bindings,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Registration{}, ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return Registration{
		name: definition.name, version: definition.version, stage: definition.stage,
		analyzerSHA256: analyzerSHA256, configSHA256: configSHA256,
		consumer: definition.consumer, requiredBindings: bindings,
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
	default:
		return false
	}
}

func bindingsForConsumer(consumer Consumer) []Binding {
	switch consumer {
	case OverlayOnly:
		return overlayBindings
	case ExecutionPatch:
		return patchBindings
	case PlanProjection:
		return projectionBindings
	case RunBinding:
		return runBindings
	default:
		return nil
	}
}

func validAnalyzerIdentity(consumer Consumer, analyzerSHA256 string) bool {
	if consumer == PlanProjection || consumer == RunBinding {
		return analyzerSHA256 == ""
	}
	return digestPattern.MatchString(analyzerSHA256)
}

type Registration struct {
	name             Name
	version          string
	stage            Stage
	analyzerSHA256   string
	configSHA256     string
	consumer         Consumer
	requiredBindings []Binding
	identitySHA256   string
}

type identity struct {
	SchemaVersion    string    `json:"schema_version"`
	Name             Name      `json:"name"`
	Version          string    `json:"version"`
	Stage            Stage     `json:"stage"`
	AnalyzerSHA256   string    `json:"analyzer_sha256"`
	ConfigSHA256     string    `json:"config_sha256"`
	Consumer         Consumer  `json:"consumer"`
	RequiredBindings []Binding `json:"required_bindings"`
}

// New preserves the original built-in constructor. New pass implementations use
// Define(...).Register(...) and do not require edits to this switch.
func New(name Name, version, analyzerSHA256, configSHA256 string, consumer Consumer, bindings []Binding) (Registration, error) {
	var definition Definition
	switch {
	case name == SemanticPreDispatch && version == SemanticPreDispatchVersion:
		definition = SemanticPreDispatchDefinition()
	case name == PreparedPureRegion && version == PreparedPureRegionVersion:
		definition = PreparedPureRegionDefinition()
	case name == PreparedNumpyLoad && version == PreparedNumpyLoadVersion:
		definition = PreparedNumpyLoadDefinition()
	case name == CapabilityFutureProjection && version == CapabilityFutureProjectionVersion:
		definition = CapabilityFutureProjectionDefinition()
	case name == PreparedValueBinding && version == PreparedValueBindingVersion:
		definition = PreparedValueBindingDefinition()
	default:
		return Registration{}, ErrInvalid
	}
	if definition.consumer != consumer || !reflect.DeepEqual(definition.requiredBindings, bindings) {
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
func (registration Registration) RequiredBindings() []Binding {
	return append([]Binding(nil), registration.requiredBindings...)
}

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
