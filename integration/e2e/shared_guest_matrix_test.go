package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type sharedMatrixRow struct {
	Logical       int    `json:"logical_invocations"`
	Mode          string `json:"mode"`
	Physical      int32  `json:"physical_guest_executions"`
	Leaders       int    `json:"leaders"`
	Waiters       int    `json:"waiters"`
	ElapsedMillis int64  `json:"elapsed_millis"`
	Passed        bool   `json:"passed"`
}

func TestRealGuestSharedExecutionMatrix(t *testing.T) {
	reportPath := os.Getenv("PYSOLATE_SHARED_MATRIX_REPORT")
	if reportPath == "" {
		t.Skip("set PYSOLATE_SHARED_MATRIX_REPORT for the bounded real-Guest matrix")
	}
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]sharedMatrixRow, 0, 4)
	for _, logical := range []int{8, 32} {
		for _, coalesced := range []bool{false, true} {
			rows = append(rows, runSharedMatrixRow(t, artifact, logical, coalesced))
		}
	}
	report := struct {
		SchemaVersion string            `json:"schema_version"`
		Rows          []sharedMatrixRow `json:"rows"`
	}{SchemaVersion: "pysolate.shared-guest-matrix.v1", Rows: rows}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(reportPath, append(encoded, '\n'), 0o600) != nil {
		t.Fatalf("write report: %v", err)
	}
}

func runSharedMatrixRow(t *testing.T, artifact []byte, logical int, coalesced bool) sharedMatrixRow {
	t.Helper()
	mode := "independent"
	var flights *agentfunction.FlightGroup
	if coalesced {
		mode = "coalesced"
		flights = agentfunction.NewFlightGroup()
	}
	functionEngine := agentfunction.Engine{Flights: flights}
	code := "result = {'value': inputs['value'] + 1}"
	inputs := []byte(`{"value":41}`)
	invocation, runConfig := sharedGuestInvocation(t, artifact, code, []string{"sys"}, inputs)
	request, err := json.Marshal(map[string]any{
		"run_id": fmt.Sprintf("matrix-%s-%d", mode, logical), "code": code, "inputs": map[string]any{"value": 41},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(ctx context.Context) (string, engine.Runner, error) {
			id := physical.Add(1)
			if flights != nil {
				deadline := time.Now().Add(10 * time.Second)
				for int(flights.Stats().Waiters) != logical-1 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
			}
			runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
			return fmt.Sprintf("matrix-%s-%d", mode, id), runner, err
		},
		Request: request, MaxResultBytes: 1024, DecodeResult: decodeSuccessfulGuestResult,
	}
	results := make(chan agentfunction.Result, logical)
	failures := make(chan error, logical)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(logical)
	started := time.Now()
	for range logical {
		go func() {
			defer wait.Done()
			<-start
			result, err := functionEngine.ExecuteGuest(context.Background(), invocation, compute)
			results <- result
			failures <- err
		}()
	}
	close(start)
	wait.Wait()
	elapsed := time.Since(started)
	close(results)
	close(failures)
	passed := true
	for err := range failures {
		if err != nil {
			t.Errorf("%s/%d: %v", mode, logical, err)
			passed = false
		}
	}
	leaders, waiters := 0, 0
	for result := range results {
		if string(result.Value) != `{"value":42}` {
			passed = false
		}
		switch result.Disposition {
		case agentfunction.Leader:
			leaders++
		case agentfunction.Waiter:
			waiters++
		}
	}
	expectedPhysical := int32(logical)
	if coalesced {
		expectedPhysical = 1
		passed = passed && leaders == 1 && waiters == logical-1
	}
	passed = passed && physical.Load() == expectedPhysical
	if !passed {
		t.Errorf("%s/%d physical=%d leaders=%d waiters=%d", mode, logical, physical.Load(), leaders, waiters)
	}
	return sharedMatrixRow{
		Logical: logical, Mode: mode, Physical: physical.Load(), Leaders: leaders, Waiters: waiters,
		ElapsedMillis: elapsed.Milliseconds(), Passed: passed,
	}
}
