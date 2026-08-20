package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

func TestExactGuestEagerStyleComparatorUsesLookaheadAndPersistentNamespace(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	binding := newBranchWorkspace(t, "eager-comparator")
	attempt, err := binding.manager.ForkAttempt(binding.ref)
	if err != nil {
		t.Fatal(err)
	}
	fragments, err := semanticspeculation.BuildEagerComparatorPrepareChunks(semanticspeculation.EagerComparatorPrepareConfig{
		Inputs: json.RawMessage(`{"value":2}`),
		Chunks: []string{
			"base = inputs['value'] + 1\n",
			"derived = base * 4\n",
			"result = derived\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	factory := wazeroengine.Factory{
		WorkspaceManager: binding.manager,
		WorkspaceRef:     attempt.Ref(),
		WorkspaceOwner:   "eager-comparator-e2e",
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
	runner, err := factory.New(context.Background(), artifact, runConfig)
	if err != nil {
		t.Fatal(err)
	}
	streamRunner, ok := runner.(streaming.StreamRunner)
	if !ok {
		t.Fatal(errors.New("wazero runner lacks live stream support"))
	}
	prepares := make(chan string, len(fragments))
	for _, fragment := range fragments {
		prepares <- fragment
	}
	close(prepares)
	request := []byte(`{"run_id":"eager-comparator-e2e","code":"result = comparator_final","inputs":{}}`)
	outcome, err := streaming.ExecuteStream(context.Background(), streamRunner, attempt, request, prepares)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			Result                 int  `json:"result"`
			ResultPresent          bool `json:"result_present"`
			PrefixPythonExecutions int  `json:"prefix_python_executions"`
			PythonExecutions       int  `json:"python_executions"`
			Sealed                 bool `json:"sealed"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outcome.Response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "ok" || envelope.Result.Result != 12 || !envelope.Result.ResultPresent ||
		envelope.Result.PrefixPythonExecutions != 2 || envelope.Result.PythonExecutions != 3 || envelope.Result.Sealed {
		t.Fatalf("response=%s", outcome.Response)
	}
}
