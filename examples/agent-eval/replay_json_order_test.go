package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDirectReplayPreservesJSONMemberOrder(t *testing.T) {
	result := []byte(`{"z":1,"a":2}`)
	rt := &episodeRuntime{fixture: &taskEpisode{Domains: []domainDeclaration{{Name: "x"}}}, playback: true, playbackCalls: []privateDomainCall{{Completed: true, Tool: "x", Arguments: []byte(`{}`), Result: result}}}
	value, err := rt.callDomain("x", context.Background(), json.RawMessage(`{}`))
	if got := domainResult(value, err); got != string(result) {
		t.Fatalf("changed recorded tool message: %s", got)
	}
}
