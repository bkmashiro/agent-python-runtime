package toolcatalog

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fixtureDiscovery struct {
	tools []DiscoveredTool
	err   error
}

func (fixture *fixtureDiscovery) Discover(context.Context) ([]DiscoveredTool, error) {
	return append([]DiscoveredTool(nil), fixture.tools...), fixture.err
}

func TestCatalogManagerChangesOnlyNewSnapshotsAndEmitsSanitizedDiff(t *testing.T) {
	source := &fixtureDiscovery{tools: []DiscoveredTool{fixtureTool("demo.echo", "echo", `{"type":"object","properties":{"text":{"type":"string"}}}`)}}
	manager, err := NewManager(source, CatalogPolicy{Allowlist: map[string]bool{"demo.echo": true}, Grants: map[string]Grant{"demo.echo": fixtureGrant("demo.echo")}})
	if err != nil {
		t.Fatal(err)
	}
	first, diff, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision() != 1 || len(diff.Added) != 1 || diff.Added[0] != "demo.echo" {
		t.Fatalf("first diff=%+v", diff)
	}
	firstDigest := first.Digest()
	firstRuntime, _, err := first.GeneratePython()
	if err != nil || !strings.Contains(firstRuntime, firstDigest) {
		t.Fatalf("first generated SDK err=%v", err)
	}

	source.tools[0] = fixtureTool("demo.echo", "renamed", `{"type":"object","required":["text"],"properties":{"text":{"type":"string","maxLength":8}}}`)
	second, diff, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision() != 2 || second.Digest() == firstDigest || len(diff.Changed) != 1 || diff.Changed[0] != "demo.echo" {
		t.Fatalf("second=%s diff=%+v", second.Digest(), diff)
	}
	secondRuntime, _, err := second.GeneratePython()
	if err != nil || !strings.Contains(secondRuntime, second.Digest()) || strings.Contains(secondRuntime, firstDigest) {
		t.Fatalf("second generated SDK did not bind new snapshot err=%v", err)
	}
	if first.Digest() != firstDigest || first.Tools()[0].PythonName != "echo" {
		t.Fatal("live snapshot drifted after refresh")
	}

	manager.SetPolicy(CatalogPolicy{Allowlist: map[string]bool{"demo.echo": true}, Grants: map[string]Grant{}})
	third, diff, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.Revision() != 3 || len(third.Tools()) != 0 || len(diff.Removed) != 1 || diff.Removed[0] != "demo.echo" {
		t.Fatalf("revocation snapshot=%+v diff=%+v", third.Tools(), diff)
	}
}

func TestCatalogManagerDiscoveryFailureRequiresExplicitPolicyValidPin(t *testing.T) {
	source := &fixtureDiscovery{tools: []DiscoveredTool{fixtureTool("demo.echo", "echo", `{"type":"object","properties":{}}`)}}
	policy := CatalogPolicy{Allowlist: map[string]bool{"demo.echo": true}, Grants: map[string]Grant{"demo.echo": fixtureGrant("demo.echo")}}
	manager, err := NewManager(source, policy)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source.err = errors.New("fixture unavailable")
	if _, _, err := manager.Refresh(context.Background()); !errors.Is(err, ErrDiscoveryFailed) {
		t.Fatalf("failure err=%v", err)
	}
	manager.PinCurrent(true)
	pinned, diff, err := manager.Refresh(context.Background())
	if err != nil || pinned.Digest() != first.Digest() || !diff.Pinned {
		t.Fatalf("pinned=%s diff=%+v err=%v", pinned.Digest(), diff, err)
	}
	manager.SetPolicy(CatalogPolicy{Allowlist: map[string]bool{}, Grants: map[string]Grant{}})
	if _, _, err := manager.Refresh(context.Background()); !errors.Is(err, ErrDiscoveryFailed) {
		t.Fatalf("revoked pin err=%v", err)
	}
}

func fixtureTool(id, name, input string) DiscoveredTool {
	return DiscoveredTool{ToolID: id, ServerID: "fixture", Name: name, HandlerVersion: "v1", InputSchema: []byte(input), OutputSchema: []byte(`{"type":"object"}`)}
}

func fixtureGrant(id string) Grant {
	return Grant{ToolID: id, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 2}
}
