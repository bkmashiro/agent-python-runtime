package agenttrajectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ResponsePlanningBrief = "planning_brief"
	ResponseCandidate     = "candidate"
	ResponseSelection     = "selection"
	ResponseFinal         = "final"
)

var ErrProvider = errors.New("agent trajectory provider failure")

type ModelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelRequest struct {
	CallID       string
	ActorID      string
	ResponseKind string
	Messages     []ModelMessage
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ModelResult struct {
	CallID      string
	ActorID     string
	Content     string
	ResponseID  string
	Model       string
	Usage       TokenUsage
	RawRequest  []byte
	RawResponse []byte
}

type ModelProvider interface {
	Complete(context.Context, ModelRequest) (ModelResult, error)
	CallCount() uint32
}

// ReplayThenProvider reuses already-recorded exact provider envelopes before
// delegating later turns to a live provider. Replayed entries must match the
// requested call and actor identities exactly.
type ReplayThenProvider struct {
	replay []ModelResult
	live   ModelProvider
	calls  atomic.Uint32
}

func NewReplayThenProvider(replay []ModelResult, live ModelProvider) (*ReplayThenProvider, error) {
	if len(replay) == 0 || live == nil {
		return nil, ErrProvider
	}
	cloned := make([]ModelResult, len(replay))
	for index, result := range replay {
		if !validProviderIdentifier(result.CallID) || !validProviderIdentifier(result.ActorID) || strings.TrimSpace(result.Content) == "" || len(result.RawRequest) == 0 || len(result.RawResponse) == 0 {
			return nil, ErrProvider
		}
		cloned[index] = result
		cloned[index].RawRequest = append([]byte(nil), result.RawRequest...)
		cloned[index].RawResponse = append([]byte(nil), result.RawResponse...)
	}
	return &ReplayThenProvider{replay: cloned, live: live}, nil
}

func (provider *ReplayThenProvider) Complete(ctx context.Context, request ModelRequest) (ModelResult, error) {
	if provider == nil {
		return ModelResult{}, ErrProvider
	}
	index := int(provider.calls.Load())
	if index < len(provider.replay) {
		result := provider.replay[index]
		if result.CallID != request.CallID || result.ActorID != request.ActorID {
			return ModelResult{}, ErrProvider
		}
		provider.calls.Add(1)
		result.RawRequest = append([]byte(nil), result.RawRequest...)
		result.RawResponse = append([]byte(nil), result.RawResponse...)
		return result, nil
	}
	result, err := provider.live.Complete(ctx, request)
	if err != nil {
		return ModelResult{}, err
	}
	provider.calls.Add(1)
	return result, nil
}

func (provider *ReplayThenProvider) CallCount() uint32 {
	if provider == nil {
		return 0
	}
	return provider.calls.Load()
}

type DeepSeekConfig struct {
	BaseURL          string
	APIKey           string
	Model            string
	MaxCalls         uint32
	Timeout          time.Duration
	MaxResponseBytes int64
}

type DeepSeekProvider struct {
	endpoint         string
	apiKey           string
	model            string
	maxCalls         uint32
	maxResponseBytes int64
	client           *http.Client
	calls            atomic.Uint32
}

type deepSeekRequest struct {
	Model          string            `json:"model"`
	Messages       []ModelMessage    `json:"messages"`
	ResponseFormat map[string]string `json:"response_format"`
	Temperature    int               `json:"temperature"`
	Stream         bool              `json:"stream"`
}

type deepSeekResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage TokenUsage `json:"usage"`
}

func NewDeepSeekProvider(config DeepSeekConfig) (*DeepSeekProvider, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) {
		return nil, ErrProvider
	}
	if strings.TrimSpace(config.APIKey) == "" || (config.Model != "deepseek-v4-flash" && config.Model != "deepseek-v4-pro") || config.MaxCalls == 0 || config.MaxCalls > 5 || config.Timeout <= 0 || config.Timeout > 2*time.Minute || config.MaxResponseBytes < 1024 || config.MaxResponseBytes > 1<<20 {
		return nil, ErrProvider
	}
	return &DeepSeekProvider{
		endpoint: strings.TrimRight(config.BaseURL, "/") + "/chat/completions",
		apiKey:   config.APIKey, model: config.Model, maxCalls: config.MaxCalls,
		maxResponseBytes: config.MaxResponseBytes, client: &http.Client{Timeout: config.Timeout},
	}, nil
}

func (provider *DeepSeekProvider) CallCount() uint32 {
	if provider == nil {
		return 0
	}
	return provider.calls.Load()
}

func (provider *DeepSeekProvider) Complete(ctx context.Context, request ModelRequest) (ModelResult, error) {
	if provider == nil || !validProviderIdentifier(request.CallID) || !validProviderIdentifier(request.ActorID) || !validResponseKind(request.ResponseKind) || !validMessages(request.Messages) {
		return ModelResult{}, ErrProvider
	}
	if !provider.reserveCall() {
		return ModelResult{}, ErrProvider
	}
	payload := deepSeekRequest{Model: provider.model, Messages: append([]ModelMessage(nil), request.Messages...), ResponseFormat: map[string]string{"type": "json_object"}, Temperature: 0, Stream: false}
	rawRequest, err := json.Marshal(payload)
	if err != nil || len(rawRequest) > MaxContractJSONBytes {
		return ModelResult{}, ErrProvider
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, bytes.NewReader(rawRequest))
	if err != nil {
		return ModelResult{}, ErrProvider
	}
	httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := provider.client.Do(httpRequest)
	if err != nil {
		return ModelResult{}, errors.Join(ErrProvider, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, provider.maxResponseBytes+1)
	rawResponse, err := io.ReadAll(limited)
	if err != nil || int64(len(rawResponse)) > provider.maxResponseBytes || response.StatusCode != http.StatusOK {
		return ModelResult{}, fmt.Errorf("%w: status %d", ErrProvider, response.StatusCode)
	}
	var envelope deepSeekResponse
	if json.Unmarshal(rawResponse, &envelope) != nil || envelope.ID == "" || envelope.Model == "" || len(envelope.Choices) != 1 || envelope.Choices[0].Index != 0 || envelope.Choices[0].Message.Role != "assistant" || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" || envelope.Choices[0].FinishReason != "stop" || envelope.Usage.TotalTokens <= 0 || envelope.Usage.TotalTokens != envelope.Usage.PromptTokens+envelope.Usage.CompletionTokens {
		return ModelResult{}, ErrProvider
	}
	return ModelResult{
		CallID: request.CallID, ActorID: request.ActorID, Content: envelope.Choices[0].Message.Content,
		ResponseID: envelope.ID, Model: envelope.Model, Usage: envelope.Usage,
		RawRequest: append([]byte(nil), rawRequest...), RawResponse: append([]byte(nil), rawResponse...),
	}, nil
}

func (provider *DeepSeekProvider) reserveCall() bool {
	for {
		current := provider.calls.Load()
		if current >= provider.maxCalls {
			return false
		}
		if provider.calls.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func validProviderIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validResponseKind(value string) bool {
	return value == ResponsePlanningBrief || value == ResponseCandidate || value == ResponseSelection || value == ResponseFinal
}

func validMessages(messages []ModelMessage) bool {
	if len(messages) == 0 || len(messages) > 16 {
		return false
	}
	total := 0
	for _, message := range messages {
		if message.Role != "system" && message.Role != "user" && message.Role != "assistant" {
			return false
		}
		if !boundedText(message.Content, 1, MaxContractTextBytes) {
			return false
		}
		total += len(message.Content)
	}
	return total <= MaxContractJSONBytes
}
