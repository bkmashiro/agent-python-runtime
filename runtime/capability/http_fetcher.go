package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type HTTPFetcher struct {
	client *http.Client
}

var ErrResponseTooLarge = errors.New("Host response exceeds response byte limit")

func NewHTTPFetcher(client *http.Client) *HTTPFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	isolated := *client
	isolated.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPFetcher{client: &isolated}
}

func (fetcher *HTTPFetcher) Fetch(ctx context.Context, request ResolvedRequest, maxResponseBytes uint32) (FetchOutput, error) {
	if fetcher == nil || fetcher.client == nil {
		return FetchOutput{}, errors.New("HTTP fetcher is not initialized")
	}
	if maxResponseBytes == 0 {
		return FetchOutput{}, errors.New("response byte limit is zero")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return FetchOutput{}, fmt.Errorf("create Host request: %w", err)
	}
	for name, value := range request.Headers {
		httpRequest.Header.Set(name, value)
	}
	response, err := fetcher.client.Do(httpRequest)
	if err != nil {
		return FetchOutput{}, fmt.Errorf("perform Host request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBytes)+1))
	if err != nil {
		return FetchOutput{}, fmt.Errorf("read Host response: %w", err)
	}
	if uint64(len(body)) > uint64(maxResponseBytes) {
		return FetchOutput{}, fmt.Errorf("%w: limit=%d", ErrResponseTooLarge, maxResponseBytes)
	}
	return FetchOutput{
		StatusCode:  response.StatusCode,
		Body:        body,
		ContentType: response.Header.Get("Content-Type"),
	}, nil
}
