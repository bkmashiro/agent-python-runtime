package wazero

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

func testPreparedNumpyInput(t *testing.T, body []byte) PreparedNumpyInput {
	t.Helper()
	bindings := numpycodec.Bindings{
		ArtifactSHA256: digestHex('a'), ExecutionProfileID: "numpy-core",
		ExecutionProfileSHA256: digestHex('b'), ImportClosureSHA256: digestHex('c'),
		SourceSHA256: digestHex('d'), InputsSHA256: digestHex('e'), PassRegistrationSHA256: digestHex('f'),
	}
	producer, err := json.Marshal(numpycodec.ProducerValue{
		SchemaVersion: numpycodec.ProducerValueSchemaVersion, DType: "<i8", Shape: []uint64{uint64(len(body) / 8)},
		Order: "C", CContiguous: true, NBytes: uint64(len(body)), BodySHA256: resultblob.BytesDigest(body),
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, decoded, err := numpycodec.DecodeProducerValue(producer, bindings, numpycodec.MaxBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewPreparedNumpyInput("dataset", descriptor, decoded)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func digestHex(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }

func TestPreparedNumpyInputCopiesAndBindsBody(t *testing.T) {
	body := make([]byte, 32)
	body[0] = 7
	input := testPreparedNumpyInput(t, body)
	identity := input.IdentitySHA256()
	body[0] = 9
	if input.body[0] != 7 || input.IdentitySHA256() != identity || input.Name() != "dataset" {
		t.Fatalf("input aliased caller body: body=%d identity=%s", input.body[0], input.IdentitySHA256())
	}
	if strings.Contains(string(input.descriptorJSON), `\u003c`) || !strings.HasPrefix(string(input.descriptorJSON), `{"body_sha256":`) {
		t.Fatalf("Guest descriptor is not Python-canonical: %s", input.descriptorJSON)
	}
	mutated := append([]byte(nil), input.body...)
	mutated[0]++
	if _, err := NewPreparedNumpyInput("dataset", input.Descriptor(), mutated); !errors.Is(err, ErrPreparedNumpyInput) {
		t.Fatalf("body drift err=%v", err)
	}
}

func TestCloneFamilyRunConfigFreezesCapabilityGrants(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.CapabilityGrants["tool"] = runtimeconfig.CapabilityGrant{Name: "old"}
	clone := cloneFamilyRunConfig(config)
	config.CapabilityGrants["tool"] = runtimeconfig.CapabilityGrant{Name: "new"}
	config.CapabilityGrants["other"] = runtimeconfig.CapabilityGrant{Name: "other"}
	if clone.CapabilityGrants["tool"].Name != "old" || len(clone.CapabilityGrants) != 1 {
		t.Fatalf("grant map remained aliased: %+v", clone.CapabilityGrants)
	}
}

func TestPreparedImageIdentityRejectsImageAffectingDrift(t *testing.T) {
	input := testPreparedNumpyInput(t, make([]byte, 32))
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = testBoundNumpyProfile(t)
	first, err := preparedImageIdentity(config, input, PreparedNumpyABIV1)
	if err != nil {
		t.Fatal(err)
	}
	clone := cloneFamilyRunConfig(config)
	clone.CapabilityGrants["tool"] = runtimeconfig.CapabilityGrant{Name: "tool"}
	second, err := preparedImageIdentity(clone, input, PreparedNumpyABIV1)
	if err != nil || second != first {
		t.Fatalf("per-consumer authority changed image identity: first=%s second=%s err=%v", first, second, err)
	}
	clone.MemoryLimitPages++
	third, err := preparedImageIdentity(clone, input, PreparedNumpyABIV1)
	if err != nil || third == first {
		t.Fatalf("memory drift retained image identity: third=%s err=%v", third, err)
	}
	if _, err := preparedImageIdentity(config, input, "unknown"); !errors.Is(err, ErrPreparedImageCompatibility) {
		t.Fatalf("unknown ABI err=%v", err)
	}
}

func TestPreparedInputBindingRejectsProfileDrift(t *testing.T) {
	profile := testBoundNumpyProfile(t)
	input := realPreparedInput(t, profile, []uint64{4}, []uint64{1, 2, 3, 4})
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = profile
	if err := input.validateForConfig(config); err != nil {
		t.Fatalf("valid binding err=%v", err)
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(profile.ArtifactSHA256(), "seed")
	if err != nil {
		t.Fatal(err)
	}
	config.DeterministicVerification = &deterministic
	if err := input.validateForConfig(config); !errors.Is(err, ErrPreparedImageCompatibility) {
		t.Fatalf("profile drift err=%v", err)
	}
}

func TestPreparedFamilyLifecycleReleasesFailedCreationReservation(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, err := lifecycle.reserve()
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.release(member); err != nil {
		t.Fatal(err)
	}
	state := lifecycle.state()
	if state.Created != 0 || state.Terminal != 0 {
		t.Fatalf("state=%+v", state)
	}
	if _, err := lifecycle.reserve(); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedFamilyLifecycleBoundsAndClose(t *testing.T) {
	lifecycle, err := newPreparedFamilyLifecycle(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := lifecycle.reserve()
	if err != nil {
		t.Fatal(err)
	}
	second, err := lifecycle.reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.reserve(); !errors.Is(err, ErrPreparedFamilyConsumerLimit) {
		t.Fatalf("third reservation err=%v", err)
	}
	if err := lifecycle.begin(first); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.begin(second); !errors.Is(err, ErrPreparedFamilyActiveLimit) {
		t.Fatalf("concurrent begin err=%v", err)
	}
	if err := lifecycle.close(); !errors.Is(err, ErrPreparedFamilyRunsActive) {
		t.Fatalf("active close err=%v", err)
	}
	lifecycle.finish(first, PreparedMemberOK)
	if err := lifecycle.begin(first); !errors.Is(err, ErrPreparedRunnerConsumed) {
		t.Fatalf("repeat begin err=%v", err)
	}
	if err := lifecycle.begin(second); err != nil {
		t.Fatal(err)
	}
	lifecycle.finish(second, PreparedMemberCancelled)
	if err := lifecycle.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.reserve(); !errors.Is(err, ErrPreparedFamilyClosed) {
		t.Fatalf("reserve after close err=%v", err)
	}
	state := lifecycle.state()
	if state.Created != 2 || state.Active != 0 || state.Terminal != 2 || !state.Closed {
		t.Fatalf("state=%+v", state)
	}
}

type fakePreparedTransfer struct {
	allocations []uint32
	writes      int
	calls       int
	releases    []uint32
	failWriteAt int
	failCall    bool
}

func (transfer *fakePreparedTransfer) allocate(_ context.Context, size uint32) (uint32, error) {
	pointer := uint32(len(transfer.allocations)+1) * 100
	transfer.allocations = append(transfer.allocations, size)
	return pointer, nil
}
func (transfer *fakePreparedTransfer) write(_ uint32, _ []byte) bool {
	transfer.writes++
	return transfer.failWriteAt == 0 || transfer.writes != transfer.failWriteAt
}
func (transfer *fakePreparedTransfer) call(context.Context, uint32, uint32, uint32, uint32) error {
	transfer.calls++
	if transfer.failCall {
		return errors.New("call failed")
	}
	return nil
}
func (transfer *fakePreparedTransfer) deallocate(pointer uint32) {
	transfer.releases = append(transfer.releases, pointer)
}

func TestPreparedGuestTransferReleasesBothAllocationsOnEveryTerminalPath(t *testing.T) {
	for _, test := range []struct {
		name        string
		failWriteAt int
		failCall    bool
		wantErr     bool
	}{
		{name: "success"},
		{name: "descriptor write", failWriteAt: 1, wantErr: true},
		{name: "body write", failWriteAt: 2, wantErr: true},
		{name: "call", failCall: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transfer := &fakePreparedTransfer{failWriteAt: test.failWriteAt, failCall: test.failCall}
			err := transferPreparedNumpyInput(context.Background(), transfer, []byte(`{"v":1}`), []byte{1, 2, 3})
			if (err != nil) != test.wantErr || len(transfer.releases) != 2 {
				t.Fatalf("err=%v releases=%v", err, transfer.releases)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	transfer := &fakePreparedTransfer{}
	if err := transferPreparedNumpyInput(cancelled, transfer, []byte(`{"v":1}`), []byte{1}); !errors.Is(err, context.Canceled) || len(transfer.releases) != 2 || transfer.calls != 0 {
		t.Fatalf("cancel err=%v releases=%v calls=%d", err, transfer.releases, transfer.calls)
	}
}

type closeRaceFamilyRunner struct {
	started chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (runner *closeRaceFamilyRunner) Run(context.Context, []byte, string) ([]byte, error) {
	close(runner.started)
	<-runner.release
	return []byte(`{"schema_version":"pysolate.run-response.v1","run_id":"run","status":"ok","result":1,"metrics":{}}`), nil
}
func (runner *closeRaceFamilyRunner) Close(context.Context) error {
	close(runner.closed)
	return nil
}
func (*closeRaceFamilyRunner) Properties() engine.Properties { return engine.Properties{} }

func TestPreparedFamilyRunnerConcurrentFirstRunAndCloseIsOrdered(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
		member, _ := lifecycle.reserve()
		delegate := &closeRaceFamilyRunner{started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "inv", InvocationAttempt: 1, ExecutionID: "run"}
		runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
		request := []byte(`{"run_id":"run","code":"result=1","inputs":{}}`)
		runDone := make(chan error, 1)
		closeDone := make(chan error, 1)
		go func() { _, err := runner.Run(context.Background(), request, ""); runDone <- err }()
		go func() { closeDone <- runner.Close(context.Background()) }()
		closeFinished := false
		select {
		case <-delegate.started:
			select {
			case <-delegate.closed:
				t.Fatal("delegate closed before active Run completed")
			default:
			}
			close(delegate.release)
		case err := <-closeDone:
			if err != nil {
				t.Fatal(err)
			}
			closeFinished = true
			close(delegate.release)
		}
		if err := <-runDone; err != nil && !errors.Is(err, ErrPreparedRunnerConsumed) {
			t.Fatal(err)
		}
		if !closeFinished {
			if err := <-closeDone; err != nil {
				t.Fatal(err)
			}
		}
		if state := lifecycle.state(); state.Active != 0 {
			t.Fatalf("state=%+v", state)
		}
	}
}

type blockingFamilyRunner struct {
	started chan struct{}
	release chan struct{}
}

func (runner *blockingFamilyRunner) Run(context.Context, []byte, string) ([]byte, error) {
	close(runner.started)
	<-runner.release
	return []byte(`{"schema_version":"pysolate.run-response.v1","run_id":"run","status":"ok","result":1,"metrics":{}}`), nil
}
func (*blockingFamilyRunner) Close(context.Context) error   { return nil }
func (*blockingFamilyRunner) Properties() engine.Properties { return engine.Properties{} }

func TestPreparedFamilyRunnerCloseWaitsForActiveRun(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &blockingFamilyRunner{started: make(chan struct{}), release: make(chan struct{})}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "inv", InvocationAttempt: 1, ExecutionID: "run"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	request := []byte(`{"run_id":"run","code":"result=1","inputs":{}}`)
	runDone := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), request, ""); runDone <- err }()
	<-delegate.started
	closeContext, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := runner.Close(closeContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active Close err=%v", err)
	}
	close(delegate.release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := lifecycle.state()
	if state.Active != 0 || state.Terminal != 1 {
		t.Fatalf("state=%+v", state)
	}
}

type familyFakeRunner struct {
	runs          int
	ref           runtimeconfig.InvocationRef
	runError      error
	closes        int
	failCloseOnce bool
}

func (runner *familyFakeRunner) Run(ctx context.Context, _ []byte, trustedPrepare string) ([]byte, error) {
	runner.runs++
	runner.ref, _ = engine.InvocationRefFromContext(ctx)
	if trustedPrepare != "" {
		return nil, errors.New("trusted prepare reached delegate")
	}
	if runner.runError != nil {
		return nil, runner.runError
	}
	return []byte(`{"schema_version":"pysolate.run-response.v1","run_id":"execution","status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1}}`), nil
}
func (runner *familyFakeRunner) Close(context.Context) error {
	runner.closes++
	if runner.failCloseOnce && runner.closes == 1 {
		return errors.New("close fixture")
	}
	return nil
}
func (*familyFakeRunner) Properties() engine.Properties { return engine.Properties{Backend: "fake"} }

type familyCapabilityRunner struct {
	familyFakeRunner
	trustedPrepare string
	ref            runtimeconfig.InvocationRef
}

func (runner *familyCapabilityRunner) RunCapabilitySourcePatchInline(ctx context.Context, _ []byte, _ passregistration.Registration, trustedPrepare string, _ []sourcepatch.CapabilityProjection) (passplugin.CapabilitySourcePatchRun, error) {
	runner.trustedPrepare = trustedPrepare
	runner.ref, _ = engine.InvocationRefFromContext(ctx)
	return passplugin.CapabilitySourcePatchRun{
		Payload: []byte(`{"schema_version":"pysolate.run-response.v1","run_id":"execution","status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1}}`),
		Applied: true,
	}, nil
}

func TestPreparedFamilyRunnerForwardsCapabilitySourcePatchWithLifecycle(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &familyCapabilityRunner{}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	runner.prepare = "sources = object()"
	runner.preparePrefix = trustedCOWPackageSource
	request := []byte(`{"run_id":"execution","code":"result=1","inputs":{}}`)
	result, err := runner.RunCapabilitySourcePatchInline(context.Background(), request, passregistration.Registration{}, "sources = object()", nil)
	if err != nil || !result.Applied || delegate.trustedPrepare != trustedCOWPackageSource+"sources = object()" || delegate.ref != ref {
		t.Fatalf("result=%+v err=%v trusted=%q ref=%+v", result, err, delegate.trustedPrepare, delegate.ref)
	}
	if state := lifecycle.state(); state.Active != 0 || state.Terminal != 1 {
		t.Fatalf("state=%+v", state)
	}
	if _, err := runner.Run(context.Background(), request, ""); !errors.Is(err, ErrPreparedRunnerConsumed) {
		t.Fatalf("repeat err=%v", err)
	}
}

func TestPreparedFamilyRunnerCloseRetriesDelegateFailure(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &familyFakeRunner{failCloseOnce: true}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	notifications := 0
	runner.onClose = func(uint64) { notifications++ }
	if err := runner.Close(context.Background()); err == nil {
		t.Fatal("first Close unexpectedly succeeded")
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delegate.closes != 2 || notifications != 1 {
		t.Fatalf("closes=%d notifications=%d", delegate.closes, notifications)
	}
}

func TestPreparedFamilyStateDoesNotAcquireLifecycleBeforeFamilyLock(t *testing.T) {
	lifecycle, err := newPreparedFamilyLifecycle(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	family := &PreparedFamily{lifecycle: lifecycle, input: PreparedNumpyInput{identity: digestHex('a')}}
	family.mu.Lock()
	started := make(chan struct{})
	stateDone := make(chan struct{})
	go func() {
		close(started)
		_ = family.State()
		close(stateDone)
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	lifecycleAcquired := make(chan struct{})
	go func() {
		lifecycle.mu.Lock()
		close(lifecycleAcquired)
	}()
	select {
	case <-lifecycleAcquired:
		lifecycle.mu.Unlock()
		family.mu.Unlock()
	case <-time.After(250 * time.Millisecond):
		family.mu.Unlock()
		<-lifecycleAcquired
		lifecycle.mu.Unlock()
		<-stateDone
		t.Fatal("State acquired lifecycle before family lock")
	}
	select {
	case <-stateDone:
	case <-time.After(time.Second):
		t.Fatal("State did not complete")
	}
}

func TestPreparedFamilyCloseRetriesAndRetainsOwnedBytesUntilSuccess(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &familyFakeRunner{failCloseOnce: true}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	family := &PreparedFamily{lifecycle: lifecycle, wasm: []byte("artifact"), input: PreparedNumpyInput{body: []byte("body")}, runners: map[uint64]*preparedFamilyRunner{member: runner}}
	if err := family.Close(context.Background()); err == nil || family.closeComplete || len(family.wasm) == 0 {
		t.Fatalf("first Close err=%v complete=%t wasm=%d", err, family.closeComplete, len(family.wasm))
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !family.closeComplete || family.wasm != nil || family.input.body != nil {
		t.Fatalf("complete=%t wasm=%v body=%v", family.closeComplete, family.wasm, family.input.body)
	}
}

func TestPreparedFamilyRunnerRecordsTimeoutSeparately(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &familyFakeRunner{runError: context.DeadlineExceeded}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	var disposition PreparedMemberDisposition
	runner.onTerminal = func(_ uint64, _ string, value PreparedMemberDisposition, _ []byte) { disposition = value }
	request := []byte(`{"run_id":"execution","code":"result=1","inputs":{}}`)
	if _, err := runner.Run(context.Background(), request, ""); !errors.Is(err, context.DeadlineExceeded) || disposition != PreparedMemberTimeout {
		t.Fatalf("run err=%v disposition=%s", err, disposition)
	}
}

func TestPreparedFamilyRunnerRejectsTrustedPrepareAndIsSingleUse(t *testing.T) {
	lifecycle, _ := newPreparedFamilyLifecycle(1, 1)
	member, _ := lifecycle.reserve()
	delegate := &familyFakeRunner{}
	ref := runtimeconfig.InvocationRef{AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution"}
	runner := newPreparedFamilyRunner(delegate, ref, lifecycle, member)
	request := []byte(`{"run_id":"execution","code":"result=1","inputs":{}}`)
	if _, err := runner.Run(context.Background(), request, "result=0"); !errors.Is(err, ErrPreparedTrustedPrepare) || delegate.runs != 0 {
		t.Fatalf("trusted prepare err=%v runs=%d", err, delegate.runs)
	}
	if _, err := runner.Run(context.Background(), []byte(`{"run_id":"other","code":"result=1","inputs":{}}`), ""); !errors.Is(err, ErrPreparedInvocationMismatch) || delegate.runs != 0 {
		t.Fatalf("identity drift err=%v runs=%d", err, delegate.runs)
	}
	if _, err := runner.Run(context.Background(), request, ""); err != nil || delegate.runs != 1 || delegate.ref != ref {
		t.Fatalf("run err=%v runs=%d ref=%+v", err, delegate.runs, delegate.ref)
	}
	if _, err := runner.Run(context.Background(), []byte(`{"run_id":"other"}`), "forbidden"); !errors.Is(err, ErrPreparedRunnerConsumed) || delegate.runs != 1 {
		t.Fatalf("repeat priority err=%v runs=%d", err, delegate.runs)
	}
	if _, err := runner.Run(context.Background(), request, ""); !errors.Is(err, ErrPreparedRunnerConsumed) || delegate.runs != 1 {
		t.Fatalf("repeat err=%v runs=%d", err, delegate.runs)
	}
}
