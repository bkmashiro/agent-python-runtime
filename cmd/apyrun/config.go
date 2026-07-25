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
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type operatorConfig struct {
	TimeoutMS              int64            `json:"timeout_ms,omitempty"`
	MaxRequestBytes        uint32           `json:"max_request_bytes,omitempty"`
	MaxResponseBytes       uint32           `json:"max_response_bytes,omitempty"`
	MemoryLimitPages       uint32           `json:"memory_limit_pages,omitempty"`
	PreparedCapacity       uint32           `json:"prepared_capacity,omitempty"`
	TransactionJournalPath string           `json:"transaction_journal_path,omitempty"`
	FetchMany              *fetchManyConfig `json:"fetch_many,omitempty"`
}

type fetchManyConfig struct {
	MaxCalls            uint32                  `json:"max_calls"`
	MaxRequestsPerCall  uint32                  `json:"max_requests_per_call"`
	MaxTotalRequests    uint32                  `json:"max_total_requests"`
	MaxConcurrency      uint32                  `json:"max_concurrency"`
	MaxResponseBytes    uint32                  `json:"max_response_bytes"`
	PerRequestTimeoutMS int64                   `json:"per_request_timeout_ms"`
	Targets             map[string]targetConfig `json:"targets"`
}

type targetConfig struct {
	BaseURL string            `json:"base_url"`
	Headers map[string]string `json:"headers,omitempty"`
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

func (config operatorConfig) resolve() (runtimeconfig.RunConfig, *capability.Grant, error) {
	runConfig := runtimeconfig.DefaultRunConfig()
	if config.TimeoutMS != 0 {
		if config.TimeoutMS < 0 {
			return runtimeconfig.RunConfig{}, nil, errors.New("timeout_ms must be positive")
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
	if err := runConfig.Validate(); err != nil {
		return runtimeconfig.RunConfig{}, nil, fmt.Errorf("invalid operator resource bounds: %w", err)
	}
	if config.TransactionJournalPath != "" && (!filepath.IsAbs(config.TransactionJournalPath) || filepath.Clean(config.TransactionJournalPath) != config.TransactionJournalPath) {
		return runtimeconfig.RunConfig{}, nil, errors.New("transaction_journal_path must be a clean absolute path")
	}
	if config.FetchMany == nil {
		if config.TransactionJournalPath != "" {
			return runtimeconfig.RunConfig{}, nil, errors.New("transaction_journal_path requires a capability grant")
		}
		return runConfig, nil, nil
	}
	if config.FetchMany.PerRequestTimeoutMS <= 0 {
		return runtimeconfig.RunConfig{}, nil, errors.New("fetch_many per_request_timeout_ms must be positive")
	}
	grant := capability.Grant{
		Name:               capability.FetchManyCapability,
		Targets:            make(map[string]capability.TargetGrant, len(config.FetchMany.Targets)),
		MaxCalls:           config.FetchMany.MaxCalls,
		MaxRequestsPerCall: config.FetchMany.MaxRequestsPerCall,
		MaxTotalRequests:   config.FetchMany.MaxTotalRequests,
		MaxConcurrency:     config.FetchMany.MaxConcurrency,
		MaxResponseBytes:   config.FetchMany.MaxResponseBytes,
		PerRequestTimeout:  time.Duration(config.FetchMany.PerRequestTimeoutMS) * time.Millisecond,
	}
	for name, target := range config.FetchMany.Targets {
		headers := make(map[string]string, len(target.Headers))
		for header, value := range target.Headers {
			headers[header] = value
		}
		grant.Targets[name] = capability.TargetGrant{BaseURL: target.BaseURL, Headers: headers}
	}
	if err := grant.Validate(); err != nil {
		return runtimeconfig.RunConfig{}, nil, fmt.Errorf("invalid fetch_many grant: %w", err)
	}
	return runConfig, &grant, nil
}
