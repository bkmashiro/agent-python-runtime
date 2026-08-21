package prepareddataset

import (
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

var ErrNoEligiblePeephole = errors.New("no eligible numpy.load peephole")

// NumpyLoadProjection asks the target-Guest analyzer for syntax facts about the
// closed NumPy call surface. It is not a capability grant; sources.read remains
// the separate Host-authorized physical effect.
var NumpyLoadProjection = semantic.CapabilityProjection{
	Name: PreparedCall, EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
	Module: "numpy", Method: "load", Arguments: []string{"path", "allow_pickle"},
}

// NumpyLoadFacts are authority-free, target-Guest-verified syntax facts.
type NumpyLoadFacts struct {
	SourcePrefix         string
	StreamEpoch          string
	AdmittedPrefixSHA256 string
	CallSite             semantic.CallSite
	verified             bool
}

func (facts NumpyLoadFacts) HasHostContract() bool { return false }

func (facts NumpyLoadFacts) Validate() error {
	if !facts.verified || facts.SourcePrefix == "" || !utf8.ValidString(facts.SourcePrefix) ||
		!validIdentity(facts.StreamEpoch) || !validDigest(facts.AdmittedPrefixSHA256) ||
		!hasPrefixDigest(facts.SourcePrefix, facts.AdmittedPrefixSHA256) ||
		facts.CallSite.Capability != PreparedCall || facts.CallSite.DynamicOccurrence != 1 ||
		!facts.CallSite.NecessarilyReached || !facts.CallSite.ArgumentsCanonical || !validSpan(facts.CallSite.Span) ||
		!validDigest(facts.CallSite.ID) || !validDigest(facts.CallSite.ControlRegionID) ||
		!canonicalArgumentsMatch(facts.CallSite.CanonicalArguments, PreparedResourcePath) {
		return ErrNoEligiblePeephole
	}
	return nil
}

// NewAnalysisRequest adds the closed syntax-only projection to the normal
// Plan-bound semantic request. The Plan still carries only real Host tools.
func NewAnalysisRequest(source string, bindings semantic.Bindings, plan *capability.Plan) (semantic.Request, error) {
	request, err := semantic.NewRequest(source, bindings, plan)
	if err != nil {
		return semantic.Request{}, err
	}
	request.Capabilities = append(request.Capabilities, NumpyLoadProjection)
	sort.Slice(request.Capabilities, func(i, j int) bool { return request.Capabilities[i].Name < request.Capabilities[j].Name })
	if err := request.Validate(); err != nil {
		return semantic.Request{}, err
	}
	return request, nil
}

// FactsFromVerifiedAnalysis accepts only the opaque result minted by the exact
// target-Guest analyzer and selects one canonical numpy.load occurrence.
func FactsFromVerifiedAnalysis(source, streamEpoch, admittedPrefixSHA256 string, verified semantic.VerifiedAnalysis) (NumpyLoadFacts, error) {
	analysis, err := verified.Analysis()
	if err != nil || analysis.SourceSHA256 != admittedPrefixSHA256 {
		return NumpyLoadFacts{}, ErrNoEligiblePeephole
	}
	return factsFromCallSites(source, streamEpoch, admittedPrefixSHA256, analysis.CallSites)
}

func factsFromCallSites(source, streamEpoch, admittedPrefixSHA256 string, callSites []semantic.CallSite) (NumpyLoadFacts, error) {
	if source == "" || !utf8.ValidString(source) || !validIdentity(streamEpoch) ||
		!validDigest(admittedPrefixSHA256) || !hasPrefixDigest(source, admittedPrefixSHA256) {
		return NumpyLoadFacts{}, ErrNoEligiblePeephole
	}
	var selected *semantic.CallSite
	for index := range callSites {
		if callSites[index].Capability != PreparedCall {
			continue
		}
		if selected != nil {
			return NumpyLoadFacts{}, ErrNoEligiblePeephole
		}
		copy := callSites[index]
		selected = &copy
	}
	if selected == nil {
		return NumpyLoadFacts{}, ErrNoEligiblePeephole
	}
	facts := NumpyLoadFacts{SourcePrefix: source, StreamEpoch: streamEpoch, AdmittedPrefixSHA256: admittedPrefixSHA256, CallSite: *selected, verified: true}
	if err := facts.Validate(); err != nil {
		return NumpyLoadFacts{}, err
	}
	return facts, nil
}
