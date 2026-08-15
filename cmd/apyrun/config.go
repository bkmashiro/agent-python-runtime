package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"
	"unicode/utf8"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type operatorConfig struct {
	TimeoutMS          int64                     `json:"timeout_ms,omitempty"`
	MaxRequestBytes    uint32                    `json:"max_request_bytes,omitempty"`
	MaxResponseBytes   uint32                    `json:"max_response_bytes,omitempty"`
	MemoryLimitPages   uint32                    `json:"memory_limit_pages,omitempty"`
	ProgramSurface     string                    `json:"program_surface,omitempty"`
	ExecutionProfile   *executionProfileConfig   `json:"execution_profile,omitempty"`
	Deterministic      *deterministicConfig      `json:"deterministic_verification,omitempty"`
	WorkspaceFiles     map[string]string         `json:"workspace_files,omitempty"`
	MaxToolCalls       uint32                    `json:"max_tool_calls,omitempty"`
	Workspace          *mountedWorkspaceConfig   `json:"workspace,omitempty"`
	InformationSources *informationSourcesConfig `json:"information_sources,omitempty"`
	GitRead            *gitReadConfig            `json:"git_read,omitempty"`
	Playback           *playbackConfig           `json:"playback,omitempty"`
}

type playbackConfig struct {
	Mode                 string `json:"mode"`
	OutputBundle         string `json:"output_bundle,omitempty"`
	InputBundle          string `json:"input_bundle,omitempty"`
	ExpectedBundleSHA256 string `json:"expected_bundle_sha256,omitempty"`
	InputBranchManifest  string `json:"input_branch_manifest,omitempty"`
	ExpectedBranchSHA256 string `json:"expected_branch_sha256,omitempty"`
}

type informationSourcesConfig struct {
	DemoCatalog       *demoCatalogSourceConfig       `json:"demo_catalog,omitempty"`
	BenchmarkManifest *benchmarkManifestSourceConfig `json:"benchmark_manifest,omitempty"`
}

type gitReadConfig struct {
	RepositoryID   string `json:"repository_id"`
	RepositoryPath string `json:"repository_path"`
	MaxEntries     uint32 `json:"max_entries"`
	MaxPatchBytes  uint32 `json:"max_patch_bytes"`
	MaxBlobBytes   uint32 `json:"max_blob_bytes"`
}

type demoCatalogSourceConfig struct {
	Endpoint         string `json:"endpoint"`
	TimeoutMS        int64  `json:"timeout_ms"`
	MaxResponseBytes uint32 `json:"max_response_bytes"`
}

type benchmarkManifestSourceConfig struct {
	Endpoint         string `json:"endpoint"`
	TimeoutMS        int64  `json:"timeout_ms"`
	MaxResponseBytes uint32 `json:"max_response_bytes"`
}

type executionProfileConfig struct {
	ID             string   `json:"id"`
	AllowedImports []string `json:"allowed_imports"`
}

type deterministicConfig struct {
	Status     string `json:"status"`
	RandomSeed string `json:"random_seed"`
}

type mountedWorkspaceConfig struct {
	SourceDirectory string                                   `json:"source_directory,omitempty"`
	InputCapsule    string                                   `json:"input_capsule,omitempty"`
	OutputCapsule   string                                   `json:"output_capsule,omitempty"`
	Disposition     runtimeconfig.WorkspaceDispositionPolicy `json:"disposition"`
	Limits          *workspaceLimitsConfig                   `json:"limits,omitempty"`
}

type workspaceLimitsConfig struct {
	MaxFiles     uint32 `json:"max_files"`
	MaxBytes     uint64 `json:"max_bytes"`
	MaxFileBytes uint64 `json:"max_file_bytes"`
	MaxDepth     uint32 `json:"max_depth"`
}

func rejectDuplicateOperatorJSON(data []byte) error {
	if !utf8.Valid(data) || len(data) > 1<<20 {
		return errors.New("invalid operator JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueOperatorJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("operator JSON has trailing data")
	}
	return nil
}

func consumeUniqueOperatorJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 65536 {
		return errors.New("operator JSON is too complex")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("operator JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("operator JSON has duplicate key")
			}
			seen[key] = struct{}{}
			if err := consumeUniqueOperatorJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid operator JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueOperatorJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid operator JSON array")
		}
	default:
		return errors.New("invalid operator JSON delimiter")
	}
	return nil
}

func decodeOperatorConfig(data []byte) (operatorConfig, error) {
	if err := rejectDuplicateOperatorJSON(data); err != nil {
		return operatorConfig{}, errors.New("operator config contains ambiguous JSON")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return operatorConfig{}, errors.New("decode operator config")
	}
	for _, field := range []string{"information_sources", "playback"} {
		if raw, exists := envelope[field]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return operatorConfig{}, errors.New(field + " cannot be null")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config operatorConfig
	if err := decoder.Decode(&config); err != nil {
		return operatorConfig{}, errors.New("decode operator config")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return operatorConfig{}, errors.New("operator config contains trailing JSON")
	}
	return config, nil
}

func (config operatorConfig) resolve() (runtimeconfig.RunConfig, error) {
	runConfig := runtimeconfig.DefaultRunConfig()
	if config.TimeoutMS != 0 {
		if config.TimeoutMS < 0 {
			return runtimeconfig.RunConfig{}, errors.New("timeout_ms must be positive")
		}
		runConfig.Timeout = time.Duration(config.TimeoutMS) * time.Millisecond
	}
	if config.MaxRequestBytes != 0 {
		runConfig.MaxRequestBytes = config.MaxRequestBytes
	}
	if config.MaxResponseBytes != 0 {
		runConfig.MaxResponseBytes = config.MaxResponseBytes
	}
	if config.MemoryLimitPages != 0 {
		runConfig.MemoryLimitPages = config.MemoryLimitPages
	}
	if config.ProgramSurface != "" {
		switch runtimeconfig.ProgramSurfaceMode(config.ProgramSurface) {
		case runtimeconfig.ProgramSurfaceDirect:
			runConfig.ProgramSurface = runtimeconfig.ProgramSurfaceDirect
		case runtimeconfig.ProgramSurfaceProgrammatic:
			runConfig.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
			runConfig.Mechanisms.ProgrammaticToolCalling = true
		case runtimeconfig.ProgramSurfaceBoth:
			runConfig.ProgramSurface = runtimeconfig.ProgramSurfaceBoth
			runConfig.Mechanisms.ProgrammaticToolCalling = true
		default:
			return runtimeconfig.RunConfig{}, errors.New("invalid program_surface")
		}
	}
	if config.ExecutionProfile != nil {
		profile, err := runtimeconfig.NewExecutionProfile(config.ExecutionProfile.ID, config.ExecutionProfile.AllowedImports)
		if err != nil {
			return runtimeconfig.RunConfig{}, errors.New("invalid execution_profile")
		}
		runConfig.ExecutionProfile = &profile
	}
	if config.Deterministic != nil {
		if config.ExecutionProfile == nil || config.Workspace != nil || config.Deterministic.Status != runtimeconfig.DeterministicVerificationExperimentalPartial {
			return runtimeconfig.RunConfig{}, errors.New("deterministic_verification requires an execution profile, no mounted workspace, and experimental_partial status")
		}
		if _, err := runtimeconfig.NewDeterministicVerificationProfile("sha256:0000000000000000000000000000000000000000000000000000000000000000", config.Deterministic.RandomSeed); err != nil {
			return runtimeconfig.RunConfig{}, errors.New("invalid deterministic_verification random_seed")
		}
	}
	if config.InformationSources != nil {
		if config.InformationSources.DemoCatalog == nil && config.InformationSources.BenchmarkManifest == nil {
			return runtimeconfig.RunConfig{}, errors.New("information_sources must configure a curated source")
		}
		if config.InformationSources.DemoCatalog != nil {
			if _, err := config.InformationSources.DemoCatalog.resolve(); err != nil {
				return runtimeconfig.RunConfig{}, err
			}
		}
		if config.InformationSources.BenchmarkManifest != nil {
			if _, err := config.InformationSources.BenchmarkManifest.resolve(); err != nil {
				return runtimeconfig.RunConfig{}, err
			}
		}
	}
	if config.GitRead != nil {
		if _, err := config.GitRead.resolve(); err != nil {
			return runtimeconfig.RunConfig{}, err
		}
	}
	if config.Playback != nil {
		if err := config.Playback.validate(); err != nil {
			return runtimeconfig.RunConfig{}, err
		}
		if config.InformationSources == nil {
			return runtimeconfig.RunConfig{}, errors.New("playback requires a curated information source")
		}
		if config.Workspace != nil && config.Workspace.OutputCapsule != "" {
			return runtimeconfig.RunConfig{}, errors.New("playback bundle and workspace capsule outputs are mutually exclusive")
		}
	}
	hasTools := config.WorkspaceFiles != nil || config.InformationSources != nil || config.GitRead != nil
	if runConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect && !hasTools {
		return runtimeconfig.RunConfig{}, errors.New("program_surface requires configured Host tools")
	}
	if config.MaxToolCalls != 0 && !hasTools {
		return runtimeconfig.RunConfig{}, errors.New("max_tool_calls requires configured Host tools")
	}
	if config.Workspace != nil {
		if config.WorkspaceFiles != nil {
			return runtimeconfig.RunConfig{}, errors.New("mounted workspace and workspace tools are mutually exclusive")
		}
		if err := config.Workspace.validate(); err != nil {
			return runtimeconfig.RunConfig{}, err
		}
	}
	if err := runConfig.Validate(); err != nil {
		return runtimeconfig.RunConfig{}, fmt.Errorf("invalid operator resource bounds: %w", err)
	}
	return runConfig, nil
}

func validPlaybackDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (config *playbackConfig) validate() error {
	if config == nil {
		return nil
	}
	validPath := func(value string) bool {
		return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.Base(value) != "."
	}
	switch config.Mode {
	case "capture":
		if !validPath(config.OutputBundle) || config.InputBundle != "" || config.ExpectedBundleSHA256 != "" || config.InputBranchManifest != "" || config.ExpectedBranchSHA256 != "" {
			return errors.New("capture playback requires one absolute output_bundle")
		}
	case "playback":
		if !validPath(config.InputBundle) || config.OutputBundle != "" || !validPlaybackDigest(config.ExpectedBundleSHA256) || config.InputBranchManifest != "" || config.ExpectedBranchSHA256 != "" {
			return errors.New("offline playback requires one absolute input_bundle and expected_bundle_sha256")
		}
	case "branch":
		if !validPath(config.InputBundle) || !validPath(config.InputBranchManifest) || !validPath(config.OutputBundle) ||
			!validPlaybackDigest(config.ExpectedBundleSHA256) || !validPlaybackDigest(config.ExpectedBranchSHA256) ||
			config.InputBundle == config.InputBranchManifest || config.InputBundle == config.OutputBundle || config.InputBranchManifest == config.OutputBundle {
			return errors.New("branch playback requires protected parent, manifest, identities, and a distinct child output_bundle")
		}
	default:
		return errors.New("playback mode must be capture, playback, or branch")
	}
	return nil
}

func (config *demoCatalogSourceConfig) resolve() (capability.DemoCatalogPolicy, error) {
	if config == nil {
		return capability.DemoCatalogPolicy{}, errors.New("demo_catalog source is required")
	}
	policy := capability.DemoCatalogPolicy{
		Endpoint: config.Endpoint, Timeout: time.Duration(config.TimeoutMS) * time.Millisecond,
		MaxResponseBytes: config.MaxResponseBytes,
	}
	if _, _, err := capability.DemoCatalogDefinition(policy); err != nil {
		return capability.DemoCatalogPolicy{}, errors.New("invalid demo_catalog source policy")
	}
	return policy, nil
}

func (config *benchmarkManifestSourceConfig) resolve() (capability.BenchmarkManifestPolicy, error) {
	if config == nil {
		return capability.BenchmarkManifestPolicy{}, errors.New("benchmark_manifest source is required")
	}
	policy := capability.BenchmarkManifestPolicy{
		Endpoint: config.Endpoint, Timeout: time.Duration(config.TimeoutMS) * time.Millisecond,
		MaxResponseBytes: config.MaxResponseBytes,
	}
	if _, _, err := capability.BenchmarkManifestDefinition(policy); err != nil {
		return capability.BenchmarkManifestPolicy{}, errors.New("invalid benchmark_manifest source policy")
	}
	return policy, nil
}

func (config *gitReadConfig) resolve() (capability.GitReadPolicy, error) {
	if config == nil {
		return capability.GitReadPolicy{}, errors.New("git_read is required")
	}
	policy := capability.GitReadPolicy{
		RepositoryID: config.RepositoryID, RepositoryPath: config.RepositoryPath,
		MaxEntries: config.MaxEntries, MaxPatchBytes: config.MaxPatchBytes, MaxBlobBytes: config.MaxBlobBytes,
	}
	registry := capability.NewRegistry()
	if err := capability.RegisterGitReadTools(registry, policy); err != nil {
		return capability.GitReadPolicy{}, errors.New("invalid git_read policy")
	}
	return policy, nil
}

func (config *mountedWorkspaceConfig) validate() error {
	if config == nil {
		return nil
	}
	if config.SourceDirectory != "" && config.InputCapsule != "" {
		return errors.New("workspace accepts at most one source")
	}
	if !config.Disposition.Valid() {
		return errors.New("workspace disposition is required")
	}
	if config.Disposition == runtimeconfig.WorkspaceDiscardPolicy {
		if config.OutputCapsule != "" {
			return errors.New("discard workspace cannot configure output_capsule")
		}
	} else if config.OutputCapsule == "" {
		return errors.New("export workspace requires output_capsule")
	}
	for _, value := range []string{config.SourceDirectory, config.InputCapsule, config.OutputCapsule} {
		if value != "" && (!filepath.IsAbs(value) || filepath.Clean(value) != value) {
			return errors.New("workspace paths must be clean and absolute")
		}
	}
	_, err := config.resolveLimits()
	return err
}

func (config *mountedWorkspaceConfig) resolveLimits() (workspace.Limits, error) {
	limits := workspace.DefaultLimits()
	if config != nil && config.Limits != nil {
		limits = workspace.Limits{
			MaxFiles: config.Limits.MaxFiles, MaxBytes: config.Limits.MaxBytes,
			MaxFileBytes: config.Limits.MaxFileBytes, MaxDepth: config.Limits.MaxDepth,
		}
	}
	if err := limits.Validate(); err != nil {
		return workspace.Limits{}, errors.New("invalid workspace limits")
	}
	return limits, nil
}
