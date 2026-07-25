package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalidDataset = errors.New("invalid evaluation dataset")

var requiredFamilies = []string{"simple_read", "fanout_join_filter", "data_dependent_branch", "partial_timeout_retry", "exact_reversible_abort", "compensatable_abort", "irreversible_staging", "dynamic_catalog_policy", "schema_projection", "adversarial_authority"}

type Manifest struct {
	SchemaVersion        string          `json:"schema_version"`
	DatasetID            string          `json:"dataset_id"`
	Revision             uint64          `json:"revision"`
	ScenarioSchema       string          `json:"scenario_schema"`
	ScenarioSchemaPath   string          `json:"scenario_schema_path"`
	ScenarioSchemaDigest string          `json:"scenario_schema_digest"`
	ScenarioCount        int             `json:"scenario_count"`
	Scenarios            []ManifestEntry `json:"scenarios"`
}

type ManifestEntry struct {
	ScenarioID string `json:"scenario_id"`
	Split      string `json:"split"`
	Family     string `json:"family"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
}

type Scenario struct {
	SchemaVersion string         `json:"schema_version"`
	ScenarioID    string         `json:"scenario_id"`
	Revision      uint64         `json:"revision"`
	Family        string         `json:"family"`
	Task          string         `json:"task"`
	Inputs        map[string]any `json:"inputs"`
	OutputSchema  map[string]any `json:"output_schema"`
	Fixture       struct {
		SetupID            string  `json:"setup_id"`
		Seed               uint64  `json:"seed"`
		InitialStateDigest string  `json:"initial_state_digest"`
		FaultPlanID        *string `json:"fault_plan_id"`
	} `json:"fixture"`
	CatalogFixtureID string   `json:"catalog_fixture_id"`
	PolicyFixtureID  string   `json:"policy_fixture_id"`
	Capabilities     []string `json:"required_capabilities"`
	Oracle           struct {
		ScorerID                    string   `json:"scorer_id"`
		ExpectedResultDigest        string   `json:"expected_result_digest"`
		ExpectedTerminalState       string   `json:"expected_terminal_state"`
		ExpectedBusinessStateDigest string   `json:"expected_business_state_digest"`
		SafetyAssertions            []string `json:"safety_assertions"`
	} `json:"oracle"`
	Tags []string `json:"tags"`
}

type Dataset struct {
	Root           string
	Manifest       Manifest
	ManifestDigest string
	Scenarios      map[string]Scenario
	Entries        map[string]ManifestEntry
}

type ModelView struct {
	ScenarioID   string         `json:"scenario_id"`
	Task         string         `json:"task"`
	Inputs       map[string]any `json:"inputs"`
	OutputSchema map[string]any `json:"output_schema"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readContainedRegular(root, path string) ([]byte, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, ErrInvalidDataset
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalidDataset
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, ErrInvalidDataset
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, ErrInvalidDataset
	}
	return os.ReadFile(resolvedPath)
}

func Load(root string) (*Dataset, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrInvalidDataset
	}
	manifestBytes, err := readContainedRegular(absolute, filepath.Join(absolute, "manifest.json"))
	if err != nil {
		return nil, ErrInvalidDataset
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || manifest.SchemaVersion != "evaluation-dataset-manifest/v1" || manifest.Revision == 0 || manifest.ScenarioCount != len(manifest.Scenarios) || len(manifest.Scenarios) == 0 {
		return nil, ErrInvalidDataset
	}
	schemaPath := filepath.Clean(filepath.Join(absolute, manifest.ScenarioSchemaPath))
	relativeSchema, relativeErr := filepath.Rel(absolute, schemaPath)
	if manifest.ScenarioSchemaPath == "" || filepath.IsAbs(manifest.ScenarioSchemaPath) || relativeErr != nil || relativeSchema == ".." || strings.HasPrefix(relativeSchema, ".."+string(filepath.Separator)) {
		return nil, ErrInvalidDataset
	}
	schemaBytes, err := readContainedRegular(absolute, schemaPath)
	if err != nil || digest(schemaBytes) != manifest.ScenarioSchemaDigest {
		return nil, ErrInvalidDataset
	}
	var document any
	if json.Unmarshal(schemaBytes, &document) != nil {
		return nil, ErrInvalidDataset
	}
	compiler := jsonschema.NewCompiler()
	if compiler.AddResource(manifest.ScenarioSchema, document) != nil {
		return nil, ErrInvalidDataset
	}
	compiled, err := compiler.Compile(manifest.ScenarioSchema)
	if err != nil {
		return nil, ErrInvalidDataset
	}
	result := &Dataset{Root: absolute, Manifest: manifest, ManifestDigest: digest(manifestBytes), Scenarios: make(map[string]Scenario, len(manifest.Scenarios)), Entries: make(map[string]ManifestEntry, len(manifest.Scenarios))}
	families := map[string]map[string]bool{"dev": {}, "evaluation": {}}
	for _, entry := range manifest.Scenarios {
		if entry.Split != "dev" && entry.Split != "evaluation" || entry.ScenarioID == "" || entry.Path == "" || filepath.IsAbs(entry.Path) {
			return nil, ErrInvalidDataset
		}
		path := filepath.Clean(filepath.Join(absolute, entry.Path))
		relative, err := filepath.Rel(absolute, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, ErrInvalidDataset
		}
		data, err := readContainedRegular(absolute, path)
		if err != nil || digest(data) != entry.SHA256 {
			return nil, ErrInvalidDataset
		}
		var raw any
		if json.Unmarshal(data, &raw) != nil || compiled.Validate(raw) != nil {
			return nil, ErrInvalidDataset
		}
		var scenario Scenario
		if json.Unmarshal(data, &scenario) != nil || scenario.ScenarioID != entry.ScenarioID || scenario.Family != entry.Family {
			return nil, ErrInvalidDataset
		}
		if _, exists := result.Scenarios[scenario.ScenarioID]; exists {
			return nil, ErrInvalidDataset
		}
		result.Scenarios[scenario.ScenarioID], result.Entries[scenario.ScenarioID] = scenario, entry
		families[entry.Split][entry.Family] = true
	}
	for _, split := range []string{"dev", "evaluation"} {
		for _, family := range requiredFamilies {
			if !families[split][family] {
				return nil, fmt.Errorf("%w: split %s missing family %s", ErrInvalidDataset, split, family)
			}
		}
	}
	return result, nil
}

func (data *Dataset) IDs(split string) []string {
	ids := make([]string, 0)
	for id, entry := range data.Entries {
		if entry.Split == split {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (data *Dataset) ModelView(id string) (ModelView, error) {
	scenario, exists := data.Scenarios[id]
	if !exists {
		return ModelView{}, ErrInvalidDataset
	}
	return ModelView{ScenarioID: scenario.ScenarioID, Task: scenario.Task, Inputs: scenario.Inputs, OutputSchema: scenario.OutputSchema}, nil
}

func CanonicalDigest(value any) (string, int, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", 0, err
	}
	return digest(encoded), len(encoded), nil
}
