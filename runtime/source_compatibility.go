package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	sourceCompatibilitySchemaVersion = 2
	sourceCompatibilityAnalyzer      = "static-agent-imports-v2"
	maxSourceCompatibilityBytes      = 1 << 20
	maxObservedSourceImports         = 1024
)

var (
	ErrSourceCompatibilityUnsupported   = errors.New("source compatibility unsupported")
	ErrSourceCompatibilityIndeterminate = errors.New("source compatibility indeterminate")
	ErrAgentSourceContractUnsupported   = errors.New("agent source contract unsupported")
	ErrAgentSourceInvalid               = errors.New("agent source invalid")
)

type SourceCompatibilityStatus string

const (
	SourceCompatible    SourceCompatibilityStatus = "compatible"
	SourceUnsupported   SourceCompatibilityStatus = "unsupported"
	SourceIndeterminate SourceCompatibilityStatus = "indeterminate"
)

type CompatibilityResult struct {
	status                SourceCompatibilityStatus
	sourceSHA256          string
	evidenceSHA256        string
	profileID             string
	artifactSHA256        string
	manifestSHA256        string
	declaredImports       []string
	observedImports       []string
	undeclaredImports     []string
	unusedDeclaredImports []string
	unqualifiedImports    []string
	indeterminateReasons  []string
}

type SourceCompatibilityError struct {
	Result CompatibilityResult
}

func (failure *SourceCompatibilityError) Error() string {
	if failure == nil {
		return ErrExecutionProfileUnsupported.Error()
	}
	return fmt.Sprintf("%s (%s)", ErrExecutionProfileUnsupported, failure.Result.Status())
}

func (failure *SourceCompatibilityError) Unwrap() error {
	return ErrExecutionProfileUnsupported
}

func (failure *SourceCompatibilityError) Is(target error) bool {
	if target == ErrExecutionProfileUnsupported {
		return true
	}
	if failure == nil {
		return false
	}
	return (failure.Result.Status() == SourceUnsupported && target == ErrSourceCompatibilityUnsupported) ||
		(failure.Result.Status() == SourceIndeterminate && target == ErrSourceCompatibilityIndeterminate)
}

type compatibilityResultDocument struct {
	SchemaVersion         int                       `json:"schema_version"`
	Analyzer              string                    `json:"analyzer"`
	Status                SourceCompatibilityStatus `json:"status"`
	SyntaxChecked         bool                      `json:"syntax_checked"`
	SourceSHA256          string                    `json:"source_sha256"`
	Profile               string                    `json:"profile"`
	ArtifactSHA256        string                    `json:"artifact_sha256,omitempty"`
	ManifestSHA256        string                    `json:"manifest_sha256,omitempty"`
	DeclaredImports       []string                  `json:"declared_imports"`
	ObservedImports       []string                  `json:"observed_imports"`
	UndeclaredImports     []string                  `json:"undeclared_imports"`
	UnusedDeclaredImports []string                  `json:"unused_declared_imports"`
	UnqualifiedImports    []string                  `json:"unqualified_imports"`
	IndeterminateReasons  []string                  `json:"indeterminate_reasons"`
	EvidenceSHA256        string                    `json:"evidence_sha256,omitempty"`
}

func (result CompatibilityResult) Status() SourceCompatibilityStatus { return result.status }
func (result CompatibilityResult) SyntaxChecked() bool               { return false }
func (result CompatibilityResult) SourceSHA256() string              { return result.sourceSHA256 }
func (result CompatibilityResult) EvidenceSHA256() string            { return result.evidenceSHA256 }
func (result CompatibilityResult) ProfileID() string                 { return result.profileID }
func (result CompatibilityResult) ArtifactSHA256() string            { return result.artifactSHA256 }
func (result CompatibilityResult) ManifestSHA256() string            { return result.manifestSHA256 }
func (result CompatibilityResult) DeclaredImports() []string {
	return cloneStrings(result.declaredImports)
}
func (result CompatibilityResult) ObservedImports() []string {
	return cloneStrings(result.observedImports)
}
func (result CompatibilityResult) UndeclaredImports() []string {
	return cloneStrings(result.undeclaredImports)
}
func (result CompatibilityResult) UnusedDeclaredImports() []string {
	return cloneStrings(result.unusedDeclaredImports)
}
func (result CompatibilityResult) UnqualifiedImports() []string {
	return cloneStrings(result.unqualifiedImports)
}
func (result CompatibilityResult) IndeterminateReasons() []string {
	return cloneStrings(result.indeterminateReasons)
}

func (result CompatibilityResult) Validate() error {
	if !validProfileID(result.profileID) || !validProfileDigest(result.sourceSHA256) ||
		(result.artifactSHA256 == "") != (result.manifestSHA256 == "") ||
		(result.artifactSHA256 != "" && (!validProfileDigest(result.artifactSHA256) || !validProfileDigest(result.manifestSHA256))) ||
		len(result.declaredImports) > maxDeclaredImports || len(result.observedImports) > maxObservedSourceImports ||
		len(result.undeclaredImports) > maxObservedSourceImports || len(result.unusedDeclaredImports) > maxDeclaredImports ||
		len(result.unqualifiedImports) > maxObservedSourceImports {
		return errors.New("invalid compatibility result")
	}
	for _, values := range [][]string{result.declaredImports, result.observedImports, result.undeclaredImports, result.unusedDeclaredImports, result.unqualifiedImports} {
		if !sortedUniqueImportRoots(values) {
			return errors.New("invalid compatibility result")
		}
	}
	for _, reason := range result.indeterminateReasons {
		switch reason {
		case "dynamic_execution", "dynamic_import", "import_set_too_large", "lexically_ambiguous", "non_preamble_import", "noncanonical_import", "relative_import", "source_too_large":
		default:
			return errors.New("invalid compatibility result")
		}
	}
	if !sort.StringsAreSorted(result.indeterminateReasons) || hasAdjacentDuplicate(result.indeterminateReasons) {
		return errors.New("invalid compatibility result")
	}
	wantStatus := SourceCompatible
	if len(result.undeclaredImports) != 0 || len(result.unusedDeclaredImports) != 0 || len(result.unqualifiedImports) != 0 || hasUnsupportedSourceReason(result.indeterminateReasons) {
		wantStatus = SourceUnsupported
	} else if len(result.indeterminateReasons) != 0 {
		wantStatus = SourceIndeterminate
	}
	if result.status != wantStatus {
		return errors.New("invalid compatibility result")
	}
	canonical, err := json.Marshal(result.document(false))
	if err != nil {
		return errors.New("invalid compatibility result")
	}
	digest := sha256.Sum256(canonical)
	if result.evidenceSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("invalid compatibility result")
	}
	return nil
}

func (result CompatibilityResult) MarshalJSON() ([]byte, error) {
	document := result.document(true)
	return json.Marshal(document)
}

func (result CompatibilityResult) document(includeEvidence bool) compatibilityResultDocument {
	document := compatibilityResultDocument{
		SchemaVersion:         sourceCompatibilitySchemaVersion,
		Analyzer:              sourceCompatibilityAnalyzer,
		Status:                result.status,
		SyntaxChecked:         false,
		SourceSHA256:          result.sourceSHA256,
		Profile:               result.profileID,
		ArtifactSHA256:        result.artifactSHA256,
		ManifestSHA256:        result.manifestSHA256,
		DeclaredImports:       cloneStrings(result.declaredImports),
		ObservedImports:       cloneStrings(result.observedImports),
		UndeclaredImports:     cloneStrings(result.undeclaredImports),
		UnusedDeclaredImports: cloneStrings(result.unusedDeclaredImports),
		UnqualifiedImports:    cloneStrings(result.unqualifiedImports),
		IndeterminateReasons:  cloneStrings(result.indeterminateReasons),
	}
	if includeEvidence {
		document.EvidenceSHA256 = result.evidenceSHA256
	}
	return document
}

// CompareSourceCompatibility compares bounded obvious import roots with caller
// declarations and Host policy. It does not parse or validate Python syntax and
// cannot grant authority. Obvious dynamic, relative, non-preamble, or declaration
// drift is unsupported; only genuine lexical or evidence-bound uncertainty is
// indeterminate. Exact enforcement belongs to the target Guest validator.
func CompareSourceCompatibility(request RunRequest, profile ExecutionProfile) CompatibilityResult {
	sourceDigest := sha256.Sum256([]byte(request.Code))
	result := CompatibilityResult{
		sourceSHA256:   "sha256:" + hex.EncodeToString(sourceDigest[:]),
		profileID:      profile.id,
		artifactSHA256: profile.artifactSHA256,
		manifestSHA256: profile.manifestSHA256,
	}
	declarations := make(map[string]struct{})
	if request.Compatibility != nil {
		result.profileID = request.Compatibility.Profile
		for _, module := range request.Compatibility.Imports {
			root := strings.SplitN(module, ".", 2)[0]
			declarations[root] = struct{}{}
		}
	}
	result.declaredImports = sortedSet(declarations)

	observed, reasons := scanConservativePythonImports(request.Code)
	result.observedImports = observed
	result.indeterminateReasons = reasons

	qualified := profile.qualifiedImports
	if len(qualified) == 0 {
		qualified = profile.allowedImports
	}
	for _, root := range observed {
		if _, ok := declarations[root]; !ok {
			result.undeclaredImports = append(result.undeclaredImports, root)
		}
		if _, ok := qualified[root]; !ok {
			result.unqualifiedImports = append(result.unqualifiedImports, root)
		}
	}
	for _, declared := range result.declaredImports {
		if !stringSliceContains(observed, declared) {
			result.unusedDeclaredImports = append(result.unusedDeclaredImports, declared)
		}
	}
	if len(result.undeclaredImports) != 0 || len(result.unusedDeclaredImports) != 0 || len(result.unqualifiedImports) != 0 || hasUnsupportedSourceReason(result.indeterminateReasons) {
		result.status = SourceUnsupported
	} else if len(result.indeterminateReasons) != 0 {
		result.status = SourceIndeterminate
	} else {
		result.status = SourceCompatible
	}
	canonical, _ := json.Marshal(result.document(false))
	evidenceDigest := sha256.Sum256(canonical)
	result.evidenceSHA256 = "sha256:" + hex.EncodeToString(evidenceDigest[:])
	return result
}

// InferStaticImportRoots derives the exact top-level import declaration for a
// bounded agent-submitted program. Callers do not need to ask the model to
// duplicate import metadata. Programs with dynamic, relative, non-preamble, or
// otherwise ambiguous imports fail closed; target-Guest validation remains the
// authoritative syntax and bytecode check.
func InferStaticImportRoots(source string) ([]string, error) {
	imports, reasons := scanConservativePythonImports(source)
	if len(reasons) == 0 {
		return imports, nil
	}
	if hasUnsupportedSourceReason(reasons) {
		return imports, ErrAgentSourceContractUnsupported
	}
	return imports, ErrSourceCompatibilityIndeterminate
}

func cloneStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringSliceContains(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func hasUnsupportedSourceReason(reasons []string) bool {
	for _, reason := range reasons {
		switch reason {
		case "dynamic_execution", "dynamic_import", "non_preamble_import", "noncanonical_import", "relative_import":
			return true
		}
	}
	return false
}

func sortedUniqueImportRoots(values []string) bool {
	if !sort.StringsAreSorted(values) || hasAdjacentDuplicate(values) {
		return false
	}
	for _, value := range values {
		if !validImportName(value) || strings.Contains(value, ".") {
			return false
		}
	}
	return true
}

func hasAdjacentDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

type sourceToken struct {
	value string
}

func scanConservativePythonImports(source string) ([]string, []string) {
	if len(source) > maxSourceCompatibilityBytes {
		return []string{}, []string{"source_too_large"}
	}
	sanitized, ambiguous := sanitizePythonSource(source)
	statements, bracketAmbiguous := sourceLogicalStatements(sanitized)
	observed := make(map[string]struct{})
	reasons := make(map[string]struct{})
	if ambiguous || bracketAmbiguous {
		reasons["lexically_ambiguous"] = struct{}{}
	}
	preambleOpen := true
	for _, statement := range statements {
		tokens := tokenizeSourceStatement(statement)
		if len(tokens) == 0 {
			continue
		}
		topLevelImport := tokens[0].value == "import" || tokens[0].value == "from"
		indented := len(statement) != len(strings.TrimLeft(statement, " 	"))
		if topLevelImport {
			if !preambleOpen || indented {
				reasons["non_preamble_import"] = struct{}{}
			}
		} else {
			preambleOpen = false
			for _, token := range tokens {
				if token.value == "import" {
					reasons["non_preamble_import"] = struct{}{}
				}
			}
		}
		consumedImport := make(map[int]struct{})
		for index := 0; index < len(tokens); index++ {
			if tokens[index].value == "__import__" || tokens[index].value == "import_module" {
				reasons["dynamic_import"] = struct{}{}
			} else if dynamicExecutionCall(tokens, index) {
				reasons["dynamic_execution"] = struct{}{}
			}
			if tokens[index].value != "from" {
				continue
			}
			importIndex, root, relative, ok := parseFromImport(tokens, index)
			if relative {
				reasons["relative_import"] = struct{}{}
			}
			if !ok {
				reasons["noncanonical_import"] = struct{}{}
				continue
			}
			consumedImport[importIndex] = struct{}{}
			if importIndex+1 < len(tokens) && tokens[importIndex+1].value == "*" {
				reasons["noncanonical_import"] = struct{}{}
			}
			if root == "builtins" || root == "importlib" {
				reasons["dynamic_import"] = struct{}{}
			}
			observed[root] = struct{}{}
		}
		for index := 0; index < len(tokens); index++ {
			if tokens[index].value != "import" {
				continue
			}
			if _, consumed := consumedImport[index]; consumed {
				continue
			}
			roots, ok := parseDirectImport(tokens, index)
			if !ok {
				reasons["noncanonical_import"] = struct{}{}
				continue
			}
			for _, root := range roots {
				if root == "builtins" || root == "importlib" {
					reasons["dynamic_import"] = struct{}{}
				}
				observed[root] = struct{}{}
			}
		}
	}
	roots := sortedSet(observed)
	if len(roots) > maxObservedSourceImports {
		reasons["import_set_too_large"] = struct{}{}
		roots = roots[:maxObservedSourceImports]
	}
	return roots, sortedSet(reasons)
}

func dynamicExecutionCall(tokens []sourceToken, index int) bool {
	if index+1 >= len(tokens) || tokens[index+1].value != "(" {
		return false
	}
	return tokens[index].value == "eval" || tokens[index].value == "exec"
}

func parseFromImport(tokens []sourceToken, start int) (int, string, bool, bool) {
	index := start + 1
	if index >= len(tokens) {
		return 0, "", false, false
	}
	relative := tokens[index].value == "."
	if relative {
		for index < len(tokens) && tokens[index].value == "." {
			index++
		}
	}
	if index >= len(tokens) || !identifierToken(tokens[index].value) {
		return 0, "", relative, false
	}
	root := tokens[index].value
	index++
	for index+1 < len(tokens) && tokens[index].value == "." && identifierToken(tokens[index+1].value) {
		index += 2
	}
	if index >= len(tokens) || tokens[index].value != "import" {
		return 0, "", relative, false
	}
	return index, root, relative, !relative
}

func parseDirectImport(tokens []sourceToken, start int) ([]string, bool) {
	index := start + 1
	roots := make([]string, 0, 2)
	for {
		if index >= len(tokens) || !identifierToken(tokens[index].value) {
			return nil, false
		}
		roots = append(roots, tokens[index].value)
		index++
		for index+1 < len(tokens) && tokens[index].value == "." && identifierToken(tokens[index+1].value) {
			index += 2
		}
		if index+1 < len(tokens) && tokens[index].value == "as" && identifierToken(tokens[index+1].value) {
			index += 2
		}
		if index >= len(tokens) {
			return roots, true
		}
		if tokens[index].value != "," {
			return nil, false
		}
		index++
	}
}

func identifierToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if index == 0 {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_') {
				return false
			}
			continue
		}
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func tokenizeSourceStatement(statement string) []sourceToken {
	tokens := make([]sourceToken, 0)
	for index := 0; index < len(statement); {
		character := statement[index]
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			index++
			continue
		}
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' {
			end := index + 1
			for end < len(statement) {
				next := statement[end]
				if !((next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_') {
					break
				}
				end++
			}
			tokens = append(tokens, sourceToken{value: statement[index:end]})
			index = end
			continue
		}
		tokens = append(tokens, sourceToken{value: string(character)})
		index++
	}
	return tokens
}

func sourceLogicalStatements(source string) ([]string, bool) {
	statements := make([]string, 0)
	var current strings.Builder
	delimiters := make([]byte, 0)
	ambiguous := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		switch character {
		case '(', '[', '{':
			delimiters = append(delimiters, character)
		case ')', ']', '}':
			if len(delimiters) == 0 || !matchingPythonDelimiter(delimiters[len(delimiters)-1], character) {
				ambiguous = true
			} else {
				delimiters = delimiters[:len(delimiters)-1]
			}
		}
		if character == '\\' && index+1 < len(source) && source[index+1] == '\n' {
			current.WriteByte(' ')
			index++
			continue
		}
		if (character == '\n' || character == ';') && len(delimiters) == 0 {
			if strings.TrimSpace(current.String()) != "" {
				statements = append(statements, current.String())
			}
			current.Reset()
			continue
		}
		current.WriteByte(character)
	}
	if strings.TrimSpace(current.String()) != "" {
		statements = append(statements, current.String())
	}
	if len(delimiters) != 0 {
		ambiguous = true
	}
	return statements, ambiguous
}

func matchingPythonDelimiter(open, close byte) bool {
	return (open == '(' && close == ')') || (open == '[' && close == ']') || (open == '{' && close == '}')
}

func sanitizePythonSource(source string) (string, bool) {
	var output strings.Builder
	output.Grow(len(source))
	quote := byte(0)
	triple := false
	escaped := false
	comment := false
	ambiguous := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		if comment {
			if character == '\n' {
				comment = false
				output.WriteByte('\n')
			} else {
				output.WriteByte(' ')
			}
			continue
		}
		if quote != 0 {
			if character == '\n' {
				output.WriteByte('\n')
			} else {
				output.WriteByte(' ')
			}
			if escaped {
				escaped = false
				continue
			}
			if character == '\n' && !triple {
				ambiguous = true
				quote = 0
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if triple {
				if character == quote && index+2 < len(source) && source[index+1] == quote && source[index+2] == quote {
					output.WriteString("  ")
					index += 2
					quote = 0
					triple = false
				}
			} else if character == quote {
				quote = 0
			}
			continue
		}
		if character == '#' {
			comment = true
			output.WriteByte(' ')
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			triple = index+2 < len(source) && source[index+1] == character && source[index+2] == character
			output.WriteByte(' ')
			if triple {
				output.WriteString("  ")
				index += 2
			}
			continue
		}
		output.WriteByte(character)
	}
	return output.String(), ambiguous || quote != 0
}
