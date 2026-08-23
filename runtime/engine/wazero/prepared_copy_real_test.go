package wazero

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
)

func realPreparedGuest(t *testing.T) ([]byte, *runtimeconfig.ExecutionProfile) {
	t.Helper()
	path := os.Getenv("AGENT_RUNTIME_GUEST")
	if path == "" {
		t.Skip("AGENT_RUNTIME_GUEST is not set")
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", digest[:])
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"numpy"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "numpy-core", ArtifactSHA256: artifactSHA, ManifestSHA256: "sha256:" + strings.Repeat("a", 64),
		ImportRoots: []string{"numpy"}, QualifiedImportRoots: []string{"numpy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact, &profile
}

func realPreparedInputRaw(t *testing.T, profile *runtimeconfig.ExecutionProfile, dtype string, shape []uint64, body []byte) PreparedNumpyInput {
	t.Helper()
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = profile
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	bindings := numpycodec.Bindings{
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileID: "numpy-core", ExecutionProfileSHA256: profileSHA256,
		ImportClosureSHA256: preparedImportClosureIdentity(profile.AvailableImports(), profile.QualifiedImports()), SourceSHA256: digestHex('d'), InputsSHA256: digestHex('e'), PassRegistrationSHA256: digestHex('f'),
	}
	producer, err := json.Marshal(numpycodec.ProducerValue{
		SchemaVersion: numpycodec.ProducerValueSchemaVersion, DType: dtype, Shape: shape, Order: "C", CContiguous: true,
		NBytes: uint64(len(body)), BodySHA256: resultblob.BytesDigest(body), BodyBase64: base64.StdEncoding.EncodeToString(body),
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

func realPreparedInput(t *testing.T, profile *runtimeconfig.ExecutionProfile, shape []uint64, values []uint64) PreparedNumpyInput {
	t.Helper()
	body := make([]byte, len(values)*8)
	for index, value := range values {
		binary.LittleEndian.PutUint64(body[index*8:], value)
	}
	return realPreparedInputRaw(t, profile, "<i8", shape, body)
}

func runPreparedCopy(t *testing.T, artifact []byte, profile *runtimeconfig.ExecutionProfile, input PreparedNumpyInput, runID, code string) json.RawMessage {
	t.Helper()
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = profile
	runner, err := newPreparedNumpyCopyEngine(context.Background(), artifact, config, nil, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: runID, Code: code, Inputs: json.RawMessage(`{}`),
		Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`)}, response)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.Result
}

func executePreparedEngine(runner *Engine, runID, code string) (json.RawMessage, error) {
	request := runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{}}}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	response, err := runner.Run(context.Background(), raw, "")
	if err != nil {
		return nil, err
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
	if err != nil {
		return nil, err
	}
	if decoded.Status != runtimeconfig.RunResponseOK {
		return nil, fmt.Errorf("Guest status is %s", decoded.Status)
	}
	return decoded.Result, nil
}

func runPreparedEngine(t *testing.T, runner *Engine, runID, code string) json.RawMessage {
	t.Helper()
	result, err := executePreparedEngine(runner, runID, code)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPreparedNumpyPrivateCOWSupportsMultipleLayoutsAndIsolatesMutation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private COW requires Linux")
	}
	artifact, profile := realPreparedGuest(t)
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = profile
	config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true, MemoryCOW: true}
	runner, err := New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	floatBody := make([]byte, 6*4)
	for index, value := range []float32{1.5, 2.5, 3.5, 4.5, 5.5, 6.5} {
		binary.LittleEndian.PutUint32(floatBody[index*4:], math.Float32bits(value))
	}
	inputs := []PreparedNumpyInput{
		realPreparedInput(t, profile, []uint64{2, 2}, []uint64{1, 2, 3, 4}),
		realPreparedInputRaw(t, profile, "<f4", []uint64{2, 3}, floatBody),
		realPreparedInputRaw(t, profile, "|u1", []uint64{3}, []byte{7, 8, 9}),
	}
	for index, input := range inputs {
		if err := runner.PrepareNumpyCOWInput(context.Background(), input); err != nil {
			t.Fatalf("prepare %d: %v", index, err)
		}
		state := runner.PreparedImageState()
		if state.PreparedInputSHA256 != input.IdentitySHA256() || state.ParentTrustedPrepareSHA256 == "" || state.BaselineBytes == 0 {
			t.Fatalf("state %d: %+v", index, state)
		}
		mutated := runPreparedEngine(t, runner, fmt.Sprintf("cow-mutate-%d", index), "dataset.flat[0] = 99\nresult = [dataset.dtype.str, list(dataset.shape), float(dataset.sum())]\n")
		fresh := runPreparedEngine(t, runner, fmt.Sprintf("cow-fresh-%d", index), "result = [dataset.dtype.str, list(dataset.shape), float(dataset.flat[0]), float(dataset.sum())]\n")
		if strings.Contains(string(fresh), "99") || len(mutated) == 0 {
			t.Fatalf("mutation leaked for input %d: mutated=%s fresh=%s", index, mutated, fresh)
		}
	}

	for _, fanout := range []int{0, 1, 2, 4} {
		values := []uint64{uint64(fanout + 1), 20, 30, 40}
		input := realPreparedInput(t, profile, []uint64{2, 2}, values)
		if err := runner.PrepareNumpyCOWInput(context.Background(), input); err != nil {
			t.Fatalf("prepare fanout %d: %v", fanout, err)
		}
		var wait sync.WaitGroup
		errorsByConsumer := make(chan error, fanout)
		for consumer := 0; consumer < fanout; consumer++ {
			wait.Add(1)
			go func(consumer int) {
				defer wait.Done()
				result, err := executePreparedEngine(runner, fmt.Sprintf("cow-fanout-%d-%d", fanout, consumer), "result = int(dataset.flat[0])\n")
				if err == nil && string(result) != fmt.Sprintf("%d", fanout+1) {
					err = fmt.Errorf("unexpected result %s", result)
				}
				errorsByConsumer <- err
			}(consumer)
		}
		wait.Wait()
		close(errorsByConsumer)
		for err := range errorsByConsumer {
			if err != nil {
				t.Fatalf("fanout %d: %v", fanout, err)
			}
		}
	}

	failedRequest := runtimeconfig.RunRequest{RunID: "cow-error", Code: "raise RuntimeError('fixture')\n", Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{}}}
	raw, err := runtimeconfig.EncodeRunRequest(failedRequest)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runner.Run(context.Background(), raw, "")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(failedRequest, response)
	if err != nil || decoded.Status != runtimeconfig.RunResponseError {
		t.Fatalf("error response=%s err=%v", response, err)
	}
	if got := runPreparedEngine(t, runner, "cow-after-error", "result = int(dataset.flat[0])\n"); string(got) != "5" {
		t.Fatalf("post-error state=%s", got)
	}
}

func TestPreparedNumpyPrivateCopySupportsMultipleLayoutsAndMutationIsolation(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
	cases := []struct {
		shape  []uint64
		values []uint64
	}{
		{shape: []uint64{4}, values: []uint64{1, 2, 3, 4}},
		{shape: []uint64{2, 2}, values: []uint64{5, 6, 7, 8}},
		{shape: []uint64{1, 2, 2}, values: []uint64{9, 10, 11, 12}},
	}
	for index, test := range cases {
		input := realPreparedInput(t, profile, test.shape, test.values)
		mutated := runPreparedCopy(t, artifact, profile, input, fmt.Sprintf("copy-mutate-%d", index), "dataset.flat[0] = 99\nresult = [list(dataset.shape), int(dataset.sum())]\n")
		fresh := runPreparedCopy(t, artifact, profile, input, fmt.Sprintf("copy-fresh-%d", index), "result = [list(dataset.shape), int(dataset.sum()), int(dataset.flat[0])]\n")
		var mutatedValue, freshValue []any
		if json.Unmarshal(mutated, &mutatedValue) != nil || json.Unmarshal(fresh, &freshValue) != nil {
			t.Fatalf("invalid results mutated=%s fresh=%s", mutated, fresh)
		}
		if got := int(freshValue[2].(float64)); got != int(test.values[0]) {
			t.Fatalf("case=%d mutation leaked: fresh=%s", index, fresh)
		}
	}
}
