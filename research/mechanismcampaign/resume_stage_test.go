package mechanismcampaign

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealResumeStageImportsBindsAndRunsFreshMainGuest(t *testing.T) {
	artifact := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifact == "" {
		t.Skip("AGENT_RUNTIME_GUEST not set")
	}
	managerRoot := filepath.Join(t.TempDir(), "source-manager")
	if err := os.Mkdir(managerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(managerRoot)
	if err != nil {
		t.Fatal(err)
	}
	base, err := manager.Create([]workspace.InitialFile{{Path: "candidate-result.json", Data: []byte(`{"candidate_id":"oxford","total_cost_gbp":78}`)}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	info, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := manager.ForkBranch(base, info.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	root, err := branch.Seal(info.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var capsule bytes.Buffer
	if _, err := manager.ExportCapsule(root.Ref(), &capsule); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RunResumeStage(context.Background(), ResumeStageConfig{
		ArtifactPath: artifact, Capsule: capsule.Bytes(), PortableRoot: root,
		WorkspaceRoot: filepath.Join(t.TempDir(), "restored-manager"), PayloadBytes: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BoundRoot.IdentitySHA256 != root.IdentitySHA256 || result.ImportedInfo.WorkspaceSHA256 != root.WorkspaceSHA256 {
		t.Fatalf("root=%+v bound=%+v imported=%+v", root, result.BoundRoot, result.ImportedInfo)
	}
	seen := map[string]bool{}
	for _, event := range result.Events {
		seen[event.Type] = true
	}
	for _, required := range []string{"capsule.import", "capsule.bind", "guest.start", "request.start", "request.finish", "guest.complete"} {
		if !seen[required] {
			t.Fatalf("missing %s in %+v", required, result.Events)
		}
	}
}
