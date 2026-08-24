package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	preparedfixture "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const dataLocalSource = "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = int(dataset.sum())\n"

func TestValueSlotExactGuestMaterializesPrivateBytes(t *testing.T) {
	artifact, _ := numpyCoreGuest(t)
	object, err := valueslot.NewPreparedObject(valueslot.KindImmutableBytes, []byte("abc"), "bytes-producer-v1", "bytes-input-v1", "run-bytes")
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{
		Spec: valueslot.SlotSpec{
			ID: "slot-bytes", SourceOccurrence: "trusted-prepare", ProducerIdentity: "bytes-producer-v1", InputIdentity: "bytes-input-v1",
			Kind: valueslot.KindImmutableBytes, MaxBytes: 3, PrivacyPartition: "run-bytes", ClaimPolicy: valueslot.ClaimPrivateCopy, MaxClaims: 1,
		},
		Object: object, Strategy: valueslot.StrategyPrivateCopy,
	}})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
	runner, err := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request := runtimeconfig.RunRequest{RunID: "value-slot-bytes", Code: "result = prepared_blob.hex()", Inputs: json.RawMessage(`{}`)}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	prepare := "import _agent_runtime_host\n_raw_slot = _agent_runtime_host.materialize_slot('slot-bytes')\nif _raw_slot[:1] != b'\\x02': raise RuntimeError('wrong value-slot strategy')\nprepared_blob = bytes(_raw_slot[1:])\n"
	response, err := runner.Run(context.Background(), raw, prepare)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
	if err != nil || decoded.Status != runtimeconfig.RunResponseOK || string(decoded.Result) != `"616263"` {
		t.Fatalf("response=%s decoded=%+v err=%v", response, decoded, err)
	}
	evidence := table.Evidence()
	if evidence.Claims != 1 || evidence.CopiedBytes != 3 || evidence.Discarded != 0 || !evidence.Closed {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestSharedImmutableObjectFeedsFreshIsolatedGuestConsumers(t *testing.T) {
	artifact, _ := numpyCoreGuest(t)
	object, err := valueslot.NewPreparedObject(valueslot.KindImmutableBytes, []byte("abc"), "shared-bytes-producer-v1", "shared-bytes-input-v1", "private-cohort-a")
	if err != nil {
		t.Fatal(err)
	}
	results := make([]string, 0, 2)
	identities := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		table, createErr := valueslot.NewTable([]valueslot.Entry{{
			Spec: valueslot.SlotSpec{
				ID: "slot-bytes", SourceOccurrence: fmt.Sprintf("agent-%d-line-1", index), ProducerIdentity: "shared-bytes-producer-v1", InputIdentity: "shared-bytes-input-v1",
				Kind: valueslot.KindImmutableBytes, MaxBytes: 3, PrivacyPartition: "private-cohort-a", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
			},
			Object: object, Strategy: valueslot.StrategyPrivateCopy,
		}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		identities = append(identities, table.BackingIdentity("slot-bytes"))
		config := runtimeconfig.DefaultRunConfig()
		config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
		runner, createErr := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), artifact, config)
		if createErr != nil {
			t.Fatal(createErr)
		}
		code := "result = prepared_blob.hex()"
		if index == 0 {
			code = "prepared_blob[0] = 88\nresult = prepared_blob.hex()"
		}
		request := runtimeconfig.RunRequest{RunID: fmt.Sprintf("shared-consumer-%d", index), Code: code, Inputs: json.RawMessage(`{}`)}
		raw, _ := runtimeconfig.EncodeRunRequest(request)
		prepare := "import _agent_runtime_host\n_raw_slot = _agent_runtime_host.materialize_slot('slot-bytes')\nif _raw_slot[:1] != b'\\x02': raise RuntimeError('wrong value-slot strategy')\nprepared_blob = bytearray(_raw_slot[1:])\n"
		response, runErr := runner.Run(context.Background(), raw, prepare)
		closeErr := runner.Close(context.Background())
		if runErr != nil || closeErr != nil {
			t.Fatalf("run=%v close=%v", runErr, closeErr)
		}
		decoded, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, response)
		if decodeErr != nil || decoded.Status != runtimeconfig.RunResponseOK {
			t.Fatalf("response=%s decoded=%+v err=%v", response, decoded, decodeErr)
		}
		var value string
		if json.Unmarshal(decoded.Result, &value) != nil {
			t.Fatalf("result=%s", decoded.Result)
		}
		results = append(results, value)
	}
	if results[0] != "586263" || results[1] != "616263" || identities[0] == "" || identities[0] != identities[1] || !object.CanEvict() {
		t.Fatalf("results=%v identities=%v consumers=%d can_evict=%t", results, identities, object.ConsumerCount(), object.CanEvict())
	}
}

func TestDataLocalNumpySumMatchedEndToEnd(t *testing.T) {
	artifact, profile := numpyCoreGuest(t)
	fixture := preparedfixture.CanonicalFixture()
	workspaceRoot := t.TempDir()
	manager, err := workspace.NewManager(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.Create([]workspace.InitialFile{{Path: "input.npy", Data: fixture}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	pass, err := sourcepatch.NewDataLocalNumpySum(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := passplugin.New(pass)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(sourcepatch.DataLocalNumpySumName)
	if err != nil {
		t.Fatal(err)
	}
	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisEngine := trustedSemanticRunner(t, analysisRunner)
	analysisSession, err := analysisEngine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 2, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	selectedPatch, err := registry.Transform(context.Background(), sourcepatch.DataLocalNumpySumName, analysisSession, dataLocalSource)
	if err != nil || !selectedPatch.Applied() {
		t.Fatalf("selected patch=%+v err=%v", selectedPatch, err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	var baselineDurations, treatmentDurations []time.Duration
	for index := 0; index < 5; index++ {
		baselineOwner := fmt.Sprintf("data-local-baseline-%d", index)
		request := dataLocalRunRequest(t, fmt.Sprintf("data-local-baseline-%d", index))
		raw, err := runtimeconfig.EncodeRunRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		baselineConfig := runtimeconfig.DefaultRunConfig()
		baselineConfig.ExecutionProfile = profile
		baseline, err := (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: baselineOwner}).New(context.Background(), artifact, baselineConfig)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		response, runErr := baseline.Run(context.Background(), raw, "")
		baselineDurations = append(baselineDurations, time.Since(started))
		closeErr := baseline.Close(context.Background())
		assertDataLocalResult(t, request, response, runErr)
		if closeErr != nil {
			t.Fatal(closeErr)
		}

		treatmentOwner := fmt.Sprintf("data-local-treatment-%d", index)
		request = dataLocalRunRequest(t, fmt.Sprintf("data-local-treatment-%d", index))
		raw, err = runtimeconfig.EncodeRunRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		started = time.Now()
		producerBytes := readWorkspaceInput(t, manager, ref, treatmentOwner+"-producer")
		metadata, err := preparedfixture.Decode(producerBytes)
		if err != nil {
			t.Fatal(err)
		}
		table := dataLocalValueTable(t, strconv.FormatInt(metadata.Metadata.Sum, 10), metadata.Metadata.FileSHA256, treatmentOwner)
		treatmentConfig := runtimeconfig.DefaultRunConfig()
		treatmentConfig.ExecutionProfile = profile
		treatmentConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
		runner, err := (wazeroengine.Factory{
			WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: treatmentOwner, ValueSlots: table,
		}).New(context.Background(), artifact, treatmentConfig)
		if err != nil {
			t.Fatal(err)
		}
		engine := trustedSemanticRunner(t, runner)
		execution, runErr := registry.ExecuteSelectedValueSlot(context.Background(), sourcepatch.DataLocalNumpySumName, engine, raw, selectedPatch)
		treatmentDurations = append(treatmentDurations, time.Since(started))
		closeErr = runner.Close(context.Background())
		assertDataLocalResult(t, request, execution.Payload, runErr)
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if !execution.Applied || execution.Patch.ReplacementCount != 1 {
			t.Fatalf("execution=%+v", execution)
		}
		evidence := table.Evidence()
		if evidence.Claims != 1 || evidence.Discarded != 0 || !evidence.Closed {
			t.Fatalf("evidence=%+v", evidence)
		}
	}
	baselineMedian := medianDuration(baselineDurations)
	treatmentMedian := medianDuration(treatmentDurations)
	if treatmentMedian >= baselineMedian {
		t.Fatalf("data-local reduction did not retain: baseline=%s treatment=%s", baselineMedian, treatmentMedian)
	}
	t.Logf("data-local matched medians: baseline=%s treatment=%s ratio=%.4f", baselineMedian, treatmentMedian, float64(treatmentMedian)/float64(baselineMedian))
}

func numpyCoreGuest(t *testing.T) ([]byte, *runtimeconfig.ExecutionProfile) {
	t.Helper()
	path := os.Getenv("AGENT_RUNTIME_GUEST")
	if path == "" {
		t.Skip("AGENT_RUNTIME_GUEST is not set; exact numpy-core artifact required")
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"numpy"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "numpy-core", ArtifactSHA256: fmt.Sprintf("sha256:%x", digest[:]), ManifestSHA256: "sha256:" + strings.Repeat("a", 64),
		ImportRoots: []string{"numpy"}, QualifiedImportRoots: []string{"numpy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact, &profile
}

func dataLocalRunRequest(t *testing.T, runID string) runtimeconfig.RunRequest {
	t.Helper()
	return runtimeconfig.RunRequest{
		RunID: runID, Code: dataLocalSource, Inputs: json.RawMessage(`{}`),
		Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}},
	}
}

func readWorkspaceInput(t *testing.T, manager *workspace.Manager, ref workspace.Ref, owner string) []byte {
	t.Helper()
	lease, err := manager.Acquire(ref, owner)
	if err != nil {
		t.Fatal(err)
	}
	root, err := lease.BindMountSource()
	if err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	payload, readErr := os.ReadFile(filepath.Join(root, "input.npy"))
	releaseErr := lease.Release()
	if readErr != nil || releaseErr != nil {
		t.Fatalf("read=%v release=%v", readErr, releaseErr)
	}
	return payload
}

func dataLocalValueTable(t *testing.T, sum, inputIdentity, privacy string) *valueslot.Table {
	t.Helper()
	object, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte(sum), "numpy-int64-sum-v1", inputIdentity, privacy)
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{
		Spec: valueslot.SlotSpec{
			ID: "slot-numpy-sum-v1", SourceOccurrence: "line-3:result", ProducerIdentity: "numpy-int64-sum-v1", InputIdentity: inputIdentity,
			Kind: valueslot.KindJSONScalar, MaxBytes: 32, PrivacyPartition: privacy, ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
		},
		Object: object, Strategy: valueslot.StrategyInlineJSON,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func assertDataLocalResult(t *testing.T, request runtimeconfig.RunRequest, response []byte, runErr error) {
	t.Helper()
	if runErr != nil {
		t.Fatal(runErr)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
	if err != nil || decoded.Status != runtimeconfig.RunResponseOK || string(decoded.Result) != "549755289600" {
		t.Fatalf("response=%s decoded=%+v err=%v", response, decoded, err)
	}
}

func medianDuration(values []time.Duration) time.Duration {
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	return copyValues[len(copyValues)/2]
}
