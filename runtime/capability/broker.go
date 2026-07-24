package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var catalogDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Observation struct {
	Capability    string
	Duration      time.Duration
	RequestBytes  int
	ResponseBytes int
	Success       bool
}

type Observer func(Observation)

type Config struct {
	RunIdentity   string
	Grants        map[string]Grant
	Observer      Observer
	CatalogDigest string
	Registry      *Registry
	Binder        CallBinder
	ToolGrants    map[string]ToolGrant
}

type ToolGrant struct {
	ToolID         string
	HandlerVersion string
	EffectClass    string
	Policy         string
	PolicyVersion  string
	MaxCalls       uint32
}

type Broker struct {
	mu            sync.Mutex
	config        Config
	fetcher       Fetcher
	calls         map[string]uint32
	totalRequests map[string]uint32
	receipts      []receipt.Receipt
	typedCalls    map[string]typedCallReplay
}

type typedCallReplay struct {
	RequestDigest string
	Response      []byte
}

type toolRequest struct {
	CallID         string          `json:"call_id"`
	Capability     string          `json:"capability"`
	CatalogDigest  *string         `json:"catalog_digest,omitempty"`
	HandlerVersion *string         `json:"handler_version,omitempty"`
	Arguments      json.RawMessage `json:"arguments"`
	EnvelopeDigest string          `json:"-"`
}

type fetchManyArguments struct {
	Requests []fetchRequest `json:"requests"`
}

type fetchRequest struct {
	RequestID string `json:"request_id"`
	Target    string `json:"target"`
	Path      string `json:"path"`
}

func NewBroker(config Config, fetcher Fetcher) (*Broker, error) {
	if config.RunIdentity == "" || len(config.RunIdentity) > 128 {
		return nil, errors.New("Host run identity must be a bounded non-empty string")
	}
	if fetcher == nil {
		return nil, errors.New("fetcher is required")
	}
	config.Grants = cloneGrants(config.Grants)
	config.ToolGrants = cloneToolGrants(config.ToolGrants)
	if len(config.ToolGrants) > 0 {
		if config.Registry == nil || config.Binder == nil || !catalogDigestPattern.MatchString(config.CatalogDigest) {
			return nil, errors.New("typed tool grants require a registry, transaction binder, and bounded catalog digest")
		}
		config.Registry = config.Registry.snapshot()
		if config.Registry.catalogDigest != "" && config.Registry.catalogDigest != config.CatalogDigest {
			return nil, errors.New("typed registry catalog digest does not match Broker catalog binding")
		}
		for name, grant := range config.ToolGrants {
			if name != grant.ToolID || !validIdentifier(grant.ToolID) ||
				!validIdentifier(grant.HandlerVersion) || !validIdentifier(grant.PolicyVersion) || grant.EffectClass != "read_only" ||
				grant.Policy != "AUTO_COMMIT" || grant.MaxCalls == 0 || grant.MaxCalls > 1024 {
				return nil, fmt.Errorf("invalid or not-yet-qualified typed tool grant %q", name)
			}
			handler, exists := config.Registry.lookup(name)
			if !exists || handler.spec.HandlerVersion != grant.HandlerVersion {
				return nil, fmt.Errorf("typed tool grant %q has no matching frozen handler", name)
			}
		}
	}
	for name, grant := range config.Grants {
		if name != grant.Name {
			return nil, fmt.Errorf("grant key %q does not match name %q", name, grant.Name)
		}
		if err := grant.Validate(); err != nil {
			return nil, fmt.Errorf("invalid grant %q: %w", name, err)
		}
	}
	return &Broker{
		config:        config,
		fetcher:       fetcher,
		calls:         map[string]uint32{},
		totalRequests: map[string]uint32{},
		typedCalls:    map[string]typedCallReplay{},
	}, nil
}

func cloneGrants(source map[string]Grant) map[string]Grant {
	cloned := make(map[string]Grant, len(source))
	for name, grant := range source {
		grant.Targets = cloneTargets(grant.Targets)
		cloned[name] = grant
	}
	return cloned
}

func cloneTargets(source map[string]TargetGrant) map[string]TargetGrant {
	cloned := make(map[string]TargetGrant, len(source))
	for name, target := range source {
		headers := make(map[string]string, len(target.Headers))
		for header, value := range target.Headers {
			headers[header] = value
		}
		target.Headers = headers
		cloned[name] = target
	}
	return cloned
}

func cloneToolGrants(source map[string]ToolGrant) map[string]ToolGrant {
	cloned := make(map[string]ToolGrant, len(source))
	for name, grant := range source {
		cloned[name] = grant
	}
	return cloned
}

func (broker *Broker) CallCount() uint32 {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	var total uint32
	for _, count := range broker.calls {
		total += count
	}
	return total
}

func (broker *Broker) Receipts() []receipt.Receipt {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	copied := make([]receipt.Receipt, len(broker.receipts))
	copy(copied, broker.receipts)
	return copied
}

func (broker *Broker) Call(ctx context.Context, payload []byte) (response []byte, err error) {
	started := time.Now()
	observer := broker.config.Observer
	broker.mu.Lock()
	var request toolRequest
	defer func() {
		broker.mu.Unlock()
		if observer != nil {
			observer(Observation{
				Capability:    request.Capability,
				Duration:      time.Since(started),
				RequestBytes:  len(payload),
				ResponseBytes: len(response),
				Success:       err == nil,
			})
		}
	}()
	if err := decodeStrict(payload, &request); err != nil {
		return nil, fmt.Errorf("invalid tool request: %w", err)
	}
	request.EnvelopeDigest = digestBytes(payload)
	if !validIdentifier(request.CallID) || !validIdentifier(request.Capability) {
		return encodeResponse(denied(request.CallID, "invalid_request", "invalid call or capability identifier"))
	}
	if request.Capability != FetchManyCapability {
		return broker.callRegistered(ctx, request)
	}
	if request.CatalogDigest != nil || request.HandlerVersion != nil {
		return encodeResponse(denied(request.CallID, "invalid_request", "legacy capability request contains typed-tool fields"))
	}
	grant, granted := broker.config.Grants[request.Capability]
	if request.Capability != FetchManyCapability || !granted {
		return encodeResponse(denied(request.CallID, "capability_denied", "capability is not granted"))
	}

	var arguments fetchManyArguments
	if err := decodeStrict(request.Arguments, &arguments); err != nil {
		return encodeResponse(failed(request.CallID, "invalid_arguments", "fetch_many arguments are invalid"))
	}
	if err := validateRequests(arguments.Requests); err != nil {
		return encodeResponse(failed(request.CallID, "invalid_arguments", err.Error()))
	}
	requestCount := uint32(len(arguments.Requests))
	if requestCount > grant.MaxRequestsPerCall {
		return encodeResponse(denied(request.CallID, "request_budget_exceeded", "per-call request budget exceeded"))
	}
	if broker.calls[grant.Name] >= grant.MaxCalls {
		return encodeResponse(denied(request.CallID, "call_budget_exceeded", "capability call budget exhausted"))
	}
	if requestCount > grant.MaxTotalRequests-broker.totalRequests[grant.Name] {
		return encodeResponse(denied(request.CallID, "request_budget_exceeded", "total request budget exhausted"))
	}
	broker.calls[grant.Name]++
	broker.totalRequests[grant.Name] += requestCount

	result := FetchManyResult{Items: make([]FetchItem, len(arguments.Requests))}
	var responseBytes uint32
	waveSize := int(grant.MaxConcurrency)
	for start := 0; start < len(arguments.Requests); start += waveSize {
		end := start + waveSize
		if end > len(arguments.Requests) {
			end = len(arguments.Requests)
		}
		candidates := make([]fetchCandidate, end-start)
		if contextErr := ctx.Err(); contextErr != nil {
			for offset, item := range arguments.Requests[start:end] {
				candidates[offset] = canceledCandidate(grant, item, contextErr)
			}
		} else {
			var workers sync.WaitGroup
			for offset, item := range arguments.Requests[start:end] {
				workers.Add(1)
				go func(offset int, item fetchRequest) {
					defer workers.Done()
					candidates[offset] = broker.fetchCandidate(ctx, grant, item)
				}(offset, item)
			}
			workers.Wait()
		}
		for offset, candidate := range candidates {
			index := start + offset
			result.Items[index] = broker.admitCandidate(
				grant,
				request.CallID,
				uint32(index),
				candidate,
				&responseBytes,
			)
		}
	}
	return encodeResponse(ToolResponse{
		CallID: request.CallID,
		Status: StatusOK,
		Result: result,
		Error:  nil,
	})
}

type fetchCandidate struct {
	request        fetchRequest
	targetIdentity string
	output         FetchOutput
	err            error
	denied         bool
	timedOut       bool
}

func canceledCandidate(grant Grant, item fetchRequest, contextErr error) fetchCandidate {
	_, targetIdentity, resolveErr := resolveRequest(grant, item)
	if resolveErr != nil {
		return fetchCandidate{
			request:        item,
			targetIdentity: targetIdentity,
			err:            resolveErr,
			denied:         true,
		}
	}
	return fetchCandidate{
		request:        item,
		targetIdentity: targetIdentity,
		err:            contextErr,
		timedOut:       errors.Is(contextErr, context.DeadlineExceeded),
	}
}

func (broker *Broker) fetchCandidate(ctx context.Context, grant Grant, item fetchRequest) fetchCandidate {
	candidate := fetchCandidate{request: item}
	resolved, targetIdentity, err := resolveRequest(grant, item)
	candidate.targetIdentity = targetIdentity
	if err != nil {
		candidate.err = err
		candidate.denied = true
		return candidate
	}
	requestContext, cancel := context.WithTimeout(ctx, grant.PerRequestTimeout)
	defer cancel()
	candidate.output, candidate.err = broker.fetcher.Fetch(requestContext, resolved, grant.MaxResponseBytes)
	candidate.timedOut = errors.Is(candidate.err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded)
	return candidate
}

func (broker *Broker) admitCandidate(
	grant Grant,
	callID string,
	operationIndex uint32,
	candidate fetchCandidate,
	responseBytes *uint32,
) FetchItem {
	if candidate.denied {
		result := FetchItem{
			RequestID: candidate.request.RequestID,
			Status:    StatusDenied,
			Error:     &Error{Code: "target_denied", Message: "target or path is not granted"},
		}
		broker.record(callID, operationIndex, candidate.targetIdentity, result.Status, nil)
		return result
	}
	if candidate.err != nil {
		status := StatusError
		code := "fetch_failed"
		message := "Host fetch failed"
		if errors.Is(candidate.err, ErrResponseTooLarge) {
			code = "response_too_large"
			message = "Host response byte budget exceeded"
		} else if candidate.timedOut {
			status = StatusTimeout
			code = "fetch_timeout"
			message = "Host fetch timed out"
		}
		result := FetchItem{RequestID: candidate.request.RequestID, Status: status, Error: &Error{Code: code, Message: message}}
		broker.record(callID, operationIndex, candidate.targetIdentity, result.Status, nil)
		return result
	}
	remaining := grant.MaxResponseBytes - *responseBytes
	if uint64(len(candidate.output.Body)) > uint64(remaining) {
		result := FetchItem{
			RequestID: candidate.request.RequestID,
			Status:    StatusError,
			Error:     &Error{Code: "response_too_large", Message: "Host response byte budget exceeded"},
		}
		broker.record(callID, operationIndex, candidate.targetIdentity, result.Status, nil)
		return result
	}
	bodyBytes := uint32(len(candidate.output.Body))
	*responseBytes += bodyBytes
	result := FetchItem{
		RequestID:   candidate.request.RequestID,
		Status:      StatusOK,
		HTTPStatus:  candidate.output.StatusCode,
		Body:        string(candidate.output.Body),
		ContentType: candidate.output.ContentType,
		Error:       nil,
	}
	broker.record(callID, operationIndex, candidate.targetIdentity, result.Status, candidate.output.Body)
	return result
}

func (broker *Broker) record(callID string, operationIndex uint32, target string, status Status, response []byte) {
	broker.receipts = append(broker.receipts, receipt.New(
		broker.config.RunIdentity,
		callID,
		FetchManyCapability,
		operationIndex,
		target,
		string(status),
		response,
	))
}

func resolveRequest(grant Grant, request fetchRequest) (ResolvedRequest, string, error) {
	target, ok := grant.Targets[request.Target]
	if !ok {
		return ResolvedRequest{}, "target:" + request.Target, errors.New("target is not granted")
	}
	if !strings.HasPrefix(request.Path, "/") || strings.HasPrefix(request.Path, "//") {
		return ResolvedRequest{}, target.BaseURL + request.Path, errors.New("path must be origin-relative")
	}
	reference, err := url.Parse(request.Path)
	if err != nil || reference.IsAbs() || reference.Host != "" || reference.User != nil || reference.Fragment != "" {
		return ResolvedRequest{}, target.BaseURL + request.Path, errors.New("path contains authority")
	}
	base, err := url.Parse(target.BaseURL)
	if err != nil {
		return ResolvedRequest{}, target.BaseURL, err
	}
	resolvedURL := base.ResolveReference(reference).String()
	headers := make(map[string]string, len(target.Headers))
	for name, value := range target.Headers {
		headers[name] = value
	}
	return ResolvedRequest{URL: resolvedURL, Headers: headers}, resolvedURL, nil
}

func validateRequests(requests []fetchRequest) error {
	if len(requests) == 0 {
		return errors.New("requests must not be empty")
	}
	seen := map[string]struct{}{}
	for _, request := range requests {
		if !validIdentifier(request.RequestID) || request.Target == "" || request.Path == "" {
			return errors.New("request contains invalid identifier, target, or path")
		}
		if _, duplicate := seen[request.RequestID]; duplicate {
			return errors.New("request IDs must be unique")
		}
		seen[request.RequestID] = struct{}{}
	}
	return nil
}

func validIdentifier(value string) bool {
	return len(value) > 0 && len(value) <= 128 && identifierPattern.MatchString(value)
}

func decodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func denied(callID, code, message string) ToolResponse {
	return ToolResponse{CallID: callID, Status: StatusDenied, Result: FetchManyResult{Items: []FetchItem{}}, Error: &Error{Code: code, Message: message}}
}

func failed(callID, code, message string) ToolResponse {
	return ToolResponse{CallID: callID, Status: StatusError, Result: FetchManyResult{Items: []FetchItem{}}, Error: &Error{Code: code, Message: message}}
}

func encodeResponse(response ToolResponse) ([]byte, error) {
	return json.Marshal(response)
}
