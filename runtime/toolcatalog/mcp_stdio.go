package toolcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type StdioConfig struct {
	Command          []string
	ServerID         string
	HandlerVersion   string
	Timeout          time.Duration
	MaxResponseBytes int64
}

type StdioDiscovery struct{ config StdioConfig }

func NewStdioDiscovery(config StdioConfig) (*StdioDiscovery, error) {
	if len(config.Command) == 0 || len(config.Command) > 16 || !filepath.IsAbs(config.Command[0]) ||
		!identifierPattern.MatchString(config.ServerID) || !identifierPattern.MatchString(config.HandlerVersion) ||
		config.Timeout <= 0 || config.Timeout > 30*time.Second || config.MaxResponseBytes < 512 || config.MaxResponseBytes > 1024*1024 {
		return nil, ErrInvalidManager
	}
	for _, argument := range config.Command {
		if argument == "" || len(argument) > 4096 || strings.ContainsRune(argument, '\x00') {
			return nil, ErrInvalidManager
		}
	}
	config.Command = append([]string(nil), config.Command...)
	return &StdioDiscovery{config: config}, nil
}

func (discovery *StdioDiscovery) Discover(parent context.Context) ([]DiscoveredTool, error) {
	if discovery == nil {
		return nil, ErrInvalidManager
	}
	ctx, cancel := context.WithTimeout(parent, discovery.config.Timeout)
	defer cancel()
	command := exec.CommandContext(ctx, discovery.config.Command[0], discovery.config.Command[1:]...)
	configureSubprocess(command)
	command.Env = []string{}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdio unavailable", ErrDiscoveryFailed)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdio unavailable", ErrDiscoveryFailed)
	}
	command.Stderr = &boundedDiscard{remaining: 4096}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("%w: fixture start failed", ErrDiscoveryFailed)
	}
	request := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n")
	if _, err := stdin.Write(request); err != nil {
		_ = terminateSubprocess(command)
		_ = stdout.Close()
		_ = command.Wait()
		return nil, fmt.Errorf("%w: fixture request failed", ErrDiscoveryFailed)
	}
	_ = stdin.Close()
	type readResult struct {
		payload []byte
		err     error
	}
	readDone := make(chan readResult, 1)
	go func() {
		payload, err := io.ReadAll(io.LimitReader(stdout, discovery.config.MaxResponseBytes+1))
		readDone <- readResult{payload: payload, err: err}
	}()
	var read readResult
	select {
	case read = <-readDone:
	case <-ctx.Done():
		_ = terminateSubprocess(command)
		_ = stdout.Close()
		read = <-readDone
		_ = command.Wait()
		return nil, fmt.Errorf("%w: fixture timeout", ErrDiscoveryFailed)
	}
	if int64(len(read.payload)) > discovery.config.MaxResponseBytes {
		_ = terminateSubprocess(command)
		_ = stdout.Close()
		_ = command.Wait()
		return nil, fmt.Errorf("%w: fixture response exceeded bound", ErrDiscoveryFailed)
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: fixture timeout", ErrDiscoveryFailed)
	}
	if read.err != nil || waitErr != nil {
		return nil, fmt.Errorf("%w: fixture process failed", ErrDiscoveryFailed)
	}
	return decodeToolsList(read.payload, discovery.config)
}

type boundedDiscard struct{ remaining int }

func (writer *boundedDiscard) Write(value []byte) (int, error) {
	if len(value) > writer.remaining {
		writer.remaining = 0
		return 0, errors.New("stderr limit exceeded")
	}
	writer.remaining -= len(value)
	return len(value), nil
}

type toolsListResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int              `json:"id"`
	Result  *toolsListResult `json:"result"`
}
type toolsListResult struct {
	Tools *[]mcpTool `json:"tools"`
}
type mcpTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

func decodeToolsList(payload []byte, config StdioConfig) ([]DiscoveredTool, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var response toolsListResponse
	if err := decoder.Decode(&response); err != nil || response.JSONRPC != "2.0" || response.ID != 1 || response.Result == nil || response.Result.Tools == nil || len(*response.Result.Tools) > maxTools {
		return nil, fmt.Errorf("%w: invalid tools/list response", ErrDiscoveryFailed)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing tools/list data", ErrDiscoveryFailed)
	}
	tools := *response.Result.Tools
	result := make([]DiscoveredTool, len(tools))
	for index, tool := range tools {
		toolID := config.ServerID + "." + tool.Name
		if !identifierPattern.MatchString(toolID) || len(tool.Description) > maxDescription || len(tool.InputSchema) == 0 {
			return nil, fmt.Errorf("%w: invalid discovered tool", ErrDiscoveryFailed)
		}
		result[index] = DiscoveredTool{ToolID: toolID, ServerID: config.ServerID, Name: tool.Name, Description: tool.Description, HandlerVersion: config.HandlerVersion, InputSchema: append(json.RawMessage(nil), tool.InputSchema...), OutputSchema: append(json.RawMessage(nil), tool.OutputSchema...)}
	}
	return result, nil
}
