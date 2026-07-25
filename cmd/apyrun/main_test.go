package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type fakeRunner struct {
	response []byte
	err      error
	request  []byte
	closed   bool
}

func (runner *fakeRunner) Run(_ context.Context, request []byte, _ string) ([]byte, error) {
	runner.request = append([]byte(nil), request...)
	return append([]byte(nil), runner.response...), runner.err
}
func (runner *fakeRunner) Close(context.Context) error { runner.closed = true; return nil }
func (runner *fakeRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}
}

type fakeFactory struct {
	runner *fakeRunner
	config runtimeconfig.RunConfig
	wasm   []byte
}

func (factory *fakeFactory) Name() string { return "fake" }
func (factory *fakeFactory) New(_ context.Context, wasm []byte, config runtimeconfig.RunConfig) (engine.Runner, error) {
	factory.wasm = append([]byte(nil), wasm...)
	factory.config = config
	return factory.runner, nil
}

func TestProductionDependenciesUseFailClosedEgressClient(t *testing.T) {
	dependencies := productionDependencies()
	transport, ok := dependencies.httpClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.DialContext == nil {
		t.Fatalf("production CLI lacks fail-closed egress transport: %#v", dependencies.httpClient.Transport)
	}
}

func TestDecodeOperatorConfigIsStrictAndHostOwned(t *testing.T) {
	config, err := decodeOperatorConfig([]byte(`{
		"timeout_ms": 1500,
		"max_request_bytes": 4096,
		"max_response_bytes": 8192,
		"memory_limit_pages": 128,
		"prepared_capacity": 2,
		"fetch_many": {
			"max_calls": 2,
			"max_requests_per_call": 5,
			"max_total_requests": 8,
			"max_concurrency": 3,
			"max_response_bytes": 2048,
			"per_request_timeout_ms": 500,
			"targets": {
				"catalog": {
					"base_url": "https://catalog.example",
					"headers": {"Authorization": "Bearer host-owned"}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	runConfig, grant, err := config.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if runConfig.Timeout.Milliseconds() != 1500 || runConfig.MaxRequestBytes != 4096 || runConfig.MaxResponseBytes != 8192 || runConfig.MemoryLimitPages != 128 {
		t.Fatalf("unexpected run config: %#v", runConfig)
	}
	if config.PreparedCapacity != 2 {
		t.Fatalf("prepared capacity was not decoded: %#v", config)
	}
	if grant == nil || grant.MaxConcurrency != 3 || grant.Targets["catalog"].Headers["Authorization"] != "Bearer host-owned" {
		t.Fatalf("Host grant was not resolved: %#v", grant)
	}

	for name, input := range map[string]string{
		"unknown":  `{"unknown": true}`,
		"trailing": `{}` + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOperatorConfig([]byte(input)); err == nil {
				t.Fatal("expected strict config rejection")
			}
		})
	}
}

func TestExecuteUsesSeparateConfigAndUntrustedRequest(t *testing.T) {
	runner := &fakeRunner{response: []byte(`{"status":"ok","result":7,"error":null,"metrics":{},"receipts":[]}`)}
	factory := &fakeFactory{runner: runner}
	files := map[string][]byte{
		"guest.wasm": []byte("wasm-fixture"),
		"host.json":  []byte(`{"timeout_ms":2000,"max_response_bytes":1024}`),
	}
	deps := dependencies{
		readFile: func(path string) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				return nil, errors.New("not found")
			}
			return value, nil
		},
		newIdentity: func() (string, error) { return "host-run-fixed", nil },
		newFactory: func(_ operatorConfig, identity string, _ *http.Client) (engine.Factory, error) {
			if identity != "host-run-fixed" {
				t.Fatalf("unexpected Host identity %q", identity)
			}
			return factory, nil
		},
	}
	request := `{"run_id":"guest-label","code":"result = 7","inputs":{}}`
	var stdout, stderr strings.Builder
	exit := execute([]string{"-artifact", "guest.wasm", "-config", "host.json"}, strings.NewReader(request), &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if stdout.String() != string(runner.response)+"\n" || stderr.Len() != 0 {
		t.Fatalf("unexpected streams stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if string(factory.wasm) != "wasm-fixture" || string(runner.request) != request || !runner.closed {
		t.Fatalf("runner lifecycle not respected: factory=%#v runner=%#v", factory, runner)
	}
	if factory.config.Timeout.Milliseconds() != 2000 || factory.config.MaxResponseBytes != 1024 {
		t.Fatalf("operator bounds not forwarded: %#v", factory.config)
	}
}

func TestExecuteRejectsAuthorityFieldsInRunRequestBeforeBackend(t *testing.T) {
	called := false
	deps := dependencies{
		readFile: func(path string) ([]byte, error) {
			if path == "guest.wasm" {
				return []byte("wasm"), nil
			}
			return []byte(`{}`), nil
		},
		newIdentity: func() (string, error) { return "host-run", nil },
		newFactory: func(operatorConfig, string, *http.Client) (engine.Factory, error) {
			called = true
			return nil, errors.New("must not be called")
		},
	}
	request := `{"run_id":"guest","code":"result=1","inputs":{},"fetch_many":{"targets":{"evil":{"base_url":"https://evil.example"}}}}`
	var stdout, stderr strings.Builder
	exit := execute([]string{"-artifact", "guest.wasm"}, strings.NewReader(request), &stdout, &stderr, deps)
	if exit == 0 || called || stdout.Len() != 0 {
		t.Fatalf("authority-bearing request reached backend: exit=%d called=%v stdout=%q", exit, called, stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid RunRequest") {
		t.Fatalf("missing bounded diagnostic: %q", stderr.String())
	}
}

func TestExecuteEnforcesResponseBoundOutsideBackend(t *testing.T) {
	runner := &fakeRunner{response: []byte(strings.Repeat("x", 17))}
	factory := &fakeFactory{runner: runner}
	deps := dependencies{
		readFile: func(path string) ([]byte, error) {
			if path == "guest.wasm" {
				return []byte("wasm"), nil
			}
			return []byte(`{"max_response_bytes":16}`), nil
		},
		newIdentity: func() (string, error) { return "host-run", nil },
		newFactory:  func(operatorConfig, string, *http.Client) (engine.Factory, error) { return factory, nil },
	}
	var stdout, stderr strings.Builder
	exit := execute([]string{"-artifact", "guest.wasm", "-config", "host.json"}, strings.NewReader(`{"run_id":"guest","code":"result=1","inputs":{}}`), &stdout, &stderr, deps)
	if exit == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "response exceeds configured bounds") {
		t.Fatalf("response bound not enforced: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestWazeroFactoryPersistsBuiltinFetchTransactionJournal(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "transactions.db")
	config := operatorConfig{TransactionJournalPath: journalPath, FetchMany: &fetchManyConfig{
		MaxCalls: 1, MaxRequestsPerCall: 1, MaxTotalRequests: 1, MaxConcurrency: 1,
		MaxResponseBytes: 1024, PerRequestTimeoutMS: 1000,
		Targets: map[string]targetConfig{"api": {BaseURL: "https://example.test"}},
	}}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: http.NoBody, Request: request}, nil
	})}
	built, err := newWazeroFactory(config, "host-run", client)
	if err != nil {
		t.Fatal(err)
	}
	factory, ok := built.(wazeroengine.Factory)
	if !ok || factory.BrokerFactory == nil {
		t.Fatalf("factory=%T", built)
	}
	broker, err := factory.BrokerFactory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"fetch:1","capability":"fetch_many","arguments":{"requests":[{"request_id":"one","target":"api","path":"item"}]}}`))
	if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	receipts := broker.Receipts()
	if len(receipts) != 1 || receipts[0].TransactionID == "" {
		t.Fatalf("receipts=%+v", receipts)
	}
	transactionID := receipts[0].TransactionID
	if err := broker.FinalizeRun(context.Background(), true, "success"); err != nil {
		t.Fatal(err)
	}
	if err := broker.CloseJournal(); err != nil {
		t.Fatal(err)
	}
	ledger, err := transaction.OpenSQLiteLedger(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	coordinator := transaction.NewCoordinator(ledger, transaction.RandomIDSource{}, time.Now, nil)
	inspection, err := coordinator.Inspect(transactionID, nil)
	if err != nil || inspection.Transaction.State != transaction.TransactionCommitted || len(inspection.Operations) != 1 || len(inspection.Attempts) != 1 {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestExecuteDoesNotEchoMalformedConfigCredential(t *testing.T) {
	const secret = "secret-do-not-print"
	deps := dependencies{
		readFile: func(path string) ([]byte, error) {
			if path == "guest.wasm" {
				return []byte("wasm"), nil
			}
			return []byte(`{"api_key":"` + secret + `"}`), nil
		},
	}
	var stdout, stderr strings.Builder
	exit := execute([]string{"-artifact", "guest.wasm", "-config", "host.json"}, strings.NewReader(`{"run_id":"guest","code":"result=1","inputs":{}}`), &stdout, &stderr, deps)
	if exit == 0 || strings.Contains(stderr.String(), secret) || stdout.Len() != 0 {
		t.Fatalf("credential leaked: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
