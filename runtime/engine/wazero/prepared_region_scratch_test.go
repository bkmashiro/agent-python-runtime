package wazero

import (
	"context"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

func TestPreparedRegionScratchRejectsAuthorityAndReturnsTypedCancellationBeforeInstantiation(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	decision := preparedregion.PreparedRegionDecision{IdentitySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	authorityEngine := &Engine{config: config, workspaceBinding: &workspaceBinding{}}
	if _, _, err := authorityEngine.ExecutePreparedRegionScratch(context.Background(), []byte(`{}`), decision); !errors.Is(err, ErrPreparedRegionScratchAuthority) {
		t.Fatalf("authority err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, evidence, err := (&Engine{config: config}).ExecutePreparedRegionScratch(ctx, []byte(`{}`), decision)
	if !errors.Is(err, context.Canceled) || result.Status != preparedregion.PreparedRegionScratchCancelled || result.ErrorType != "context_canceled" || evidence.ModuleInstantiations != 0 || evidence.TerminalStatus != preparedregion.PreparedRegionScratchCancelled {
		t.Fatalf("result=%+v evidence=%+v err=%v", result, evidence, err)
	}
}
