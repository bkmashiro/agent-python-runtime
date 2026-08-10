package workspacecapsule

import (
	"context"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestVerifyRejectsEmptyArtifact(t *testing.T) {
	report, err := Verify(context.Background(), nil, runtimeconfig.DefaultRunConfig(), wazeroengine.Factory{})
	if err == nil || report.SchemaVersion != SchemaVersion || report.Status != StatusFailed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestVerifyRejectsPreboundFactory(t *testing.T) {
	report, err := Verify(context.Background(), []byte("not reached"), runtimeconfig.DefaultRunConfig(), wazeroengine.Factory{
		WorkspaceManager: &workspace.Manager{}, WorkspaceRef: "ws-existing", WorkspaceOwner: "owner",
	})
	if err == nil || !strings.Contains(err.Error(), "already bound") || report.Status != StatusFailed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSanitizeErrorRemovesEveryHostPath(t *testing.T) {
	err := sanitizeError("probe", &pathError{"/private/base", "/private/source"}, "/private/base", "/private/source")
	if strings.Contains(err.Error(), "/private/") || !strings.Contains(err.Error(), "[HOST_PATH]") {
		t.Fatalf("unsanitized error: %v", err)
	}
}

type pathError struct {
	base   string
	source string
}

func (value *pathError) Error() string { return value.base + ": " + value.source }
