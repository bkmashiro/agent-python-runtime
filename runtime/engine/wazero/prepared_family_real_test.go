package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func preparedInvocation(runID string, attempt uint32) runtimeconfig.InvocationRef {
	return runtimeconfig.InvocationRef{AgentRunID: "family-parent", InvocationID: runID, InvocationAttempt: attempt, ExecutionID: runID}
}

func runFamilyMember(t *testing.T, runner interface {
	Run(context.Context, []byte, string) ([]byte, error)
}, runID string) {
	t.Helper()
	request := runtimeconfig.RunRequest{RunID: runID, Code: "dataset.flat[0] = dataset.flat[0] + 10\nresult = int(dataset.sum())\n", Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{}}}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runner.Run(context.Background(), raw, "")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
	if err != nil || decoded.Status != runtimeconfig.RunResponseOK {
		t.Fatalf("response=%s err=%v", response, err)
	}
}

func TestPreparedFamilyRealGuestSingleUseBoundsAndRecords(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
	imageConfig := runtimeconfig.DefaultRunConfig()
	imageConfig.ExecutionProfile = profile
	input := realPreparedInput(t, profile, []uint64{2, 2}, []uint64{1, 2, 3, 4})
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{
		RunConfig: imageConfig, MaxConsumers: 2, MaxActive: 1, Mode: PreparedFamilyAuto,
	}, input)
	if err != nil {
		t.Fatal(err)
	}

	memberConfig := imageConfig
	first, err := family.NewRunner(context.Background(), PreparedRunnerConfig{RunConfig: memberConfig, InvocationRef: preparedInvocation("family-first", 1)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := family.NewRunner(context.Background(), PreparedRunnerConfig{RunConfig: memberConfig, InvocationRef: preparedInvocation("family-second", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := family.NewRunner(context.Background(), PreparedRunnerConfig{RunConfig: memberConfig, InvocationRef: preparedInvocation("family-third", 1)}); !errors.Is(err, ErrPreparedFamilyConsumerLimit) {
		t.Fatalf("third runner err=%v", err)
	}
	runFamilyMember(t, first, "family-first")
	if _, err := first.Run(context.Background(), []byte(`{}`), ""); !errors.Is(err, ErrPreparedRunnerConsumed) {
		t.Fatalf("second Run err=%v", err)
	}
	runFamilyMember(t, second, "family-second")
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := family.State()
	if !state.Closed || state.Created != 2 || state.Terminal != 2 || state.FamilySHA256 == "" || state.InputSHA256 != input.IdentitySHA256() {
		t.Fatalf("state=%+v", state)
	}
	records := family.Records()
	if len(records) != 2 {
		t.Fatalf("records=%+v", records)
	}
	for _, record := range records {
		if err := record.Validate(); err != nil || record.FamilySHA256 != state.FamilySHA256 || record.InputSHA256 != input.IdentitySHA256() {
			t.Fatalf("record=%+v err=%v", record, err)
		}
	}
}
