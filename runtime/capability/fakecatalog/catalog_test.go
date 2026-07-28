package fakecatalog_test

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakecatalog"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

func TestCombinedFakeCatalogGeneratesBoundSynchronousPythonSurface(t *testing.T) {
	snapshot, err := fakecatalog.Build(1, 8, "fake:v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tools()) != 15 || !strings.HasPrefix(snapshot.Digest(), "sha256:") {
		t.Fatalf("tools=%d digest=%q", len(snapshot.Tools()), snapshot.Digest())
	}
	runtimeSource, stub, err := snapshot.GeneratePython()
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []string{
		"def repo_open(", "def repo_manifest(", "def workspace_search(", "def workspace_read_many(",
		"def workspace_list(", "def workspace_glob(", "def workspace_stat_many(",
		"def cloudflare_dns_list(", "def cloudflare_dns_plan_change(", "def cloudflare_dns_apply_change(",
		"def mail_search(", "def mail_read_many(", "def mail_draft_prepare(", "def mail_draft_update(", "def mail_draft_delete(",
	} {
		if !strings.Contains(runtimeSource, binding) || !strings.Contains(stub, binding) {
			names := make([]string, 0, len(snapshot.Tools()))
			for _, tool := range snapshot.Tools() {
				names = append(names, tool.PythonName+":"+string(tool.Projection))
			}
			t.Fatalf("missing generated binding %q; tools=%v", binding, names)
		}
	}
	if strings.Contains(runtimeSource, "mail_send") || strings.Contains(runtimeSource, "mail.send") {
		t.Fatal("Host-only irreversible mail send leaked into Guest catalog")
	}
	if !strings.Contains(runtimeSource, "CATALOG_DIGEST = \""+snapshot.Digest()+"\"") || !strings.Contains(runtimeSource, "fake-mail-v1") || !strings.Contains(runtimeSource, "fake-cloudflare-dns-v1") {
		t.Fatal("generated surface is not bound to catalog and handler versions")
	}
	prepare, err := snapshot.GenerateTrustedPrepareWithToolBindings()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepare, "_host_sys.modules[\"host_tools\"]") || !strings.Contains(prepare, "mail_draft_prepare =") {
		t.Fatal("trusted preparation omitted generated fake tool bindings")
	}
	for _, tool := range snapshot.Tools() {
		if tool.Projection == toolcatalog.ProjectionUnsupported {
			t.Fatalf("tool %s cannot be projected to Python", tool.ToolID)
		}
	}
}

func TestCombinedFakeCatalogRejectsUnboundedOrInvalidGrants(t *testing.T) {
	for _, test := range []struct {
		revision uint64
		calls    uint32
		version  string
	}{
		{revision: 0, calls: 1, version: "fake:v1"},
		{revision: 1, calls: 0, version: "fake:v1"},
		{revision: 1, calls: 1025, version: "fake:v1"},
		{revision: 1, calls: 1, version: "bad version"},
	} {
		if _, err := fakecatalog.Build(test.revision, test.calls, test.version); err == nil {
			t.Fatalf("accepted invalid fake catalog config: %+v", test)
		}
	}
}
