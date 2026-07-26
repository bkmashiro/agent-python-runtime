package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestHostContextContainsOnlyPriorSuccessfulModelEffects(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := tools.InvokeDirect(ctx, "context-cd", "context:cd", "cd", json.RawMessage(`{"folder":"Documents"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.InvokeDirect(ctx, "context-pwd", "context:pwd", "pwd", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.InvokeDirect(ctx, "context-touch", "context:touch", "touch", json.RawMessage(`{"file_name":"summary.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.InvokeDirect(ctx, "context-failed-touch", "context:failed-touch", "touch", json.RawMessage(`{"file_name":"summary.txt"}`)); !errors.Is(err, ErrBenchmarkToolOperation) {
		t.Fatalf("failed touch err=%v", err)
	}
	projection, encoded, err := BuildHostContext(tools, 1)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Version != "agentic-host-context/v1" || projection.Turn != 1 || projection.CWD != "/alex/Documents" || len(projection.SuccessfulEffects) != 2 ||
		projection.SuccessfulEffects[0].Tool != "cd" || projection.SuccessfulEffects[1].Tool != "touch" || projection.OmittedEffects != 0 {
		t.Fatalf("projection=%+v encoded=%s", projection, encoded)
	}
	text := string(encoded)
	for _, forbidden := range []string{"context-pwd", "current_working_directory", "No such", "error", "file_content", "quantum computing"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("context leaked %q: %s", forbidden, text)
		}
	}
	_, encodedAgain, err := BuildHostContext(tools, 1)
	if err != nil || string(encodedAgain) != text || len(encoded) > maxHostContextBytes {
		t.Fatalf("context is not canonical/bounded: %s / %s err=%v", encoded, encodedAgain, err)
	}
}

func TestHostContextRejectsFirstTurnStatelessAndFutureTurns(t *testing.T) {
	stateful := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	statefulTools, _ := NewToolRuntime(stateful)
	for _, turn := range []int{0, len(stateful.Interaction.Turns) + 1} {
		if _, _, err := BuildHostContext(statefulTools, turn); err == nil {
			t.Fatalf("turn %d accepted", turn)
		}
	}
	stateless := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	statelessTools, _ := NewToolRuntime(stateless)
	if _, _, err := BuildHostContext(statelessTools, 1); err == nil {
		t.Fatal("stateless context accepted")
	}
}

func TestHostContextLargeArgumentsUseDigestInsteadOfLeakingPayload(t *testing.T) {
	effect := hostEffectFromCall(RawToolCall{Name: "echo", Arguments: json.RawMessage(`{"content":"` + strings.Repeat("x", maxHostContextArgumentBytes+1) + `","file_name":"summary.txt"}`)})
	if len(effect.Arguments) != 0 || effect.ArgumentsDigest == "" || !strings.HasPrefix(effect.ArgumentsDigest, "sha256:") {
		t.Fatalf("effect=%+v", effect)
	}
}
