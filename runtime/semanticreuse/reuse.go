package semanticreuse

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

var ErrReuseQualification = errors.New("semantic whole-run reuse qualification failed")

type Stats struct {
	Attempts           uint64 `json:"attempts"`
	Rejected           uint64 `json:"rejected"`
	Independent        uint64 `json:"independent"`
	Leaders            uint64 `json:"leaders"`
	Waiters            uint64 `json:"waiters"`
	Retained           uint64 `json:"retained"`
	PhysicalComputes   uint64 `json:"physical_computes"`
	ResultBytes        uint64 `json:"result_bytes_total"`
	QualificationNanos uint64 `json:"qualification_nanos_total"`
	ExecutionNanos     uint64 `json:"execution_nanos_total"`
}

type Pass struct {
	Enabled        bool
	FunctionEngine agentfunction.Engine
	mu             sync.Mutex
	stats          Stats
}

func (pass *Pass) ExecuteGuest(ctx context.Context, invocation agentfunction.Invocation, verified semantic.VerifiedWholeRunPlan, compute agentfunction.FreshGuestCompute) (agentfunction.Result, error) {
	if pass == nil || !pass.Enabled {
		return agentfunction.Result{}, ErrReuseQualification
	}
	qualificationStarted := time.Now()
	qualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, verified, compute.Request)
	qualificationNanos := uint64(time.Since(qualificationStarted))
	pass.mu.Lock()
	pass.stats.Attempts++
	pass.stats.QualificationNanos += qualificationNanos
	if err != nil {
		pass.stats.Rejected++
		pass.mu.Unlock()
		return agentfunction.Result{}, errors.Join(ErrReuseQualification, err)
	}
	pass.mu.Unlock()

	executionStarted := time.Now()
	result, err := pass.FunctionEngine.ExecuteQualifiedGuest(ctx, qualified, compute)
	executionNanos := uint64(time.Since(executionStarted))
	pass.mu.Lock()
	pass.stats.ExecutionNanos += executionNanos
	if err == nil {
		pass.stats.ResultBytes += uint64(len(result.Value))
		switch result.Disposition {
		case agentfunction.Independent:
			pass.stats.Independent++
			pass.stats.PhysicalComputes++
		case agentfunction.Leader:
			pass.stats.Leaders++
			pass.stats.PhysicalComputes++
		case agentfunction.Waiter:
			pass.stats.Waiters++
		case agentfunction.Retained:
			pass.stats.Retained++
		}
	}
	pass.mu.Unlock()
	return result, err
}

func (pass *Pass) Stats() Stats {
	if pass == nil {
		return Stats{}
	}
	pass.mu.Lock()
	defer pass.mu.Unlock()
	return pass.stats
}
