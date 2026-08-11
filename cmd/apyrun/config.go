package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

type operatorConfig struct {
	TimeoutMS        int64                   `json:"timeout_ms,omitempty"`
	MaxRequestBytes  uint32                  `json:"max_request_bytes,omitempty"`
	MaxResponseBytes uint32                  `json:"max_response_bytes,omitempty"`
	MemoryLimitPages uint32                  `json:"memory_limit_pages,omitempty"`
	ExecutionProfile *executionProfileConfig `json:"execution_profile,omitempty"`
	WorkspaceFiles   map[string]string       `json:"workspace_files,omitempty"`
	MaxToolCalls     uint32                  `json:"max_tool_calls,omitempty"`
}

type executionProfileConfig struct {
	ID             string   `json:"id"`
	AllowedImports []string `json:"allowed_imports"`
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
	if err := runConfig.Validate(); err != nil {
		return runtimeconfig.RunConfig{}, fmt.Errorf("invalid operator resource bounds: %w", err)
	}
	return runConfig, nil
}
