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
	SourceRegistrationSchemaVersion = "pysolate.semantic-pass-registration.v2"

	SemanticPreDispatch Name = "semantic_pre_dispatch"
	PreparedPureRegion  Name = "prepared_pure_region"
	PreparedNumpyLoad   Name = "prepared_numpy_load"

	SemanticPreDispatchVersion = "pysolate.semantic-pre-dispatch-pass.v0"
	PreparedPureRegionVersion  = "pysolate.prepared-pure-region-pass.v1"
	PreparedNumpyLoadVersion   = "pysolate.prepared-numpy-load-pass.v1"
	SemanticAnalyzerSHA256     = "sha256:9ed43801b84228c031ba1c3df35dbeab924f1de6d43bb41836b9be894b7be94e"

	OverlayOnly    Consumer = "overlay_only"
	ExecutionPatch Consumer = "execution_patch"

	StagePrefixOverlay      Stage = "prefix_overlay"
	StageHybridPreparePatch Stage = "hybrid_prepare_patch"
	StageWholeProgramPatch  Stage = "whole_program_patch"
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

func (definition Definition) Name() Name         { return definition.name }
func (definition Definition) Version() string    { return definition.version }
func (definition Definition) Stage() Stage       { return definition.stage }
func (definition Definition) Consumer() Consumer { return definition.consumer }

func (definition Definition) Register(analyzerSHA256, configSHA256 string) (Registration, error) {
	if !digestPattern.MatchString(analyzerSHA256) || !digestPattern.MatchString(configSHA256) ||
		definition.name == "" {
		return Registration{}, ErrInvalid
	}
	value := identity{
		SchemaVersion: SourceRegistrationSchemaVersion, Name: definition.name, Version: definition.version,
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
		return stage == StageHybridPreparePatch || stage == StageWholeProgramPatch
	default:
		return false
	}
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
