package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type phaseLog struct {
	mutex  sync.Mutex
	phases []string
}

func (log *phaseLog) observe(observation wazeroengine.Observation) {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	log.phases = append(log.phases, observation.Phase)
}

func (log *phaseLog) reset() {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	log.phases = nil
}

func (log *phaseLog) snapshot() []string {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return append([]string(nil), log.phases...)
}

func TestSingleUsePreparedPoolSkipsRequestInitAndPreservesFreshness(t *testing.T) {
	phases := &phaseLog{}
	factory := wazeroengine.Factory{PreparedCapacity: 1, Observer: phases.observe}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	ready, ok := instance.(interface {
		PreparedReady() int
		PreparedRetainedGuestMemoryBytes() uint64
	})
	if !ok {
		t.Fatalf("prepared diagnostics unavailable: properties=%#v", instance.Properties())
	}
	if ready.PreparedReady() != 1 || ready.PreparedRetainedGuestMemoryBytes() != 128*1024*1024 {
		t.Fatalf("prepared instance was not ready: retained=%d properties=%#v", ready.PreparedRetainedGuestMemoryBytes(), instance.Properties())
	}
	if instance.Properties().ResetMode != enginecontract.ResetModeFreshInstance {
		t.Fatalf("single-use pool widened reset claim: %#v", instance.Properties())
	}

	phases.reset()
	first := runWithPrepare(
		t,
		instance,
		"prepared-pool-1",
		"leaked_execute = prepared_value\nresult = leaked_execute + 1",
		map[string]any{},
		"prepared_value = 41",
	)
	if first.Status != "ok" || first.Result != float64(42) {
		t.Fatalf("first prepared Run failed: %#v", first)
	}
	assertPreparedHitWithoutRequestInit(t, phases.snapshot())

	deadline := time.Now().Add(45 * time.Second)
	for ready.PreparedReady() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ready.PreparedReady() != 1 {
		t.Fatal("prepared pool did not refill")
	}

	phases.reset()
	second := run(t, instance, "prepared-pool-2", "result = 'prepared_value' in globals() or 'leaked_execute' in globals()", map[string]any{})
	if second.Status != "ok" || second.Result != false {
		t.Fatalf("served instance leaked into second Run: %#v", second)
	}
	assertPreparedHitWithoutRequestInit(t, phases.snapshot())
}

func TestPreparedPoolMissFallsBackToExclusiveFreshInstance(t *testing.T) {
	phases := &phaseLog{}
	hit := make(chan struct{})
	refillBlocked := make(chan struct{})
	releaseRefill := make(chan struct{})
	var hitOnce sync.Once
	var refillOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefill) }) }
	defer release()
	factory := wazeroengine.Factory{
		PreparedCapacity: 1,
		Observer: func(observation wazeroengine.Observation) {
			phases.observe(observation)
			if observation.Phase == "pool_hit" {
				hitOnce.Do(func() { close(hit) })
			}
			if observation.Phase == "pool_prepare_instantiate_guest" {
				select {
				case <-hit:
					refillOnce.Do(func() { close(refillBlocked) })
					<-releaseRefill
				default:
				}
			}
		},
	}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	phases.reset()
	firstRequest := encodedRequest(t, "pool-exclusive-1", "exclusive_marker = 1\nwhile True:\n    pass")
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := instance.Run(firstContext, firstRequest, "")
		firstResult <- err
	}()
	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		t.Fatal("first Run did not check out prepared instance")
	}
	select {
	case <-refillBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("prepared refill did not reach deterministic block")
	}
	second := run(t, instance, "pool-exclusive-2", "result = 'exclusive_marker' in globals()", map[string]any{})
	if second.Status != "ok" || second.Result != false {
		t.Fatalf("pool miss shared an in-flight instance: %#v", second)
	}
	release()
	cancelFirst()
	if err := <-firstResult; err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("first in-flight Run did not cancel cleanly: %v", err)
	}
	seenHit, seenMiss := false, false
	for _, phase := range phases.snapshot() {
		seenHit = seenHit || phase == "pool_hit"
		seenMiss = seenMiss || phase == "pool_miss"
	}
	if !seenHit || !seenMiss {
		t.Fatalf("expected hit and non-blocking miss fallback: %v", phases.snapshot())
	}
}

func TestPreparedPoolCloseCancelsAndJoinsRefill(t *testing.T) {
	factory := wazeroengine.Factory{PreparedCapacity: 1}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	ready := instance.(interface {
		PreparedReady() int
		PreparedRetainedGuestMemoryBytes() uint64
	})
	response := run(t, instance, "prepared-close", "result = 42", map[string]any{})
	if response.Status != "ok" || response.Result != float64(42) {
		t.Fatalf("warm Run failed before close: %#v", response)
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := instance.Close(closeContext); err != nil {
		t.Fatalf("close failed while refill was active: %v", err)
	}
	if ready.PreparedReady() != 0 || ready.PreparedRetainedGuestMemoryBytes() != 0 {
		t.Fatalf("prepared state remained after close: ready=%d retained=%d", ready.PreparedReady(), ready.PreparedRetainedGuestMemoryBytes())
	}
}

func TestPreparedPoolCreatesFreshBrokerForEveryHostCallRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"value":42}`))
	}))
	defer server.Close()
	factory := wazeroengine.Factory{
		PreparedCapacity: 1,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			grant := capability.Grant{
				Name: capability.FetchManyCapability, MaxCalls: 1, MaxRequestsPerCall: 1,
				MaxTotalRequests: 1, MaxConcurrency: 1, MaxResponseBytes: 1024,
				PerRequestTimeout: time.Second,
				Targets:           map[string]capability.TargetGrant{"fixture": {BaseURL: server.URL}},
			}
			return capability.NewBroker(capability.Config{
				RunIdentity: "prepared-host-run",
				Grants:      map[string]capability.Grant{grant.Name: grant},
			}, capability.NewHTTPFetcher(server.Client()))
		},
	}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	ready := instance.(interface{ PreparedReady() int })

	first := run(t, instance, "prepared-host-1", `
from agent_runtime import tools
leaked_host_success = 1
items = tools.fetch_many([{"request_id":"ok","target":"fixture","path":"/value"}])
result = {"status": items[0]["status"]}
`, map[string]any{})
	if first.Status != "ok" || first.Result.(map[string]any)["status"] != "ok" || first.Metrics["capability_calls"] != float64(1) || len(first.Receipts) != 1 {
		t.Fatalf("prepared Host success failed: %#v", first)
	}
	waitPrepared(t, ready)

	second := run(t, instance, "prepared-host-2", `
from agent_runtime import tools
prior_leaked = "leaked_host_success" in globals()
leaked_host_denied = 1
items = tools.fetch_many([{"request_id":"denied","target":"missing","path":"/value"}])
result = {"prior_leaked": prior_leaked, "status": items[0]["status"]}
`, map[string]any{})
	secondResult := second.Result.(map[string]any)
	if second.Status != "ok" || secondResult["prior_leaked"] != false || secondResult["status"] != "denied" || second.Metrics["capability_calls"] != float64(1) || len(second.Receipts) != 1 {
		t.Fatalf("prepared Host denial was not isolated: %#v", second)
	}
	waitPrepared(t, ready)

	third := run(t, instance, "prepared-host-3", `result = "leaked_host_denied" in globals()`, map[string]any{})
	if third.Status != "ok" || third.Result != false || third.Metrics["capability_calls"] != float64(0) || len(third.Receipts) != 0 {
		t.Fatalf("broker or guest evidence leaked across prepared Runs: %#v", third)
	}
}

func TestPreparedPoolDiscardsEveryFailurePath(t *testing.T) {
	config := runtime.DefaultRunConfig()
	config.Timeout = 60 * time.Second
	config.MaxResponseBytes = 1024
	factory := wazeroengine.Factory{PreparedCapacity: 1}
	instance := newEngineWithFactory(t, config, factory)
	ready := instance.(interface{ PreparedReady() int })

	structured := run(t, instance, "pool-structured", "leaked_structured = 1\nraise RuntimeError('expected')", map[string]any{})
	if structured.Status != "error" {
		t.Fatalf("structured failure missing: %#v", structured)
	}
	assertFreshAfterPoolFailure(t, instance, ready, "leaked_structured")

	timeoutRequest := encodedRequest(t, "pool-timeout", "leaked_timeout = 1\nwhile True:\n    pass")
	timeoutContext, timeoutCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer timeoutCancel()
	if _, err := instance.Run(timeoutContext, timeoutRequest, ""); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("timeout did not fail closed: %v", err)
	}
	assertFreshAfterPoolFailure(t, instance, ready, "leaked_timeout")

	cancelRequest := encodedRequest(t, "pool-cancel", "leaked_cancel = 1\nwhile True:\n    pass")
	cancelContext, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := instance.Run(cancelContext, cancelRequest, ""); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation did not fail closed: %v", err)
	}
	assertFreshAfterPoolFailure(t, instance, ready, "leaked_cancel")

	oversizedRequest := encodedRequest(t, "pool-oversized", "leaked_oversized = 1\nresult = 'x' * 10000")
	if _, err := instance.Run(context.Background(), oversizedRequest, ""); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("oversized response did not fail closed: %v", err)
	}
	assertFreshAfterPoolFailure(t, instance, ready, "leaked_oversized")
}

func encodedRequest(t *testing.T, runID, code string) []byte {
	t.Helper()
	request, err := json.Marshal(map[string]any{"run_id": runID, "code": code, "inputs": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func waitPrepared(t *testing.T, ready interface{ PreparedReady() int }) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for ready.PreparedReady() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ready.PreparedReady() != 1 {
		t.Fatal("prepared pool did not refill")
	}
}

func assertFreshAfterPoolFailure(t *testing.T, instance enginecontract.Runner, ready interface{ PreparedReady() int }, name string) {
	t.Helper()
	waitPrepared(t, ready)
	response := run(t, instance, "check-"+name, "result = "+repr(name)+" in globals()", map[string]any{})
	if response.Status != "ok" || response.Result != false {
		t.Fatalf("%s leaked after failed Run: %#v", name, response)
	}
	waitPrepared(t, ready)
}

func repr(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func assertPreparedHitWithoutRequestInit(t *testing.T, phases []string) {
	t.Helper()
	hit := false
	for _, phase := range phases {
		if phase == "pool_hit" {
			hit = true
		}
		if phase == "runtime_init" || phase == "_initialize" {
			t.Fatalf("request path repeated trusted initialization: %v", phases)
		}
	}
	if !hit {
		t.Fatalf("prepared pool was not used: %v", phases)
	}
}
