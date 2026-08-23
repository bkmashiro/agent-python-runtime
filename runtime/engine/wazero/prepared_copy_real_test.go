package wazero

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

func realPreparedInput(t *testing.T, shape []uint64, values []uint64) PreparedNumpyInput {
	t.Helper()
	body := make([]byte, len(values)*8)
	for index, value := range values {
		binary.LittleEndian.PutUint64(body[index*8:], value)
	}
	bindings := numpycodec.Bindings{
		ArtifactSHA256: digestHex('a'), ExecutionProfileID: "numpy-core", ExecutionProfileSHA256: digestHex('b'),
		ImportClosureSHA256: digestHex('c'), SourceSHA256: digestHex('d'), InputsSHA256: digestHex('e'), PassRegistrationSHA256: digestHex('f'),
	}
	producer, err := json.Marshal(numpycodec.ProducerValue{
		SchemaVersion: numpycodec.ProducerValueSchemaVersion, DType: "<i8", Shape: shape, Order: "C", CContiguous: true,
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

func TestPreparedNumpyPrivateCopyRunsThreeLayoutsAndIsolatesMutation(t *testing.T) {
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
		input := realPreparedInput(t, test.shape, test.values)
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
