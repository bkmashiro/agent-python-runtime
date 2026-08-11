package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const (
	LinkAPIResponsesProtocol = "openai-responses-v3"
	LinkAPIResponsesEndpoint = "https://api.linkapi.ai/v1/responses"
	maxExchangeBytes         = 1024 * 1024
)

var ErrExchange = errors.New("provider exchange failed")
var boundedIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var boundedModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

type Request struct {
	Model   string          `json:"model"`
	Payload json.RawMessage `json:"-"`
}

type Usage struct {
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
	TotalTokens  uint64 `json:"total_tokens"`
}

type Response struct {
	Protocol       string          `json:"protocol,omitempty"`
	StatusCode     int             `json:"status_code"`
	Body           json.RawMessage `json:"-"`
	RequestID      string          `json:"request_id,omitempty"`
	RequestDigest  string          `json:"request_digest"`
	ResponseDigest string          `json:"response_digest"`
	Usage          *Usage          `json:"usage,omitempty"`
}

type Evidence struct {
	Protocol       string `json:"protocol"`
	StatusCode     int    `json:"status_code"`
	RequestID      string `json:"request_id,omitempty"`
	RequestDigest  string `json:"request_digest"`
	ResponseDigest string `json:"response_digest"`
	Usage          *Usage `json:"usage,omitempty"`
}

func (response Response) Evidence() Evidence {
	protocol := response.Protocol
	if protocol == "" {
		protocol = LinkAPIResponsesProtocol
	}
	return Evidence{Protocol: protocol, StatusCode: response.StatusCode, RequestID: response.RequestID, RequestDigest: response.RequestDigest, ResponseDigest: response.ResponseDigest, Usage: response.Usage}
}

type Adapter interface {
	Protocol() string
	Exchange(context.Context, Request) (Response, error)
}

type LinkAPIResponses struct {
	client     *http.Client
	endpoint   string
	credential func() (string, bool)
}

func NewLinkAPIResponses() (*LinkAPIResponses, error) {
	return newLinkAPIResponses(capability.NewPublicHTTPClient(), LinkAPIResponsesEndpoint, func() (string, bool) { return os.LookupEnv("LINKAPI_API_KEY") }, false)
}

func newLinkAPIResponses(client *http.Client, endpoint string, credential func() (string, bool), allowLoopback bool) (*LinkAPIResponses, error) {
	parsed, err := url.Parse(endpoint)
	if client == nil || credential == nil || err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/v1/responses" {
		return nil, fmt.Errorf("%w: invalid LinkAPI adapter configuration", ErrExchange)
	}
	if endpoint != LinkAPIResponsesEndpoint && !(allowLoopback && parsed.Scheme == "http" && parsed.Hostname() == "127.0.0.1") {
		return nil, fmt.Errorf("%w: untrusted LinkAPI endpoint", ErrExchange)
	}
	return &LinkAPIResponses{client: client, endpoint: endpoint, credential: credential}, nil
}

func (*LinkAPIResponses) Protocol() string { return LinkAPIResponsesProtocol }

func (adapter *LinkAPIResponses) Exchange(ctx context.Context, request Request) (Response, error) {
	if adapter == nil || !boundedModel.MatchString(request.Model) || len(request.Payload) == 0 || len(request.Payload) > maxExchangeBytes || !json.Valid(request.Payload) {
		return Response{}, fmt.Errorf("%w: invalid request", ErrExchange)
	}
	var envelope map[string]any
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil || len(envelope) == 0 || len(envelope) > 64 || envelope["model"] != request.Model {
		return Response{}, fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	stream, hasStream := envelope["stream"]
	_, hasInput := envelope["input"]
	maxOutputTokens, hasMaxOutputTokens := envelope["max_output_tokens"].(json.Number)
	background, hasBackground := envelope["background"]
	_, hasMessages := envelope["messages"]
	_, hasChatMax := envelope["max_tokens"]
	parsedMaxOutputTokens, maxTokensErr := parseUint(maxOutputTokens)
	if !hasInput || !hasMaxOutputTokens || maxTokensErr != nil || parsedMaxOutputTokens == 0 || hasMessages || hasChatMax ||
		(hasStream && stream != false) || (hasBackground && background != false) {
		return Response{}, fmt.Errorf("%w: invalid Responses payload", ErrExchange)
	}
	token, exists := adapter.credential()
	if !exists || !validBearerToken(token) {
		return Response{}, fmt.Errorf("%w: credential unavailable", ErrExchange)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.endpoint, bytes.NewReader(request.Payload))
	if err != nil {
		return Response{}, fmt.Errorf("%w: construct request", ErrExchange)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpResponse, err := adapter.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("%w: transport", ErrExchange)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxExchangeBytes+1))
	var responseEnvelope map[string]json.RawMessage
	if err != nil || len(body) == 0 || len(body) > maxExchangeBytes || !json.Valid(body) || json.Unmarshal(body, &responseEnvelope) != nil || len(responseEnvelope) == 0 || len(responseEnvelope) > 128 {
		return Response{}, fmt.Errorf("%w: invalid bounded response", ErrExchange)
	}
	response := Response{Protocol: LinkAPIResponsesProtocol, StatusCode: httpResponse.StatusCode, Body: append(json.RawMessage(nil), body...), RequestDigest: digest(request.Payload), ResponseDigest: digest(body)}
	if candidate := httpResponse.Header.Get("x-request-id"); boundedIdentity.MatchString(candidate) {
		response.RequestID = candidate
	}
	response.Usage = parseUsage(body)
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return response, fmt.Errorf("%w: provider status %d", ErrExchange, httpResponse.StatusCode)
	}
	return response, nil
}

func validBearerToken(token string) bool {
	if token == "" || len(token) > 4096 || strings.TrimSpace(token) != token {
		return false
	}
	for index := 0; index < len(token); index++ {
		if token[index] < 0x21 || token[index] > 0x7e {
			return false
		}
	}
	return true
}

func parseUsage(body []byte) *Usage {
	var envelope struct {
		Usage *struct {
			Input  json.Number `json:"input_tokens"`
			Output json.Number `json:"output_tokens"`
			Total  json.Number `json:"total_tokens"`
		} `json:"usage"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil || envelope.Usage == nil {
		return nil
	}
	input, inputErr := parseUint(envelope.Usage.Input)
	output, outputErr := parseUint(envelope.Usage.Output)
	total, totalErr := parseUint(envelope.Usage.Total)
	if inputErr != nil || outputErr != nil || totalErr != nil || input > total || output > total-input {
		return nil
	}
	return &Usage{InputTokens: input, OutputTokens: output, TotalTokens: total}
}

func parseUint(number json.Number) (uint64, error) {
	if number == "" {
		return 0, errors.New("missing usage")
	}
	value, err := number.Int64()
	if err != nil || value < 0 {
		return 0, errors.New("invalid usage")
	}
	return uint64(value), nil
}

func digest(value []byte) string {
	hashed := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(hashed[:])
}
