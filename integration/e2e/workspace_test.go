package e2e_test

import (
	"context"
	"os"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/verification/workspacecapsule"
)

func TestWorkspaceCapsuleVerifierAgainstRealArtifact(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	report, err := workspacecapsule.Verify(
		context.Background(),
		wasm,
		runtimeconfig.DefaultRunConfig(),
		wazeroengine.Factory{
			Strategy:            enginecontract.StrategySingleUsePrepared,
			PreparedCapacity:    1,
			PreparedMaxCapacity: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != workspacecapsule.StatusVerified {
		t.Fatalf("workspace verification failed: %+v", report)
	}
	if report.SchemaVersion != workspacecapsule.SchemaVersion || report.Engine.ResetMode != enginecontract.ResetModeFreshInstance || len(report.Checks) != 30 || report.Stress != nil {
		t.Fatalf("unexpected workspace verification report: %+v", report)
	}
	seen := make(map[string]bool, len(report.Checks))
	for _, check := range report.Checks {
		if check.Status != workspacecapsule.CheckPass || seen[check.Name] {
			t.Fatalf("invalid workspace verification check: %+v", check)
		}
		seen[check.Name] = true
	}
}
