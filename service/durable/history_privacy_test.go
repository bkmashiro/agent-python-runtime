package durableservice

import (
	"context"
	"errors"
	"github.com/bkmashiro/agent-python-runtime/durable"
	"net/http"
	"net/http/httptest"
	"testing"
)

type failingHistoryRuntime struct{ *fakeRuntime }

func (r failingHistoryRuntime) ReplayHistory(context.Context, string, durable.ReplayHistoryOptions) (durable.ReplayHistoryPage, error) {
	return durable.ReplayHistoryPage{}, errors.New("database path and secret payload")
}
func TestHistoryPaginationRejectsMalformedInputs(t *testing.T) {
	for _, query := range []string{"limit=-1", "limit=", "limit=1&limit=2", "from_sequence=4294967296", "limit=%zz"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.URL.RawQuery = query
		if _, err := parseHistoryOptions(request); err == nil {
			t.Fatalf("accepted %q", query)
		}
	}
}

func TestHistoryInternalErrorsDoNotLeak(t *testing.T) {
	server, err := New(failingHistoryRuntime{newFakeRuntime()}, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	status, body := durableRequest(t, httpServer.Client(), http.MethodGet, httpServer.URL+"/v1/durable/runs/run/history", nil)
	if status != 500 || body["error"] != "unable to read durable history" {
		t.Fatalf("status=%d body=%v", status, body)
	}
}
