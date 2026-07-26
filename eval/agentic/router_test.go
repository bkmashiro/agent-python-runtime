package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func TestHybridRouterSelectsOneSurfaceWithoutFutureTurnOrOracleLeakage(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	for _, route := range []HybridRoute{HybridRouteDirect, HybridRoutePython} {
		t.Run(string(route), func(t *testing.T) {
			body := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-1","name":"select_execution_surface","arguments":"{\"surface\":\"` + string(route) + `\"}"}]}`
			adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(body, 10, 2)}}
			session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
			if err != nil {
				t.Fatal(err)
			}
			decision, err := DecideHybridRoute(context.Background(), session, task)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Route != route || decision.PromptDigest == "" || decision.SurfaceDigest == "" || session.ProviderCalls() != 1 || len(adapter.requests) != 1 {
				t.Fatalf("decision=%+v calls=%d", decision, session.ProviderCalls())
			}
			payload := string(adapter.requests[0].Payload)
			if !strings.Contains(payload, "Pop on over") || !strings.Contains(payload, "select_execution_surface") ||
				strings.Contains(payload, "quantum computing") || strings.Contains(payload, "expected_call_trace") || strings.Contains(payload, task.ID) {
				t.Fatalf("router request leaked unavailable context: %s", payload)
			}
		})
	}
}

func TestHybridRouterRejectsAmbiguousOrMalformedDecisions(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	for _, body := range []string{
		`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"direct"}]}]}`,
		`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-1","name":"select_execution_surface","arguments":"{\"surface\":\"hybrid\"}"}]}`,
		`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-1","name":"select_execution_surface","arguments":"{\"surface\":\"direct\"}"},{"type":"function_call","status":"completed","call_id":"route-2","name":"select_execution_surface","arguments":"{\"surface\":\"python\"}"}]}`,
	} {
		adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(body, 10, 2)}}
		session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecideHybridRoute(context.Background(), session, task); err == nil || session.ProviderCalls() != 1 {
			t.Fatalf("body=%s err=%v calls=%d", body, err, session.ProviderCalls())
		}
	}
}
