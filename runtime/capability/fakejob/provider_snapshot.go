package fakejob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"sort"
	"strconv"
	"strings"
)

const providerSnapshotVersion = 1

type ProviderSnapshot struct {
	SchemaVersion uint32                `json:"schema_version"`
	Digest        string                `json:"digest"`
	NextJob       uint64                `json:"next_job"`
	NextVersion   uint64                `json:"next_version"`
	Recipes       []Recipe              `json:"recipes"`
	Jobs          []ProviderJobSnapshot `json:"jobs"`
}

type ProviderJobSnapshot struct {
	Job         Job        `json:"job"`
	OperationID string     `json:"operation_id"`
	Logs        []LogLine  `json:"logs"`
	Artifacts   []Artifact `json:"artifacts"`
}

func (provider *Provider) ExportSnapshot() (ProviderSnapshot, error) {
	if provider == nil {
		return ProviderSnapshot{}, ErrJobDenied
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.exportSnapshotLocked()
}

func (provider *Provider) exportSnapshotLocked() (ProviderSnapshot, error) {
	result := ProviderSnapshot{SchemaVersion: providerSnapshotVersion, NextJob: provider.nextJob, NextVersion: provider.nextVersion, Recipes: make([]Recipe, 0, len(provider.recipes)), Jobs: make([]ProviderJobSnapshot, 0, len(provider.jobs))}
	for _, recipe := range provider.recipes {
		result.Recipes = append(result.Recipes, recipe)
	}
	sort.Slice(result.Recipes, func(i, j int) bool { return result.Recipes[i].Alias < result.Recipes[j].Alias })
	for _, state := range provider.jobs {
		result.Jobs = append(result.Jobs, ProviderJobSnapshot{Job: state.job, OperationID: state.operation, Logs: append([]LogLine(nil), state.logs...), Artifacts: append([]Artifact(nil), state.artifacts...)})
	}
	sort.Slice(result.Jobs, func(i, j int) bool { return result.Jobs[i].Job.ID < result.Jobs[j].Job.ID })
	if err := validateProviderSnapshot(result, false); err != nil {
		return ProviderSnapshot{}, err
	}
	result.Digest = providerSnapshotDigest(result)
	return result, nil
}

func NewProviderFromSnapshot(snapshot ProviderSnapshot, readCredential, controlCredential []byte) (*Provider, error) {
	if err := validateProviderSnapshot(snapshot, true); err != nil {
		return nil, err
	}
	provider, err := NewProvider(snapshot.Recipes, readCredential, controlCredential)
	if err != nil {
		return nil, err
	}
	provider.nextJob = snapshot.NextJob
	provider.nextVersion = snapshot.NextVersion
	for _, item := range snapshot.Jobs {
		state := &jobState{job: item.Job, operation: item.OperationID, logs: append([]LogLine(nil), item.Logs...), artifacts: append([]Artifact(nil), item.Artifacts...)}
		for _, line := range state.logs {
			state.logBytes += len(line.Text)
		}
		provider.jobs[item.Job.ID] = state
		provider.operationJobs[item.OperationID] = item.Job.ID
	}
	return provider, nil
}

func validateProviderSnapshot(snapshot ProviderSnapshot, requireDigest bool) error {
	if snapshot.SchemaVersion != providerSnapshotVersion || len(snapshot.Recipes) == 0 || len(snapshot.Recipes) > 1024 || len(snapshot.Jobs) > maxJobs || snapshot.NextVersion == 0 {
		return ErrJobDenied
	}
	if requireDigest && (!validDigest(snapshot.Digest) || snapshot.Digest != providerSnapshotDigest(snapshot)) {
		return ErrJobDenied
	}
	recipes := map[string]Recipe{}
	lastRecipe := ""
	for _, recipe := range snapshot.Recipes {
		if !validIdentity(recipe.Alias) || !validDigest(recipe.Digest) || recipe.Alias <= lastRecipe {
			return ErrJobDenied
		}
		recipes[recipe.Alias] = recipe
		lastRecipe = recipe.Alias
	}
	ids := map[string]struct{}{}
	operations := map[string]struct{}{}
	lastID := ""
	var maxJob, maxVersion uint64
	for _, item := range snapshot.Jobs {
		job := item.Job
		recipe, exists := recipes[job.RecipeAlias]
		if !exists || recipe.Digest != job.RecipeDigest || !validJobStatus(job.Status) || job.Version == 0 || !validIdentity(item.OperationID) || job.ID <= lastID {
			return ErrJobDenied
		}
		number, ok := jobNumber(job.ID)
		if !ok {
			return ErrJobDenied
		}
		if _, duplicate := ids[job.ID]; duplicate {
			return ErrJobDenied
		}
		if _, duplicate := operations[item.OperationID]; duplicate {
			return ErrJobDenied
		}
		ids[job.ID] = struct{}{}
		operations[item.OperationID] = struct{}{}
		lastID = job.ID
		if number > maxJob {
			maxJob = number
		}
		if job.Version > maxVersion {
			maxVersion = job.Version
		}
		if len(item.Logs) > maxLogsPerJob || len(item.Artifacts) > maxArtifacts {
			return ErrJobDenied
		}
		logBytes := 0
		for index, line := range item.Logs {
			if line.Sequence != uint32(index+1) || (line.Stream != "stdout" && line.Stream != "stderr") || line.Text == "" || len(line.Text) > 4096 || strings.ContainsRune(line.Text, 0) {
				return ErrJobDenied
			}
			logBytes += len(line.Text)
		}
		if logBytes > maxLogBytes {
			return ErrJobDenied
		}
		seenArtifacts := map[string]struct{}{}
		for _, artifact := range item.Artifacts {
			if !validArtifact(artifact) {
				return ErrJobDenied
			}
			if _, duplicate := seenArtifacts[artifact.Name]; duplicate {
				return ErrJobDenied
			}
			seenArtifacts[artifact.Name] = struct{}{}
		}
	}
	if snapshot.NextJob < maxJob || snapshot.NextVersion <= maxVersion {
		return ErrJobDenied
	}
	return nil
}

func providerSnapshotDigest(snapshot ProviderSnapshot) string {
	copySnapshot := snapshot
	copySnapshot.Digest = ""
	encoded, err := json.Marshal(copySnapshot)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func jobNumber(id string) (uint64, bool) {
	if !strings.HasPrefix(id, "job:") {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimPrefix(id, "job:"), 10, 64)
	return value, err == nil && value > 0
}
func validJobStatus(value string) bool {
	switch value {
	case "queued", "running", "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}
