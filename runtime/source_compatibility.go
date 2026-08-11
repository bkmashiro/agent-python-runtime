package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxSourceCompatibilityBytes = 1 << 20

var (
	ErrSourceCompatibilityUnsupported   = errors.New("source imports are not admitted")
	ErrSourceCompatibilityIndeterminate = errors.New("source imports are not statically determinable")
	ErrAgentSourceContractUnsupported   = errors.New("Guest rejected the source contract")
	ErrAgentSourceInvalid               = errors.New("Guest rejected invalid Python source")
)

// InferStaticImportRoots implements the intentionally small PoC source contract.
// Imports must be single-line top-level statements in the initial import
// preamble. The trusted Guest performs the authoritative AST check again before
// execution, so ambiguous syntax is rejected rather than guessed here.
func InferStaticImportRoots(source string) ([]string, error) {
	if len(source) > maxSourceCompatibilityBytes {
		return nil, fmt.Errorf("%w: source exceeds one MiB", ErrSourceCompatibilityIndeterminate)
	}
	roots := map[string]struct{}{}
	bodyStarted := false
	for number, raw := range strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "__import__") || strings.Contains(trimmed, "importlib") {
			return nil, fmt.Errorf("%w: dynamic import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
		}
		indented := len(raw) != len(strings.TrimLeft(raw, " \t"))
		isImport := strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ")
		if indented && isImport {
			return nil, fmt.Errorf("%w: nested import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
		}
		if !isImport {
			bodyStarted = true
			continue
		}
		if bodyStarted {
			return nil, fmt.Errorf("%w: import after executable code at line %d", ErrSourceCompatibilityIndeterminate, number+1)
		}
		if strings.ContainsAny(trimmed, ";\\()") {
			return nil, fmt.Errorf("%w: multiline or compound import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
		}
		var imported []string
		if strings.HasPrefix(trimmed, "import ") {
			for _, item := range strings.Split(strings.TrimSpace(strings.TrimPrefix(trimmed, "import ")), ",") {
				name := strings.Fields(strings.TrimSpace(item))
				if len(name) != 1 && !(len(name) == 3 && name[1] == "as") {
					return nil, fmt.Errorf("%w: malformed import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
				}
				imported = append(imported, name[0])
			}
		} else {
			fields := strings.Fields(trimmed)
			if len(fields) < 4 || fields[0] != "from" || fields[2] != "import" || strings.HasPrefix(fields[1], ".") {
				return nil, fmt.Errorf("%w: malformed or relative import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
			}
			imported = []string{fields[1]}
		}
		for _, module := range imported {
			root := strings.SplitN(module, ".", 2)[0]
			if !validImportName(root) {
				return nil, fmt.Errorf("%w: invalid import at line %d", ErrSourceCompatibilityIndeterminate, number+1)
			}
			if root != "__future__" {
				roots[root] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(roots))
	for root := range roots {
		result = append(result, root)
	}
	sort.Strings(result)
	return result, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
