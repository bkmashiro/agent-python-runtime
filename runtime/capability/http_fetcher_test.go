package capability_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestHTTPFetcherUsesHostHeadersAndDoesNotFollowRedirects(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ok":
			if request.Header.Get("Authorization") != "Host secret" {
				t.Errorf("Host header missing")
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true}`))
		case "/redirect":
			http.Redirect(writer, request, serverURL(request)+"/unexpected", http.StatusFound)
		case "/unexpected":
			redirected = true
			writer.WriteHeader(http.StatusTeapot)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	fetcher := capability.NewHTTPFetcher(server.Client())
	ok, err := fetcher.Fetch(context.Background(), capability.ResolvedRequest{
		URL:     server.URL + "/ok",
		Headers: map[string]string{"Authorization": "Host secret"},
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if ok.StatusCode != http.StatusOK || string(ok.Body) != `{"ok":true}` || ok.ContentType != "application/json" {
		t.Fatalf("unexpected output: %#v", ok)
	}

	redirect, err := fetcher.Fetch(context.Background(), capability.ResolvedRequest{URL: server.URL + "/redirect"}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if redirect.StatusCode != http.StatusFound || redirected {
		t.Fatalf("redirect escaped target policy: %#v redirected=%v", redirect, redirected)
	}
}

func TestHTTPFetcherEnforcesStreamingResponseCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 64)))
	}))
	defer server.Close()
	fetcher := capability.NewHTTPFetcher(server.Client())
	_, err := fetcher.Fetch(context.Background(), capability.ResolvedRequest{URL: server.URL}, 8)
	if !errors.Is(err, capability.ErrResponseTooLarge) {
		t.Fatalf("expected response cap error, got %v", err)
	}
}

func TestHTTPFetcherHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = writer.Write([]byte("late"))
	}))
	defer server.Close()
	fetcher := capability.NewHTTPFetcher(server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := fetcher.Fetch(ctx, capability.ResolvedRequest{URL: server.URL}, 1024)
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
