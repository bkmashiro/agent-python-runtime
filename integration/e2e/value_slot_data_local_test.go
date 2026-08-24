package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

const dataLocalSource = "import io\nimport numpy as np\ndataset = np.load(io.BytesIO(open('/workspace/input.npy', 'rb').read()), allow_pickle=False)\nresult = int(dataset.sum())\n"

const directPreparedValueSource = "result = prepared_value\n"

func TestDataLocalNumpySumRejectsUnattestedScalarBeforeGuest(t *testing.T) {
	artifact, profile := numpyCoreGuest(t)
	object, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("0"), "numpy-int64-sum-v1", valueslot.CanonicalNumpyInt64FileSHA256, "private-cohort")
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{
		Spec: valueslot.SlotSpec{
			ID: "slot-numpy-sum-v1", SourceOccurrence: "line-4:result",
			ProducerIdentity: "numpy-int64-sum-v1", InputIdentity: valueslot.CanonicalNumpyInt64FileSHA256,
			Kind: valueslot.KindJSONScalar, MaxBytes: 32, PrivacyPartition: "private-cohort",
			ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
		},
		Object: object, Strategy: valueslot.StrategyInlineJSON,
	}})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
	runner, err := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	request := runtimeconfig.RunRequest{RunID: "unattested-scalar", Code: "result = 7", Inputs: json.RawMessage(`{}`)}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := engine.RunValueSlotSourcePatchDerived(context.Background(), raw, sourcepatch.Patch{}, passregistration.Registration{})
	decoded, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, result.Payload)
	if runErr != nil || decodeErr != nil || result.Applied || string(decoded.Result) != "7" {
		t.Fatalf("result=%+v run=%v decode=%v decoded=%+v", result, runErr, decodeErr, decoded)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDataLocalNumpySumDigestDriftFallsBackWithoutSlotClaim(t *testing.T) {
	artifact, profile := numpyCoreGuest(t)
	fixture := preparedfixture.CanonicalFixture()
	workspaceRoot := t.TempDir()
	if err := os.Chmod(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
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
	analysisConfig.Timeout = 90 * time.Second
	analysisConfig.ExecutionProfile = profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisEngine := trustedSemanticRunner(t, analysisRunner)
	analysisSession, err := analysisEngine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 4, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer analysisSession.Close(context.Background())
	defer analysisRunner.Close(context.Background())

	request := dataLocalRunRequest(t, "data-local-digest-drift")
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var selectedEngine *wazeroengine.Engine
	execution, runErr := registry.ExecuteValueSlot(
		context.Background(), sourcepatch.DataLocalNumpySumName, analysisSession,
		func(context.Context) (passplugin.ValueSlotSourcePatchRunner, error) {
			return nil, errors.New("unexpected registry fallback factory")
		},
		func(context.Context) (passplugin.ValueSlotSourcePatchRunner, error) {
			lease, acquireErr := manager.Acquire(ref, "digest-drift-producer")
			if acquireErr != nil {
				return nil, acquireErr
			}
			root, bindErr := lease.BindMountSource()
			if bindErr != nil {
				_ = lease.Release()
				return nil, bindErr
			}
			payload, readErr := os.ReadFile(filepath.Join(root, "input.npy"))
			if readErr != nil {
				_ = lease.Release()
				return nil, readErr
			}
			table, producerErr := valueslot.NewCanonicalNumpyInt64SumTable(payload, "digest-drift-treatment")
			if producerErr != nil {
				_ = lease.Release()
				return nil, producerErr
			}
			payload[len(payload)-1] ^= 1
			writeErr := os.WriteFile(filepath.Join(root, "input.npy"), payload, 0o600)
			releaseErr := lease.Release()
			if writeErr != nil || releaseErr != nil {
				_ = table.Close()
				return nil, errors.Join(writeErr, releaseErr)
			}
			config := runtimeconfig.DefaultRunConfig()
			config.ExecutionProfile = profile
			config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
			runner, factoryErr := (wazeroengine.Factory{
				WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "digest-drift-treatment", ValueSlots: table,
			}).New(context.Background(), artifact, config)
			if factoryErr != nil {
				_ = table.Close()
				return nil, factoryErr
			}
			selectedEngine = trustedSemanticRunner(t, runner)
			return selectedEngine, nil
		},
		raw, "",
	)
	if runErr != nil {
		t.Fatal(runErr)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, execution.Payload)
	if err != nil || decoded.Status != runtimeconfig.RunResponseOK || string(decoded.Result) == valueslot.CanonicalNumpyInt64Sum || execution.Applied {
		t.Fatalf("execution=%+v decoded=%+v err=%v", execution, decoded, err)
	}
	if selectedEngine == nil {
		t.Fatal("selected runner was not constructed")
	}
	if evidence := selectedEngine.ValueSlotEvidence(); evidence.Claims != 0 || !evidence.Closed {
		t.Fatalf("evidence=%+v", evidence)
	}
}

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
	prepare := "import _agent_runtime_host\n_raw_slot = _agent_runtime_host.materialize_slot('slot-bytes')\nif _raw_slot[:1] != b'\\x02': raise RuntimeError('wrong value-slot strategy')\nprepared_blob = bytes(_raw_slot[1:])\n"
	engine := trustedSemanticRunner(t, runner)
	for index := 0; index < 2; index++ {
		request := runtimeconfig.RunRequest{RunID: fmt.Sprintf("value-slot-bytes-%d", index), Code: "result = prepared_blob.hex()", Inputs: json.RawMessage(`{}`)}
		raw, encodeErr := runtimeconfig.EncodeRunRequest(request)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		response, runErr := runner.Run(context.Background(), raw, prepare)
		if runErr != nil {
			t.Fatal(runErr)
		}
		decoded, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, response)
		if decodeErr != nil || decoded.Status != runtimeconfig.RunResponseOK || string(decoded.Result) != `"616263"` {
			t.Fatalf("run=%d response=%s decoded=%+v err=%v", index, response, decoded, decodeErr)
		}
		evidence := engine.ValueSlotEvidence()
		if evidence.Claims != 1 || evidence.CopiedBytes != 3 || evidence.Discarded != 0 || !evidence.Closed {
			t.Fatalf("run=%d evidence=%+v", index, evidence)
		}
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evidence := table.Evidence(); !evidence.Closed || evidence.Claims != 0 {
		t.Fatalf("template evidence=%+v", evidence)
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
		config.Mechanisms = runtimeconfig.MechanismSet{ValueSlots: true}
		runner, createErr := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), artifact, config)
		if createErr != nil {
			t.Fatal(createErr)
		}
		code := "result = prepared_value.hex()"
		if index == 0 {
			code = "prepared_value[0] = 88\nresult = prepared_value.hex()"
		}
		request := runtimeconfig.RunRequest{RunID: fmt.Sprintf("shared-consumer-%d", index), Code: code, Inputs: json.RawMessage(`{}`)}
		raw, _ := runtimeconfig.EncodeRunRequest(request)
		prepare, prepareErr := valueslot.PythonPrelude("slot-bytes")
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
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
	if err := os.Chmod(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
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
	analysisConfig.Timeout = 90 * time.Second
	analysisConfig.ExecutionProfile = profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisEngine := trustedSemanticRunner(t, analysisRunner)
	analysisSession, err := analysisEngine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 8, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var baselineDurations, treatmentDurations []time.Duration
	var treatmentCopiedBytes []uint64
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
		var selectedEngine *wazeroengine.Engine
		execution, runErr := registry.ExecuteValueSlot(
			context.Background(), sourcepatch.DataLocalNumpySumName, analysisSession,
			func(context.Context) (passplugin.ValueSlotSourcePatchRunner, error) {
				fallbackConfig := runtimeconfig.DefaultRunConfig()
				fallbackConfig.ExecutionProfile = profile
				fallback, factoryErr := (wazeroengine.Factory{
					WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: treatmentOwner + "-fallback",
				}).New(context.Background(), artifact, fallbackConfig)
				if factoryErr != nil {
					return nil, factoryErr
				}
				return trustedSemanticRunner(t, fallback), nil
			},
			func(context.Context) (passplugin.ValueSlotSourcePatchRunner, error) {
				producerBytes := readWorkspaceInput(t, manager, ref, treatmentOwner+"-producer")
				table, producerErr := valueslot.NewCanonicalNumpyInt64SumTable(producerBytes, treatmentOwner)
				if producerErr != nil {
					return nil, producerErr
				}
				treatmentConfig := runtimeconfig.DefaultRunConfig()
				treatmentConfig.ExecutionProfile = profile
				treatmentConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, ValueSlots: true}
				runner, factoryErr := (wazeroengine.Factory{
					WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: treatmentOwner, ValueSlots: table,
				}).New(context.Background(), artifact, treatmentConfig)
				if factoryErr != nil {
					_ = table.Close()
					return nil, factoryErr
				}
				selectedEngine = trustedSemanticRunner(t, runner)
				return selectedEngine, nil
			},
			raw, "",
		)
		treatmentDurations = append(treatmentDurations, time.Since(started))
		assertDataLocalResult(t, request, execution.Payload, runErr)
		if !execution.Applied || execution.Patch.ReplacementCount != 1 {
			t.Fatalf("execution=%+v", execution)
		}
		if selectedEngine == nil {
			t.Fatal("selected runner was not constructed")
		}
		evidence := selectedEngine.ValueSlotEvidence()
		if evidence.Claims != 1 || evidence.Discarded != 0 || !evidence.Closed {
			t.Fatalf("evidence=%+v", evidence)
		}
		treatmentCopiedBytes = append(treatmentCopiedBytes, evidence.CopiedBytes)
	}
	if err := analysisSession.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	baselineMedian := medianDuration(baselineDurations)
	treatmentMedian := medianDuration(treatmentDurations)
	disposition := "retain"
	if treatmentMedian >= baselineMedian {
		disposition = "reject"
	}
	t.Logf("data-local matched evidence: baseline_ns=%v treatment_ns=%v baseline_median_ns=%d treatment_median_ns=%d ratio=%.6f disposition=%s fixture_bytes=%d host_to_guest_bytes=%v guest_peak_memory=unavailable", durationsNanos(baselineDurations), durationsNanos(treatmentDurations), baselineMedian.Nanoseconds(), treatmentMedian.Nanoseconds(), float64(treatmentMedian)/float64(baselineMedian), disposition, len(fixture), treatmentCopiedBytes)
}

func TestDirectPreparedNumpySumMatchedEndToEnd(t *testing.T) {
	artifact, profile := numpyCoreGuest(t)
	fixture := preparedfixture.CanonicalFixture()
	workspaceRoot := t.TempDir()
	if err := os.Chmod(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.Create([]workspace.InitialFile{{Path: "input.npy", Data: fixture}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	producerStarted := time.Now()
	table, err := valueslot.NewCanonicalNumpyInt64SumTable(fixture, "private-cohort")
	if err != nil {
		t.Fatal(err)
	}
	producerDuration := time.Since(producerStarted)
	prelude, err := valueslot.PythonPrelude("slot-numpy-sum-v1")
	if err != nil {
		t.Fatal(err)
	}

	baselineConfig := runtimeconfig.DefaultRunConfig()
	baselineConfig.Timeout = 90 * time.Second
	baselineConfig.ExecutionProfile = profile
	baselineConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	baselineRunner, err := (wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "direct-value-slot-baseline",
	}).New(context.Background(), artifact, baselineConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer baselineRunner.Close(context.Background())

	treatmentConfig := runtimeconfig.DefaultRunConfig()
	treatmentConfig.Timeout = 90 * time.Second
	treatmentConfig.ExecutionProfile = profile
	treatmentConfig.Mechanisms = runtimeconfig.MechanismSet{ValueSlots: true}
	treatmentRunner, err := (wazeroengine.Factory{ValueSlots: table}).New(context.Background(), artifact, treatmentConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer treatmentRunner.Close(context.Background())
	treatmentEngine := trustedSemanticRunner(t, treatmentRunner)

	var baselineDurations, treatmentDurations []time.Duration
	for index := 0; index < 5; index++ {
		baselineRequest := dataLocalRunRequest(t, fmt.Sprintf("direct-value-slot-baseline-%d", index))
		baselineRaw, encodeErr := runtimeconfig.EncodeRunRequest(baselineRequest)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		treatmentRequest := directPreparedValueRunRequest(fmt.Sprintf("direct-value-slot-treatment-%d", index))
		treatmentRaw, encodeErr := runtimeconfig.EncodeRunRequest(treatmentRequest)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}

		runBaseline := func() {
			started := time.Now()
			response, runErr := baselineRunner.Run(context.Background(), baselineRaw, "")
			baselineDurations = append(baselineDurations, time.Since(started))
			assertDataLocalResult(t, baselineRequest, response, runErr)
		}
		runTreatment := func() {
			started := time.Now()
			response, runErr := treatmentRunner.Run(context.Background(), treatmentRaw, prelude)
			treatmentDurations = append(treatmentDurations, time.Since(started))
			assertDataLocalResult(t, treatmentRequest, response, runErr)
			if evidence := treatmentEngine.ValueSlotEvidence(); evidence.Claims != 1 || evidence.CopiedBytes != 12 || evidence.Discarded != 0 || !evidence.Closed {
				t.Fatalf("value-slot evidence=%+v", evidence)
			}
		}
		if index%2 == 0 {
			runBaseline()
			runTreatment()
		} else {
			runTreatment()
			runBaseline()
		}
	}

	baselineMedian := medianDuration(baselineDurations)
	treatmentMedian := medianDuration(treatmentDurations)
	if lifecycle := treatmentEngine.SemanticAnalysisLifecycleEvidence(); lifecycle.Invocations != 0 || lifecycle.ModuleInstantiations != 0 {
		t.Fatalf("direct value-slot lane constructed analyzer: %+v", lifecycle)
	}
	if treatmentMedian >= baselineMedian {
		t.Fatalf("direct prepared value did not improve complete Run latency: baseline=%v treatment=%v", baselineDurations, treatmentDurations)
	}
	t.Logf("direct prepared value evidence: baseline_ns=%v treatment_ns=%v baseline_median_ns=%d treatment_median_ns=%d ratio=%.6f producer_ns=%d fixture_bytes=%d host_to_guest_bytes=12 analyzer_invocations=0", durationsNanos(baselineDurations), durationsNanos(treatmentDurations), baselineMedian.Nanoseconds(), treatmentMedian.Nanoseconds(), float64(treatmentMedian)/float64(baselineMedian), producerDuration.Nanoseconds(), len(fixture))
}

func durationsNanos(values []time.Duration) []int64 {
	result := make([]int64, len(values))
	for index, value := range values {
		result[index] = value.Nanoseconds()
	}
	return result
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
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"io", "numpy"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "numpy-core", ArtifactSHA256: fmt.Sprintf("sha256:%x", digest[:]), ManifestSHA256: "sha256:" + strings.Repeat("a", 64),
		ImportRoots: []string{"io", "numpy"}, QualifiedImportRoots: []string{"io", "numpy"},
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
		Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"io", "numpy"}},
	}
}

func directPreparedValueRunRequest(runID string) runtimeconfig.RunRequest {
	return runtimeconfig.RunRequest{
		RunID: runID, Code: directPreparedValueSource, Inputs: json.RawMessage(`{}`),
		Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{}},
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
