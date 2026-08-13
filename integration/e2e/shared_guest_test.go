package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestIdenticalLogicalInvocationsShareOneRealFreshGuest(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{
		"run_id": "shared-physical-guest",
		"code":   "result = {'value': inputs['value'] + 1}",
		"inputs": map[string]any{"value": 41},
	})
	if err != nil {
		t.Fatal(err)
	}

	flights := agentfunction.NewFlightGroup()
	functionEngine := agentfunction.Engine{Flights: flights}
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(ctx context.Context) (string, engine.Runner, error) {
			id := physical.Add(1)
			deadline := time.Now().Add(5 * time.Second)
			for flights.Stats().Waiters != 7 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runtimeconfig.DefaultRunConfig())
			return fmt.Sprintf("physical-%d", id), runner, err
		},
		Request: request, MaxResultBytes: 1024,
		DecodeResult: decodeSuccessfulGuestResult,
	}

	const logical = 8
	results := make(chan agentfunction.Result, logical)
	errorsChannel := make(chan error, logical)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(logical)
	for range logical {
		go func() {
			defer wait.Done()
			<-start
			result, err := functionEngine.Execute(context.Background(), sharedGuestInvocation(), compute.Run)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	leaders, waiters := 0, 0
	for result := range results {
		if string(result.Value) != `{"value":42}` || result.PhysicalExecutionID != "physical-1" {
			t.Fatalf("result=%+v", result)
		}
		switch result.Disposition {
		case agentfunction.Leader:
			leaders++
		case agentfunction.Waiter:
			waiters++
		}
	}
	if physical.Load() != 1 || leaders != 1 || waiters != logical-1 {
		t.Fatalf("physical=%d leaders=%d waiters=%d stats=%+v", physical.Load(), leaders, waiters, flights.Stats())
	}
}

func decodeSuccessfulGuestResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	if response.Status != "ok" || len(response.Result) == 0 {
		return nil, errors.New("Guest result is not publishable")
	}
	return response.Result, nil
}

func sharedGuestInvocation() agentfunction.Invocation {
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: hashCharacter('1'), FunctionSourceSHA256: hashCharacter('2'),
		ArtifactSHA256: hashCharacter('3'), ExecutionProfileSHA256: hashCharacter('4'),
		ImportClosureSHA256: hashCharacter('5'), CanonicalInputs: []byte(`{"value":41}`),
		ImmutableRootSHA256: []string{hashCharacter('6')}, DeterministicSettingsSHA256: hashCharacter('7'),
		OutputSchemaSHA256: hashCharacter('8'), PrivacyPartition: "shared-guest-test", PolicyEpochSHA256: hashCharacter('9'),
	}
}
