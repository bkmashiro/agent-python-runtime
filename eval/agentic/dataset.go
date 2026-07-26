package agentic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrDataset = errors.New("invalid external agentic dataset")

const (
	manifestVersion = "external-agentic-dataset-manifest/v1"
	taskVersion     = "external-agentic-task/v1"
	maxManifestSize = 1 << 20
	maxTaskSize     = 2 << 20
)

type Dataset struct {
	Manifest Manifest
	Tasks    []Task
}

type Manifest struct {
	Version         string           `json:"version"`
	DatasetID       string           `json:"dataset_id"`
	SelectionPolicy string           `json:"selection_policy"`
	Sources         []ManifestSource `json:"sources"`
	Tasks           []ManifestTask   `json:"tasks"`
}

type ManifestSource struct {
	Benchmark   string       `json:"benchmark"`
	Version     string       `json:"version"`
	Repository  string       `json:"repository"`
	Revision    string       `json:"revision"`
	License     string       `json:"license"`
	LicenseURL  string       `json:"license_url"`
	SourceFiles []SourceFile `json:"source_files"`
}

type SourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ManifestTask struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Split  string `json:"split"`
	Track  string `json:"track"`
}

type Task struct {
	Version     string          `json:"version"`
	ID          string          `json:"id"`
	Split       string          `json:"split"`
	Track       string          `json:"track"`
	Source      TaskSource      `json:"source"`
	Interaction Interaction     `json:"interaction"`
	Tools       []Tool          `json:"tools"`
	Environment Environment     `json:"environment"`
	Oracle      json.RawMessage `json:"oracle"`
	Safety      Safety          `json:"safety"`
}

type TaskSource struct {
	Benchmark    string `json:"benchmark"`
	Version      string `json:"version"`
	Revision     string `json:"revision"`
	SourceID     string `json:"source_id"`
	RecordSHA256 string `json:"record_sha256"`
	License      string `json:"license"`
	Adaptation   string `json:"adaptation"`
}

type Interaction struct {
	Mode  string            `json:"mode"`
	Turns []json.RawMessage `json:"turns"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Response    json.RawMessage `json:"response,omitempty"`
}

type Environment struct {
	Kind         string          `json:"kind"`
	InitialState json.RawMessage `json:"initial_state"`
}

type Safety struct {
	NetworkDisabled  bool   `json:"network_disabled"`
	RealWorldEffects bool   `json:"real_world_effects"`
	Credentials      string `json:"credentials"`
}

type StatefulCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type StatefulOracle struct {
	Kind  string           `json:"kind"`
	Turns [][]StatefulCall `json:"turns"`
}

func Load(root string) (*Dataset, error) {
	manifest, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(manifest.Tasks))
	tasks := make([]Task, 0, len(manifest.Tasks))
	previousPath := ""
	for _, entry := range manifest.Tasks {
		if entry.ID == "" || (entry.Split != "dev" && entry.Split != "evaluation") ||
			(entry.Track != "stateless_function_calling" && entry.Track != "stateful_local_tools") {
			return nil, ErrDataset
		}
		if _, ok := seen[entry.ID]; ok || (previousPath != "" && entry.Path <= previousPath) {
			return nil, ErrDataset
		}
		seen[entry.ID] = struct{}{}
		previousPath = entry.Path
		data, err := readContainedRegular(root, entry.Path, maxTaskSize)
		if err != nil || digest(data) != entry.SHA256 {
			return nil, ErrDataset
		}
		var task Task
		if err := decodeStrict(data, &task); err != nil || validateTask(task, entry, manifest.Sources) != nil {
			return nil, ErrDataset
		}
		tasks = append(tasks, task)
	}
	return &Dataset{Manifest: *manifest, Tasks: tasks}, nil
}

func LoadManifest(root string) (*Manifest, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrDataset
	}
	data, err := readContainedRegular(root, "manifest.json", maxManifestSize)
	if err != nil {
		return nil, ErrDataset
	}
	var manifest Manifest
	if err := decodeStrict(data, &manifest); err != nil || validateManifest(manifest) != nil {
		return nil, ErrDataset
	}
	return &manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != manifestVersion || manifest.DatasetID == "" || manifest.SelectionPolicy == "" ||
		len(manifest.Sources) != 1 || len(manifest.Tasks) == 0 {
		return ErrDataset
	}
	source := manifest.Sources[0]
	if source.Benchmark != "BFCL" || source.Version != "v4" || source.Repository != "https://github.com/ShishirPatil/gorilla" ||
		len(source.Revision) != 40 || source.License != "Apache-2.0" || !strings.HasPrefix(source.LicenseURL, "https://") || len(source.SourceFiles) == 0 {
		return ErrDataset
	}
	for _, file := range source.SourceFiles {
		if !safeRelative(file.Path) || !validDigest(file.SHA256) {
			return ErrDataset
		}
	}
	return nil
}

func validateTask(task Task, entry ManifestTask, sources []ManifestSource) error {
	if task.Version != taskVersion || task.ID != entry.ID || task.Split != entry.Split || task.Track != entry.Track ||
		task.Source.Benchmark != "BFCL" || task.Source.Version != "v4" || task.Source.Revision != sources[0].Revision ||
		task.Source.SourceID == "" || !validDigest(task.Source.RecordSHA256) || task.Source.License != "Apache-2.0" || task.Source.Adaptation == "" ||
		(task.Interaction.Mode != "single_turn" && task.Interaction.Mode != "multi_turn") || len(task.Interaction.Turns) == 0 || len(task.Tools) == 0 ||
		(task.Environment.Kind != "stateless" && task.Environment.Kind != "local_stateful") || len(bytes.TrimSpace(task.Oracle)) == 0 ||
		!task.Safety.NetworkDisabled || task.Safety.RealWorldEffects || task.Safety.Credentials != "none" {
		return ErrDataset
	}
	toolSchemas := make(map[string]*jsonschema.Schema, len(task.Tools))
	for _, tool := range task.Tools {
		if tool.Name == "" || len(bytes.TrimSpace(tool.Parameters)) == 0 {
			return ErrDataset
		}
		if _, duplicate := toolSchemas[tool.Name]; duplicate {
			return ErrDataset
		}
		var schema map[string]any
		compiled, err := compileSchema(tool.Parameters)
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema["type"] != "object" || err != nil {
			return ErrDataset
		}
		toolSchemas[tool.Name] = compiled
		if len(bytes.TrimSpace(tool.Response)) != 0 {
			if _, err := compileSchema(tool.Response); err != nil {
				return ErrDataset
			}
		}
	}
	return validateOracle(task, toolSchemas)
}

func validateOracle(task Task, toolSchemas map[string]*jsonschema.Schema) error {
	if task.Track == "stateful_local_tools" {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" ||
			len(oracle.Turns) != len(task.Interaction.Turns) || len(oracle.Turns) == 0 || len(oracle.Turns) > 64 {
			return ErrDataset
		}
		total := 0
		for _, turn := range oracle.Turns {
			if len(turn) == 0 || len(turn) > 128 {
				return ErrDataset
			}
			total += len(turn)
			if total > 128 {
				return ErrDataset
			}
			for _, call := range turn {
				schema := toolSchemas[call.Name]
				var arguments map[string]any
				if schema == nil || len(call.Arguments) == 0 || len(call.Arguments) > maxArgumentsBytes ||
					decodeUseNumber(call.Arguments, &arguments) != nil || arguments == nil || schema.Validate(arguments) != nil {
					return ErrDataset
				}
			}
		}
		return nil
	}
	var oracle expectedCallOracle
	if decodeStrict(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" || len(oracle.Turns) == 0 || len(oracle.Turns) > 128 {
		return ErrDataset
	}
	for _, expected := range oracle.Turns {
		if len(expected) != 1 {
			return ErrDataset
		}
		for name, arguments := range expected {
			if toolSchemas[name] == nil || len(arguments) > 128 {
				return ErrDataset
			}
			for _, options := range arguments {
				if len(options) == 0 || len(options) > 64 {
					return ErrDataset
				}
			}
		}
	}
	return nil
}

func (denyExternalSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

type denyExternalSchemaLoader struct{}

func readContainedRegular(root, relative string, limit int64) ([]byte, error) {
	if !safeRelative(relative) {
		return nil, ErrDataset
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrDataset
	}
	current := cleanRoot
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrDataset
		}
		if current != filepath.Join(cleanRoot, filepath.FromSlash(relative)) && !info.IsDir() {
			return nil, ErrDataset
		}
	}
	info, err := os.Lstat(current)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrDataset
	}
	data, err := os.ReadFile(current)
	if err != nil || int64(len(data)) > limit {
		return nil, ErrDataset
	}
	return data, nil
}

func safeRelative(path string) bool {
	return path != "" && filepath.IsLocal(filepath.FromSlash(path)) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) == path
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
