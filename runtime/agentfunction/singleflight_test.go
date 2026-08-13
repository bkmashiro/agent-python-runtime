package agentfunction_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
)

func TestLeaderPanicReleasesFollowersAndCleansEntry(t *testing.T) {
	flights := agentfunction.NewFlightGroup()
	engine := agentfunction.Engine{Flights: flights}
	started := make(chan struct{})
	release := make(chan struct{})
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		panic("fixture")
	}
	errorsChannel := make(chan error, 2)
	go func() {
		_, err := engine.Execute(context.Background(), cacheableInvocation(), compute)
		errorsChannel <- err
	}()
	<-started
	go func() {
		_, err := engine.Execute(context.Background(), cacheableInvocation(), compute)
		errorsChannel <- err
	}()
	for flights.Stats().Waiters == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	for range 2 {
		if err := <-errorsChannel; !errors.Is(err, agentfunction.ErrFlightPanic) {
			t.Fatalf("error=%v", err)
		}
	}
	if flights.Stats().InFlight != 0 {
		t.Fatalf("stats=%+v", flights.Stats())
	}
}

func TestSingleFlightOffPermitsIndependentConcurrentEvaluations(t *testing.T) {
	engine := agentfunction.Engine{}
	var calls atomic.Int32
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, _ = engine.Execute(context.Background(), cacheableInvocation(), func(context.Context, *agentfunction.Guard) ([]byte, error) {
				calls.Add(1)
				return []byte("ok"), nil
			})
		}()
	}
	wait.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestSingleFlightCollapsesConcurrentCallsWithoutRetention(t *testing.T) {
	flights := agentfunction.NewFlightGroup()
	engine := agentfunction.Engine{CacheEnabled: false, Flights: flights}
	invocation := cacheableInvocation()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("shared"), nil
	}
	const count = 16
	results := make(chan agentfunction.Result, count)
	errorsChannel := make(chan error, count)
	var wait sync.WaitGroup
	wait.Add(count)
	for range count {
		go func() {
			defer wait.Done()
			result, err := engine.Execute(context.Background(), invocation, compute)
			results <- result
			errorsChannel <- err
		}()
	}
	<-started
	time.Sleep(10 * time.Millisecond)
	close(release)
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if string(result.Value) != "shared" || result.CacheHit {
			t.Fatalf("result=%+v", result)
		}
	}
	stats := flights.Stats()
	if calls.Load() != 1 || stats.Leaders != 1 || stats.Waiters != count-1 || stats.InFlight != 0 {
		t.Fatalf("calls=%d stats=%+v", calls.Load(), stats)
	}

	if _, err := engine.Execute(context.Background(), invocation, func(context.Context, *agentfunction.Guard) ([]byte, error) {
		calls.Add(1)
		return []byte("fresh"), nil
	}); err != nil || calls.Load() != 2 {
		t.Fatalf("single-flight became retention calls=%d err=%v", calls.Load(), err)
	}
}

func TestCancelledWaiterDoesNotCancelLeader(t *testing.T) {
	flights := agentfunction.NewFlightGroup()
	engine := agentfunction.Engine{Flights: flights}
	invocation := cacheableInvocation()
	started := make(chan struct{})
	release := make(chan struct{})
	leaderDone := make(chan error, 1)
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		close(started)
		<-release
		return []byte("ok"), nil
	}
	go func() {
		_, err := engine.Execute(context.Background(), invocation, compute)
		leaderDone <- err
	}()
	<-started
	waiterContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Execute(waiterContext, invocation, compute); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error=%v", err)
	}
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader error=%v", err)
	}
}

func TestSingleFlightSeparatesInvocationIdentities(t *testing.T) {
	flights := agentfunction.NewFlightGroup()
	engine := agentfunction.Engine{Flights: flights}
	first := cacheableInvocation()
	second := cacheableInvocation()
	second.CanonicalInputs = []byte(`{"value":2}`)
	var calls atomic.Int32
	var wait sync.WaitGroup
	wait.Add(2)
	for _, invocation := range []agentfunction.Invocation{first, second} {
		invocation := invocation
		go func() {
			defer wait.Done()
			_, _ = engine.Execute(context.Background(), invocation, func(context.Context, *agentfunction.Guard) ([]byte, error) {
				calls.Add(1)
				return []byte("ok"), nil
			})
		}()
	}
	wait.Wait()
	if calls.Load() != 2 || flights.Stats().Leaders != 2 {
		t.Fatalf("calls=%d stats=%+v", calls.Load(), flights.Stats())
	}
}

func TestSingleFlightReportsOneLeaderAndWaitersForOnePhysicalExecution(t *testing.T) {
	flights := agentfunction.NewFlightGroup()
	engine := agentfunction.Engine{Flights: flights}
	invocation := cacheableInvocation()
	started := make(chan struct{})
	release := make(chan struct{})
	compute := func(_ context.Context, guard *agentfunction.Guard) ([]byte, error) {
		if err := guard.BindPhysicalExecution("physical-1"); err != nil {
			return nil, err
		}
		close(started)
		<-release
		return []byte("shared"), nil
	}

	const count = 8
	results := make(chan agentfunction.Result, count)
	var wait sync.WaitGroup
	wait.Add(count)
	for range count {
		go func() {
			defer wait.Done()
			result, _ := engine.Execute(context.Background(), invocation, compute)
			results <- result
		}()
	}
	<-started
	for flights.Stats().Waiters != count-1 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wait.Wait()
	close(results)
	leaders, waiters := 0, 0
	for result := range results {
		if result.PhysicalExecutionID != "physical-1" {
			t.Fatalf("result=%+v", result)
		}
		switch result.Disposition {
		case agentfunction.Leader:
			leaders++
		case agentfunction.Waiter:
			waiters++
		default:
			t.Fatalf("result=%+v", result)
		}
	}
	if leaders != 1 || waiters != count-1 {
		t.Fatalf("leaders=%d waiters=%d", leaders, waiters)
	}
}
