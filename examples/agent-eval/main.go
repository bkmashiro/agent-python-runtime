// Command agent-eval compares direct OpenAI tool calls with Pysolate code on
// four deterministic, read-only task classes. It is intentionally a small
// validation harness, not a general benchmark framework.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

const (
	defaultBaseURL       = "https://api.deepseek.com/v1"
	defaultModel         = "deepseek-flash"
	defaultKeyEnv        = "DEEPSEEK_API_KEY"
	defaultGuest         = "dist/pysolate.wasm"
	defaultMaxTurns      = 12
	defaultMaxRequests   = 96
	maxCampaignRequests  = 96
	maxTokensPerRequest  = 4096
	maxRequestBodyBytes  = 1 << 20
	maxResponseBodyBytes = 256 << 10
	maxContextBytes      = 1 << 20
	maxSourceBytes       = 128 << 10
	maxToolResultBytes   = 256 << 10
	maxHostCalls         = 32
	episodeTimeout       = 2 * time.Minute
	codeExecutionTimeout = 30 * time.Second
)

var errGlobalBudget = errors.New("shared model-request budget exhausted")

// Message and the following OpenAI-compatible types deliberately retain
// reasoning_content in the in-memory continuation. Traces and JSONL rows never
// contain it.
type Message struct {
	Role             string     `json:"role"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Content          string     `json:"content,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ProviderUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type ProviderResponse struct {
	Message Message
	Model   string
	Usage   *ProviderUsage
}

type ChatProvider interface {
	Complete(context.Context, []Message, []ToolDefinition, int) (ProviderResponse, error)
}

type ProviderError struct {
	Message string
	Model   string
	Usage   *ProviderUsage
}

func (e *ProviderError) Error() string { return e.Message }

type OpenAIProvider struct {
	Client           *http.Client
	BaseURL          string
	APIKey           string
	Model            string
	MaxResponseBytes int
}

func (p *OpenAIProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition, maxTokens int) (ProviderResponse, error) {
	if p == nil || p.Client == nil {
		return ProviderResponse{}, &ProviderError{Message: "model provider is not configured"}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return ProviderResponse{}, &ProviderError{Message: "model base URL is empty"}
	}
	if maxTokens <= 0 || maxTokens > maxTokensPerRequest {
		return ProviderResponse{}, &ProviderError{Message: "invalid max_tokens"}
	}
	body, err := json.Marshal(struct {
		Model     string           `json:"model"`
		Messages  []Message        `json:"messages"`
		Tools     []ToolDefinition `json:"tools"`
		MaxTokens int              `json:"max_tokens"`
	}{p.Model, messages, tools, maxTokens})
	if err != nil {
		return ProviderResponse{}, &ProviderError{Message: "encode model request failed"}
	}
	if len(body) > maxRequestBodyBytes {
		return ProviderResponse{}, &ProviderError{Message: "model request exceeded body limit", Model: p.Model}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResponse{}, &ProviderError{Message: "create model request failed"}
	}
	request.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	limit := p.MaxResponseBytes
	if limit <= 0 || limit > maxResponseBodyBytes {
		limit = maxResponseBodyBytes
	}
	response, err := p.Client.Do(request)
	if err != nil {
		return ProviderResponse{}, &ProviderError{Message: "model request failed", Model: p.Model}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if readErr != nil || len(data) > limit {
		return ProviderResponse{}, &ProviderError{Message: "model response exceeded body limit", Model: p.Model}
	}
	var envelope struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string  `json:"finish_reason"`
			Message      Message `json:"message"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	usage := decodeUsage(envelope.Usage)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProviderResponse{Model: envelope.Model, Usage: usage}, &ProviderError{Message: "model returned a non-success HTTP status", Model: envelope.Model, Usage: usage}
	}
	if decodeErr != nil || len(envelope.Choices) != 1 {
		return ProviderResponse{Model: envelope.Model, Usage: usage}, &ProviderError{Message: "model response was not one valid choice", Model: envelope.Model, Usage: usage}
	}
	if envelope.Choices[0].FinishReason == "length" || envelope.Choices[0].FinishReason == "content_filter" {
		return ProviderResponse{Model: envelope.Model, Usage: usage}, &ProviderError{Message: "model response was incomplete", Model: envelope.Model, Usage: usage}
	}
	return ProviderResponse{Message: envelope.Choices[0].Message, Model: envelope.Model, Usage: usage}, nil
}

// A declaration is the sole source for the direct function schema, the
// Pysolate Manifest entry, and the code-arm context description.
type domainDeclaration struct {
	Name        string
	PythonPath  string
	Description string
	Schema      json.RawMessage
	Call        func(context.Context, json.RawMessage) (any, error)
}

func (d domainDeclaration) directTool() ToolDefinition {
	return ToolDefinition{Type: "function", Function: ToolFunctionDefinition{Name: d.Name, Description: d.Description, Parameters: d.Schema}}
}

func (d domainDeclaration) manifestTool(host *episodeHost) (string, pysolate.ToolSpec) {
	return d.Name, pysolate.ToolSpec{
		PythonPath:  d.PythonPath,
		Description: d.Description,
		InputSchema: d.Schema,
		Annotations: pysolate.ToolAnnotations{ReadOnlyHint: true},
		Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return host.call(d.Name, ctx, raw)
		},
	}
}

type taskCase struct {
	ID           string
	Prompt       string
	AnswerSchema json.RawMessage
	NewEpisode   func() *taskEpisode
}

type taskEpisode struct {
	Task         *taskCase
	Domains      []domainDeclaration
	CheckAnswer  func(json.RawMessage) (bool, error)
	TransientKey string
	transientHit bool
}

func (e *taskEpisode) domain(name string) (domainDeclaration, bool) {
	for _, d := range e.Domains {
		if d.Name == name {
			return d, true
		}
	}
	return domainDeclaration{}, false
}

// episodeHost is shared by one warm Runner. Runs are sequential, so changing
// active between episodes is safe; each active taskEpisode owns its counters
// and transient injection state.
type episodeHost struct {
	mu     sync.Mutex
	active *episodeRuntime
}

func (h *episodeHost) call(name string, ctx context.Context, raw json.RawMessage) (any, error) {
	h.mu.Lock()
	ep := h.active
	h.mu.Unlock()
	if ep == nil {
		return nil, errors.New("no active evaluation episode")
	}
	return ep.callDomain(name, ctx, raw)
}

type episodeRuntime struct {
	fixture       *taskEpisode
	domainCalls   int
	hostCalls     int
	callbackNanos int64
	toolLimit     bool
}

func (e *episodeRuntime) callDomain(name string, ctx context.Context, raw json.RawMessage) (any, error) {
	if e.hostCalls >= maxHostCalls {
		e.toolLimit = true
		return nil, errors.New("Host-call budget exhausted")
	}
	d, ok := e.fixture.domain(name)
	if !ok {
		return nil, errors.New("unknown domain tool")
	}
	e.hostCalls++
	e.domainCalls++
	started := time.Now()
	value, err := d.Call(ctx, raw)
	e.callbackNanos += time.Since(started).Nanoseconds()
	return value, err
}

func (e *taskEpisode) manifest(host *episodeHost) pysolate.Manifest {
	manifest := make(pysolate.Manifest, len(e.Domains))
	for _, d := range e.Domains {
		name, spec := d.manifestTool(host)
		manifest[name] = spec
	}
	return manifest
}

func submitDefinition(schema json.RawMessage) ToolDefinition {
	return ToolDefinition{Type: "function", Function: ToolFunctionDefinition{
		Name:        "submit_answer",
		Description: "Submit the final structured answer for this task. Call only after checking the exact requested fields; all numeric amounts are integer cents.",
		Parameters:  schema,
	}}
}

func executePythonDefinition() ToolDefinition {
	return ToolDefinition{Type: "function", Function: ToolFunctionDefinition{
		Name:        "execute_python",
		Description: "Execute one complete Python program in a fresh private Pysolate Guest. No shell, subprocess, network, credentials, or host filesystem.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["source"],"properties":{"source":{"type":"string","description":"Complete Python source; use only injected read-only tools and ordinary computation."}}}`),
	}}
}

func allTasks() map[string]taskCase {
	return map[string]taskCase{
		"lookup":        lookupTask(),
		"paginated_sum": paginatedSumTask(),
		"join":          joinTask(),
		"transient":     transientTask(),
	}
}

func lookupTask() taskCase {
	return taskCase{ID: "lookup", Prompt: "Use the catalog price lookup for SKU SKU-3 and submit its price in integer cents.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["price_cents"],"properties":{"price_cents":{"type":"integer"}}}`), NewEpisode: func() *taskEpisode {
		products := map[string]int64{"SKU-1": 1250, "SKU-2": 2075, "SKU-3": 3199, "SKU-4": 4810}
		domains := []domainDeclaration{{Name: "catalog_price", PythonPath: "catalog.price", Description: "Readonly catalog lookup. Call catalog.price(sku='SKU-3') with keyword arguments only. Returns a JSON object with string field sku and integer field price_cents.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["sku"],"properties":{"sku":{"type":"string"}}}`), Call: func(_ context.Context, raw json.RawMessage) (any, error) {
			args, err := exactObject(raw, "sku")
			if err != nil {
				return nil, errors.New("catalog_price arguments are invalid")
			}
			sku, err := stringField(args, "sku")
			if err != nil {
				return nil, errors.New("catalog_price sku is invalid")
			}
			price, ok := products[sku]
			if !ok {
				return nil, errors.New("catalog_price SKU is not in the fixed catalog")
			}
			return map[string]any{"sku": sku, "price_cents": price}, nil
		}}}
		return &taskEpisode{Domains: domains, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "price_cents")
			if err != nil {
				return false, err
			}
			value, err := intField(obj, "price_cents")
			return value == 3199, err
		}}
	}}
}

func paginatedSumTask() taskCase {
	return taskCase{ID: "paginated_sum", Prompt: "Read all six pages of the 120-record sales ledger. Keep records whose status is open and whose region is west, then submit the matching record_count and sum_cents. Amounts are integer cents.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["record_count","sum_cents"],"properties":{"record_count":{"type":"integer"},"sum_cents":{"type":"integer"}}}`), NewEpisode: func() *taskEpisode {
		type record struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Region string `json:"region"`
			Amount int64  `json:"amount_cents"`
		}
		all := make([]record, 120)
		statuses := []string{"open", "closed", "pending", "open"}
		regions := []string{"east", "west", "north"}
		for i := range all {
			n := i + 1
			all[i] = record{ID: fmt.Sprintf("R-%03d", n), Status: statuses[i%len(statuses)], Region: regions[i%len(regions)], Amount: int64(101 + i*37)}
		}
		domains := []domainDeclaration{{Name: "ledger_page", PythonPath: "ledger.page", Description: "Readonly paginated ledger. Call ledger.page(page=1) with keyword arguments only for pages 1 through 6. Returns a JSON object with integer page, boolean has_more, and records array; every record has string id/status/region and integer amount_cents. Do not skip pages.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["page"],"properties":{"page":{"type":"integer","minimum":1,"maximum":6}}}`), Call: func(_ context.Context, raw json.RawMessage) (any, error) {
			args, err := exactObject(raw, "page")
			if err != nil {
				return nil, errors.New("ledger_page arguments are invalid")
			}
			page, err := intField(args, "page")
			if err != nil || page < 1 || page > 6 {
				return nil, errors.New("ledger_page page must be 1 through 6")
			}
			start := (page - 1) * 20
			end := start + 20
			rows := all[start:end]
			return map[string]any{"page": page, "has_more": page < 6, "records": rows}, nil
		}}}
		var count, sum int64
		for _, r := range all {
			if r.Status == "open" && r.Region == "west" {
				count++
				sum += r.Amount
			}
		}
		return &taskEpisode{Domains: domains, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "record_count", "sum_cents")
			if err != nil {
				return false, err
			}
			gotCount, e1 := intField(obj, "record_count")
			gotSum, e2 := intField(obj, "sum_cents")
			return gotCount == count && gotSum == sum, errors.Join(e1, e2)
		}}
	}}
}

func joinTask() taskCase {
	return taskCase{ID: "join", Prompt: "Join all 20 order rows with the catalog product lookup. Aggregate quantity times price_cents by category and submit category_totals with integer values.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["category_totals"],"properties":{"category_totals":{"type":"object","additionalProperties":{"type":"integer"}}}}`), NewEpisode: func() *taskEpisode {
		type order struct {
			ID       string `json:"order_id"`
			SKU      string `json:"sku"`
			Quantity int64  `json:"quantity"`
		}
		type product struct {
			SKU      string `json:"sku"`
			Price    int64  `json:"price_cents"`
			Category string `json:"category"`
		}
		products := []product{{"SKU-1", 1250, "alpha"}, {"SKU-2", 2075, "beta"}, {"SKU-3", 3199, "alpha"}, {"SKU-4", 4810, "gamma"}}
		orders := make([]order, 20)
		for i := range orders {
			orders[i] = order{ID: fmt.Sprintf("O-%02d", i+1), SKU: products[i%4].SKU, Quantity: int64(i%3 + 1)}
		}
		domains := []domainDeclaration{
			{Name: "orders_rows", PythonPath: "orders.rows", Description: "Readonly order rows. Call orders.rows() with keyword arguments only (no arguments). Returns a JSON object with a rows array of 20 objects containing order_id, sku, and integer quantity.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				if _, err := exactObject(raw); err != nil {
					return nil, errors.New("orders_rows arguments are invalid")
				}
				return map[string]any{"rows": orders}, nil
			}},
			{Name: "catalog_products", PythonPath: "catalog.products", Description: "Readonly product catalog. Call catalog.products() with keyword arguments only (no arguments). Returns a JSON object with products array; each product has sku, integer price_cents, and category.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				if _, err := exactObject(raw); err != nil {
					return nil, errors.New("catalog_products arguments are invalid")
				}
				return map[string]any{"products": products}, nil
			}},
		}
		expected := map[string]int64{}
		for _, o := range orders {
			p := products[0]
			for _, candidate := range products {
				if candidate.SKU == o.SKU {
					p = candidate
					break
				}
			}
			expected[p.Category] += o.Quantity * p.Price
		}
		return &taskEpisode{Domains: domains, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "category_totals")
			if err != nil {
				return false, err
			}
			var totals map[string]json.RawMessage
			if err := json.Unmarshal(obj["category_totals"], &totals); err != nil {
				return false, errors.New("category_totals must be an object")
			}
			for _, raw := range totals {
				if _, err := intRaw(raw); err != nil {
					return false, err
				}
			}
			if len(totals) != len(expected) {
				return false, nil
			}
			for category, want := range expected {
				raw, present := totals[category]
				if !present {
					return false, nil
				}
				got, err := intRaw(raw)
				if err != nil || got != want {
					return false, err
				}
			}
			return true, nil
		}}
	}}
}

func transientTask() taskCase {
	return taskCase{ID: "transient", Prompt: "Look up the price for SKU-2 using the readonly transient catalog tool. If the first call reports a transient error, retry the same lookup and submit the resulting integer price_cents.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["price_cents"],"properties":{"price_cents":{"type":"integer"}}}`), NewEpisode: func() *taskEpisode {
		domains := []domainDeclaration{{Name: "transient_price", PythonPath: "catalog.transient_price", Description: "Readonly catalog lookup with a controlled transient fault. Call catalog.transient_price(sku='SKU-2') with keyword arguments only. The first lookup per evaluation episode returns an explicit transient error; retrying returns a JSON object with sku and integer price_cents.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["sku"],"properties":{"sku":{"type":"string"}}}`), Call: func(_ context.Context, raw json.RawMessage) (any, error) {
			args, err := exactObject(raw, "sku")
			if err != nil {
				return nil, errors.New("transient_price arguments are invalid")
			}
			sku, err := stringField(args, "sku")
			if err != nil || sku != "SKU-2" {
				return nil, errors.New("transient_price SKU is invalid")
			}
			return nil, errors.New("transient catalog error: retry this readonly lookup")
		}}}
		ep := &taskEpisode{Domains: domains, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "price_cents")
			if err != nil {
				return false, err
			}
			got, err := intField(obj, "price_cents")
			return got == 2075, err
		}}
		// Replace the callback after the episode is allocated so only this episode
		// sees the first-call fault, for both direct and code arms.
		base := domains[0].Call
		ep.Domains[0].Call = func(ctx context.Context, raw json.RawMessage) (any, error) {
			if !ep.transientHit {
				ep.transientHit = true
				return base(ctx, raw)
			}
			args, err := exactObject(raw, "sku")
			if err != nil {
				return nil, errors.New("transient_price arguments are invalid")
			}
			sku, err := stringField(args, "sku")
			if err != nil || sku != "SKU-2" {
				return nil, errors.New("transient_price SKU is invalid")
			}
			return map[string]any{"sku": sku, "price_cents": int64(2075)}, nil
		}
		return ep
	}}
}

func exactObject(raw json.RawMessage, required ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, errors.New("expected JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	wanted := make(map[string]bool, len(required))
	for _, key := range required {
		wanted[key] = true
		if _, ok := obj[key]; !ok {
			return nil, fmt.Errorf("missing %s", key)
		}
	}
	if len(obj) != len(wanted) {
		return nil, errors.New("unexpected field")
	}
	for key := range obj {
		if !wanted[key] {
			return nil, errors.New("unexpected field")
		}
	}
	return obj, nil
}

func stringField(obj map[string]json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(obj[name], &value); err != nil || value == "" {
		return "", errors.New("invalid string")
	}
	return value, nil
}
func intField(obj map[string]json.RawMessage, name string) (int64, error) { return intRaw(obj[name]) }
func intRaw(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing integer")
	}
	text := string(bytes.TrimSpace(raw))
	if len(text) > 64 || !json.Valid(raw) || text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return 0, errors.New("integer required")
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exp, err := strconv.Atoi(text[i+1:])
		if err != nil || exp < -100 || exp > 100 {
			return 0, errors.New("integer out of range")
		}
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || !n.IsInt() || !n.Num().IsInt64() {
		return 0, errors.New("integer required")
	}
	return n.Num().Int64(), nil
}

// Shared request accounting is intentionally a single campaign-wide counter.
type requestBudget struct {
	mu          sync.Mutex
	used, limit int
}

func (b *requestBudget) take(reserve int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reserve < 0 {
		reserve = 0
	}
	if b.used >= b.limit-reserve {
		return errGlobalBudget
	}
	b.used++
	return nil
}
func (b *requestBudget) remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit - b.used
}
func (b *requestBudget) count() int { b.mu.Lock(); defer b.mu.Unlock(); return b.used }

func decodeUsage(raw json.RawMessage) *ProviderUsage {
	var v struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Total      *int64 `json:"total_tokens"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Prompt == nil || v.Completion == nil || v.Total == nil || *v.Prompt < 0 || *v.Completion < 0 || *v.Total < 0 {
		return nil
	}
	return &ProviderUsage{PromptTokens: *v.Prompt, CompletionTokens: *v.Completion, TotalTokens: *v.Total}
}

type toolTrace struct {
	Turn      int    `json:"turn"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Result    string `json:"result,omitempty"`
}

type usageTotals struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type episodeRow struct {
	TotalNS           int64           `json:"total_ns"`
	ExecutionAttempts int             `json:"execution_attempts"`
	Answer            json.RawMessage `json:"answer,omitempty"`
	Trace             []toolTrace     `json:"trace,omitempty"`
	FixtureVersion    string          `json:"fixture_version"`
	Task              string          `json:"task"`
	Arm               string          `json:"arm"`
	Repeat            int             `json:"repeat"`
	Sequence          int             `json:"sequence"`
	CompletionStatus  string          `json:"completion_status"`
	Correctness       *bool           `json:"correctness"`
	Turns             int             `json:"turns"`
	ModelRequests     int             `json:"model_requests"`
	ModelToolCalls    int             `json:"model_tool_calls"`
	DomainToolCalls   int             `json:"domain_tool_calls"`
	RequestedModel    string          `json:"requested_model"`
	ReturnedModel     *string         `json:"returned_model"`
	ReturnedModels    []string        `json:"returned_models,omitempty"`
	ProviderUsage     *usageTotals    `json:"provider_usage"`
	UsageComplete     bool            `json:"usage_complete"`
	ModelRoundtripNS  int64           `json:"model_roundtrip_ns"`
	PysolateNS        int64           `json:"pysolate_ns"`
	DomainCallbackNS  int64           `json:"domain_callback_ns"`
	SetupNS           int64           `json:"setup_ns"`
}

func (r *episodeRow) recordProvider(response ProviderResponse) {
	if response.Model != "" {
		r.ReturnedModels = append(r.ReturnedModels, response.Model)
		model := response.Model
		r.ReturnedModel = &model
	}
	if response.Usage == nil {
		r.UsageComplete = false
		return
	}
	if r.ProviderUsage == nil {
		r.ProviderUsage = &usageTotals{}
	}
	r.ProviderUsage.PromptTokens += response.Usage.PromptTokens
	r.ProviderUsage.CompletionTokens += response.Usage.CompletionTokens
	r.ProviderUsage.TotalTokens += response.Usage.TotalTokens
}

func initialRow(task, arm string, repeat, sequence int, requested string, setupNS int64) episodeRow {
	return episodeRow{FixtureVersion: "agent-eval-v1", Task: task, Arm: arm, Repeat: repeat, Sequence: sequence, CompletionStatus: "provider_error", RequestedModel: requested, SetupNS: setupNS, UsageComplete: true}
}

func domainContext(domains []domainDeclaration) string {
	items := make([]map[string]any, 0, len(domains))
	for _, d := range domains {
		items = append(items, map[string]any{"name": d.Name, "python_path": d.PythonPath, "description": d.Description, "parameters": d.Schema})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	data, _ := json.Marshal(items)
	return string(data)
}

func runEpisode(ctx context.Context, provider ChatProvider, budget *requestBudget, task taskCase, repeat, sequence int, arm string, runner *pysolate.Runner, setupNS int64, host *episodeHost, requestedModel string, maxTurns, reserve int) (row episodeRow) {
	episodeStarted := time.Now()
	fixture := task.NewEpisode()
	runtime := &episodeRuntime{fixture: fixture}
	row = initialRow(task.ID, arm, repeat, sequence, requestedModel, setupNS)
	defer func() {
		row.TotalNS = time.Since(episodeStarted).Nanoseconds()
		row.DomainToolCalls = runtime.domainCalls
		row.DomainCallbackNS = runtime.callbackNanos
		if !row.UsageComplete {
			row.ProviderUsage = nil
		}
	}()
	if arm == "code" && runner == nil {
		row.CompletionStatus = "provider_error"
		return
	}
	if arm == "code" {
		host.mu.Lock()
		host.active = runtime
		host.mu.Unlock()
		defer func() { host.mu.Lock(); host.active = nil; host.mu.Unlock() }()
	}
	tools := make([]ToolDefinition, 0, len(fixture.Domains)+2)
	for _, d := range fixture.Domains {
		if arm == "direct" {
			tools = append(tools, d.directTool())
		}
	}
	if arm == "code" {
		tools = append(tools, executePythonDefinition())
	}
	tools = append(tools, submitDefinition(task.AnswerSchema))
	modeInstructions := "Use each listed readonly function tool by its schema name (Python binding names are informational), then call submit_answer exactly once with the task-specific structured answer. Tool errors are feedback; repair and retry. Do not invent values."
	if arm == "code" {
		modeInstructions = "Write complete Python in execute_python, calling approved tools by their python_path with keyword arguments only. Each execute_python call starts a fresh Guest, so do not rely on Python state between calls. Readonly tool errors are feedback; repair and retry. Set the Python global result to your computed JSON-compatible answer (for example result = some_dict), or print intermediate observations, then call submit_answer. Never use shell, subprocess, network, credentials, environment variables, or host filesystem."
	}
	system := modeInstructions
	if arm == "code" {
		system += "\nDomain declarations (schemas and descriptions are authoritative): " + domainContext(fixture.Domains)
	}
	messages := []Message{{Role: "system", Content: system}, {Role: "user", Content: task.Prompt}}
	reply := func(call ToolCall, name, content string) {
		row.Trace = append(row.Trace, toolTrace{Turn: row.Turns, Tool: name, Arguments: call.Function.Arguments, Result: content})
		messages = append(messages, toolMessage(call, name, content))
	}
	protocolFailure := false
	for turn := 1; turn <= maxTurns; turn++ {
		row.Turns = turn
		if err := ctx.Err(); err != nil {
			row.CompletionStatus = "timeout"
			return
		}
		encoded, err := json.Marshal(messages)
		if err != nil || len(encoded) > maxContextBytes {
			row.CompletionStatus = "provider_error"
			return
		}
		if err := budget.take(reserve); err != nil {
			row.CompletionStatus = "globalbudget"
			return
		}
		row.ModelRequests++
		started := time.Now()
		response, callErr := provider.Complete(ctx, messages, tools, maxTokensPerRequest)
		row.ModelRoundtripNS += time.Since(started).Nanoseconds()
		row.recordProvider(response)
		if callErr != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				row.CompletionStatus = "timeout"
			} else if errors.Is(callErr, errGlobalBudget) {
				row.CompletionStatus = "globalbudget"
			} else {
				row.CompletionStatus = "provider_error"
			}
			return
		}
		if response.Message.Role == "" {
			response.Message.Role = "assistant"
		}
		if response.Message.Role != "assistant" {
			protocolFailure = true
			messages = append(messages, Message{Role: "user", Content: "Controlled error: response must have assistant role and function calls."})
			continue
		}
		validCalls := true
		ids := map[string]bool{}
		for _, call := range response.Message.ToolCalls {
			if call.ID == "" || ids[call.ID] || (call.Type != "" && call.Type != "function") {
				validCalls = false
			}
			ids[call.ID] = true
		}
		row.ModelToolCalls += len(response.Message.ToolCalls)
		if !validCalls {
			protocolFailure = true
			messages = append(messages, Message{Role: "user", Content: "Controlled error: tool calls must have unique ids and function type."})
			continue
		}
		messages = append(messages, response.Message)
		if len(response.Message.ToolCalls) == 0 {
			protocolFailure = true
			messages = append(messages, Message{Role: "user", Content: "Controlled error: call a listed tool; a plain response is not a submission."})
			continue
		}
		for _, call := range response.Message.ToolCalls {
			if call.ID == "" {
				protocolFailure = true
				messages = append(messages, Message{Role: "user", Content: "Controlled error: every function call needs a call id."})
				continue
			}
			if call.Type != "" && call.Type != "function" {
				protocolFailure = true
				messages = append(messages, Message{Role: "user", Content: "Controlled error: only function calls are supported."})
				continue
			}
			if call.Function.Name == "submit_answer" {
				correct, validationErr := fixture.CheckAnswer(json.RawMessage(call.Function.Arguments))
				if validationErr != nil {
					protocolFailure = true
					reply(call, "submit_answer", jsonError(validationErr))
					continue
				}
				row.Answer = append(json.RawMessage(nil), call.Function.Arguments...)
				row.Trace = append(row.Trace, toolTrace{Turn: row.Turns, Tool: "submit_answer", Arguments: call.Function.Arguments})
				row.Correctness = boolPtr(correct)
				if correct {
					row.CompletionStatus = "completed"
				} else {
					row.CompletionStatus = "wrong_answer"
				}
				return
			}
			if arm == "direct" {
				value, invokeErr := runtime.callDomain(call.Function.Name, ctx, json.RawMessage(call.Function.Arguments))
				if runtime.toolLimit {
					row.CompletionStatus = "tool_limit"
					return
				}
				reply(call, call.Function.Name, domainResult(value, invokeErr))
				continue
			}
			if call.Function.Name != "execute_python" {
				reply(call, call.Function.Name, `{"error":"unknown tool; only execute_python and submit_answer are available"}`)
				continue
			}
			source, sourceErr := validateExecuteArguments(call.Function.Arguments)
			if sourceErr != nil {
				reply(call, "execute_python", jsonError(sourceErr))
				continue
			}
			execCtx, cancel := context.WithTimeout(ctx, codeExecutionTimeout)
			startedCode := time.Now()
			row.ExecutionAttempts++
			output, runErr := runner.Run(execCtx, source, nil)
			row.PysolateNS += time.Since(startedCode).Nanoseconds()
			cancel()
			if runtime.toolLimit {
				row.CompletionStatus = "tool_limit"
				return
			}
			if runErr != nil {
				reply(call, "execute_python", executionResult(nil, output.Stdout, runErr))
				continue
			}
			if len(output.Value)+len(output.Stdout) > maxToolResultBytes {
				reply(call, "execute_python", `{"error":"Python output exceeds the result bound"}`)
				continue
			}
			reply(call, "execute_python", executionResult(output.Value, output.Stdout, nil))
		}
	}
	if protocolFailure {
		row.CompletionStatus = "protocol_error"
	} else if runtime.toolLimit {
		row.CompletionStatus = "tool_limit"
	} else {
		row.CompletionStatus = "turn_limit"
	}
	return
}

func boolPtr(value bool) *bool { return &value }
func toolMessage(call ToolCall, name, content string) Message {
	return Message{Role: "tool", Name: name, ToolCallID: call.ID, Content: content}
}
func jsonError(err error) string {
	if err == nil {
		return `{"error":"unknown error"}`
	}
	encoded, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(encoded)
}
func domainResult(value any, err error) string {
	if err != nil {
		return jsonError(err)
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return jsonError(marshalErr)
	}
	return string(encoded)
}
func executionResult(value json.RawMessage, stdout string, err error) string {
	if err != nil {
		encoded, marshalErr := json.Marshal(map[string]any{"error": err.Error(), "stdout": stdout})
		if marshalErr != nil {
			return jsonError(err)
		}
		return string(encoded)
	}
	if value == nil {
		value = json.RawMessage("null")
	}
	encoded, marshalErr := json.Marshal(map[string]any{"value": json.RawMessage(value), "stdout": stdout})
	if marshalErr != nil {
		return jsonError(marshalErr)
	}
	return string(encoded)
}

func validateExecuteArguments(raw string) (string, error) {
	obj, err := exactObject(json.RawMessage(raw), "source")
	if err != nil {
		return "", errors.New("execute_python arguments must be exactly {source:string}")
	}
	source, err := stringField(obj, "source")
	if err != nil {
		return "", errors.New("execute_python source must be a string")
	}
	if len(source) > maxSourceBytes {
		return "", errors.New("execute_python source exceeds the bound")
	}
	return source, nil
}

func prepareRunner(ctx context.Context, wasm []byte, task taskCase, host *episodeHost) (*pysolate.Runner, int64, error) {
	fixture := task.NewEpisode()
	started := time.Now()
	runner, err := pysolate.NewPrepared(ctx, wasm, fixture.manifest(host))
	return runner, time.Since(started).Nanoseconds(), err
}

func selectedTasks(cases string) ([]taskCase, error) {
	catalog := allTasks()
	names := []string{"lookup", "paginated_sum", "join", "transient"}
	if strings.TrimSpace(cases) != "" {
		names = strings.Split(cases, ",")
	}
	selected := make([]taskCase, 0, len(names))
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		task, ok := catalog[name]
		if !ok {
			return nil, fmt.Errorf("unknown case %q", name)
		}
		seen[name] = true
		selected = append(selected, task)
	}
	if len(selected) == 0 {
		return nil, errors.New("no cases selected")
	}
	return selected, nil
}

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "OpenAI-compatible Chat Completions base URL")
	model := flag.String("model", defaultModel, "requested model name")
	keyEnv := flag.String("api-key-env", defaultKeyEnv, "environment variable containing the API key: OPENAI_API_KEY or DEEPSEEK_API_KEY")
	guest := flag.String("guest", defaultGuest, "Pysolate Guest artifact")
	outPath := flag.String("out", "", "new JSONL output path; never overwritten")
	cases := flag.String("cases", "", "optional comma-separated cases: lookup,paginated_sum,join,transient")
	repeats := flag.Int("repeats", 2, "number of paired repeats (4 cases x 2 arms x 2 repeats = 16 episodes)")
	maxTurns := flag.Int("max-turns", defaultMaxTurns, "maximum model turns per episode")
	maxRequests := flag.Int("max-model-requests", defaultMaxRequests, "shared campaign model-request cap, at most 96")
	flag.Parse()
	if *outPath == "" || *repeats <= 0 || *maxTurns <= 0 || *maxRequests <= 0 || *maxRequests > maxCampaignRequests {
		fmt.Fprintln(os.Stderr, "agent-eval: require -out, positive -repeats/-max-turns, and 1 <= -max-model-requests <= 96")
		os.Exit(2)
	}
	if *keyEnv != "OPENAI_API_KEY" && *keyEnv != "DEEPSEEK_API_KEY" {
		fmt.Fprintln(os.Stderr, "agent-eval: -api-key-env must be OPENAI_API_KEY or DEEPSEEK_API_KEY")
		os.Exit(2)
	}
	selected, err := selectedTasks(*cases)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-eval:", err)
		os.Exit(2)
	}
	apiKey := os.Getenv(*keyEnv)
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "agent-eval: selected API-key environment variable is empty")
		os.Exit(2)
	}
	output, err := os.OpenFile(filepath.Clean(*outPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-eval: create output:", err)
		os.Exit(2)
	}
	defer output.Close()
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-eval: read Guest:", err)
		os.Exit(2)
	}
	provider := &OpenAIProvider{Client: &http.Client{}, BaseURL: *baseURL, APIKey: apiKey, Model: *model, MaxResponseBytes: maxResponseBodyBytes}
	campaignCtx, cancel := context.WithTimeout(context.Background(), time.Duration((*repeats)*len(selected)*2)*episodeTimeout)
	defer cancel()
	budget := &requestBudget{limit: *maxRequests}
	prepared := make(map[string]*pysolate.Runner)
	setup := make(map[string]int64)
	hosts := make(map[string]*episodeHost)
	for _, task := range selected {
		host := &episodeHost{}
		runner, setupNS, prepareErr := prepareRunner(campaignCtx, wasm, task, host)
		if prepareErr != nil {
			fmt.Fprintln(os.Stderr, "agent-eval: prepare", task.ID, ":", prepareErr)
			os.Exit(1)
		}
		prepared[task.ID], setup[task.ID], hosts[task.ID] = runner, setupNS, host
		defer runner.Close(context.Background())
	}
	encoder := json.NewEncoder(output)
	sequence := 0
	for repeat := 1; repeat <= *repeats; repeat++ {
		for _, task := range selected {
			sequence++
			order := []string{"direct", "code"}
			if repeat%2 == 0 {
				order = []string{"code", "direct"}
			}
			if budget.remaining() < 2 {
				for _, arm := range order {
					row := initialRow(task.ID, arm, repeat, sequence, *model, setup[task.ID])
					row.CompletionStatus = "skipped"
					row.UsageComplete = false
					row.SetupNS = 0
					if arm == "code" {
						row.SetupNS = setup[task.ID]
					}
					if err := encoder.Encode(row); err != nil {
						fmt.Fprintln(os.Stderr, "agent-eval: write:", err)
						os.Exit(1)
					}
				}
				continue
			}
			for _, arm := range order {
				rowCtx, rowCancel := context.WithTimeout(campaignCtx, episodeTimeout)
				reserve := 0
				if arm == order[0] {
					reserve = 1
				}
				row := runEpisode(rowCtx, provider, budget, task, repeat, sequence, arm, prepared[task.ID], setup[task.ID], hosts[task.ID], *model, *maxTurns, reserve)
				rowCancel()
				if arm == "direct" {
					row.SetupNS = 0
				}
				if err := encoder.Encode(row); err != nil {
					fmt.Fprintln(os.Stderr, "agent-eval: write:", err)
					os.Exit(1)
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "agent-eval: wrote %s (%d model requests)\n", filepath.Clean(*outPath), budget.count())
}
