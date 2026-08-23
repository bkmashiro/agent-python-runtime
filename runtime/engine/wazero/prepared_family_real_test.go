package wazero

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
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
	request := runtimeconfig.RunRequest{RunID: runID, Code: "import numpy\ndataset.flat[0] = dataset.flat[0] + 10\nresult = int(dataset.sum())\n", Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}}
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

func TestPreparedFamilyAutoFallsBackOnlyForCOWIneligibleArtifact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux eligibility fallback")
	}
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	digest := sha256.Sum256(wasm)
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"numpy"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "numpy-core", ArtifactSHA256: fmt.Sprintf("sha256:%x", digest[:]), ManifestSHA256: digestHex('a'),
		ImportRoots: []string{"numpy"}, QualifiedImportRoots: []string{"numpy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	input := realPreparedInput(t, &profile, []uint64{1}, []uint64{7})
	family, err := PrepareNumpyFamily(context.Background(), wasm, PreparedFamilyConfig{ImageConfig: config, MaxConsumers: 1, MaxActive: 1, Mode: PreparedFamilyAuto}, input)
	if err != nil || family.State().Disposition != PreparedDispositionPrivateCopy {
		t.Fatalf("auto family=%v err=%v", family, err)
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareNumpyFamily(context.Background(), wasm, PreparedFamilyConfig{ImageConfig: config, MaxConsumers: 1, MaxActive: 1, Mode: PreparedFamilyPrivateCOW}, input); !errors.Is(err, ErrCOWIneligible) {
		t.Fatalf("explicit COW err=%v", err)
	}
}

func TestPreparedFamilyExplicitPrivateCopyClearsConsumerCOW(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
	imageConfig := runtimeconfig.DefaultRunConfig()
	imageConfig.ExecutionProfile = profile
	input := realPreparedInput(t, profile, []uint64{2}, []uint64{3, 4})
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{ImageConfig: imageConfig, MaxConsumers: 1, MaxActive: 1, Mode: PreparedFamilyPrivateCopy}, input)
	if err != nil {
		t.Fatal(err)
	}
	memberConfig := imageConfig
	memberConfig.Mechanisms.PreparedRuntime = true
	memberConfig.Mechanisms.MemoryCOW = true
	runner, err := family.NewRunner(context.Background(), PreparedRunnerConfig{RunConfig: memberConfig, InvocationRef: preparedInvocation("copy-member", 1)})
	if err != nil {
		t.Fatal(err)
	}
	runFamilyMember(t, runner, "copy-member")
	if family.State().Disposition != PreparedDispositionPrivateCopy {
		t.Fatalf("state=%+v", family.State())
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedFamilyRealGuestSingleUseBoundsAndRecords(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
	imageConfig := runtimeconfig.DefaultRunConfig()
	imageConfig.ExecutionProfile = profile
	input := realPreparedInput(t, profile, []uint64{2, 2}, []uint64{1, 2, 3, 4})
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{
		ImageConfig: imageConfig, MaxConsumers: 2, MaxActive: 1, Mode: PreparedFamilyAuto,
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
	drift := memberConfig
	drift.MemoryLimitPages++
	if _, err := family.NewRunner(context.Background(), PreparedRunnerConfig{RunConfig: drift, InvocationRef: preparedInvocation("family-drift", 1)}); !errors.Is(err, ErrPreparedFamilyDrift) {
		t.Fatalf("drift runner err=%v", err)
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
