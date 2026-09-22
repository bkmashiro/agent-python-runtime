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

// Capability is one canonical Host tool discovered from a provider. Spec
// carries presentation metadata and the Host implementation; authority and
// durable recovery policy remain Host-owned decisions.
type Capability struct {
	Name string
	Spec ToolSpec
}

// ToolDefinition preserves the original provider API name.
type ToolDefinition = Capability

// ToolProvider discovers a bounded set of Host-owned tools before Runner
// construction. Providers retain their connections and credentials; only
// normalized metadata and Call closures enter the Manifest.
type ToolProvider interface {
	Tools(context.Context) ([]ToolDefinition, error)
}

// DiscoverCapabilities discovers, validates, copies and sorts provider output.
// Duplicate canonical names fail closed instead of being resolved by provider
// order. Discovery metadata never grants execution or recovery authority.
func DiscoverCapabilities(ctx context.Context, providers ...ToolProvider) ([]Capability, error) {
	discovered := make([]Capability, 0)
	seen := make(map[string]bool)
	for index, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("tool provider %d is nil", index)
		}
		definitions, err := provider.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover tool provider %d: %w", index, err)
		}
		for _, definition := range definitions {
			if seen[definition.Name] {
				return nil, fmt.Errorf("duplicate tool from providers: %s", definition.Name)
			}
			seen[definition.Name] = true
			discovered = append(discovered, definition)
		}
	}
	manifest, err := ManifestFromCapabilities(discovered)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(manifest))
	for name := range manifest {
		names = append(names, name)
	}
	sort.Strings(names)
	capabilities := make([]Capability, 0, len(names))
	for _, name := range names {
		capabilities = append(capabilities, Capability{Name: name, Spec: manifest[name]})
	}
	return capabilities, nil
}

// ManifestFromCapabilities validates a Host-approved catalog and returns the
// normalized execution manifest consumed by Runner.
func ManifestFromCapabilities(capabilities []Capability) (Manifest, error) {
	manifest := make(Manifest, len(capabilities))
	for _, capability := range capabilities {
		if _, exists := manifest[capability.Name]; exists {
			return nil, fmt.Errorf("duplicate capability: %s", capability.Name)
		}
		manifest[capability.Name] = capability.Spec
	}
	normalized, _, err := normalizeManifest(manifest)
	return normalized, err
}

// ManifestFromProviders discovers providers and builds an ordinary execution
// manifest. Use DiscoverCapabilities when a Host must first attach durable
// policy to each discovered capability.
func ManifestFromProviders(ctx context.Context, providers ...ToolProvider) (Manifest, error) {
	capabilities, err := DiscoverCapabilities(ctx, providers...)
	if err != nil {
		return nil, err
	}
	return ManifestFromCapabilities(capabilities)
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
	paths := make([]string, 0, len(manifest))
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
		if spec.PythonPath == "" {
			if !validPythonPath(name) {
				return nil, nil, fmt.Errorf("tool %s requires an explicit Python path", name)
			}
			spec.PythonPath = name
		} else if !validPythonPath(spec.PythonPath) {
			return nil, nil, fmt.Errorf("invalid Python tool path for %s: %s", name, spec.PythonPath)
		}
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
		guest = append(guest, guestToolSpec{Name: name, PythonPath: spec.PythonPath, AllowEarlyRead: spec.AllowEarlyRead})
		paths = append(paths, spec.PythonPath)
	}
	sort.Strings(paths)
	for index, pythonPath := range paths {
		if index > 0 && (pythonPath == paths[index-1] || strings.HasPrefix(pythonPath, paths[index-1]+".")) {
			return nil, nil, fmt.Errorf("Python tool path collision: %s", pythonPath)
		}
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

func validPythonPath(name string) bool {
	parts := strings.Split(name, ".")
	for index, part := range parts {
		if !pythonIdentifier.MatchString(part) || pythonKeywords[part] || (index == 0 && (part == "inputs" || part == "__name__" || part == "__builtins__" || strings.HasPrefix(part, "_pysolate"))) {
			return false
		}
	}
	return len(parts) > 0
}
