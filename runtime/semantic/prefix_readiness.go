package semantic

import (
	"errors"
	"strings"
	"sync"
	"unicode"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

// ConservativePrefixReadinessFilter is a Run-scoped, Host-owned skip-only
// screen. true requests exact target-Guest analysis; false can only forgo
// speculation and never admits a call.
type ConservativePrefixReadinessFilter struct {
	mu sync.Mutex

	projections []capability.PythonProjection
	lastSource  string
	lastIndex   uint32
	lastCalls   int
	lastRisks   int
	lastGeneric int
	opaque      bool
}

func NewConservativePrefixReadinessFilter(plan *capability.Plan) (*ConservativePrefixReadinessFilter, error) {
	if plan == nil || plan.Identity() == "" {
		return nil, errors.New("invalid prefix-readiness capability plan")
	}
	return &ConservativePrefixReadinessFilter{projections: plan.PreDispatchPythonProjections()}, nil
}

// ShouldAnalyzePrefix fails open to exact analysis on non-monotonic input. For
// monotonic source it requests analysis only when a projected call becomes
// lexically complete, a projected binding becomes uncertain, or a call is
// appended after an opaque definition/dynamic construct. Lexical false
// positives cost analysis only; false never grants authority.
func (filter *ConservativePrefixReadinessFilter) ShouldAnalyzePrefix(prefixIndex uint32, source string) bool {
	if filter == nil || prefixIndex == 0 || source == "" {
		return true
	}
	filter.mu.Lock()
	defer filter.mu.Unlock()

	monotonic := prefixIndex > filter.lastIndex && strings.HasPrefix(source, filter.lastSource)
	calls := projectedCallCount(source, filter.projections)
	risks, opaque := projectedRiskCount(source, filter.projections)
	generic := genericCallCount(source)
	shouldAnalyze := !monotonic || calls != filter.lastCalls || risks != filter.lastRisks || (filter.opaque && generic != filter.lastGeneric)

	filter.lastSource = source
	filter.lastIndex = prefixIndex
	filter.lastCalls = calls
	filter.lastRisks = risks
	filter.lastGeneric = generic
	filter.opaque = filter.opaque || opaque
	return shouldAnalyze
}

func projectedCallCount(source string, projections []capability.PythonProjection) int {
	count := 0
	for _, projection := range projections {
		count += pythonCallTokenCount(source, projection.Module+"."+projection.Method)
		if projection.GlobalAlias != "" {
			count += pythonCallTokenCount(source, projection.GlobalAlias)
		}
	}
	return count
}

func projectedRiskCount(source string, projections []capability.PythonProjection) (int, bool) {
	dynamicMarkers := []string{"def ", "class ", "lambda ", "eval(", "exec(", "getattr(", "setattr(", "__import__("}
	count := 0
	opaque := false
	for _, marker := range dynamicMarkers {
		occurrences := strings.Count(source, marker)
		count += occurrences
		opaque = opaque || occurrences > 0
	}
	for _, projection := range projections {
		for _, name := range []string{projection.Module, projection.GlobalAlias} {
			if name == "" {
				continue
			}
			count += bindingMarkerCount(source, name)
		}
		count += strings.Count(source, projection.Module+"."+projection.Method+" =")
		count += strings.Count(source, "del "+projection.Module+"."+projection.Method)
	}
	return count, opaque
}

func bindingMarkerCount(source, name string) int {
	markers := []string{name + " =", name + "=", "del " + name, "import " + name, "import " + name + " as ", "global " + name, "nonlocal " + name}
	count := 0
	for _, marker := range markers {
		count += strings.Count(source, marker)
	}
	return count
}

func pythonCallTokenCount(source, token string) int {
	count := 0
	for offset := 0; offset < len(source); {
		relative := strings.Index(source[offset:], token)
		if relative < 0 {
			break
		}
		start := offset + relative
		end := start + len(token)
		if identifierBoundaryBefore(source, start) && identifierBoundaryAfter(source, end) {
			cursor := end
			for cursor < len(source) && unicode.IsSpace(rune(source[cursor])) {
				cursor++
			}
			if cursor < len(source) && source[cursor] == '(' {
				count++
			}
		}
		offset = end
	}
	return count
}

func genericCallCount(source string) int {
	count := 0
	for index := 0; index < len(source); {
		if !isASCIIIdentifierStart(source[index]) {
			index++
			continue
		}
		index++
		for index < len(source) && isASCIIIdentifierContinue(source[index]) {
			index++
		}
		for index < len(source) && unicode.IsSpace(rune(source[index])) {
			index++
		}
		if index < len(source) && source[index] == '(' {
			count++
		}
	}
	return count
}

func identifierBoundaryBefore(source string, index int) bool {
	return index == 0 || !isASCIIIdentifierContinue(source[index-1])
}

func identifierBoundaryAfter(source string, index int) bool {
	return index == len(source) || !isASCIIIdentifierContinue(source[index])
}

func isASCIIIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIIdentifierContinue(value byte) bool {
	return isASCIIIdentifierStart(value) || value >= '0' && value <= '9'
}
