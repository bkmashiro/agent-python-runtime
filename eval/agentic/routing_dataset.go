package agentic

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrRoutingDataset = errors.New("invalid routing diagnostic dataset")

const (
	routingManifestVersion = "external-agentic-routing-diagnostic-manifest/v1"
	routingPackVersion     = "routing-diagnostic-pack/v1"
	routingPlanVersion     = "routing-diagnostic-development-plan/v1"
	maxRoutingPlanSize     = 1 << 20
)

var routingTaskVersion = taskVersion

const (
	routingBenchmark  = "RoutingDiagnostic"
	routingSourceVers = "v1"
	routingDatasetID  = "agentic-routing-diagnostic-v1"
	routingSelection  = "balanced"
)

var routingTaskIDPattern = regexp.MustCompile(`^rd-\d{3}$`)

// RoutingManifest is an independent manifest for the routing diagnostic dataset.
type RoutingManifest struct {
	Version         string                  `json:"version"`
	DatasetID       string                  `json:"dataset_id"`
	SelectionPolicy string                  `json:"selection_policy"`
	Sources         []RoutingManifestSource `json:"sources"`
	Pack            string                  `json:"pack"`
	PackDigest      string                  `json:"pack_digest"`
	Tasks           []RoutingManifestTask   `json:"tasks"`
}

// RoutingManifestSource describes non-BFCL source metadata.
type RoutingManifestSource struct {
	Benchmark   string              `json:"benchmark"`
	Version     string              `json:"version"`
	Repository  string              `json:"repository"`
	Revision    string              `json:"revision"`
	License     string              `json:"license"`
	LicenseURL  string              `json:"license_url"`
	SourceFiles []RoutingSourceFile `json:"source_files"`
}

type RoutingSourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type RoutingSourceRecord struct {
	Version        string          `json:"version"`
	ID             string          `json:"id"`
	Request        Interaction     `json:"request"`
	Environment    Environment     `json:"environment"`
	Tools          []Tool          `json:"tools"`
	Oracle         json.RawMessage `json:"oracle"`
	Safety         Safety          `json:"safety"`
	RecordSHA256   string          `json:"-"`
	SourceRevision string          `json:"-"`
}

// RoutingManifestTask holds byte-bound metadata and private archetypes.
type RoutingManifestTask struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Split     string `json:"split"`
	Track     string `json:"track"`
	Archetype string `json:"archetype"`
}

// RoutingPack mirrors the full task manifest and keeps private diagnostics metadata.
type RoutingPack struct {
	Version   string            `json:"version"`
	DatasetID string            `json:"dataset_id"`
	Tasks     []RoutingPackTask `json:"tasks"`
}

type RoutingPackTask struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Archetype string `json:"archetype"`
}

// RoutingEvaluationPlan is the sealed binding for the routing diagnostic dataset.
type RoutingEvaluationPlan struct {
	SchemaVersion         string   `json:"schema_version"`
	Status                string   `json:"status"`
	DatasetID             string   `json:"dataset_id"`
	SelectionPolicy       string   `json:"selection_policy"`
	DatasetManifestDigest string   `json:"dataset_manifest_digest"`
	TaskPackDigest        string   `json:"task_pack_digest"`
	TaskIDs               []string `json:"task_ids"`
}

type RoutingDataset struct {
	Root       string
	Manifest   RoutingManifest
	Pack       RoutingPack
	Plan       RoutingEvaluationPlan
	Tasks      []Task
	Archetypes map[string]string // taskID -> archetype
}

// LoadRoutingDataset loads the routing diagnostic data root and validates all digest bindings.
func LoadRoutingDataset(root string) (*RoutingDataset, error) {
	manifest, manifestBytes, err := loadRoutingManifest(root)
	if err != nil {
		return nil, err
	}
	pack, packBytes, err := loadRoutingPack(root, manifest.Pack)
	if err != nil {
		return nil, err
	}
	if !validDigest(manifest.PackDigest) || manifest.PackDigest != digest(packBytes) {
		return nil, ErrRoutingDataset
	}
	if manifest.Version != routingManifestVersion || manifest.DatasetID != routingDatasetID || manifest.SelectionPolicy != routingSelection {
		return nil, ErrRoutingDataset
	}
	sourceRecords, err := loadRoutingSources(root, manifest.Sources)
	if err != nil {
		return nil, ErrRoutingDataset
	}
	if len(manifest.Tasks) != 6 || len(manifest.Tasks) != len(pack.Tasks) {
		return nil, ErrRoutingDataset
	}

	manifestTaskMap := make(map[string]RoutingManifestTask, len(manifest.Tasks))
	seenIDs := make(map[string]struct{}, len(manifest.Tasks))
	seenPaths := make(map[string]struct{}, len(manifest.Tasks))
	counts := map[string]int{}
	for _, entry := range manifest.Tasks {
		if entry.ID == "" || entry.Path == "" || entry.Split != "dev" || entry.Track != "stateful_local_tools" {
			return nil, ErrRoutingDataset
		}
		if !routingTaskIDPattern.MatchString(entry.ID) {
			return nil, ErrRoutingDataset
		}
		if !strings.HasPrefix(entry.ID, "rd-00") {
			return nil, ErrRoutingDataset
		}
		if !allowedRoutingArchetype(entry.Archetype) {
			return nil, ErrRoutingDataset
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, ErrRoutingDataset
		}
		if _, exists := seenPaths[entry.Path]; exists {
			return nil, ErrRoutingDataset
		}
		if !safeRelative(entry.Path) || !safeRelative(manifest.Pack) {
			return nil, ErrRoutingDataset
		}
		if !validDigest(entry.SHA256) {
			return nil, ErrRoutingDataset
		}
		seenIDs[entry.ID] = struct{}{}
		seenPaths[entry.Path] = struct{}{}
		manifestTaskMap[entry.ID] = entry
		counts[entry.Archetype]++
	}
	if counts["direct_favored"] != 2 || counts["python_favored"] != 2 || counts["boundary"] != 2 {
		return nil, ErrRoutingDataset
	}
	if !isSortedByID(manifest.Tasks) {
		return nil, ErrRoutingDataset
	}

	packTaskMap := make(map[string]RoutingPackTask, len(pack.Tasks))
	for _, entry := range pack.Tasks {
		if entry.ID == "" || entry.Path == "" || !safeRelative(entry.Path) || !validDigest(entry.SHA256) {
			return nil, ErrRoutingDataset
		}
		if !allowedRoutingArchetype(entry.Archetype) {
			return nil, ErrRoutingDataset
		}
		manTask, ok := manifestTaskMap[entry.ID]
		if !ok || manTask.Path != entry.Path || manTask.Archetype != entry.Archetype || manTask.SHA256 != entry.SHA256 {
			return nil, ErrRoutingDataset
		}
		if _, exists := packTaskMap[entry.ID]; exists {
			return nil, ErrRoutingDataset
		}
		packTaskMap[entry.ID] = entry
	}
	if len(packTaskMap) != len(manifestTaskMap) || pack.Version != routingPackVersion || pack.DatasetID != routingDatasetID {
		return nil, ErrRoutingDataset
	}

	plan, planBytes, err := loadRoutingPlan(root)
	if err != nil {
		return nil, err
	}
	if !validDigest(plan.DatasetManifestDigest) || !validDigest(plan.TaskPackDigest) ||
		plan.SchemaVersion != routingPlanVersion || plan.Status != "frozen_development" || plan.DatasetID != routingDatasetID || plan.SelectionPolicy != routingSelection ||
		plan.DatasetManifestDigest != digest(manifestBytes) || plan.TaskPackDigest != manifest.PackDigest {
		return nil, ErrRoutingDataset
	}
	if len(plan.TaskIDs) != len(manifest.Tasks) {
		return nil, ErrRoutingDataset
	}
	if len(planBytes) > maxRoutingPlanSize {
		return nil, ErrRoutingDataset
	}
	for i, id := range plan.TaskIDs {
		if id != manifest.Tasks[i].ID {
			return nil, ErrRoutingDataset
		}
		if _, ok := manifestTaskMap[id]; !ok {
			return nil, ErrRoutingDataset
		}
	}

	tasks := make([]Task, 0, len(manifest.Tasks))
	taskArchetypes := make(map[string]string, len(manifest.Tasks))
	for _, entry := range manifest.Tasks {
		data, err := readContainedRegular(root, entry.Path, maxTaskSize)
		if err != nil || digest(data) != entry.SHA256 {
			return nil, ErrRoutingDataset
		}
		var task Task
		if decodeStrict(data, &task) != nil {
			return nil, ErrRoutingDataset
		}
		record, ok := sourceRecords[entry.ID]
		if !ok {
			return nil, ErrRoutingDataset
		}
		if err := validateRoutingTask(task, entry, record); err != nil {
			return nil, err
		}
		if err := validateRoutingToolOracle(task); err != nil {
			return nil, err
		}
		if _, err := NewToolRuntime(task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
		taskArchetypes[task.ID] = entry.Archetype
	}

	return &RoutingDataset{
		Root:       root,
		Manifest:   *manifest,
		Pack:       *pack,
		Plan:       *plan,
		Tasks:      tasks,
		Archetypes: taskArchetypes,
	}, nil
}

func loadRoutingManifest(root string) (*RoutingManifest, []byte, error) {
	data, err := readContainedRegular(root, "manifest.json", maxManifestSize)
	if err != nil {
		return nil, nil, ErrRoutingDataset
	}
	var manifest RoutingManifest
	if decodeStrict(data, &manifest) != nil {
		return nil, nil, ErrRoutingDataset
	}
	return &manifest, append([]byte(nil), data...), nil
}

func loadRoutingPack(root, relative string) (*RoutingPack, []byte, error) {
	if !safeRelative(relative) {
		return nil, nil, ErrRoutingDataset
	}
	data, err := readContainedRegular(root, relative, maxManifestSize)
	if err != nil {
		return nil, nil, ErrRoutingDataset
	}
	var pack RoutingPack
	if decodeStrict(data, &pack) != nil {
		return nil, nil, ErrRoutingDataset
	}
	return &pack, append([]byte(nil), data...), nil
}

func loadRoutingPlan(root string) (*RoutingEvaluationPlan, []byte, error) {
	data, err := readContainedRegular(root, "development-plan.json", maxRoutingPlanSize)
	if err != nil {
		return nil, nil, ErrRoutingDataset
	}
	var plan RoutingEvaluationPlan
	if decodeStrict(data, &plan) != nil {
		return nil, nil, ErrRoutingDataset
	}
	return &plan, append([]byte(nil), data...), nil
}

func loadRoutingSources(root string, sources []RoutingManifestSource) (map[string]RoutingSourceRecord, error) {
	if len(sources) != 1 {
		return nil, ErrRoutingDataset
	}
	source := sources[0]
	if source.Benchmark != routingBenchmark || source.Version != routingSourceVers || !validRevisionHex(source.Revision) ||
		source.License != "Apache-2.0" || source.LicenseURL != "https://www.apache.org/licenses/LICENSE-2.0" ||
		source.Repository != "https://github.com/bkmashiro/agent-python-runtime" || len(source.SourceFiles) != 6 {
		return nil, ErrRoutingDataset
	}
	records := make(map[string]RoutingSourceRecord, len(source.SourceFiles))
	for _, file := range source.SourceFiles {
		if !safeRelative(file.Path) || !validDigest(file.SHA256) || !strings.HasPrefix(file.Path, "sources/rd-") || !strings.HasSuffix(file.Path, ".json") {
			return nil, ErrRoutingDataset
		}
		data, err := readContainedRegular(root, file.Path, maxTaskSize)
		if err != nil || digest(data) != file.SHA256 {
			return nil, ErrRoutingDataset
		}
		var record RoutingSourceRecord
		if decodeStrict(data, &record) != nil || record.Version != "routing-diagnostic-source-record/v1" ||
			!routingTaskIDPattern.MatchString(record.ID) || file.Path != "sources/"+record.ID+".json" {
			return nil, ErrRoutingDataset
		}
		if _, exists := records[record.ID]; exists {
			return nil, ErrRoutingDataset
		}
		record.RecordSHA256 = file.SHA256
		record.SourceRevision = source.Revision
		records[record.ID] = record
	}
	return records, nil
}

func validateRoutingTask(task Task, entry RoutingManifestTask, record RoutingSourceRecord) error {
	if task.Version != routingTaskVersion || task.ID != entry.ID || task.Split != entry.Split || task.Track != entry.Track ||
		task.Source.Benchmark != routingBenchmark || task.Source.Version != routingSourceVers || !validRevisionHex(task.Source.Revision) ||
		(task.Interaction.Mode != "single_turn" && task.Interaction.Mode != "multi_turn") || len(task.Interaction.Turns) != 1 || task.Track != "stateful_local_tools" ||
		(task.Environment.Kind != "local_stateful") || !task.Safety.NetworkDisabled || task.Safety.RealWorldEffects || task.Safety.Credentials != "none" {
		return ErrRoutingDataset
	}
	if task.Source.SourceID != task.ID || task.Source.RecordSHA256 != record.RecordSHA256 ||
		task.Source.Revision != record.SourceRevision ||
		task.Source.Adaptation != "Adapted from the digest-bound local routing source record into external-agentic-task/v1." ||
		task.Source.License != "Apache-2.0" || !routingRecordMatchesTask(record, task) {
		return ErrRoutingDataset
	}
	if !validDigest(task.Source.RecordSHA256) {
		return ErrRoutingDataset
	}
	if len(task.Tools) == 0 {
		return ErrRoutingDataset
	}
	return nil
}

func routingRecordMatchesTask(record RoutingSourceRecord, task Task) bool {
	recordProjection := struct {
		Request     Interaction     `json:"request"`
		Environment Environment     `json:"environment"`
		Tools       []Tool          `json:"tools"`
		Oracle      json.RawMessage `json:"oracle"`
		Safety      Safety          `json:"safety"`
	}{record.Request, record.Environment, record.Tools, record.Oracle, record.Safety}
	taskProjection := struct {
		Request     Interaction     `json:"request"`
		Environment Environment     `json:"environment"`
		Tools       []Tool          `json:"tools"`
		Oracle      json.RawMessage `json:"oracle"`
		Safety      Safety          `json:"safety"`
	}{task.Interaction, task.Environment, task.Tools, task.Oracle, task.Safety}
	a, errA := json.Marshal(recordProjection)
	b, errB := json.Marshal(taskProjection)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func validateRoutingToolOracle(task Task) error {
	if len(bytes.TrimSpace(task.Oracle)) == 0 {
		return ErrRoutingDataset
	}
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" || len(oracle.Turns) != len(task.Interaction.Turns) {
		return ErrRoutingDataset
	}
	if len(oracle.Turns) == 0 || len(oracle.Turns) > 64 {
		return ErrRoutingDataset
	}

	toolSchemas := make(map[string]bool, len(task.Tools))
	for _, tool := range task.Tools {
		if tool.Name == "" || tool.Parameters == nil || len(bytes.TrimSpace(tool.Parameters)) == 0 {
			return ErrRoutingDataset
		}
		compiledInput, err := compileSchema(tool.Parameters)
		if err != nil {
			return err
		}
		if err := validateToolArguments(tool.Name, &oracle, compiledInput); err != nil {
			return err
		}
		if len(bytes.TrimSpace(tool.Response)) != 0 {
			if _, err := compileSchema(tool.Response); err != nil {
				return err
			}
		}
		toolSchemas[tool.Name] = true
	}
	for _, turn := range oracle.Turns {
		if len(turn) == 0 || len(turn) > 128 {
			return ErrRoutingDataset
		}
		for _, call := range turn {
			if call.Name == "" || call.Arguments == nil {
				return ErrRoutingDataset
			}
			if !toolSchemas[call.Name] {
				return ErrRoutingDataset
			}
		}
	}
	return nil
}

func validateToolArguments(toolName string, oracle *StatefulOracle, schema *jsonschema.Schema) error {
	for _, turn := range oracle.Turns {
		for _, call := range turn {
			if call.Name != toolName {
				continue
			}
			var arguments map[string]any
			if len(call.Arguments) == 0 || len(call.Arguments) > maxArgumentsBytes || decodeUseNumber(call.Arguments, &arguments) != nil ||
				arguments == nil || schema.Validate(arguments) != nil {
				return ErrRoutingDataset
			}
		}
	}
	return nil
}

func isSortedByID(tasks []RoutingManifestTask) bool {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			return false
		}
	}
	return true
}

func allowedRoutingArchetype(name string) bool {
	switch name {
	case "direct_favored", "python_favored", "boundary":
		return true
	default:
		return false
	}
}

func validRevisionHex(revision string) bool {
	if len(revision) != 40 {
		return false
	}
	_, err := hex.DecodeString(revision)
	return err == nil
}
