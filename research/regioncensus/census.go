package regioncensus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const SchemaVersion = "pysolate.python-region-census.v0"

var ErrInvalid = errors.New("invalid Python region census")

type VerifiedObservation struct {
	ProgramID string
	Source    []byte
	Verified  semantic.VerifiedAnalysis
}

type observation struct {
	ProgramID string
	Source    []byte
	Analysis  semantic.Analysis
}

type KindCounts struct {
	StraightLine  uint32 `json:"straight_line"`
	Declaration   uint32 `json:"declaration"`
	OpaqueControl uint32 `json:"opaque_control"`
}

type EffectCounts struct {
	Pure        uint32 `json:"pure"`
	Publish     uint32 `json:"may_publish"`
	ObserveLive uint32 `json:"may_observe_live"`
	Suspend     uint32 `json:"may_suspend"`
	Unknown     uint32 `json:"may_be_unknown"`
}

type RejectionCount struct {
	Reason semantic.CandidateRejection `json:"reason"`
	Count  uint32                      `json:"count"`
}

type RegionResult struct {
	Ordinal          uint32                        `json:"ordinal"`
	Fingerprint      string                        `json:"fingerprint"`
	Kind             semantic.CandidateRegionKind  `json:"kind"`
	Effects          semantic.EffectSummary        `json:"effects"`
	Materializable   bool                          `json:"statically_materializable"`
	RejectionReasons []semantic.CandidateRejection `json:"rejection_reasons"`
}

type ProgramResult struct {
	ID                    string         `json:"id"`
	SourceSHA256          string         `json:"source_sha256"`
	Regions               []RegionResult `json:"regions"`
	MaterializableRegions uint32         `json:"statically_materializable_regions"`
}

type OverlapCounts struct {
	UniqueFingerprints             uint32 `json:"unique_fingerprints"`
	RepeatedFingerprints           uint32 `json:"repeated_fingerprints"`
	RepeatedOccurrences            uint32 `json:"repeated_occurrences"`
	SameProgramFingerprints        uint32 `json:"same_program_fingerprints"`
	SameProgramExcessOccurrences   uint32 `json:"same_program_excess_occurrences"`
	CrossProgramFingerprints       uint32 `json:"cross_program_fingerprints"`
	CrossProgramCoveredOccurrences uint32 `json:"cross_program_covered_occurrences"`
}

type Decision struct {
	Status           string   `json:"status"`
	ReasonCodes      []string `json:"reason_codes"`
	ConsumerAdmitted bool     `json:"consumer_admitted"`
}

type Report struct {
	SchemaVersion          string           `json:"schema_version"`
	CorpusSHA256           string           `json:"corpus_sha256"`
	AnalyzerSHA256         string           `json:"analyzer_sha256"`
	ArtifactSHA256         string           `json:"artifact_sha256"`
	ExecutionProfileSHA256 string           `json:"execution_profile_sha256"`
	ImportClosureSHA256    string           `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string           `json:"capability_plan_sha256"`
	ProgramsAnalyzed       uint32           `json:"programs_analyzed"`
	RegionsAnalyzed        uint32           `json:"regions_analyzed"`
	Kinds                  KindCounts       `json:"kind_counts"`
	Effects                EffectCounts     `json:"effect_counts"`
	MaterializableRegions  uint32           `json:"statically_materializable_regions"`
	Rejections             []RejectionCount `json:"rejection_counts"`
	ExactOverlap           OverlapCounts    `json:"exact_overlap"`
	MaterializableOverlap  OverlapCounts    `json:"statically_materializable_exact_overlap"`
	Decision               Decision         `json:"consumer_spike_decision"`
	Programs               []ProgramResult  `json:"programs"`
	seal                   [sha256.Size]byte
}

type fingerprintOccurrence struct {
	programID string
}

type fingerprintCall struct {
	Capability         string          `json:"capability"`
	NecessarilyReached bool            `json:"necessarily_reached"`
	ArgumentsCanonical bool            `json:"arguments_canonical"`
	CanonicalArguments json.RawMessage `json:"canonical_arguments"`
	DynamicOccurrence  uint32          `json:"dynamic_occurrence"`
}

type fingerprintPayload struct {
	SourceSliceSHA256 string                        `json:"source_slice_sha256"`
	Kind              semantic.CandidateRegionKind  `json:"kind"`
	LiveIns           []string                      `json:"live_ins"`
	LiveOuts          []string                      `json:"live_outs"`
	LiveInsCanonical  bool                          `json:"live_ins_canonical"`
	LiveOutsCanonical bool                          `json:"live_outs_canonical"`
	DataDependencies  []string                      `json:"data_dependencies"`
	Effects           semantic.EffectSummary        `json:"effects"`
	Calls             []fingerprintCall             `json:"calls"`
	Barriers          []semantic.BarrierCode        `json:"barriers"`
	Rejections        []semantic.CandidateRejection `json:"rejections"`
}

// BuildVerified derives a sealed, analysis-only census from opaque Host-verified analyses.
// Program IDs are treated as independent logical agent submissions; repeated fingerprints
// within one program are same-agent overlap and repeats across IDs are cross-agent overlap.
func BuildVerified(corpusSHA256 string, values []VerifiedObservation) (Report, error) {
	observations := make([]observation, len(values))
	for index, value := range values {
		analysis, err := value.Verified.Analysis()
		if err != nil {
			return Report{}, ErrInvalid
		}
		observations[index] = observation{ProgramID: value.ProgramID, Source: value.Source, Analysis: analysis}
	}
	return build(corpusSHA256, observations)
}

func build(corpusSHA256 string, observations []observation) (Report, error) {
	if !validDigest(corpusSHA256) || len(observations) == 0 || len(observations) > 256 {
		return Report{}, ErrInvalid
	}
	report := Report{
		SchemaVersion: SchemaVersion, CorpusSHA256: corpusSHA256,
		Rejections: []RejectionCount{}, Programs: make([]ProgramResult, 0, len(observations)),
	}
	all := map[string][]fingerprintOccurrence{}
	materializable := map[string][]fingerprintOccurrence{}
	rejectionCounts := map[semantic.CandidateRejection]uint32{}
	lastProgram := ""
	for _, observation := range observations {
		if !validID(observation.ProgramID) || observation.ProgramID <= lastProgram || len(observation.Source) == 0 ||
			observation.Analysis.Validate() != nil || sourceDigest(observation.Source) != observation.Analysis.SourceSHA256 {
			return Report{}, ErrInvalid
		}
		if report.AnalyzerSHA256 == "" {
			report.AnalyzerSHA256 = observation.Analysis.AnalyzerSHA256
			report.ArtifactSHA256 = observation.Analysis.ArtifactSHA256
			report.ExecutionProfileSHA256 = observation.Analysis.ExecutionProfileSHA256
			report.ImportClosureSHA256 = observation.Analysis.ImportClosureSHA256
			report.CapabilityPlanSHA256 = observation.Analysis.CapabilityPlanSHA256
		} else if report.AnalyzerSHA256 != observation.Analysis.AnalyzerSHA256 {
			return Report{}, ErrInvalid
		} else if report.ArtifactSHA256 != observation.Analysis.ArtifactSHA256 ||
			report.ExecutionProfileSHA256 != observation.Analysis.ExecutionProfileSHA256 ||
			report.ImportClosureSHA256 != observation.Analysis.ImportClosureSHA256 ||
			report.CapabilityPlanSHA256 != observation.Analysis.CapabilityPlanSHA256 {
			return Report{}, ErrInvalid
		}
		program := ProgramResult{
			ID: observation.ProgramID, SourceSHA256: observation.Analysis.SourceSHA256,
			Regions: make([]RegionResult, 0, len(observation.Analysis.CandidateRegions)),
		}
		callSites := make(map[string]semantic.CallSite, len(observation.Analysis.CallSites))
		for _, site := range observation.Analysis.CallSites {
			callSites[site.ID] = site
		}
		for ordinal, region := range observation.Analysis.CandidateRegions {
			fingerprint, err := fingerprintRegion(observation.Source, region, callSites)
			if err != nil {
				return Report{}, err
			}
			isMaterializable := staticallyMaterializable(region, observation.Analysis.ModuleEffects)
			row := RegionResult{
				Ordinal: uint32(ordinal), Fingerprint: fingerprint, Kind: region.Kind,
				Effects: region.Effects, Materializable: isMaterializable,
				RejectionReasons: append([]semantic.CandidateRejection{}, region.RejectionReasons...),
			}
			program.Regions = append(program.Regions, row)
			report.RegionsAnalyzed++
			incrementKind(&report.Kinds, region.Kind)
			incrementEffects(&report.Effects, region.Effects)
			for _, rejection := range region.RejectionReasons {
				rejectionCounts[rejection]++
			}
			occurrence := fingerprintOccurrence{programID: observation.ProgramID}
			all[fingerprint] = append(all[fingerprint], occurrence)
			if isMaterializable {
				program.MaterializableRegions++
				report.MaterializableRegions++
				materializable[fingerprint] = append(materializable[fingerprint], occurrence)
			}
		}
		report.Programs = append(report.Programs, program)
		report.ProgramsAnalyzed++
		lastProgram = observation.ProgramID
	}
	for reason, count := range rejectionCounts {
		report.Rejections = append(report.Rejections, RejectionCount{Reason: reason, Count: count})
	}
	sort.Slice(report.Rejections, func(i, j int) bool { return report.Rejections[i].Reason < report.Rejections[j].Reason })
	report.ExactOverlap = overlapCounts(all)
	report.MaterializableOverlap = overlapCounts(materializable)
	report.Decision = decide(report)
	if err := report.validateShape(); err != nil {
		return Report{}, err
	}
	report.seal = reportSeal(report)
	return report, nil
}

func Encode(report Report) ([]byte, error) {
	if report.Validate() != nil {
		return nil, ErrInvalid
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, ErrInvalid
	}
	return append(encoded, '\n'), nil
}

func (report Report) Validate() error {
	if report.validateShape() != nil || report.seal != reportSeal(report) {
		return ErrInvalid
	}
	return nil
}

func (report Report) validateShape() error {
	if report.SchemaVersion != SchemaVersion || !validDigest(report.CorpusSHA256) || !validDigest(report.AnalyzerSHA256) ||
		!validDigest(report.ArtifactSHA256) || !validDigest(report.ExecutionProfileSHA256) ||
		!validDigest(report.ImportClosureSHA256) || !validDigest(report.CapabilityPlanSHA256) ||
		report.ProgramsAnalyzed == 0 || len(report.Programs) != int(report.ProgramsAnalyzed) ||
		report.MaterializableRegions > report.RegionsAnalyzed || report.Decision.ConsumerAdmitted {
		return ErrInvalid
	}
	var regions, materializable uint32
	lastProgram := ""
	for _, program := range report.Programs {
		if !validID(program.ID) || program.ID <= lastProgram || !validDigest(program.SourceSHA256) || program.Regions == nil {
			return ErrInvalid
		}
		var programMaterializable uint32
		for ordinal, region := range program.Regions {
			if region.Ordinal != uint32(ordinal) || !validDigest(region.Fingerprint) ||
				(region.Kind != semantic.CandidateRegionStraightLine && region.Kind != semantic.CandidateRegionDeclaration && region.Kind != semantic.CandidateRegionOpaqueControl) ||
				region.RejectionReasons == nil || !strictRejections(region.RejectionReasons) {
				return ErrInvalid
			}
			regions++
			if region.Materializable {
				programMaterializable++
				materializable++
			}
		}
		if program.MaterializableRegions != programMaterializable {
			return ErrInvalid
		}
		lastProgram = program.ID
	}
	if regions != report.RegionsAnalyzed || materializable != report.MaterializableRegions ||
		report.ExactOverlap.UniqueFingerprints > report.RegionsAnalyzed ||
		report.MaterializableOverlap.UniqueFingerprints > report.MaterializableRegions {
		return ErrInvalid
	}
	if report.Decision.Status != "go_for_executable_region_spike" && report.Decision.Status != "no_go" {
		return ErrInvalid
	}
	if report.Decision.ReasonCodes == nil || !strictStrings(report.Decision.ReasonCodes) {
		return ErrInvalid
	}
	return nil
}

func fingerprintRegion(source []byte, region semantic.CandidateRegion, callSites map[string]semantic.CallSite) (string, error) {
	fragment, err := sourceSlice(source, region.Span)
	if err != nil || len(fragment) == 0 {
		return "", ErrInvalid
	}
	calls := make([]fingerprintCall, 0, len(region.CapabilityOccurrences))
	for _, id := range region.CapabilityOccurrences {
		site, ok := callSites[id]
		if !ok {
			return "", ErrInvalid
		}
		calls = append(calls, fingerprintCall{
			Capability: site.Capability, NecessarilyReached: site.NecessarilyReached,
			ArgumentsCanonical: site.ArgumentsCanonical,
			CanonicalArguments: append(json.RawMessage{}, site.CanonicalArguments...),
			DynamicOccurrence:  site.DynamicOccurrence,
		})
	}
	sort.Slice(calls, func(i, j int) bool {
		left, _ := json.Marshal(calls[i])
		right, _ := json.Marshal(calls[j])
		return string(left) < string(right)
	})
	payload := fingerprintPayload{
		SourceSliceSHA256: sourceDigest(fragment), Kind: region.Kind,
		LiveIns: append([]string{}, region.LiveIns...), LiveOuts: append([]string{}, region.LiveOuts...),
		LiveInsCanonical: region.LiveInsCanonical, LiveOutsCanonical: region.LiveOutsCanonical,
		DataDependencies: dependencyNames(region.DataDependencies),
		Effects:          region.Effects, Calls: calls, Barriers: append([]semantic.BarrierCode{}, region.Barriers...),
		Rejections: append([]semantic.CandidateRejection{}, region.RejectionReasons...),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalid
	}
	return sourceDigest(encoded), nil
}

func sourceSlice(source []byte, span semantic.SourceSpan) ([]byte, error) {
	lines := strings.Split(string(source), "\n")
	if span.StartLine == 0 || span.EndLine < span.StartLine || int(span.EndLine) > len(lines) {
		return nil, ErrInvalid
	}
	startLine := []byte(lines[span.StartLine-1])
	endLine := []byte(lines[span.EndLine-1])
	if int(span.StartColumn) > len(startLine) || int(span.EndColumn) > len(endLine) {
		return nil, ErrInvalid
	}
	if span.StartLine == span.EndLine {
		return append([]byte{}, startLine[span.StartColumn:span.EndColumn]...), nil
	}
	var builder strings.Builder
	builder.Write(startLine[span.StartColumn:])
	builder.WriteByte('\n')
	for line := span.StartLine; line < span.EndLine-1; line++ {
		builder.WriteString(lines[line])
		builder.WriteByte('\n')
	}
	builder.Write(endLine[:span.EndColumn])
	return []byte(builder.String()), nil
}

func dependencyNames(values []semantic.RegionDataDependency) []string {
	names := make([]string, len(values))
	for index, value := range values {
		names[index] = value.Name
	}
	return names
}

func staticallyMaterializable(region semantic.CandidateRegion, moduleEffects semantic.EffectSummary) bool {
	return moduleEffects == (semantic.EffectSummary{}) && region.Kind == semantic.CandidateRegionStraightLine &&
		region.LiveInsCanonical && region.LiveOutsCanonical &&
		len(region.LiveOuts) > 0 && region.Effects == (semantic.EffectSummary{}) &&
		len(region.CapabilityOccurrences) == 0 && len(region.Barriers) == 0 && len(region.RejectionReasons) == 0
}

func incrementKind(counts *KindCounts, kind semantic.CandidateRegionKind) {
	switch kind {
	case semantic.CandidateRegionStraightLine:
		counts.StraightLine++
	case semantic.CandidateRegionDeclaration:
		counts.Declaration++
	case semantic.CandidateRegionOpaqueControl:
		counts.OpaqueControl++
	}
}

func incrementEffects(counts *EffectCounts, effects semantic.EffectSummary) {
	if effects == (semantic.EffectSummary{}) {
		counts.Pure++
	}
	if effects.MayPublish {
		counts.Publish++
	}
	if effects.MayObserveLive {
		counts.ObserveLive++
	}
	if effects.MaySuspend {
		counts.Suspend++
	}
	if effects.MayBeUnknown {
		counts.Unknown++
	}
}

func overlapCounts(values map[string][]fingerprintOccurrence) OverlapCounts {
	counts := OverlapCounts{UniqueFingerprints: uint32(len(values))}
	for _, occurrences := range values {
		if len(occurrences) > 1 {
			counts.RepeatedFingerprints++
			counts.RepeatedOccurrences += uint32(len(occurrences))
		}
		byProgram := map[string]uint32{}
		for _, occurrence := range occurrences {
			byProgram[occurrence.programID]++
		}
		same := false
		for _, count := range byProgram {
			if count > 1 {
				same = true
				counts.SameProgramExcessOccurrences += count - 1
			}
		}
		if same {
			counts.SameProgramFingerprints++
		}
		if len(byProgram) > 1 {
			counts.CrossProgramFingerprints++
			counts.CrossProgramCoveredOccurrences += uint32(len(occurrences))
		}
	}
	return counts
}

func decide(report Report) Decision {
	reasons := []string{}
	if report.MaterializableRegions == 0 {
		reasons = append(reasons, "no_statically_materializable_regions")
	}
	if report.MaterializableOverlap.CrossProgramFingerprints == 0 {
		reasons = append(reasons, "no_cross_program_exact_materializable_overlap")
	}
	if len(reasons) != 0 {
		sort.Strings(reasons)
		return Decision{Status: "no_go", ReasonCodes: reasons, ConsumerAdmitted: false}
	}
	return Decision{
		Status:           "go_for_executable_region_spike",
		ReasonCodes:      []string{"cross_program_exact_materializable_overlap_observed"},
		ConsumerAdmitted: false,
	}
}

func reportSeal(report Report) [sha256.Size]byte {
	encoded, err := json.Marshal(report)
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(encoded)
}

func sourceDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validID(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if character != '-' && character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func strictStrings(values []string) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func strictRejections(values []semantic.CandidateRejection) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func (report Report) String() string {
	return fmt.Sprintf("programs=%d regions=%d materializable=%d cross_program_materializable=%d decision=%s",
		report.ProgramsAnalyzed, report.RegionsAnalyzed, report.MaterializableRegions,
		report.MaterializableOverlap.CrossProgramFingerprints, report.Decision.Status)
}
