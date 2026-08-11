package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type operatorConfig struct {
	TimeoutMS        int64                   `json:"timeout_ms,omitempty"`
	MaxRequestBytes  uint32                  `json:"max_request_bytes,omitempty"`
	MaxResponseBytes uint32                  `json:"max_response_bytes,omitempty"`
	MemoryLimitPages uint32                  `json:"memory_limit_pages,omitempty"`
	ExecutionProfile *executionProfileConfig `json:"execution_profile,omitempty"`
	WorkspaceFiles   map[string]string       `json:"workspace_files,omitempty"`
	MaxToolCalls     uint32                  `json:"max_tool_calls,omitempty"`
	Workspace        *mountedWorkspaceConfig `json:"workspace,omitempty"`
}

type executionProfileConfig struct {
	ID             string   `json:"id"`
	AllowedImports []string `json:"allowed_imports"`
}

type mountedWorkspaceConfig struct {
	SourceDirectory string                 `json:"source_directory,omitempty"`
	InputCapsule    string                 `json:"input_capsule,omitempty"`
	OutputCapsule   string                 `json:"output_capsule,omitempty"`
	Limits          *workspaceLimitsConfig `json:"limits,omitempty"`
}

type workspaceLimitsConfig struct {
	MaxFiles     uint32 `json:"max_files"`
	MaxBytes     uint64 `json:"max_bytes"`
	MaxFileBytes uint64 `json:"max_file_bytes"`
	MaxDepth     uint32 `json:"max_depth"`
}

func decodeOperatorConfig(data []byte) (operatorConfig, error) {
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
	if config.ExecutionProfile != nil {
		profile, err := runtimeconfig.NewExecutionProfile(config.ExecutionProfile.ID, config.ExecutionProfile.AllowedImports)
		if err != nil {
			return runtimeconfig.RunConfig{}, errors.New("invalid execution_profile")
		}
		runConfig.ExecutionProfile = &profile
	}
	if config.Workspace != nil {
		if config.WorkspaceFiles != nil || config.MaxToolCalls != 0 {
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

func (config *mountedWorkspaceConfig) validate() error {
	if config == nil {
		return nil
	}
	if config.SourceDirectory != "" && config.InputCapsule != "" {
		return errors.New("workspace accepts at most one source")
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
