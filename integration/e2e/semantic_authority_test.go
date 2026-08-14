package e2e_test

import (
	"testing"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func trustedSemanticRunner(t *testing.T, runner enginecontract.Runner) *wazeroengine.Engine {
	t.Helper()
	trusted, ok := runner.(*wazeroengine.Engine)
	if !ok {
		t.Fatalf("semantic authority requires target Wazero engine, got %T", runner)
	}
	return trusted
}
