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

const (
	SemanticPreDispatch Name = "semantic_pre_dispatch"
	PreparedPureRegion  Name = "prepared_pure_region"

	SemanticPreDispatchVersion = "pysolate.semantic-pre-dispatch-pass.v0"
	PreparedPureRegionVersion  = "pysolate.prepared-pure-region-pass.v1"
	SemanticAnalyzerSHA256     = "sha256:9ed43801b84228c031ba1c3df35dbeab924f1de6d43bb41836b9be894b7be94e"

	OverlayOnly    Consumer = "overlay_only"
	ExecutionPatch Consumer = "execution_patch"

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
	FinalSourceSHA256      Binding = "final_source_sha256"
)

var (
	ErrInvalid    = errors.New("invalid semantic pass registration")
	ErrDuplicate  = errors.New("duplicate semantic pass registration")
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
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

func OverlayBindings() []Binding { return append([]Binding(nil), overlayBindings...) }
func PatchBindings() []Binding   { return append([]Binding(nil), patchBindings...) }

type Registration struct {
	name             Name
	version          string
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
	AnalyzerSHA256   string    `json:"analyzer_sha256"`
	ConfigSHA256     string    `json:"config_sha256"`
	Consumer         Consumer  `json:"consumer"`
	RequiredBindings []Binding `json:"required_bindings"`
}

func New(name Name, version, analyzerSHA256, configSHA256 string, consumer Consumer, bindings []Binding) (Registration, error) {
	expected := []Binding(nil)
	switch {
	case name == SemanticPreDispatch && version == SemanticPreDispatchVersion && consumer == OverlayOnly:
		expected = overlayBindings
	case name == PreparedPureRegion && version == PreparedPureRegionVersion && consumer == ExecutionPatch:
		expected = patchBindings
	default:
		return Registration{}, ErrInvalid
	}
	if !digestPattern.MatchString(analyzerSHA256) || !digestPattern.MatchString(configSHA256) || !reflect.DeepEqual(bindings, expected) {
		return Registration{}, ErrInvalid
	}
	bindings = append([]Binding(nil), bindings...)
	value := identity{
		SchemaVersion: "pysolate.semantic-pass-registration.v1", Name: name, Version: version,
		AnalyzerSHA256: analyzerSHA256, ConfigSHA256: configSHA256, Consumer: consumer,
		RequiredBindings: bindings,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Registration{}, ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return Registration{
		name: name, version: version, analyzerSHA256: analyzerSHA256, configSHA256: configSHA256,
		consumer: consumer, requiredBindings: bindings, identitySHA256: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func (registration Registration) Name() Name             { return registration.name }
func (registration Registration) Version() string        { return registration.version }
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
