package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	passregistration "github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
)

type bundle struct {
	wasm    []byte
	profile runtimeconfig.ExecutionProfile
	root    string
}

type guestResponse struct {
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
	Logs    json.RawMessage `json:"logs"`
	Metrics struct {
		CapabilityCalls uint64 `json:"capability_calls"`
	} `json:"metrics"`
}

type consumerResult struct {
	First       int64 `json:"first"`
	Sum         int64 `json:"sum"`
	CContiguous bool  `json:"c_contiguous"`
}

type materializedResult struct {
	SchemaVersion             string         `json:"schema_version"`
	Value                     consumerResult `json:"value"`
	MaterializationDurationNS int64          `json:"materialization_duration_ns"`
}

type guestEvidence struct {
	RunID           string                          `json:"run_id"`
	CapabilityCalls uint64                          `json:"capability_calls"`
	COWProbe        wazeroengine.COWProbe           `json:"cow_probe"`
	Prepared        wazeroengine.PreparedState      `json:"prepared_state"`
	Image           wazeroengine.PreparedImageState `json:"prepared_image"`
}

type output struct {
	SchemaVersion            string                         `json:"schema_version"`
	Platform                 string                         `json:"platform"`
	PrivateCOWRequired       bool                           `json:"private_cow_required"`
	SourceCommit             string                         `json:"source_commit"`
	SourceTree               string                         `json:"source_tree"`
	ProbeBinarySHA256        string                         `json:"probe_binary_sha256"`
	ArtifactSHA256           string                         `json:"artifact_sha256"`
	DescriptorSHA256         string                         `json:"descriptor_sha256"`
	BodySHA256               string                         `json:"body_sha256"`
	BodyBytes                uint64                         `json:"body_bytes"`
	Producer                 guestEvidence                  `json:"producer"`
	Publication              numpycodec.PublicationEvidence `json:"publication"`
	OriginalA                guestEvidence                  `json:"original_a"`
	OriginalB                guestEvidence                  `json:"original_b"`
	ConsumerA                guestEvidence                  `json:"consumer_a"`
	ConsumerB                guestEvidence                  `json:"consumer_b"`
	PlanA                    numpycodec.MaterializationPlan `json:"plan_a"`
	PlanB                    numpycodec.MaterializationPlan `json:"plan_b"`
	OriginalAResult          consumerResult                 `json:"original_a_result"`
	OriginalBResult          consumerResult                 `json:"original_b_result"`
	ConsumerAResult          consumerResult                 `json:"consumer_a_result"`
	ConsumerBResult          consumerResult                 `json:"consumer_b_result"`
	MaterializationDurationA int64                          `json:"materialization_duration_a_ns"`
	MaterializationDurationB int64                          `json:"materialization_duration_b_ns"`
	ResultParity             bool                           `json:"result_parity"`
	OriginalError            guestEvidence                  `json:"original_error"`
	ConsumerError            guestEvidence                  `json:"consumer_error"`
	PlanError                numpycodec.MaterializationPlan `json:"plan_error"`
	OriginalErrorObject      json.RawMessage                `json:"original_error_object"`
	ConsumerErrorObject      json.RawMessage                `json:"consumer_error_object"`
	OriginalErrorLogs        json.RawMessage                `json:"original_error_logs"`
	ConsumerErrorLogs        json.RawMessage                `json:"consumer_error_logs"`
	ErrorTracebackLogParity  bool                           `json:"error_traceback_log_parity"`
	HostHashAfterA           string                         `json:"host_hash_after_a"`
	ConsumerMutationIsolated bool                           `json:"consumer_mutation_isolated"`
	Store                    resultblob.Snapshot            `json:"store"`
}

const producerSource = `import base64
import hashlib
import numpy as np
array = np.arange(6, dtype=np.int64).reshape((2, 3)).copy(order='C')
body = array.tobytes(order='C')
result = {
    "schema_version": "pysolate.numpy-ndarray-producer-value.v1",
    "dtype": array.dtype.str,
    "shape": list(array.shape),
    "order": "C",
    "c_contiguous": bool(array.flags.c_contiguous),
    "nbytes": len(body),
    "body_sha256": "sha256:" + hashlib.sha256(body).hexdigest(),
    "body_base64": base64.b64encode(body).decode("ascii"),
}`

const (
	consumerASource = "array[0, 0] = 999\nresult = {'first': int(array[0, 0]), 'sum': int(array.sum()), 'c_contiguous': bool(array.flags.c_contiguous)}"
	consumerBSource = "result = {'first': int(array[0, 0]), 'sum': int(array.sum()), 'c_contiguous': bool(array.flags.c_contiguous)}"
	originalPrefix  = "import numpy as np\narray = np.arange(6, dtype=np.int64).reshape((2, 3)).copy(order='C')\n"
	errorSource     = "print('parity-log')\nraise ValueError('probe-error')"
)

func main() {
	artifactRoot := flag.String("artifact-root", "", "verified numpy-core bundle root")
	privateCOW := flag.Bool("private-cow", false, "require Linux private-COW for every Guest")
	sourceCommit := flag.String("source-commit", "", "exact signed source commit")
	sourceTree := flag.String("source-tree", "", "exact source tree")
	flag.Parse()
	if *artifactRoot == "" || *sourceCommit == "" || *sourceTree == "" {
		fail(errors.New("artifact-root and source identities are required"))
	}
	probeSHA, err := verifyBuildIdentity(*sourceCommit)
	if err != nil {
		fail(err)
	}
	if *privateCOW && goruntime.GOOS != "linux" {
		fail(errors.New("private-cow requires linux"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	b, err := loadBundle(*artifactRoot)
	if err != nil {
		fail(err)
	}
	bindings, err := probeBindings(b.profile)
	if err != nil {
		fail(err)
	}
	producerRequest, _ := json.Marshal(struct {
		RunID         string         `json:"run_id"`
		Code          string         `json:"code"`
		Inputs        map[string]any `json:"inputs"`
		Compatibility struct {
			Profile string   `json:"profile"`
			Imports []string `json:"imports"`
		} `json:"compatibility"`
	}{RunID: "numpy-producer", Code: producerSource, Inputs: map[string]any{}})
	var producerRequestValue map[string]any
	_ = json.Unmarshal(producerRequest, &producerRequestValue)
	producerRequestValue["compatibility"] = map[string]any{"profile": "numpy-core", "imports": []string{"base64", "hashlib", "numpy"}}
	producerRequest, _ = json.Marshal(producerRequestValue)

	producerRaw, producerEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-producer", producerRequest)
	if err != nil {
		fail(err)
	}
	producerResponse, err := decodeGuestResponse(producerRaw)
	if err != nil {
		fail(err)
	}
	store, err := resultblob.NewStore("numpy-reuse-probe", resultblob.Limits{
		MaxEntries: 1, MaxBodyBytes: numpycodec.MaxBodyBytes, MaxRetainedBytes: numpycodec.MaxBodyBytes + 64*1024,
		MaxMetadataBytes: 64 * 1024, MaxLeases: 4,
	})
	if err != nil {
		fail(err)
	}
	descriptor, blobDescriptor, publicationEvidence, err := numpycodec.Publish(ctx, store, "numpy-reuse-probe", producerResponse.Result, bindings,
		resultblob.NewPublicationGuard(publicationauth.Mint(publicationauth.ResultPublicationBinding)), numpycodec.MaxBodyBytes)
	if err != nil {
		fail(err)
	}

	originalARequest := buildOriginalRequest("numpy-original-a", originalPrefix+consumerASource)
	originalARaw, originalAEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-original-a", originalARequest)
	if err != nil {
		fail(err)
	}
	originalAResponse, err := decodeGuestResponse(originalARaw)
	if err != nil {
		fail(err)
	}
	var originalAResult consumerResult
	if json.Unmarshal(originalAResponse.Result, &originalAResult) != nil {
		fail(errors.New("original A result invalid"))
	}

	originalBRequest := buildOriginalRequest("numpy-original-b", originalPrefix+consumerBSource)
	originalBRaw, originalBEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-original-b", originalBRequest)
	if err != nil {
		fail(err)
	}
	originalBResponse, err := decodeGuestResponse(originalBRaw)
	if err != nil {
		fail(err)
	}
	var originalBResult consumerResult
	if json.Unmarshal(originalBResponse.Result, &originalBResult) != nil {
		fail(errors.New("original B result invalid"))
	}

	consumerASHA, err := numpycodec.ConsumerIdentity(descriptor, "array", consumerASource)
	if err != nil {
		fail(err)
	}
	leaseA, _ := store.NewLease(blobDescriptor.IdentitySHA256, consumerASHA)
	claimA, _ := store.Claim(leaseA)
	planA, err := numpycodec.BuildMaterializationRequest("numpy-consumer-a", "array", claimA, consumerASource)
	if err != nil {
		fail(err)
	}
	consumerARaw, consumerAEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-consumer-a", planA.Request)
	if err != nil {
		fail(err)
	}
	consumerAResponse, err := decodeGuestResponse(consumerARaw)
	if err != nil {
		fail(err)
	}
	var materializedA materializedResult
	if json.Unmarshal(consumerAResponse.Result, &materializedA) != nil || materializedA.SchemaVersion != "pysolate.numpy-materialization-result.v1" ||
		materializedA.MaterializationDurationNS < 0 || store.Consume(leaseA) != nil {
		fail(errors.New("consumer A result or lifecycle invalid"))
	}
	resultA := materializedA.Value

	verifyLease, _ := store.NewLease(blobDescriptor.IdentitySHA256, digest("host-verifier"))
	verifyClaim, _ := store.Claim(verifyLease)
	hostHashAfterA := resultblob.BytesDigest(verifyClaim.Body)
	if store.Reject(verifyLease) != nil {
		fail(errors.New("host verification lease rejection failed"))
	}

	consumerBSHA, err := numpycodec.ConsumerIdentity(descriptor, "array", consumerBSource)
	if err != nil {
		fail(err)
	}
	leaseB, _ := store.NewLease(blobDescriptor.IdentitySHA256, consumerBSHA)
	claimB, _ := store.Claim(leaseB)
	planB, err := numpycodec.BuildMaterializationRequest("numpy-consumer-b", "array", claimB, consumerBSource)
	if err != nil {
		fail(err)
	}
	consumerBRaw, consumerBEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-consumer-b", planB.Request)
	if err != nil {
		fail(err)
	}
	consumerBResponse, err := decodeGuestResponse(consumerBRaw)
	if err != nil {
		fail(err)
	}
	var materializedB materializedResult
	if json.Unmarshal(consumerBResponse.Result, &materializedB) != nil || materializedB.SchemaVersion != "pysolate.numpy-materialization-result.v1" ||
		materializedB.MaterializationDurationNS < 0 || store.Consume(leaseB) != nil {
		fail(errors.New("consumer B result or lifecycle invalid"))
	}
	resultB := materializedB.Value

	originalErrorRequest := buildOriginalRequest("numpy-original-error", originalPrefix+errorSource)
	originalErrorRaw, originalErrorEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-original-error", originalErrorRequest)
	if err != nil {
		fail(err)
	}
	originalErrorResponse, err := decodeGuestEnvelope(originalErrorRaw)
	if err != nil || originalErrorResponse.Status != "error" {
		fail(errors.New("original error lane did not fail exactly"))
	}
	errorConsumerSHA, err := numpycodec.ConsumerIdentity(descriptor, "array", errorSource)
	if err != nil {
		fail(err)
	}
	errorLease, _ := store.NewLease(blobDescriptor.IdentitySHA256, errorConsumerSHA)
	errorClaim, _ := store.Claim(errorLease)
	errorPlan, err := numpycodec.BuildMaterializationRequest("numpy-consumer-error", "array", errorClaim, errorSource)
	if err != nil {
		fail(err)
	}
	consumerErrorRaw, consumerErrorEvidence, err := runFresh(ctx, b, *privateCOW, "numpy-consumer-error", errorPlan.Request)
	if err != nil {
		fail(err)
	}
	consumerErrorResponse, err := decodeGuestEnvelope(consumerErrorRaw)
	if err != nil || consumerErrorResponse.Status != "error" || store.Reject(errorLease) != nil {
		fail(errors.New("derived error lane or lifecycle invalid"))
	}
	errorParity := string(originalErrorResponse.Error) == string(consumerErrorResponse.Error) &&
		string(originalErrorResponse.Logs) == string(consumerErrorResponse.Logs)
	if !errorParity {
		fail(fmt.Errorf("error/log parity failed: original=%s derived=%s original_logs=%s derived_logs=%s",
			originalErrorResponse.Error, consumerErrorResponse.Error, originalErrorResponse.Logs, consumerErrorResponse.Logs))
	}

	parity := resultA == originalAResult && resultB == originalBResult
	isolated := parity && resultA.First == 999 && resultA.Sum == 1014 && resultA.CContiguous &&
		resultB.First == 0 && resultB.Sum == 15 && resultB.CContiguous && hostHashAfterA == descriptor.BodySHA256
	if !isolated {
		fail(fmt.Errorf("mutation isolation failed: A=%+v B=%+v host=%s", resultA, resultB, hostHashAfterA))
	}
	if err := store.Close(); err != nil {
		fail(err)
	}
	encoded, _ := json.Marshal(output{
		SchemaVersion: "pysolate.numpy-reuse-probe.v1", Platform: goruntime.GOOS + "_" + goruntime.GOARCH,
		PrivateCOWRequired: *privateCOW, SourceCommit: *sourceCommit, SourceTree: *sourceTree, ProbeBinarySHA256: probeSHA,
		ArtifactSHA256: b.profile.ArtifactSHA256(), DescriptorSHA256: descriptor.IdentitySHA256,
		BodySHA256: descriptor.BodySHA256, BodyBytes: descriptor.NBytes, Producer: producerEvidence, Publication: publicationEvidence,
		OriginalA: originalAEvidence, OriginalB: originalBEvidence, ConsumerA: consumerAEvidence, ConsumerB: consumerBEvidence,
		PlanA: planA, PlanB: planB, OriginalAResult: originalAResult, OriginalBResult: originalBResult,
		ConsumerAResult: resultA, ConsumerBResult: resultB, MaterializationDurationA: materializedA.MaterializationDurationNS,
		MaterializationDurationB: materializedB.MaterializationDurationNS, ResultParity: parity,
		OriginalError: originalErrorEvidence, ConsumerError: consumerErrorEvidence, PlanError: errorPlan,
		OriginalErrorObject: originalErrorResponse.Error, ConsumerErrorObject: consumerErrorResponse.Error,
		OriginalErrorLogs: originalErrorResponse.Logs, ConsumerErrorLogs: consumerErrorResponse.Logs, ErrorTracebackLogParity: errorParity,
		HostHashAfterA: hostHashAfterA, ConsumerMutationIsolated: isolated, Store: store.Snapshot(),
	})
	fmt.Println(string(encoded))
}

func buildOriginalRequest(runID, source string) []byte {
	request, err := json.Marshal(struct {
		RunID         string         `json:"run_id"`
		Code          string         `json:"code"`
		Inputs        map[string]any `json:"inputs"`
		Compatibility struct {
			Profile string   `json:"profile"`
			Imports []string `json:"imports"`
		} `json:"compatibility"`
	}{RunID: runID, Code: source, Inputs: map[string]any{}, Compatibility: struct {
		Profile string   `json:"profile"`
		Imports []string `json:"imports"`
	}{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		panic(err)
	}
	return request
}

func loadBundle(root string) (bundle, error) {
	dist := filepath.Join(root, "dist")
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dist, name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return bundle{}, err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return bundle{}, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return bundle{}, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return bundle{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return bundle{}, err
	}
	allowed := []string{"base64", "datetime", "hashlib", "numpy"}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", allowed)
	if err != nil {
		return bundle{}, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return bundle{wasm: wasm, profile: profile, root: root}, err
}

func probeBindings(profile runtimeconfig.ExecutionProfile) (numpycodec.Bindings, error) {
	configDigest := digest("numpy_ndarray_c_v1.materialization")
	registration, err := passregistration.New(passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion,
		passregistration.SemanticAnalyzerSHA256, configDigest, passregistration.ExecutionPatch, passregistration.PatchBindings())
	if err != nil {
		return numpycodec.Bindings{}, err
	}
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	importsRaw, _ := json.Marshal(imports)
	profileRaw, _ := json.Marshal(struct {
		ID       string   `json:"id"`
		Artifact string   `json:"artifact_sha256"`
		Manifest string   `json:"manifest_sha256"`
		Imports  []string `json:"imports"`
	}{profile.ID(), profile.ArtifactSHA256(), profile.ManifestSHA256(), imports})
	return numpycodec.Bindings{
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileID: profile.ID(), ExecutionProfileSHA256: digestBytes(profileRaw),
		ImportClosureSHA256: digestBytes(importsRaw), SourceSHA256: digest(producerSource), InputsSHA256: digest("{}"),
		PassRegistrationSHA256: registration.IdentitySHA256(),
	}, nil
}

func runFresh(ctx context.Context, b bundle, privateCOW bool, runID string, request []byte) ([]byte, guestEvidence, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 5 * time.Minute
	config.MaxRequestBytes = 16 * 1024 * 1024
	config.MaxResponseBytes = 16 * 1024 * 1024
	config.MemoryLimitPages = 16384
	profile := b.profile
	config.ExecutionProfile = &profile
	if privateCOW {
		config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, PreparedRuntime: true, MemoryCOW: true}
	}
	engine, err := wazeroengine.New(ctx, b.wasm, config)
	if err != nil {
		return nil, guestEvidence{}, err
	}
	defer engine.Close(context.Background())
	if privateCOW {
		if err := engine.PrepareSemanticRuntime(ctx); err != nil {
			return nil, guestEvidence{}, err
		}
		probe := engine.COWProbe()
		if !probe.COWSelected || probe.Fallback || !probe.MemoryCOWCandidate {
			return nil, guestEvidence{}, fmt.Errorf("private COW unavailable: %+v", probe)
		}
	}
	response, err := engine.Run(ctx, request, "")
	evidence := guestEvidence{RunID: runID, COWProbe: engine.COWProbe(), Prepared: engine.PreparedState(), Image: engine.PreparedImageState()}
	if err != nil {
		return nil, evidence, err
	}
	decoded, err := decodeGuestEnvelope(response)
	if err != nil {
		return nil, evidence, err
	}
	evidence.CapabilityCalls = decoded.Metrics.CapabilityCalls
	if decoded.Metrics.CapabilityCalls != 0 {
		return nil, evidence, errors.New("unexpected capability call")
	}
	return response, evidence, nil
}

func decodeGuestEnvelope(raw []byte) (guestResponse, error) {
	var response guestResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return guestResponse{}, fmt.Errorf("decode Guest response: %w", err)
	}
	if response.Status != "ok" && response.Status != "error" {
		return guestResponse{}, fmt.Errorf("invalid Guest status: %s", response.Status)
	}
	return response, nil
}

func decodeGuestResponse(raw []byte) (guestResponse, error) {
	response, err := decodeGuestEnvelope(raw)
	if err != nil {
		return guestResponse{}, err
	}
	if response.Status != "ok" || len(response.Result) == 0 {
		diagnostic := raw
		if len(diagnostic) > 4096 {
			diagnostic = diagnostic[:4096]
		}
		return guestResponse{}, fmt.Errorf("Guest response failed: %s", diagnostic)
	}
	return response, nil
}

func digest(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func verifyBuildIdentity(expectedCommit string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("missing Go build identity")
	}
	revision := ""
	modified := ""
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision != expectedCommit || modified != "false" {
		return "", fmt.Errorf("probe source identity mismatch: revision=%s modified=%s", revision, modified)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
