package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrInvalidTool    = errors.New("invalid Host tool")
	ErrToolExists     = errors.New("Host tool is already registered")
	ErrRegistrySealed = errors.New("Host tool registry is sealed")
)

const (
	capabilityPlanSchemaVersion = "pysolate.capability-plan.v5"
	maxCapabilitySchemaBytes    = 64 << 10
	maxCapabilityJSONNodes      = 16384

	EffectPure           = "pure"
	EffectWorkspaceRead  = "workspace_read"
	EffectWorkspaceWrite = "workspace_write"
	EffectExternalRead   = "external_read"

	PlaybackLiveOnly = "live_only"
	PlaybackCaptured = "captured"

	FreshnessPlanEpoch              = "plan_epoch"
	UnclaimedDiscardWithDisposition = "discard_with_disposition"
)

// Handler is the entire Host-tool execution contract. Authority and schema
// selection remain Host-owned; Guest code can only submit JSON arguments.
type Handler interface {
	Call(context.Context, json.RawMessage) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (function HandlerFunc) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	return function(ctx, arguments)
}

// PythonProjection describes a generated convenience wrapper. It is only a
// presentation surface: the Broker still admits and validates the named Spec.
type PythonProjection struct {
	Module      string   `json:"module"`
	Method      string   `json:"method"`
	GlobalAlias string   `json:"global_alias,omitempty"`
	Arguments   []string `json:"arguments"`
	ResultField string   `json:"result_field,omitempty"`
}

// ResourceReference names one Host-owned logical read resource. Exactly one key
// selector is present: an exact Python argument or a Host-authored constant.
type ResourceReference struct {
	Namespace string `json:"namespace"`
	Argument  string `json:"argument,omitempty"`
	Constant  string `json:"constant,omitempty"`
}

// PreDispatchContract is the minimal v0 contract for starting one exact read
// before unchanged Python reaches and claims its original call boundary.
type PreDispatchContract struct {
	Resource  ResourceReference `json:"resource"`
	Freshness string            `json:"freshness"`
	Unclaimed string            `json:"unclaimed"`
}

// Spec is the canonical Host-owned definition shared by registration, plan
// identity, Broker validation and Python projection.
type Spec struct {
	Name            string               `json:"capability"`
	Version         string               `json:"version"`
	Description     string               `json:"description"`
	EffectClass     string               `json:"effect_class"`
	Playback        string               `json:"playback"`
	HandlerIdentity string               `json:"handler_identity"`
	InputSchema     json.RawMessage      `json:"input_schema"`
	OutputSchema    json.RawMessage      `json:"output_schema"`
	Python          *PythonProjection    `json:"python,omitempty"`
	ReadOnly        bool                 `json:"read_only,omitempty"`
	Idempotent      bool                 `json:"idempotent,omitempty"`
	PreDispatch     *PreDispatchContract `json:"pre_dispatch,omitempty"`
}

// PreDispatchQualification is Host-authored adapter policy, never inferred
// from an HTTP verb, capability name, or Agent-produced request.
type PreDispatchQualification struct {
	readOnly   bool
	idempotent bool
	contract   PreDispatchContract
}

func (qualification PreDispatchQualification) Eligible() bool {
	return qualification.readOnly && qualification.idempotent && validPreDispatchContract(nil, &qualification.contract)
}

// Contract returns the defensive Host-authored contract carried by this sealed
// qualification.
func (qualification PreDispatchQualification) Contract() PreDispatchContract {
	return qualification.contract
}

type registration struct {
	spec         Spec
	grant        Grant
	handler      Handler
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
}

type Registry struct {
	mu            sync.RWMutex
	registrations map[string]registration
	pythonNames   map[string]string
	sealed        bool
}

type CapabilityBinding = Spec

// StreamingObservationBinding is the Host-owned capability/policy fragment used
// by streaming staged-observation identities. It carries digests and stable
// adapter identity only, never grants or handlers themselves.
type StreamingObservationBinding struct {
	Capability        string
	SpecSHA256        string
	HandlerIdentity   string
	PlanSHA256        string
	GrantPolicySHA256 string
}

type ToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	EffectClass  string          `json:"effect_class"`
	Playback     string          `json:"playback"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

type PlanConfig struct {
	MaxCalls uint32
}

type Plan struct {
	identity      string
	maxCalls      uint32
	specs         []Spec
	grants        []GrantBinding
	registrations map[string]registration
	pythonPrelude string
}

func NewRegistry() *Registry {
	return &Registry{registrations: make(map[string]registration), pythonNames: make(map[string]string)}
}

func (registry *Registry) Register(spec Spec, grant Grant, handler Handler) error {
	canonical, inputSchema, outputSchema, err := prepareSpec(spec)
	if registry == nil || err != nil || handler == nil {
		return ErrInvalidTool
	}
	if canonical.Playback == PlaybackCaptured {
		if _, ok := handler.(EvidenceHandler); !ok {
			return ErrInvalidTool
		}
	}
	if !validGrant(grant) {
		return ErrInvalidGrant
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return ErrRegistrySealed
	}
	if _, exists := registry.registrations[canonical.Name]; exists {
		return ErrToolExists
	}
	if canonical.Python != nil {
		projection := canonical.Python
		methodKey := "method:" + projection.Module + "." + projection.Method
		moduleKey := "global:" + projection.Module
		moduleOwner := "module:" + projection.Module
		if _, exists := registry.pythonNames[methodKey]; exists {
			return ErrToolExists
		}
		if owner, exists := registry.pythonNames[moduleKey]; exists && owner != moduleOwner {
			return ErrToolExists
		}
		if projection.GlobalAlias != "" {
			aliasKey := "global:" + projection.GlobalAlias
			if _, exists := registry.pythonNames[aliasKey]; exists {
				return ErrToolExists
			}
		}
		registry.pythonNames[methodKey] = canonical.Name
		registry.pythonNames[moduleKey] = moduleOwner
		if projection.GlobalAlias != "" {
			registry.pythonNames["global:"+projection.GlobalAlias] = canonical.Name
		}
	}
	registry.registrations[canonical.Name] = registration{
		spec: canonical, grant: grant, handler: handler, inputSchema: inputSchema, outputSchema: outputSchema,
	}
	return nil
}

func (registry *Registry) Seal(config PlanConfig) (*Plan, error) {
	if registry == nil || config.MaxCalls == 0 {
		return nil, ErrInvalidTool
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return nil, ErrRegistrySealed
	}
	registry.sealed = true
	specs := make([]Spec, 0, len(registry.registrations))
	grants := make([]GrantBinding, 0, len(registry.registrations))
	registrations := make(map[string]registration, len(registry.registrations))
	for name, registered := range registry.registrations {
		specs = append(specs, cloneSpec(registered.spec))
		grants = append(grants, GrantBinding{Capability: name, PolicySHA256: registered.grant.Identity()})
		registrations[name] = registered
	}
	sortSpecs(specs)
	sortGrants(grants)
	document := struct {
		SchemaVersion string         `json:"schema_version"`
		MaxCalls      uint32         `json:"max_calls"`
		Capabilities  []Spec         `json:"capabilities"`
		Grants        []GrantBinding `json:"grants"`
	}{SchemaVersion: capabilityPlanSchemaVersion, MaxCalls: config.MaxCalls, Capabilities: specs, Grants: grants}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvalidTool
	}
	digest := sha256.Sum256(encoded)
	return &Plan{
		identity:      "sha256:" + hex.EncodeToString(digest[:]),
		maxCalls:      config.MaxCalls,
		specs:         cloneSpecs(specs),
		grants:        append([]GrantBinding(nil), grants...),
		registrations: registrations,
		pythonPrelude: generatePythonPrelude(specs),
	}, nil
}

func (plan *Plan) Identity() string {
	if plan == nil {
		return ""
	}
	return plan.identity
}

// StreamingObservationBinding returns frozen identity material for a future verified
// consumer. It does not add the capability to the streaming eager-call map or start
// physical work.
func (plan *Plan) StreamingObservationBinding(name string) (StreamingObservationBinding, bool) {
	if plan == nil {
		return StreamingObservationBinding{}, false
	}
	registered, ok := plan.registrations[name]
	qualification, qualified := preDispatchQualification(registered.spec)
	if !ok || !qualified || !qualification.Eligible() {
		return StreamingObservationBinding{}, false
	}
	specBytes, err := json.Marshal(registered.spec)
	if err != nil {
		return StreamingObservationBinding{}, false
	}
	specDigest := sha256.Sum256(specBytes)
	return StreamingObservationBinding{
		Capability: name, SpecSHA256: "sha256:" + hex.EncodeToString(specDigest[:]),
		HandlerIdentity: registered.spec.HandlerIdentity, PlanSHA256: plan.identity,
		GrantPolicySHA256: registered.grant.Identity(),
	}, true
}

func (plan *Plan) MaxCalls() uint32 {
	if plan == nil {
		return 0
	}
	return plan.maxCalls
}

func (plan *Plan) Capabilities() []CapabilityBinding { return plan.Specs() }

func (plan *Plan) Specs() []Spec {
	if plan == nil {
		return nil
	}
	return cloneSpecs(plan.specs)
}

func (plan *Plan) ToolSchemas() []ToolSchema {
	if plan == nil {
		return nil
	}
	tools := make([]ToolSchema, len(plan.specs))
	for index, spec := range plan.specs {
		tools[index] = ToolSchema{
			Name: spec.Name, Description: spec.Description, EffectClass: spec.EffectClass, Playback: spec.Playback,
			InputSchema: append(json.RawMessage(nil), spec.InputSchema...), OutputSchema: append(json.RawMessage(nil), spec.OutputSchema...),
		}
	}
	return tools
}

func (plan *Plan) Grants() []GrantBinding {
	if plan == nil {
		return nil
	}
	return append([]GrantBinding(nil), plan.grants...)
}

// PreDispatch returns a defensive, Host-sealed capability-side qualification. A
// caller still needs verified program facts and exact authority/observation identity.
func (plan *Plan) PreDispatch(name string) (PreDispatchQualification, bool) {
	registered, ok := plan.lookup(name)
	if !ok {
		return PreDispatchQualification{}, false
	}
	return preDispatchQualification(registered.spec)
}

func preDispatchQualification(spec Spec) (PreDispatchQualification, bool) {
	if spec.PreDispatch == nil {
		return PreDispatchQualification{}, false
	}
	return PreDispatchQualification{
		readOnly: spec.ReadOnly, idempotent: spec.Idempotent, contract: *spec.PreDispatch,
	}, true
}

func (plan *Plan) PythonPrelude() string {
	if plan == nil {
		return ""
	}
	return plan.pythonPrelude
}

// StreamingPythonPrelude projects only capabilities that are safe to invoke
// before final source/workspace/effect seal. Write-class capabilities remain
// absent even when they exist in the final sealed Plan.
func (plan *Plan) StreamingPythonPrelude() string {
	if plan == nil {
		return ""
	}
	allowed := make([]Spec, 0, len(plan.specs))
	for _, spec := range plan.specs {
		if spec.EffectClass == EffectPure || spec.EffectClass == EffectWorkspaceRead || spec.EffectClass == EffectExternalRead {
			allowed = append(allowed, spec)
		}
	}
	return generatePythonPrelude(allowed)
}

func (plan *Plan) lookup(name string) (registration, bool) {
	if plan == nil {
		return registration{}, false
	}
	registered, ok := plan.registrations[name]
	return registered, ok
}

func prepareSpec(spec Spec) (Spec, *jsonschema.Schema, *jsonschema.Schema, error) {
	if !validName(spec.Name) || !validHandlerIdentity(spec.Version) || !validHandlerIdentity(spec.HandlerIdentity) ||
		len(spec.Description) == 0 || len(spec.Description) > 1024 || !utf8.ValidString(spec.Description) ||
		!validEffectClass(spec.EffectClass) || !validPlaybackTreatment(spec.Playback) ||
		len(spec.InputSchema) == 0 || len(spec.InputSchema) > maxCapabilitySchemaBytes ||
		len(spec.OutputSchema) == 0 || len(spec.OutputSchema) > maxCapabilitySchemaBytes || !validPythonProjection(spec.Python) ||
		!validPreDispatchQualification(spec) {
		return Spec{}, nil, nil, ErrInvalidTool
	}
	inputDocument, inputCanonical, err := canonicalJSON(spec.InputSchema)
	if err != nil {
		return Spec{}, nil, nil, ErrInvalidTool
	}
	outputDocument, outputCanonical, err := canonicalJSON(spec.OutputSchema)
	if err != nil {
		return Spec{}, nil, nil, ErrInvalidTool
	}
	inputSchema, err := compileCapabilitySchema("input", inputDocument)
	if err != nil {
		return Spec{}, nil, nil, ErrInvalidTool
	}
	outputSchema, err := compileCapabilitySchema("output", outputDocument)
	if err != nil {
		return Spec{}, nil, nil, ErrInvalidTool
	}
	canonical := cloneSpec(spec)
	canonical.InputSchema = inputCanonical
	canonical.OutputSchema = outputCanonical
	return canonical, inputSchema, outputSchema, nil
}

func validPreDispatchQualification(spec Spec) bool {
	any := spec.ReadOnly || spec.Idempotent || spec.PreDispatch != nil
	if !any {
		return true
	}
	readClass := spec.EffectClass == EffectPure || spec.EffectClass == EffectWorkspaceRead || spec.EffectClass == EffectExternalRead
	return readClass && spec.ReadOnly && spec.Idempotent && spec.Python != nil && validPreDispatchContract(spec.Python, spec.PreDispatch)
}

func validPreDispatchContract(projection *PythonProjection, contract *PreDispatchContract) bool {
	if contract == nil || contract.Freshness != FreshnessPlanEpoch ||
		contract.Unclaimed != UnclaimedDiscardWithDisposition || !validHandlerIdentity(contract.Resource.Namespace) ||
		(contract.Resource.Argument == "") == (contract.Resource.Constant == "") {
		return false
	}
	if contract.Resource.Constant != "" {
		return validHandlerIdentity(contract.Resource.Constant)
	}
	if !validPythonIdentifier(contract.Resource.Argument) {
		return false
	}
	if projection == nil {
		return true
	}
	for _, argument := range projection.Arguments {
		if argument == contract.Resource.Argument {
			return true
		}
	}
	return false
}

type denyCapabilitySchemaLoader struct{}

func (denyCapabilitySchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external capability schema resources are disabled")
}

func compileCapabilitySchema(kind string, document any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(denyCapabilitySchemaLoader{})
	resource := "mem:///capability-" + kind + ".schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func canonicalJSON(raw []byte) (any, json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, nil, errors.New("JSON is not valid UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("JSON contains trailing data")
	}
	canonical, err := json.Marshal(document)
	return document, canonical, err
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxCapabilityJSONNodes {
		return errors.New("JSON is too complex")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return errors.New("JSON contains duplicate keys")
			}
			seen[key] = true
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is invalid")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is invalid")
		}
	default:
		return errors.New("JSON is invalid")
	}
	return nil
}

func canonicalForSchema(schema *jsonschema.Schema, raw []byte) (json.RawMessage, error) {
	document, canonical, err := canonicalJSON(raw)
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(document); err != nil {
		return nil, err
	}
	return canonical, nil
}

func validateAgainst(schema *jsonschema.Schema, raw []byte) error {
	_, err := canonicalForSchema(schema, raw)
	return err
}

func generatePythonPrelude(specs []Spec) string {
	projected := make([]Spec, 0, len(specs))
	modules := make(map[string]struct{})
	for _, spec := range specs {
		if spec.Python != nil {
			projected = append(projected, spec)
			modules[spec.Python.Module] = struct{}{}
		}
	}
	if len(projected) == 0 {
		return ""
	}
	moduleNames := make([]string, 0, len(modules))
	for module := range modules {
		moduleNames = append(moduleNames, module)
	}
	sortStrings(moduleNames)
	var builder strings.Builder
	builder.WriteString("\nimport json as _host_json\nimport _agent_runtime_host as _host_bridge\n_capability_call_sequence = 0\n_stream_eager_calls = {}\n\nclass _CapabilityModule:\n    pass\n\ndef _capability_call(capability, arguments):\n    global _capability_call_sequence\n    _capability_call_sequence += 1\n    request = {\n        \"call_id\": \"capability-\" + str(_capability_call_sequence),\n        \"capability\": capability,\n        \"arguments\": arguments,\n    }\n    response = _host_json.loads(_host_bridge.call(_host_json.dumps(request, separators=(\",\", \":\"))))\n    if response[\"status\"] != \"ok\":\n        raise RuntimeError(response[\"error\"][\"message\"])\n    return response[\"result\"]\n")
	for _, module := range moduleNames {
		fmt.Fprintf(&builder, "\n%s = _CapabilityModule()\n", module)
	}
	for index, spec := range projected {
		projection := spec.Python
		proxy := fmt.Sprintf("_capability_proxy_%d", index)
		fmt.Fprintf(&builder, "\ndef %s(%s):\n    return _capability_call(%s, {", proxy, strings.Join(projection.Arguments, ", "), pythonString(spec.Name))
		for argumentIndex, argument := range projection.Arguments {
			if argumentIndex != 0 {
				builder.WriteString(", ")
			}
			fmt.Fprintf(&builder, "%s: %s", pythonString(argument), argument)
		}
		builder.WriteString("})")
		if projection.ResultField != "" {
			fmt.Fprintf(&builder, "[%s]", pythonString(projection.ResultField))
		}
		builder.WriteByte('\n')
		fmt.Fprintf(&builder, "%s.%s = %s\n", projection.Module, projection.Method, proxy)
		if projection.GlobalAlias != "" {
			fmt.Fprintf(&builder, "%s = %s.%s\n", projection.GlobalAlias, projection.Module, projection.Method)
		}
	}
	return builder.String()
}

func pythonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func validPythonProjection(projection *PythonProjection) bool {
	if projection == nil {
		return true
	}
	if !validPythonProjectionName(projection.Module) || !validPythonProjectionName(projection.Method) ||
		(projection.GlobalAlias != "" && !validPythonProjectionName(projection.GlobalAlias)) || projection.GlobalAlias == projection.Module ||
		len(projection.ResultField) > 128 || !utf8.ValidString(projection.ResultField) {
		return false
	}
	seen := make(map[string]struct{}, len(projection.Arguments))
	for _, argument := range projection.Arguments {
		if !validPythonIdentifier(argument) {
			return false
		}
		if _, exists := seen[argument]; exists {
			return false
		}
		seen[argument] = struct{}{}
	}
	return true
}

func validPythonProjectionName(value string) bool {
	if !validPythonIdentifier(value) || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validPythonIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 || pythonKeywords[value] || pythonReservedNames[value] {
		return false
	}
	for index, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '_' &&
			(index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}

var pythonReservedNames = map[string]bool{
	"_host_json": true, "_host_bridge": true, "_capability_call": true, "_capability_call_sequence": true,
	"inputs": true, "result": true,
	"abs": true, "aiter": true, "all": true, "anext": true, "any": true, "ascii": true,
	"bin": true, "bool": true, "breakpoint": true, "bytearray": true, "bytes": true,
	"callable": true, "chr": true, "classmethod": true, "compile": true, "complex": true,
	"delattr": true, "dict": true, "dir": true, "divmod": true, "enumerate": true,
	"copyright": true, "credits": true, "eval": true, "exec": true, "exit": true, "filter": true,
	"float": true, "format": true, "frozenset": true,
	"getattr": true, "globals": true, "hasattr": true, "hash": true, "help": true, "hex": true,
	"id": true, "input": true, "int": true, "isinstance": true, "issubclass": true, "iter": true,
	"len": true, "list": true, "locals": true, "map": true, "max": true, "memoryview": true,
	"license": true, "min": true, "next": true, "object": true, "oct": true, "open": true, "ord": true,
	"pow": true, "print": true, "property": true, "range": true, "repr": true, "reversed": true,
	"quit": true, "round": true, "set": true, "setattr": true, "slice": true, "sorted": true, "staticmethod": true,
	"str": true, "sum": true, "super": true, "tuple": true, "type": true, "vars": true, "zip": true,
	"__import__": true,
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true, "async": true,
	"await": true, "break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true,
	"else": true, "except": true, "finally": true, "for": true, "from": true, "global": true, "if": true,
	"import": true, "in": true, "is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true, "with": true, "yield": true,
}

func cloneSpec(spec Spec) Spec {
	cloned := spec
	cloned.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
	cloned.OutputSchema = append(json.RawMessage(nil), spec.OutputSchema...)
	if spec.Python != nil {
		projection := *spec.Python
		projection.Arguments = append([]string{}, spec.Python.Arguments...)
		cloned.Python = &projection
	}
	if spec.PreDispatch != nil {
		contract := *spec.PreDispatch
		cloned.PreDispatch = &contract
	}
	return cloned
}

func cloneSpecs(specs []Spec) []Spec {
	cloned := make([]Spec, len(specs))
	for index, spec := range specs {
		cloned[index] = cloneSpec(spec)
	}
	return cloned
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}

func sortSpecs(values []Spec) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current].Name < values[current-1].Name; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}

func sortGrants(values []GrantBinding) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current].Capability < values[current-1].Capability; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}

func validEffectClass(value string) bool {
	return value == EffectPure || value == EffectWorkspaceRead || value == EffectWorkspaceWrite || value == EffectExternalRead
}

func validPlaybackTreatment(value string) bool {
	return value == PlaybackLiveOnly || value == PlaybackCaptured
}

func validName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func validHandlerIdentity(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}
