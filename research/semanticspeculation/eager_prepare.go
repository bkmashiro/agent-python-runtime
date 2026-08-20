package semanticspeculation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type EagerComparatorPrepareConfig struct {
	Inputs             json.RawMessage
	Chunks             []string
	Plan               *capability.Plan
	AllowedImportRoots []string
}

func BuildEagerComparatorBeginPrepare(config EagerComparatorPrepareConfig) (string, error) {
	var inputs any
	if len(config.Inputs) == 0 || json.Unmarshal(config.Inputs, &inputs) != nil {
		return "", errors.New("eager comparator requires JSON inputs")
	}
	canonicalInputs, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("canonicalize comparator inputs: %w", err)
	}
	allowedRoots := append([]string(nil), config.AllowedImportRoots...)
	if allowedRoots == nil {
		allowedRoots = []string{}
	}
	for index, root := range allowedRoots {
		if !validComparatorImportRoot(root) || (index > 0 && allowedRoots[index-1] >= root) {
			return "", errors.New("comparator import roots must be sorted unique public identifiers")
		}
	}
	allowedImports, err := json.Marshal(allowedRoots)
	if err != nil {
		return "", fmt.Errorf("canonicalize comparator imports: %w", err)
	}
	var begin strings.Builder
	if config.Plan != nil {
		begin.WriteString(config.Plan.StreamingPythonPrelude())
	}
	begin.WriteString("\nimport json as _eager_json\nimport agent_runtime as _eager_runtime\n")
	begin.WriteString("from agent_runtime import eager_comparator as _eager_comparator_module\n")
	fmt.Fprintf(&begin, "_eager_comparator_module._begin(_eager_json.loads(%s), dict(_eager_runtime._prepared_globals), %s)\n", strconv.Quote(string(canonicalInputs)), allowedImports)
	return begin.String(), nil
}

func BuildEagerComparatorChunkPrepare(chunk string) (string, error) {
	if chunk == "" {
		return "", errors.New("eager comparator chunk must be non-empty")
	}
	return "from agent_runtime import eager_comparator as _eager_comparator_module\ncomparator_event = _eager_comparator_module._chunk(" + strconv.Quote(chunk) + ")\n", nil
}

func BuildEagerComparatorFinishPrepare() string {
	return "from agent_runtime import eager_comparator as _eager_comparator_module\ncomparator_final = _eager_comparator_module._finish()\n"
}

func BuildEagerComparatorPrepareChunks(config EagerComparatorPrepareConfig) ([]string, error) {
	if len(config.Chunks) == 0 {
		return nil, errors.New("eager comparator requires source chunks")
	}
	begin, err := BuildEagerComparatorBeginPrepare(config)
	if err != nil {
		return nil, err
	}
	fragments := []string{begin}
	for _, chunk := range config.Chunks {
		fragment, err := BuildEagerComparatorChunkPrepare(chunk)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	return append(fragments, BuildEagerComparatorFinishPrepare()), nil
}

func validComparatorImportRoot(root string) bool {
	if root == "" || root[0] == '_' || !((root[0] >= 'a' && root[0] <= 'z') || (root[0] >= 'A' && root[0] <= 'Z')) {
		return false
	}
	for index := 1; index < len(root); index++ {
		character := root[index]
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}
