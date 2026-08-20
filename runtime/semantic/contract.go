// Package semantic defines the bounded, Host-validated contract emitted by the
// exact target-Guest Python analyzer. It carries no executable authority.
package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"
)

const (
	AnalysisSchemaVersion = "pysolate.semantic-analysis.v3"
	PlanSchemaVersion     = "pysolate.semantic-plan.v0"
	MaxDocumentBytes      = 1 << 20

	maxFunctions       = 256
	maxRegions         = 256
	maxReferences      = 256
	maxDependencies    = 128
	maxBarriers        = 256
	maxCallSites       = 256
	maxIdentifierBytes = 256
)

var (
	ErrInvalidAnalysis = errors.New("invalid semantic analysis")
	ErrInvalidPlan     = errors.New("invalid semantic plan")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	capabilityPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

type BarrierCode string
type RegionKind string
type DependencyKind string
type CandidateRegionKind string
type CandidateRejection string

const (
	BarrierDynamicCall          BarrierCode = "dynamic_call"
	BarrierDynamicImport        BarrierCode = "dynamic_import"
	BarrierEvalExec             BarrierCode = "eval_exec"
	BarrierToolRebinding        BarrierCode = "tool_rebinding"
	BarrierUnsupportedDecorator BarrierCode = "unsupported_decorator"
	BarrierUnknownWASI          BarrierCode = "unknown_wasi"
	BarrierUnsupportedControl   BarrierCode = "unsupported_control_flow"

	RegionWholeRun      RegionKind = "whole_run"
	RegionWholeFunction RegionKind = "whole_function"

	DependencyCanonicalInputs     DependencyKind = "canonical_inputs"
	DependencyImmutableRoot       DependencyKind = "immutable_root"
	DependencyCapturedObservation DependencyKind = "captured_observation"

	CandidateRegionStraightLine  CandidateRegionKind = "straight_line"
	CandidateRegionOpaqueControl CandidateRegionKind = "opaque_control"
	CandidateRegionDeclaration   CandidateRegionKind = "declaration"

	CandidateRejectOpaqueControl       CandidateRejection = "opaque_control"
	CandidateRejectDeclaration         CandidateRejection = "declaration"
	CandidateRejectHeapMutation        CandidateRejection = "heap_mutation"
	CandidateRejectMayRaise            CandidateRejection = "may_raise"
	CandidateRejectUnknownEffect       CandidateRejection = "unknown_effect"
	CandidateRejectLiveInNotCanonical  CandidateRejection = "live_in_not_canonical"
	CandidateRejectLiveOutNotCanonical CandidateRejection = "live_out_not_canonical"
)

type EffectSummary struct {
	MayPublish     bool `json:"may_publish"`
	MayObserveLive bool `json:"may_observe_live"`
	MaySuspend     bool `json:"may_suspend"`
	MayBeUnknown   bool `json:"may_be_unknown"`
}

type SourceSpan struct {
	StartLine   uint32 `json:"start_line"`
	StartColumn uint32 `json:"start_column"`
	EndLine     uint32 `json:"end_line"`
	EndColumn   uint32 `json:"end_column"`
}

func (span SourceSpan) valid() bool {
	return span.StartLine > 0 && span.EndLine >= span.StartLine &&
		(span.EndLine != span.StartLine || span.EndColumn >= span.StartColumn)
}

func (span SourceSpan) contains(child SourceSpan) bool {
	if !span.valid() || !child.valid() {
		return false
	}
	startsBefore := span.StartLine < child.StartLine || (span.StartLine == child.StartLine && span.StartColumn <= child.StartColumn)
	endsAfter := span.EndLine > child.EndLine || (span.EndLine == child.EndLine && span.EndColumn >= child.EndColumn)
	return startsBefore && endsAfter
}

type FunctionSummary struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	SCCID              string        `json:"scc_id"`
	Span               SourceSpan    `json:"span"`
	Effects            EffectSummary `json:"effects"`
	Calls              []string      `json:"calls"`
	DirectCapabilities []string      `json:"direct_capabilities"`
}

type Barrier struct {
	Code       BarrierCode `json:"code"`
	FunctionID string      `json:"function_id,omitempty"`
	Span       SourceSpan  `json:"span"`
}

type CallSite struct {
	ID                 string          `json:"id"`
	Span               SourceSpan      `json:"span"`
	Capability         string          `json:"capability"`
	ControlRegionID    string          `json:"control_region_id"`
	NecessarilyReached bool            `json:"necessarily_reached"`
	ArgumentsCanonical bool            `json:"arguments_canonical"`
	CanonicalArguments json.RawMessage `json:"canonical_arguments"`
	DynamicOccurrence  uint32          `json:"dynamic_occurrence"`
}

type RegionDataDependency struct {
	Name             string `json:"name"`
	ProducerRegionID string `json:"producer_region_id"`
}

type CandidateRegion struct {
	ID                    string                 `json:"id"`
	Kind                  CandidateRegionKind    `json:"kind"`
	Span                  SourceSpan             `json:"span"`
	ControlRegionID       string                 `json:"control_region_id"`
	ControlPredecessors   []string               `json:"control_predecessors"`
	DataDependencies      []RegionDataDependency `json:"data_dependencies"`
	LiveIns               []string               `json:"live_ins"`
	LiveOuts              []string               `json:"live_outs"`
	LiveInsCanonical      bool                   `json:"live_ins_canonical"`
	LiveOutsCanonical     bool                   `json:"live_outs_canonical"`
	Effects               EffectSummary          `json:"effects"`
	CapabilityOccurrences []string               `json:"capability_occurrences"`
	Barriers              []BarrierCode          `json:"barriers"`
	RejectionReasons      []CandidateRejection   `json:"rejection_reasons"`
}

// LocallyReusable reports only analyzer-local eligibility. It carries no Host
// authority to execute, cache, transport, admit, or replace the region.
func (region CandidateRegion) LocallyReusable() bool {
	return region.Kind == CandidateRegionStraightLine &&
		region.Effects == (EffectSummary{}) && region.LiveInsCanonical && region.LiveOutsCanonical &&
		len(region.CapabilityOccurrences) == 0 && len(region.Barriers) == 0 && len(region.RejectionReasons) == 0
}

type Analysis struct {
	SchemaVersion           string            `json:"schema_version"`
	SourceSHA256            string            `json:"source_sha256"`
	ASTSHA256               string            `json:"ast_sha256"`
	AnalyzerSHA256          string            `json:"analyzer_sha256"`
	ArtifactSHA256          string            `json:"artifact_sha256"`
	ExecutionProfileSHA256  string            `json:"execution_profile_sha256"`
	ImportClosureSHA256     string            `json:"import_closure_sha256"`
	CapabilityPlanSHA256    string            `json:"capability_plan_sha256"`
	ModuleSpan              SourceSpan        `json:"module_span"`
	ModuleEffects           EffectSummary     `json:"module_effects"`
	Functions               []FunctionSummary `json:"functions"`
	Barriers                []Barrier         `json:"barriers"`
	CallSiteCoverage        string            `json:"call_site_coverage"`
	CallSites               []CallSite        `json:"call_sites"`
	CandidateRegionCoverage string            `json:"candidate_region_coverage"`
	CandidateRegionCount    int               `json:"candidate_region_count"`
	CandidateRegions        []CandidateRegion `json:"candidate_regions"`
}

func (analysis Analysis) Validate() error {
	if analysis.SchemaVersion != AnalysisSchemaVersion || !analysis.ModuleSpan.valid() ||
		len(analysis.Functions) > maxFunctions || len(analysis.Barriers) > maxBarriers ||
		len(analysis.CallSites) > maxCallSites || analysis.CallSites == nil ||
		len(analysis.CandidateRegions) > maxRegions || analysis.CandidateRegions == nil ||
		analysis.CandidateRegionCount != len(analysis.CandidateRegions) ||
		analysis.CallSiteCoverage != "positive_only" || analysis.CandidateRegionCoverage != "module_top_level_complete" {
		return ErrInvalidAnalysis
	}
	for _, digest := range []string{
		analysis.SourceSHA256, analysis.ASTSHA256, analysis.AnalyzerSHA256,
		analysis.ArtifactSHA256, analysis.ExecutionProfileSHA256,
		analysis.ImportClosureSHA256, analysis.CapabilityPlanSHA256,
	} {
		if !digestPattern.MatchString(digest) {
			return ErrInvalidAnalysis
		}
	}
	if !sort.SliceIsSorted(analysis.Functions, func(i, j int) bool { return analysis.Functions[i].ID < analysis.Functions[j].ID }) {
		return ErrInvalidAnalysis
	}
	functions := make(map[string]FunctionSummary, len(analysis.Functions))
	for index, function := range analysis.Functions {
		if !digestPattern.MatchString(function.ID) || !digestPattern.MatchString(function.SCCID) ||
			!boundedString(function.Name) || !analysis.ModuleSpan.contains(function.Span) ||
			len(function.Calls) > maxReferences || len(function.DirectCapabilities) > maxReferences ||
			!sortedUnique(function.Calls) || !sortedUnique(function.DirectCapabilities) ||
			(index > 0 && analysis.Functions[index-1].ID == function.ID) {
			return ErrInvalidAnalysis
		}
		for _, call := range function.Calls {
			if !digestPattern.MatchString(call) {
				return ErrInvalidAnalysis
			}
		}
		for _, capability := range function.DirectCapabilities {
			if !capabilityPattern.MatchString(capability) {
				return ErrInvalidAnalysis
			}
		}
		functions[function.ID] = function
	}
	for _, function := range analysis.Functions {
		for _, call := range function.Calls {
			if _, ok := functions[call]; !ok {
				return ErrInvalidAnalysis
			}
		}
	}
	if !sort.SliceIsSorted(analysis.Barriers, func(i, j int) bool { return barrierKey(analysis.Barriers[i]) < barrierKey(analysis.Barriers[j]) }) {
		return ErrInvalidAnalysis
	}
	for index, barrier := range analysis.Barriers {
		if !validBarrierCode(barrier.Code) || !analysis.ModuleSpan.contains(barrier.Span) ||
			(index > 0 && barrierKey(analysis.Barriers[index-1]) == barrierKey(barrier)) {
			return ErrInvalidAnalysis
		}
		if barrier.FunctionID == "" {
			if !analysis.ModuleEffects.MayBeUnknown {
				return ErrInvalidAnalysis
			}
			continue
		}
		function, ok := functions[barrier.FunctionID]
		if !ok || !function.Effects.MayBeUnknown || !function.Span.contains(barrier.Span) {
			return ErrInvalidAnalysis
		}
	}
	if !sort.SliceIsSorted(analysis.CallSites, func(i, j int) bool { return analysis.CallSites[i].ID < analysis.CallSites[j].ID }) {
		return ErrInvalidAnalysis
	}
	for index, site := range analysis.CallSites {
		if !digestPattern.MatchString(site.ID) || !digestPattern.MatchString(site.ControlRegionID) ||
			!capabilityPattern.MatchString(site.Capability) || !analysis.ModuleSpan.contains(site.Span) ||
			!site.ArgumentsCanonical || site.DynamicOccurrence != 1 || !validCanonicalArguments(site.CanonicalArguments) ||
			(index > 0 && analysis.CallSites[index-1].ID == site.ID) {
			return ErrInvalidAnalysis
		}
	}
	callSites := make(map[string]CallSite, len(analysis.CallSites))
	for _, site := range analysis.CallSites {
		callSites[site.ID] = site
	}
	seenRegions := make(map[string]struct{}, len(analysis.CandidateRegions))
	occurrenceCounts := make(map[string]int, len(analysis.CallSites))
	for index, region := range analysis.CandidateRegions {
		if !digestPattern.MatchString(region.ID) || !digestPattern.MatchString(region.ControlRegionID) ||
			!analysis.ModuleSpan.contains(region.Span) || region.ControlPredecessors == nil || region.DataDependencies == nil ||
			region.LiveIns == nil || region.LiveOuts == nil || region.CapabilityOccurrences == nil ||
			region.Barriers == nil || region.RejectionReasons == nil ||
			len(region.ControlPredecessors) > 1 || len(region.DataDependencies) > maxDependencies ||
			len(region.LiveIns) > maxReferences || len(region.LiveOuts) > maxReferences ||
			len(region.CapabilityOccurrences) > maxReferences || len(region.Barriers) > maxBarriers ||
			len(region.RejectionReasons) > maxBarriers || !sortedUnique(region.LiveIns) || !sortedUnique(region.LiveOuts) ||
			!sortedUnique(region.CapabilityOccurrences) || !validCandidateBarriers(region.Barriers) ||
			!validCandidateRejections(region.RejectionReasons) {
			return ErrInvalidAnalysis
		}
		if _, exists := seenRegions[region.ID]; exists {
			return ErrInvalidAnalysis
		}
		if index == 0 {
			if len(region.ControlPredecessors) != 0 {
				return ErrInvalidAnalysis
			}
		} else if len(region.ControlPredecessors) != 1 || region.ControlPredecessors[0] != analysis.CandidateRegions[index-1].ID ||
			!spanEndsBeforeOrAt(analysis.CandidateRegions[index-1].Span, region.Span) {
			return ErrInvalidAnalysis
		}
		if !validRegionDataDependencies(region.DataDependencies, region.LiveIns, seenRegions) {
			return ErrInvalidAnalysis
		}
		seenRegions[region.ID] = struct{}{}
		for _, name := range append(append([]string{}, region.LiveIns...), region.LiveOuts...) {
			if !validIdentifier(name) {
				return ErrInvalidAnalysis
			}
		}
		for _, occurrence := range region.CapabilityOccurrences {
			site, ok := callSites[occurrence]
			if !ok || site.ControlRegionID != region.ControlRegionID || !region.Span.contains(site.Span) {
				return ErrInvalidAnalysis
			}
			occurrenceCounts[occurrence]++
		}
		if !validCandidateRegionShape(region) {
			return ErrInvalidAnalysis
		}
	}
	for id := range callSites {
		if occurrenceCounts[id] != 1 {
			return ErrInvalidAnalysis
		}
	}
	return nil
}

func (analysis Analysis) Identity() (string, []byte, error) {
	if err := analysis.Validate(); err != nil {
		return "", nil, err
	}
	return identity(analysis)
}

type Dependency struct {
	Kind           DependencyKind `json:"kind"`
	IdentitySHA256 string         `json:"identity_sha256"`
}

type Region struct {
	ID               string        `json:"id"`
	Kind             RegionKind    `json:"kind"`
	FunctionID       string        `json:"function_id,omitempty"`
	Span             SourceSpan    `json:"span"`
	ASTSHA256        string        `json:"ast_sha256"`
	Effects          EffectSummary `json:"effects"`
	Dependencies     []Dependency  `json:"dependencies"`
	InputsCanonical  bool          `json:"inputs_canonical"`
	OutputsCanonical bool          `json:"outputs_canonical"`
	RejectionReasons []BarrierCode `json:"rejection_reasons"`
}

func (region Region) Reusable() bool {
	return !region.Effects.MayPublish && !region.Effects.MayObserveLive &&
		!region.Effects.MaySuspend && !region.Effects.MayBeUnknown &&
		region.InputsCanonical && region.OutputsCanonical && len(region.RejectionReasons) == 0
}

type Plan struct {
	SchemaVersion string   `json:"schema_version"`
	Analysis      Analysis `json:"analysis"`
	Regions       []Region `json:"regions"`
}

func (plan Plan) Validate() error {
	if plan.SchemaVersion != PlanSchemaVersion || plan.Analysis.Validate() != nil || len(plan.Regions) > maxRegions ||
		!sort.SliceIsSorted(plan.Regions, func(i, j int) bool { return plan.Regions[i].ID < plan.Regions[j].ID }) {
		return ErrInvalidPlan
	}
	functions := make(map[string]FunctionSummary, len(plan.Analysis.Functions))
	for _, function := range plan.Analysis.Functions {
		functions[function.ID] = function
	}
	for index, region := range plan.Regions {
		if !digestPattern.MatchString(region.ID) || !digestPattern.MatchString(region.ASTSHA256) ||
			!plan.Analysis.ModuleSpan.contains(region.Span) || len(region.Dependencies) > maxDependencies ||
			len(region.RejectionReasons) > maxBarriers || !validDependencies(region.Dependencies) ||
			!validRejections(region.RejectionReasons) || (index > 0 && plan.Regions[index-1].ID == region.ID) {
			return ErrInvalidPlan
		}
		switch region.Kind {
		case RegionWholeFunction:
			function, ok := functions[region.FunctionID]
			if !ok || !function.Span.contains(region.Span) || !effectCovers(region.Effects, function.Effects) {
				return ErrInvalidPlan
			}
		case RegionWholeRun:
			if region.FunctionID != "" || !effectCovers(region.Effects, plan.Analysis.ModuleEffects) {
				return ErrInvalidPlan
			}
			for _, function := range plan.Analysis.Functions {
				if !effectCovers(region.Effects, function.Effects) {
					return ErrInvalidPlan
				}
			}
		default:
			return ErrInvalidPlan
		}
		if len(region.RejectionReasons) > 0 && !region.Effects.MayBeUnknown {
			return ErrInvalidPlan
		}
	}
	return nil
}

func (plan Plan) Identity() (string, []byte, error) {
	if err := plan.Validate(); err != nil {
		return "", nil, err
	}
	return identity(plan)
}

func DecodeAnalysis(raw []byte) (Analysis, error) {
	var analysis Analysis
	if err := strictDecode(raw, &analysis); err != nil || analysis.Validate() != nil {
		return Analysis{}, ErrInvalidAnalysis
	}
	return analysis, nil
}

func DecodePlan(raw []byte) (Plan, error) {
	var plan Plan
	if err := strictDecode(raw, &plan); err != nil || plan.Validate() != nil {
		return Plan{}, ErrInvalidPlan
	}
	return plan, nil
}

func strictDecode(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes {
		return errors.New("semantic document size out of bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("semantic document has trailing data")
	}
	return nil
}

func validCanonicalArguments(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 64<<10 || !json.Valid(raw) {
		return false
	}
	var arguments map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&arguments); err != nil || arguments == nil {
		return false
	}
	for _, value := range arguments {
		switch value.(type) {
		case nil, bool, string, json.Number:
		default:
			return false
		}
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(arguments); err != nil {
		return false
	}
	return bytes.Equal(raw, bytes.TrimSuffix(canonical.Bytes(), []byte{'\n'}))
}

func identity(value any) (string, []byte, error) {
	document, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:]), document, nil
}

func boundedString(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= maxIdentifierBytes
}

func sortedUnique(values []string) bool {
	if !sort.StringsAreSorted(values) {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] == value) {
			return false
		}
	}
	return true
}

func spanEndsBeforeOrAt(left, right SourceSpan) bool {
	return left.EndLine < right.StartLine || left.EndLine == right.StartLine && left.EndColumn <= right.StartColumn
}

func validRegionDataDependencies(values []RegionDataDependency, liveIns []string, seenRegions map[string]struct{}) bool {
	live := make(map[string]struct{}, len(liveIns))
	for _, name := range liveIns {
		live[name] = struct{}{}
	}
	for index, dependency := range values {
		if !validIdentifier(dependency.Name) || !digestPattern.MatchString(dependency.ProducerRegionID) {
			return false
		}
		if _, ok := live[dependency.Name]; !ok {
			return false
		}
		if _, ok := seenRegions[dependency.ProducerRegionID]; !ok {
			return false
		}
		if index > 0 && (values[index-1].Name > dependency.Name ||
			values[index-1].Name == dependency.Name && values[index-1].ProducerRegionID >= dependency.ProducerRegionID) {
			return false
		}
	}
	return true
}

func validCandidateBarriers(values []BarrierCode) bool {
	for index, value := range values {
		if !validBarrierCode(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validCandidateRejections(values []CandidateRejection) bool {
	for index, value := range values {
		if !validCandidateRejection(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validCandidateRejection(value CandidateRejection) bool {
	switch value {
	case CandidateRejectOpaqueControl, CandidateRejectDeclaration, CandidateRejectHeapMutation, CandidateRejectMayRaise,
		CandidateRejectUnknownEffect, CandidateRejectLiveInNotCanonical, CandidateRejectLiveOutNotCanonical:
		return true
	default:
		return false
	}
}

func hasCandidateRejection(region CandidateRegion, expected CandidateRejection) bool {
	for _, value := range region.RejectionReasons {
		if value == expected {
			return true
		}
	}
	return false
}

func hasCandidateBarrier(region CandidateRegion, expected BarrierCode) bool {
	for _, value := range region.Barriers {
		if value == expected {
			return true
		}
	}
	return false
}

func validCandidateRegionShape(region CandidateRegion) bool {
	switch region.Kind {
	case CandidateRegionStraightLine:
	case CandidateRegionOpaqueControl:
		if !hasCandidateRejection(region, CandidateRejectOpaqueControl) || !hasCandidateBarrier(region, BarrierUnsupportedControl) {
			return false
		}
	case CandidateRegionDeclaration:
		if !hasCandidateRejection(region, CandidateRejectDeclaration) {
			return false
		}
	default:
		return false
	}
	if region.Effects.MayBeUnknown != hasCandidateRejection(region, CandidateRejectUnknownEffect) ||
		region.LiveInsCanonical == hasCandidateRejection(region, CandidateRejectLiveInNotCanonical) ||
		region.LiveOutsCanonical == hasCandidateRejection(region, CandidateRejectLiveOutNotCanonical) {
		return false
	}
	return true
}

func barrierKey(barrier Barrier) string {
	return barrier.FunctionID + "\x00" + string(barrier.Code) + "\x00" + spanKey(barrier.Span)
}

func spanKey(span SourceSpan) string {
	encoded, _ := json.Marshal(span)
	return string(encoded)
}

func validBarrierCode(code BarrierCode) bool {
	switch code {
	case BarrierDynamicCall, BarrierDynamicImport, BarrierEvalExec, BarrierToolRebinding,
		BarrierUnsupportedDecorator, BarrierUnknownWASI, BarrierUnsupportedControl:
		return true
	default:
		return false
	}
}

func validDependencyKind(kind DependencyKind) bool {
	switch kind {
	case DependencyCanonicalInputs, DependencyImmutableRoot, DependencyCapturedObservation:
		return true
	default:
		return false
	}
}

func validDependencies(dependencies []Dependency) bool {
	for index, dependency := range dependencies {
		if !validDependencyKind(dependency.Kind) || !digestPattern.MatchString(dependency.IdentitySHA256) {
			return false
		}
		if index > 0 {
			previous := dependencies[index-1]
			if previous.Kind > dependency.Kind || (previous.Kind == dependency.Kind && previous.IdentitySHA256 >= dependency.IdentitySHA256) {
				return false
			}
		}
	}
	return true
}

func validRejections(reasons []BarrierCode) bool {
	for index, reason := range reasons {
		if !validBarrierCode(reason) || (index > 0 && reasons[index-1] >= reason) {
			return false
		}
	}
	return true
}

func effectCovers(claim, source EffectSummary) bool {
	return (!source.MayPublish || claim.MayPublish) &&
		(!source.MayObserveLive || claim.MayObserveLive) &&
		(!source.MaySuspend || claim.MaySuspend) &&
		(!source.MayBeUnknown || claim.MayBeUnknown)
}
