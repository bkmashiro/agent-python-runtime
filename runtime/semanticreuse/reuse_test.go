package semanticreuse_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/semanticreuse"
)

func TestPassIsDefaultOff(t *testing.T) {
	pass := &semanticreuse.Pass{}
	result, err := pass.ExecuteGuest(context.Background(), agentfunction.Invocation{}, semantic.Analysis{}, semantic.Plan{}, agentfunction.FreshGuestCompute{})
	if !errors.Is(err, semanticreuse.ErrReuseQualification) || len(result.Value) != 0 || pass.Stats().Attempts != 0 {
		t.Fatalf("result=%+v stats=%+v err=%v", result, pass.Stats(), err)
	}
}
