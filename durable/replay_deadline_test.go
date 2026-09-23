package durable

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
	"github.com/tetratelabs/wazero"
	"os"
	"testing"
	"time"
)

func TestOfflineReplayDeadlineIsNotHistoryMismatch(t *testing.T) {
	guest, err := os.ReadFile(testGuestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	cache := wazero.NewCompilationCache()
	defer cache.Close(context.Background())
	base := pysolate.WithCompilationCache(context.Background(), cache)
	warm, err := pysolate.New(base, guest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Close(base); err != nil {
		t.Fatal(err)
	}
	tools, _ := json.Marshal([]toolDeclaration{{Name: "echo", Version: "v1", Recovery: RetrySafe, PythonPath: "echo"}})
	outcome, _ := json.Marshal(pysolate.Output{Value: json.RawMessage(`7`)})
	bundle := RunBundle{Version: RunBundleVersion, PrivacyWarning: RunBundlePrivacyWarning,
		Run:   BundleRun{ID: "deadline", Code: "while True: pass", Seed: "seed", ArtifactSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(guest)), EnvironmentVersion: "env-v1", Inputs: json.RawMessage(`{}`), Tools: tools, Status: StatusCompleted, Outcome: outcome},
		Calls: []BundleCall{{Sequence: 0, CallID: "call-0", Tool: "echo", OperationKey: operationKey("deadline", 0), Arguments: json.RawMessage(`{}`), State: CallCompleted, Outcome: json.RawMessage(`{"value":7}`)}}}
	collector := perfdiag.NewCollector()
	ctx, cancel := context.WithTimeout(perfdiag.WithCollector(base, collector), 2*time.Second)
	defer cancel()
	_, err = ReplayBundle(ctx, bundle, guest)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrBundleMismatch) || ErrorCategory(err) != ReplayErrorCategoryRuntime {
		t.Fatalf("wrong timeout classification: %v", err)
	}
	for _, entry := range collector.Snapshot() {
		if entry.Phase == "execute" && entry.Count > 0 {
			return
		}
	}
	t.Fatal("deadline did not exercise Guest execution")
}
