// Package fakeweb provides a deterministic, network-free Provider fixture.
// It proves websearch adapter behavior, not compatibility with a real provider.
package fakeweb

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/websearch"
)

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var (
	ErrFixtureDenied  = errors.New("fake web fixture denied")
	ErrProviderFailed = errors.New("fake web provider failed")
)

type Document = websearch.ProviderResult
type SearchPage = websearch.SearchPage

type Provider struct {
	mu         sync.Mutex
	documents  []Document
	observedAt time.Time
	searches   uint32
	failNext   bool
}

func NewProvider(documents []Document, observedAt time.Time) (*Provider, error) {
	if observedAt.IsZero() || len(documents) == 0 || len(documents) > 256 {
		return nil, ErrFixtureDenied
	}
	cloned := make([]Document, len(documents))
	for index, document := range documents {
		parsed, err := url.Parse(document.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".invalid") ||
			document.Title == "" || len(document.Title) > 512 || document.Snippet == "" || len(document.Snippet) > 4096 ||
			!identityPattern.MatchString(document.Source) || len(document.PublishedAt) > 64 {
			return nil, ErrFixtureDenied
		}
		cloned[index] = document
	}
	return &Provider{documents: cloned, observedAt: observedAt.UTC()}, nil
}

func (provider *Provider) SearchCount() uint32 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.searches
}

// FailNext is a Host-only fixture control.
func (provider *Provider) FailNext() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.failNext = true
}

func (provider *Provider) Search(ctx context.Context, request websearch.ProviderRequest) (websearch.ProviderPage, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.searches++
	if ctx.Err() != nil {
		return websearch.ProviderPage{}, ctx.Err()
	}
	if provider.failNext {
		provider.failNext = false
		return websearch.ProviderPage{}, ErrProviderFailed
	}
	allowed := make(map[string]struct{}, len(request.AllowedSources))
	for _, source := range request.AllowedSources {
		allowed[source] = struct{}{}
	}
	terms := strings.Fields(strings.ToLower(request.Query))
	results := make([]websearch.ProviderResult, 0, request.MaxResults)
	for _, document := range provider.documents {
		if _, ok := allowed[document.Source]; !ok {
			continue
		}
		haystack := strings.ToLower(document.Title + " " + document.Snippet)
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		results = append(results, document)
		if uint32(len(results)) == request.MaxResults {
			break
		}
	}
	return websearch.ProviderPage{ObservedAt: provider.observedAt, Results: results}, nil
}
