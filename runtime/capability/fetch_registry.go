package capability

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
)

const (
	fetchManyHandlerVersion = "builtin-fetch-many-v1"
	fetchManyPolicyVersion  = "builtin-fetch-many-policy-v1"
)

var fetchManyInputSchema = []byte(`{"type":"object","additionalProperties":false,"required":["requests"],"properties":{"requests":{"type":"array","minItems":1,"maxItems":4096,"items":{"type":"object","additionalProperties":false,"required":["request_id","target","path"],"properties":{"request_id":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$"},"target":{"type":"string","minLength":1,"maxLength":128},"path":{"type":"string","minLength":1,"maxLength":2048}}}}}}`)
var fetchManyOutputSchema = []byte(`{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","maxItems":4096,"items":{"type":"object","additionalProperties":false,"required":["request_id","status","error"],"properties":{"request_id":{"type":"string"},"status":{"enum":["ok","denied","error","timeout"]},"http_status":{"type":"integer"},"body":{"type":"string"},"content_type":{"type":"string"},"error":{"oneOf":[{"type":"null"},{"type":"object","additionalProperties":false,"required":["code","message"],"properties":{"code":{"type":"string"},"message":{"type":"string"}}}]}}}}}}`)

type builtinFetchManyHandler struct {
	grant   Grant
	fetcher Fetcher
}

func (handler *builtinFetchManyHandler) Handle(ctx context.Context, call HostCall) (json.RawMessage, error) {
	evidence, err := handler.HandleWithEvidence(ctx, call)
	return evidence.Result, err
}

func (handler *builtinFetchManyHandler) HandleWithEvidence(ctx context.Context, call HostCall) (HandlerEvidence, error) {
	legacy, err := NewBroker(Config{RunIdentity: call.RunIdentity, Grants: map[string]Grant{FetchManyCapability: handler.grant}}, handler.fetcher)
	if err != nil {
		return HandlerEvidence{}, err
	}
	payload, err := json.Marshal(map[string]any{"call_id": call.CallID, "capability": FetchManyCapability, "arguments": json.RawMessage(call.Arguments)})
	if err != nil {
		return HandlerEvidence{}, err
	}
	responseBytes, err := legacy.Call(ctx, payload)
	if err != nil {
		return HandlerEvidence{}, err
	}
	var response ToolResponse
	if decodeStrict(responseBytes, &response) != nil || response.Status != StatusOK || response.Error != nil {
		return HandlerEvidence{}, errors.New("builtin fetch_many executor rejected admitted arguments")
	}
	result, err := json.Marshal(response.Result)
	if err != nil {
		return HandlerEvidence{}, err
	}
	legacyReceipts := legacy.Receipts()
	drafts := make([]ReceiptDraft, len(legacyReceipts))
	for index, value := range legacyReceipts {
		drafts[index] = ReceiptDraft{RequestSHA256: value.RequestSHA256, Outcome: Status(value.Outcome)}
		if value.ResponseSHA256 != "" {
			item := response.Result.Items[index]
			drafts[index].Response = []byte(item.Body)
		}
	}
	return HandlerEvidence{Result: result, Receipts: drafts}, nil
}

func BuildBuiltinFetchManyRegistry(grant Grant, fetcher Fetcher) (*Registry, ToolGrant, string, error) {
	if fetcher == nil || grant.Name != FetchManyCapability {
		return nil, ToolGrant{}, "", errors.New("invalid builtin fetch_many registry configuration")
	}
	if err := grant.Validate(); err != nil {
		return nil, ToolGrant{}, "", err
	}
	targets := make(map[string]any, len(grant.Targets))
	names := make([]string, 0, len(grant.Targets))
	for name := range grant.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := grant.Targets[name]
		headerDigests := make(map[string]string, len(target.Headers))
		for header, value := range target.Headers {
			headerDigests[header] = digestBytes([]byte(value))
		}
		targets[name] = map[string]any{"base_url": target.BaseURL, "header_digests": headerDigests}
	}
	identity := map[string]any{
		"schema_version": "builtin-tool-catalog/v1", "tool_id": FetchManyCapability,
		"handler_version": fetchManyHandlerVersion, "policy_version": fetchManyPolicyVersion,
		"input_schema_digest": digestBytes(fetchManyInputSchema), "output_schema_digest": digestBytes(fetchManyOutputSchema),
		"max_calls": grant.MaxCalls, "max_requests_per_call": grant.MaxRequestsPerCall,
		"max_total_requests": grant.MaxTotalRequests, "max_concurrency": grant.MaxConcurrency,
		"max_response_bytes": grant.MaxResponseBytes, "per_request_timeout_ns": grant.PerRequestTimeout.Nanoseconds(),
		"targets": targets,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return nil, ToolGrant{}, "", err
	}
	catalogDigest := digestBytes(encoded)
	registry := NewRegistry()
	registry.catalogDigest = catalogDigest
	handler := &builtinFetchManyHandler{grant: grant, fetcher: fetcher}
	if err := registry.Register(HandlerSpec{ToolID: FetchManyCapability, HandlerVersion: fetchManyHandlerVersion, InputSchema: fetchManyInputSchema, OutputSchema: fetchManyOutputSchema, Handler: handler}); err != nil {
		return nil, ToolGrant{}, "", err
	}
	registry.sealed = true
	toolGrant := ToolGrant{ToolID: FetchManyCapability, HandlerVersion: fetchManyHandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: fetchManyPolicyVersion, MaxCalls: grant.MaxCalls}
	return registry, toolGrant, catalogDigest, nil
}
