package capability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	demoCatalogCapability      = "sources.demo_catalog"
	demoCatalogHandlerIdentity = "pysolate.sources.demo-catalog-http.v1"
	maxSourceResponseBytes     = 1 << 20
	maxSourceTimeout           = 30 * time.Second
)

type DemoCatalogPolicy struct {
	Endpoint         string
	Timeout          time.Duration
	MaxResponseBytes uint32
}

type demoCatalogPolicyDocument struct {
	SchemaVersion     string `json:"schema_version"`
	Capability        string `json:"capability"`
	Endpoint          string `json:"endpoint"`
	Method            string `json:"method"`
	Redirects         string `json:"redirects"`
	ExpectedStatus    int    `json:"expected_status"`
	ExpectedMediaType string `json:"expected_media_type"`
	TimeoutMS         int64  `json:"timeout_ms"`
	MaxResponseBytes  uint32 `json:"max_response_bytes"`
}

func DemoCatalogDefinition(policy DemoCatalogPolicy) (Spec, Grant, error) {
	if err := validateDemoCatalogPolicy(policy); err != nil {
		return Spec{}, Grant{}, err
	}
	policyDocument, err := json.Marshal(demoCatalogPolicyDocument{
		SchemaVersion: "pysolate.source-policy.v1", Capability: demoCatalogCapability,
		Endpoint: policy.Endpoint, Method: http.MethodGet, Redirects: "deny",
		ExpectedStatus: http.StatusOK, ExpectedMediaType: "application/json",
		TimeoutMS: policy.Timeout.Milliseconds(), MaxResponseBytes: policy.MaxResponseBytes,
	})
	if err != nil {
		return Spec{}, Grant{}, ErrInvalidGrant
	}
	grant, err := NewGrant(policyDocument)
	if err != nil {
		return Spec{}, Grant{}, err
	}
	return Spec{
		Name: demoCatalogCapability, Version: "pysolate.sources.demo-catalog.v1",
		Description: "Read the Host-approved structured demo catalog.",
		EffectClass: EffectExternalRead, Playback: PlaybackCaptured,
		HandlerIdentity: demoCatalogHandlerIdentity,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","maxItems":256,"items":{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":128},"title":{"type":"string","minLength":1,"maxLength":512},"score":{"type":"integer","minimum":0,"maximum":4294967295}},"required":["id","title","score"],"additionalProperties":false}}},"required":["items"],"additionalProperties":false}`),
		Python:          &PythonProjection{Module: "sources", Method: "demo_catalog", Arguments: []string{}, ResultField: "items"},
	}, grant, nil
}

func RegisterDemoCatalog(registry *Registry, policy DemoCatalogPolicy) error {
	if registry == nil {
		return ErrInvalidTool
	}
	spec, grant, err := DemoCatalogDefinition(policy)
	if err != nil {
		return err
	}
	handler, err := newDemoCatalogHandler(policy)
	if err != nil {
		return err
	}
	return registry.Register(spec, grant, handler)
}

func validateDemoCatalogPolicy(policy DemoCatalogPolicy) error {
	if len(policy.Endpoint) == 0 || len(policy.Endpoint) > 2048 || !utf8.ValidString(policy.Endpoint) ||
		policy.Timeout <= 0 || policy.Timeout > maxSourceTimeout || policy.Timeout%time.Millisecond != 0 ||
		policy.MaxResponseBytes == 0 || policy.MaxResponseBytes > maxSourceResponseBytes {
		return errors.New("invalid demo catalog policy")
	}
	parsed, err := url.Parse(policy.Endpoint)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("invalid demo catalog endpoint")
	}
	return nil
}

type demoCatalogHandler struct {
	endpoint string
	maximum  uint32
	client   *http.Client
}

func newDemoCatalogHandler(policy DemoCatalogPolicy) (Handler, error) {
	if err := validateDemoCatalogPolicy(policy); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	client := &http.Client{
		Transport:     transport,
		Timeout:       policy.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &demoCatalogHandler{endpoint: policy.Endpoint, maximum: policy.MaxResponseBytes, client: client}, nil
}

func (handler *demoCatalogHandler) Call(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if handler == nil || handler.client == nil {
		return nil, errors.New("invalid demo catalog call")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, handler.endpoint, nil)
	if err != nil {
		return nil, errors.New("create demo catalog request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "pysolate-source-adapter/1")
	response, err := handler.client.Do(request)
	if err != nil {
		return nil, errors.New("read demo catalog")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("demo catalog returned an unexpected status")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, errors.New("demo catalog returned an unexpected content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(handler.maximum)+1))
	if err != nil || len(body) > int(handler.maximum) {
		return nil, errors.New("demo catalog response exceeds its bound")
	}
	_, canonical, err := canonicalJSON(body)
	if err != nil {
		return nil, errors.New("demo catalog returned invalid JSON")
	}
	return canonical, nil
}
