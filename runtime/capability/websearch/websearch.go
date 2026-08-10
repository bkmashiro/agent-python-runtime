// Package websearch defines the Host-owned typed web-search capability.
// Providers remain Host-side; Guest input cannot select credentials, endpoints,
// headers, or arbitrary source policy.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

const (
	SearchToolID   = "web.search"
	HandlerVersion = "web-search-v1"

	hardMaxQueryBytes = 512
	hardMaxResults    = 10
)

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var (
	ErrSearchDenied         = errors.New("web search denied")
	ErrSearchProviderFailed = errors.New("web search provider failed")
	ErrSearchResultInvalid  = errors.New("web search provider result invalid")
)

const searchInputSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["query","max_results"],
  "properties":{
    "query":{"type":"string","minLength":1,"maxLength":512},
    "max_results":{"type":"integer","minimum":1,"maximum":10}
  }
}`

const searchOutputSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["query","provider","observed_at","results"],
  "properties":{
    "query":{"type":"string"},
    "provider":{"type":"string"},
    "observed_at":{"type":"string"},
    "results":{
      "type":"array","maxItems":10,
      "items":{
        "type":"object","additionalProperties":false,
        "required":["rank","title","url","snippet","source","published_at","observed_at"],
        "properties":{
          "rank":{"type":"integer","minimum":1,"maximum":10},
          "title":{"type":"string"},
          "url":{"type":"string"},
          "snippet":{"type":"string"},
          "source":{"type":"string"},
          "published_at":{"type":"string"},
          "observed_at":{"type":"string"}
        }
      }
    }
  }
}`

type ProviderRequest struct {
	Query          string
	MaxResults     uint32
	AllowedSources []string
}

type ProviderResult struct {
	Title       string
	URL         string
	Snippet     string
	Source      string
	PublishedAt string
}

type ProviderPage struct {
	ObservedAt time.Time
	Results    []ProviderResult
}

type Provider interface {
	Search(context.Context, ProviderRequest) (ProviderPage, error)
}

type SearchResult struct {
	Rank        uint32 `json:"rank"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
	ObservedAt  string `json:"observed_at"`
}

type SearchPage struct {
	Query      string         `json:"query"`
	Provider   string         `json:"provider"`
	ObservedAt string         `json:"observed_at"`
	Results    []SearchResult `json:"results"`
}

type Config struct {
	Provider       Provider
	ProviderID     string
	AllowedSources []string
	MaxQueryBytes  uint32
	MaxResults     uint32
}

type Adapter struct {
	provider       Provider
	providerID     string
	allowedSources []string
	allowedSet     map[string]struct{}
	maxQueryBytes  uint32
	maxResults     uint32
}

func NewAdapter(config Config) (*Adapter, error) {
	if config.Provider == nil || !identityPattern.MatchString(config.ProviderID) || config.MaxQueryBytes == 0 || config.MaxQueryBytes > hardMaxQueryBytes ||
		config.MaxResults == 0 || config.MaxResults > hardMaxResults || len(config.AllowedSources) == 0 || len(config.AllowedSources) > 32 {
		return nil, ErrSearchDenied
	}
	allowed := make(map[string]struct{}, len(config.AllowedSources))
	ordered := make([]string, len(config.AllowedSources))
	for index, source := range config.AllowedSources {
		if !identityPattern.MatchString(source) {
			return nil, ErrSearchDenied
		}
		if _, duplicate := allowed[source]; duplicate {
			return nil, ErrSearchDenied
		}
		allowed[source] = struct{}{}
		ordered[index] = source
	}
	return &Adapter{provider: config.Provider, providerID: config.ProviderID, allowedSources: ordered, allowedSet: allowed, maxQueryBytes: config.MaxQueryBytes, maxResults: config.MaxResults}, nil
}

func HandlerSpecs(adapter *Adapter) ([]capability.HandlerSpec, error) {
	if adapter == nil {
		return nil, ErrSearchDenied
	}
	return []capability.HandlerSpec{{ToolID: SearchToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema), Handler: adapter}}, nil
}

func ToolGrants(policyVersion string, maxCalls uint32) (map[string]capability.ToolGrant, error) {
	if !identityPattern.MatchString(policyVersion) || maxCalls == 0 || maxCalls > 1024 {
		return nil, ErrSearchDenied
	}
	return map[string]capability.ToolGrant{SearchToolID: {
		ToolID: SearchToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only",
		Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls,
	}}, nil
}

func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !identityPattern.MatchString(grantVersion) {
		return nil, nil, ErrSearchDenied
	}
	tool := toolcatalog.DiscoveredTool{
		ToolID: SearchToolID, ServerID: "web-search", Name: "web_search",
		Description:    "Search a Host-scoped provider with bounded results and source provenance.",
		HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema),
	}
	grant := toolcatalog.Grant{ToolID: SearchToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls}
	return []toolcatalog.DiscoveredTool{tool}, map[string]toolcatalog.Grant{SearchToolID: grant}, nil
}

type searchArguments struct {
	Query      string `json:"query"`
	MaxResults uint32 `json:"max_results"`
}

func (adapter *Adapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.ToolID != SearchToolID || ctx.Err() != nil {
		return nil, classified(ErrSearchDenied)
	}
	var arguments searchArguments
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return nil, classified(ErrSearchDenied)
	}
	query := strings.TrimSpace(arguments.Query)
	if query == "" || uint32(len(query)) > adapter.maxQueryBytes || arguments.MaxResults == 0 || arguments.MaxResults > adapter.maxResults {
		return nil, classified(ErrSearchDenied)
	}
	providerPage, err := adapter.provider.Search(ctx, ProviderRequest{Query: query, MaxResults: arguments.MaxResults, AllowedSources: append([]string(nil), adapter.allowedSources...)})
	if err != nil {
		return nil, classified(err)
	}
	page, err := adapter.validateProviderPage(query, arguments.MaxResults, providerPage)
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(page)
}

func (adapter *Adapter) validateProviderPage(query string, maximum uint32, providerPage ProviderPage) (SearchPage, error) {
	if providerPage.ObservedAt.IsZero() || uint32(len(providerPage.Results)) > maximum {
		return SearchPage{}, ErrSearchResultInvalid
	}
	observedAt := providerPage.ObservedAt.UTC().Format(time.RFC3339)
	results := make([]SearchResult, len(providerPage.Results))
	for index, result := range providerPage.Results {
		parsed, err := url.Parse(result.URL)
		_, sourceAllowed := adapter.allowedSet[result.Source]
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || !sourceAllowed ||
			result.Title == "" || len(result.Title) > 512 || result.Snippet == "" || len(result.Snippet) > 4096 || len(result.PublishedAt) > 64 {
			return SearchPage{}, ErrSearchResultInvalid
		}
		results[index] = SearchResult{
			Rank: uint32(index + 1), Title: result.Title, URL: result.URL, Snippet: result.Snippet,
			Source: result.Source, PublishedAt: result.PublishedAt, ObservedAt: observedAt,
		}
	}
	return SearchPage{Query: query, Provider: adapter.providerID, ObservedAt: observedAt, Results: results}, nil
}

func classified(err error) error {
	switch {
	case errors.Is(err, ErrSearchDenied):
		return capability.NewHandlerFailure("search_denied", "Host search policy denied the request", err)
	case errors.Is(err, ErrSearchResultInvalid):
		return capability.NewHandlerFailure("search_result_invalid", "Host search provider returned an invalid bounded result", err)
	default:
		return capability.NewHandlerFailure("search_provider_failed", "Host search provider failed", err)
	}
}
