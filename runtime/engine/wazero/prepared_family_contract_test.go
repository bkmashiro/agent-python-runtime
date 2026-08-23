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
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
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

type familyFakeRunner struct {
	runs int
	ref  runtimeconfig.InvocationRef
}

func (runner *familyFakeRunner) Run(ctx context.Context, _ []byte, trustedPrepare string) ([]byte, error) {
	runner.runs++
	runner.ref, _ = engine.InvocationRefFromContext(ctx)
	if trustedPrepare != "" {
		return nil, errors.New("trusted prepare reached delegate")
	}
	return []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1}}`), nil
}
func (*familyFakeRunner) Close(context.Context) error   { return nil }
func (*familyFakeRunner) Properties() engine.Properties { return engine.Properties{Backend: "fake"} }

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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, request, ""); err != nil || delegate.runs != 1 || delegate.ref != ref {
		t.Fatalf("run err=%v runs=%d ref=%+v", err, delegate.runs, delegate.ref)
	}
	if _, err := runner.Run(context.Background(), request, ""); !errors.Is(err, ErrPreparedRunnerConsumed) || delegate.runs != 1 {
		t.Fatalf("repeat err=%v runs=%d", err, delegate.runs)
	}
}
