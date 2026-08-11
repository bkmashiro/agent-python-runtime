package placement

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrCorpus = errors.New("invalid placement corpus")

const (
	manifestSchema = "placement-corpus-manifest/v1"
	taskSchema     = "placement-task/v1"
	maxManifest    = 1 << 20
	maxTask        = 2 << 20
)

type Corpus struct {
	Manifest Manifest
	Tasks    []Task
}

type Manifest struct {
	SchemaVersion       string         `json:"schema_version"`
	DatasetID           string         `json:"dataset_id"`
	Status              string         `json:"status"`
	SelectionPolicy     string         `json:"selection_policy"`
	SelectionSeed       string         `json:"selection_seed"`
	CandidatePool       string         `json:"candidate_pool"`
	CandidatePoolDigest string         `json:"candidate_pool_digest"`
	Sources             []Source       `json:"sources"`
	Tasks               []ManifestTask `json:"tasks"`
}

type Source struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision"`
	License    string `json:"license"`
	Evidence   string `json:"evidence"`
}

type ManifestTask struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Split    string `json:"split"`
	Stratum  string `json:"stratum"`
	SourceID string `json:"source_id"`
}

type Task struct {
	SchemaVersion string                  `json:"schema_version"`
	ID            string                  `json:"id"`
	Split         string                  `json:"split"`
	Stratum       string                  `json:"stratum"`
	Source        TaskSource              `json:"source"`
	ModelVisible  bool                    `json:"model_visible"`
	Request       string                  `json:"request"`
	Environment   json.RawMessage         `json:"environment"`
	Capabilities  json.RawMessage         `json:"capabilities"`
	Admission     map[string]ArmAdmission `json:"admission"`
	Oracle        TaskOracle              `json:"oracle"`
	Limits        TaskLimits              `json:"limits"`
}

type TaskSource struct {
	SourceID   string `json:"source_id"`
	RecordID   string `json:"record_id"`
	RecordHash string `json:"record_sha256"`
	Adaptation string `json:"adaptation"`
}

type ArmAdmission struct {
	Status  string `json:"status"`
	Profile string `json:"profile,omitempty"`
	Backend string `json:"backend,omitempty"`
	Reason  string `json:"reason"`
}

type TaskOracle struct {
	FinalState     json.RawMessage `json:"final_state"`
	EffectContract json.RawMessage `json:"effect_contract"`
}

type TaskLimits struct {
	MaxProviderCalls  uint32 `json:"max_provider_calls"`
	MaxToolCalls      uint32 `json:"max_tool_calls"`
	MaxTotalTokens    uint64 `json:"max_total_tokens"`
	TimeoutMillis     uint64 `json:"timeout_millis"`
	MaxOutputBytes    uint64 `json:"max_output_bytes"`
	MaxWorkspaceBytes uint64 `json:"max_workspace_bytes"`
}

func Load(root string) (*Corpus, error) {
	manifestData, err := readContained(root, "manifest.json", maxManifest)
	if err != nil {
		return nil, ErrCorpus
	}
	var manifest Manifest
	if decodeStrict(manifestData, &manifest) != nil || validateManifest(root, manifest) != nil {
		return nil, ErrCorpus
	}
	seen := map[string]bool{}
	tasks := make([]Task, 0, len(manifest.Tasks))
	for _, entry := range manifest.Tasks {
		if seen[entry.ID] {
			return nil, ErrCorpus
		}
		seen[entry.ID] = true
		data, err := readContained(root, entry.Path, maxTask)
		if err != nil || digest(data) != entry.SHA256 {
			return nil, ErrCorpus
		}
		var task Task
		if decodeStrict(data, &task) != nil || validateTask(task, entry) != nil {
			return nil, ErrCorpus
		}
		tasks = append(tasks, task)
	}
	return &Corpus{Manifest: manifest, Tasks: tasks}, nil
}

func validateManifest(root string, manifest Manifest) error {
	if manifest.SchemaVersion != manifestSchema || manifest.DatasetID != "pysolate-placement-v1" ||
		manifest.Status != "frozen_pre_model" || !validDigest(manifest.SelectionSeed) ||
		manifest.SelectionPolicy == "" || manifest.CandidatePool != "candidate-pool.json" ||
		!validDigest(manifest.CandidatePoolDigest) || len(manifest.Sources) != 4 || len(manifest.Tasks) != 60 {
		return ErrCorpus
	}
	pool, err := readContained(root, manifest.CandidatePool, maxManifest)
	if err != nil || digest(pool) != manifest.CandidatePoolDigest {
		return ErrCorpus
	}
	sourceIDs := map[string]bool{}
	for _, source := range manifest.Sources {
		if source.ID == "" || sourceIDs[source.ID] || source.Kind == "" || source.Revision == "" || source.License == "" || source.Evidence == "" {
			return ErrCorpus
		}
		sourceIDs[source.ID] = true
	}
	paths := make([]string, 0, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		if task.ID == "" || !safeRelative(task.Path) || !validDigest(task.SHA256) ||
			(task.Split != "development" && task.Split != "decision") || !validStratum(task.Stratum) || !sourceIDs[task.SourceID] {
			return ErrCorpus
		}
		paths = append(paths, task.Path)
	}
	if !sort.StringsAreSorted(paths) {
		return ErrCorpus
	}
	return nil
}

func validateTask(task Task, entry ManifestTask) error {
	if task.SchemaVersion != taskSchema || task.ID != entry.ID || task.Split != entry.Split || task.Stratum != entry.Stratum ||
		task.Source.SourceID != entry.SourceID || task.Source.RecordID == "" || !validDigest(task.Source.RecordHash) || task.Source.Adaptation == "" ||
		strings.TrimSpace(task.Request) == "" || len(task.Request) > 16*1024 || len(bytes.TrimSpace(task.Environment)) == 0 ||
		len(bytes.TrimSpace(task.Capabilities)) == 0 || len(bytes.TrimSpace(task.Oracle.FinalState)) == 0 ||
		len(bytes.TrimSpace(task.Oracle.EffectContract)) == 0 || task.Limits.MaxProviderCalls == 0 ||
		task.Limits.MaxToolCalls == 0 || task.Limits.MaxTotalTokens == 0 || task.Limits.TimeoutMillis == 0 ||
		task.Limits.MaxOutputBytes == 0 || task.Limits.MaxWorkspaceBytes == 0 || len(task.Admission) != 3 {
		return ErrCorpus
	}
	if task.Split == "decision" && task.ModelVisible {
		return ErrCorpus
	}
	for _, arm := range []string{"direct", "pysolate", "computer"} {
		admission, ok := task.Admission[arm]
		if !ok || (admission.Status != "admitted" && admission.Status != "rejected") || admission.Reason == "" {
			return ErrCorpus
		}
		if arm == "pysolate" && admission.Status == "admitted" && admission.Profile == "" {
			return ErrCorpus
		}
		if arm == "computer" && admission.Status == "admitted" && admission.Backend == "" {
			return ErrCorpus
		}
	}
	var environment, capabilities, finalState, effects any
	for raw, target := range map[string]*any{
		string(task.Environment):           &environment,
		string(task.Capabilities):          &capabilities,
		string(task.Oracle.FinalState):     &finalState,
		string(task.Oracle.EffectContract): &effects,
	} {
		if json.Unmarshal([]byte(raw), target) != nil {
			return ErrCorpus
		}
	}
	return nil
}

func validStratum(value string) bool {
	switch value {
	case "direct_favored", "pysolate_favored", "mixed_capability", "computer_favored", "boundary":
		return true
	default:
		return false
	}
}

func readContained(root, relative string, limit int64) ([]byte, error) {
	if !safeRelative(relative) {
		return nil, ErrCorpus
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(cleanRoot, filepath.FromSlash(relative))
	if !strings.HasPrefix(path, cleanRoot+string(filepath.Separator)) {
		return nil, ErrCorpus
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, ErrCorpus
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) > limit {
		return nil, ErrCorpus
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
		return ErrCorpus
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
