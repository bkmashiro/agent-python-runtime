package toolcatalog

import "testing"

func TestProjectionNeverClaimsExactForUnrepresentedCompositionConstraints(t *testing.T) {
	discovered := []DiscoveredTool{{
		ToolID: "demo.conditional", ServerID: "demo", Name: "conditional", HandlerVersion: "v1",
		InputSchema: raw(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},
			"allOf":[{"if":{"required":["value"]},"then":{"properties":{"value":{"minLength":2}}}}]
		}`),
		OutputSchema: raw(`{"type":"string"}`),
	}}
	grants := map[string]Grant{
		"demo.conditional": {ToolID: "demo.conditional", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1},
	}
	snapshot, err := BuildSnapshot(discovered, grants, BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	tool := snapshot.Tools()[0]
	if tool.Projection != ProjectionLossy {
		t.Fatalf("composition constraint projection = %q, want lossy", tool.Projection)
	}
	if len(tool.Parameters) != 1 || tool.Parameters[0].Annotation != "None | str" {
		t.Fatalf("nullable optional annotation = %+v", tool.Parameters)
	}
}
