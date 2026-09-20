package pysolate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	maxToolNameBytes        = 128
	maxToolDescriptionBytes = 4096
	maxToolSchemaBytes      = 64 << 10
)

var canonicalToolName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:/-]*$`)

// ToolAnnotations are discovery hints for Agents and adapters. They do not
// grant authority or enable speculative execution. AllowEarlyRead remains the
// explicit Host-owned execution contract.
type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"read_only_hint,omitempty"`
	DestructiveHint bool `json:"destructive_hint,omitempty"`
	IdempotentHint  bool `json:"idempotent_hint,omitempty"`
	OpenWorldHint   bool `json:"open_world_hint,omitempty"`
}

// ToolDefinition is one canonical Host tool discovered from a provider.
type ToolDefinition struct {
	Name string
	Spec ToolSpec
}

// ToolProvider discovers a bounded set of Host-owned tools before Runner
// construction. Providers retain their connections and credentials; only
// normalized metadata and Call closures enter the Manifest.
type ToolProvider interface {
	Tools(context.Context) ([]ToolDefinition, error)
}

// ManifestFromProviders discovers and merges providers. Duplicate canonical
// names fail closed instead of being resolved by provider order.
func ManifestFromProviders(ctx context.Context, providers ...ToolProvider) (Manifest, error) {
	manifest := make(Manifest)
	for index, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("tool provider %d is nil", index)
		}
		definitions, err := provider.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover tool provider %d: %w", index, err)
		}
		for _, definition := range definitions {
			if _, exists := manifest[definition.Name]; exists {
				return nil, fmt.Errorf("duplicate tool from providers: %s", definition.Name)
			}
			manifest[definition.Name] = definition.Spec
		}
	}
	normalized, _, err := normalizeManifest(manifest)
	return normalized, err
}

// NewFromProviders discovers a Host-owned catalog and constructs a Runner.
// Provider discovery happens once; rebuild the Runner to refresh the catalog.
func NewFromProviders(ctx context.Context, wasm []byte, providers ...ToolProvider) (*Runner, error) {
	manifest, err := ManifestFromProviders(ctx, providers...)
	if err != nil {
		return nil, err
	}
	return New(ctx, wasm, manifest)
}

func normalizeManifest(manifest Manifest) (Manifest, []guestToolSpec, error) {
	normalized := make(Manifest, len(manifest))
	guest := make([]guestToolSpec, 0, len(manifest))
	for name, source := range manifest {
		if err := validateToolName(name); err != nil {
			return nil, nil, err
		}
		if source.Call == nil {
			return nil, nil, fmt.Errorf("tool has no Host implementation: %s", name)
		}
		if len(source.Description) > maxToolDescriptionBytes {
			return nil, nil, fmt.Errorf("tool description exceeds %d bytes: %s", maxToolDescriptionBytes, name)
		}
		spec := source
		if spec.InputSchema != nil {
			if len(spec.InputSchema) > maxToolSchemaBytes {
				return nil, nil, fmt.Errorf("tool input schema exceeds %d bytes: %s", maxToolSchemaBytes, name)
			}
			var object map[string]any
			if err := json.Unmarshal(spec.InputSchema, &object); err != nil || object == nil {
				return nil, nil, fmt.Errorf("tool input schema must be a JSON object: %s", name)
			}
			spec.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
		}
		normalized[name] = spec
		var schema any
		if spec.InputSchema != nil {
			if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
				return nil, nil, fmt.Errorf("decode tool input schema %s: %w", name, err)
			}
		}
		guest = append(guest, guestToolSpec{
			Name:           name,
			Description:    spec.Description,
			InputSchema:    schema,
			Annotations:    spec.Annotations,
			AllowEarlyRead: spec.AllowEarlyRead,
			InjectGlobal:   injectableToolName(name),
		})
	}
	sort.Slice(guest, func(i, j int) bool { return guest[i].Name < guest[j].Name })
	return normalized, guest, nil
}

func validateToolName(name string) error {
	if name == "" || len(name) > maxToolNameBytes || !canonicalToolName.MatchString(name) {
		return fmt.Errorf("invalid canonical tool name: %s", name)
	}
	return nil
}

func injectableToolName(name string) bool {
	return pythonIdentifier.MatchString(name) && !pythonKeywords[name] && name != "inputs" && name != "__name__" && name != "__builtins__" && !strings.HasPrefix(name, "_pysolate")
}
