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

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpyproducer"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

type bundle struct {
	wasm    []byte
	profile runtimeconfig.ExecutionProfile
}

type engineEvidence struct {
	RunID    string                          `json:"run_id"`
	COWProbe wazeroengine.COWProbe           `json:"cow_probe"`
	Prepared wazeroengine.PreparedState      `json:"prepared_state"`
	Image    wazeroengine.PreparedImageState `json:"prepared_image"`
}

type runEvidence struct {
	engineEvidence
	CapabilityCalls uint64 `json:"capability_calls"`
}

type adversarialEvidence struct {
	Name               string         `json:"name"`
	SourceSHA256       string         `json:"source_sha256"`
	AnalysisSHA256     string         `json:"analysis_sha256"`
	ExactGuestAnalyzed bool           `json:"exact_guest_analyzed"`
	Engine             engineEvidence `json:"engine"`
	Rejected           bool           `json:"rejected"`
}

type supportedProducerEvidence struct {
	Operation      string         `json:"operation"`
	DeclarationSHA string         `json:"declaration_sha256"`
	AdmissionSHA   string         `json:"admission_sha256"`
	AnalysisSHA    string         `json:"analysis_sha256"`
	BodyBytes      uint64         `json:"body_bytes"`
	Analysis       engineEvidence `json:"analysis"`
	Execution      runEvidence    `json:"execution"`
}

type consumerValue struct {
	First       int64 `json:"first"`
	Sum         int64 `json:"sum"`
	CContiguous bool  `json:"c_contiguous"`
}

type materializedValue struct {
	SchemaVersion             string        `json:"schema_version"`
	Value                     consumerValue `json:"value"`
	MaterializationDurationNS int64         `json:"materialization_duration_ns"`
}

type guestResponse struct {
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
	Result        json.RawMessage `json:"result"`
	ResultPresent bool            `json:"result_present"`
	Metrics       struct {
		CapabilityCalls uint64 `json:"capability_calls"`
	} `json:"metrics"`
	SourceContract struct {
		ModelSourceSHA256 string `json:"model_source_sha256"`
	} `json:"source_contract"`
}

type output struct {
	SchemaVersion       string                         `json:"schema_version"`
	Platform            string                         `json:"platform"`
	PrivateCOWRequired  bool                           `json:"private_cow_required"`
	SourceCommit        string                         `json:"source_commit"`
	SourceTree          string                         `json:"source_tree"`
	ProbeBinarySHA256   string                         `json:"probe_binary_sha256"`
	ArtifactSHA256      string                         `json:"artifact_sha256"`
	Declaration         numpyproducer.Declaration      `json:"declaration"`
	Admission           numpyproducer.Admission        `json:"admission"`
	ProducerAnalysisSHA string                         `json:"producer_analysis_sha256"`
	FinalAnalysisSHA    string                         `json:"final_analysis_sha256"`
	Lineage             numpyproducer.PreparedLineage  `json:"lineage"`
	NDArrayDescriptor   numpycodec.Descriptor          `json:"ndarray_descriptor"`
	BlobDescriptor      resultblob.Descriptor          `json:"blob_descriptor"`
	Publication         numpycodec.PublicationEvidence `json:"publication"`
	MaterializationPlan numpycodec.MaterializationPlan `json:"materialization_plan"`
	ProducerAnalysis    engineEvidence                 `json:"producer_analysis"`
	ProducerRun         runEvidence                    `json:"producer_run"`
	FinalAnalysis       engineEvidence                 `json:"final_analysis"`
	ConsumerRun         runEvidence                    `json:"consumer_run"`
	OriginalRun         runEvidence                    `json:"original_run"`
	ConsumerResult      consumerValue                  `json:"consumer_result"`
	OriginalResult      consumerValue                  `json:"original_result"`
	ResultParity        bool                           `json:"result_parity"`
	SupportedProducers  []supportedProducerEvidence    `json:"supported_producers"`
	Adversarial         []adversarialEvidence          `json:"adversarial"`
	Store               resultblob.Snapshot            `json:"store"`
}

const consumerSource = "result = {'first': int(array[0, 0]), 'sum': int(array.sum()), 'c_contiguous': bool(array.flags.c_contiguous)}"

func main() {
	artifactRoot := flag.String("artifact-root", "", "verified numpy-core bundle root")
	privateCOW := flag.Bool("private-cow", false, "require Linux private-COW for every Guest")
	sourceCommit := flag.String("source-commit", "", "exact signed source commit")
	sourceTree := flag.String("source-tree", "", "exact source tree")
	flag.Parse()
	if *artifactRoot == "" || *sourceCommit == "" || *sourceTree == "" {
		fail(errors.New("artifact-root and source identities are required"))
	}
	binarySHA, err := verifyBuildIdentity(*sourceCommit)
	if err != nil {
		fail(err)
	}
	if *privateCOW && goruntime.GOOS != "linux" {
		fail(errors.New("private-cow requires linux"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	b, err := loadBundle(*artifactRoot)
	if err != nil {
		fail(err)
	}
	bindings, err := analysisBindings(b.profile, *privateCOW)
	if err != nil {
		fail(err)
	}
	declarationRaw, declaration, err := numpyproducer.SealDeclaration(numpyproducer.DeclarationInput{Rows: 2, Cols: 3, Start: 1, Step: 2, Add: 5})
	if err != nil {
		fail(err)
	}
	producerSource, err := numpyproducer.RenderSource(declaration)
	if err != nil {
		fail(err)
	}
	producerAnalysis, producerAnalysisEvidence, err := analyzeFresh(ctx, b, *privateCOW, "producer-analysis", producerSource, bindings)
	if err != nil {
		fail(err)
	}
	admission, err := numpyproducer.Admit(declarationRaw, producerSource, producerAnalysis, bindings)
	if err != nil {
		fail(err)
	}
	producerRequest := buildRequest("producer-run", producerSource, []string{"base64", "hashlib", "numpy"})
	producerRaw, producerRunEvidence, err := runFresh(ctx, b, *privateCOW, "producer-run", producerRequest)
	if err != nil {
		fail(err)
	}
	producerResult, guard, err := numpyproducer.ValidateExecutionResponse(producerRaw, admission)
	if err != nil {
		fail(err)
	}
	store, err := resultblob.NewStore("numpy-admission-probe", resultblob.Limits{
		MaxEntries: 1, MaxBodyBytes: numpycodec.MaxBodyBytes, MaxRetainedBytes: numpycodec.MaxBodyBytes + 64*1024,
		MaxMetadataBytes: 64 * 1024, MaxLeases: 1,
	})
	if err != nil {
		fail(err)
	}
	codecBindings := numpycodec.Bindings{
		ArtifactSHA256: admission.ArtifactSHA256, ExecutionProfileID: admission.ExecutionProfileID,
		ExecutionProfileSHA256: admission.ExecutionProfileSHA256, ImportClosureSHA256: admission.ImportClosureSHA256,
		SourceSHA256: admission.SourceSHA256, InputsSHA256: admission.InputsSHA256,
		PassRegistrationSHA256: admission.PassRegistrationSHA256,
	}
	descriptor, blobDescriptor, publication, err := numpycodec.Publish(ctx, store, "numpy-admission-probe", producerResult, codecBindings, guard, numpycodec.MaxBodyBytes)
	if err != nil {
		fail(err)
	}
	consumerSHA, err := numpycodec.ConsumerIdentity(descriptor, "array", consumerSource)
	if err != nil {
		fail(err)
	}
	lease, err := store.NewLease(blobDescriptor.IdentitySHA256, consumerSHA)
	if err != nil {
		fail(err)
	}
	claim, err := store.Claim(lease)
	if err != nil {
		fail(err)
	}
	plan, err := numpycodec.BuildMaterializationRequest("consumer-run", "array", claim, consumerSource)
	if err != nil {
		fail(err)
	}
	finalSource := requestSource(plan.Request)
	finalAnalysis, finalAnalysisEvidence, err := analyzeFresh(ctx, b, *privateCOW, "final-analysis", finalSource, bindings)
	if err != nil {
		fail(err)
	}
	_, lineage, err := numpyproducer.SealPreparedLineage(admission, declaration, plan, finalAnalysis)
	if err != nil || lineage.Validate(admission) != nil {
		fail(errors.Join(err, numpyproducer.ErrLineage))
	}
	consumerRaw, consumerRunEvidence, err := runFresh(ctx, b, *privateCOW, "consumer-run", plan.Request)
	if err != nil {
		fail(err)
	}
	consumerResponse := decodeSuccess(consumerRaw)
	var materialized materializedValue
	if json.Unmarshal(consumerResponse.Result, &materialized) != nil || materialized.SchemaVersion != "pysolate.numpy-materialization-result.v1" || store.Consume(lease) != nil {
		fail(errors.New("derived consumer result invalid"))
	}
	originalSource := renderOriginal(declaration, consumerSource)
	originalRaw, originalRunEvidence, err := runFresh(ctx, b, *privateCOW, "original-run", buildRequest("original-run", originalSource, []string{"numpy"}))
	if err != nil {
		fail(err)
	}
	originalResponse := decodeSuccess(originalRaw)
	var originalResult consumerValue
	if json.Unmarshal(originalResponse.Result, &originalResult) != nil {
		fail(errors.New("original result invalid"))
	}
	parity := originalResult == materialized.Value && originalResult == (consumerValue{First: 6, Sum: 66, CContiguous: true})
	if !parity {
		fail(fmt.Errorf("result parity failed original=%+v consumer=%+v", originalResult, materialized.Value))
	}
	supported := runSupportedProducers(ctx, b, *privateCOW, bindings)
	adversarial := runAdversarial(ctx, b, *privateCOW, declarationRaw, producerSource, producerAnalysis, bindings)
	if err := store.Close(); err != nil {
		fail(err)
	}
	producerAnalysisSHA, _, _ := producerAnalysis.Identity()
	finalAnalysisSHA, _, _ := finalAnalysis.Identity()
	encoded, _ := json.Marshal(output{
		SchemaVersion: "pysolate.numpy-producer-admission-probe.v1", Platform: goruntime.GOOS + "_" + goruntime.GOARCH,
		PrivateCOWRequired: *privateCOW, SourceCommit: *sourceCommit, SourceTree: *sourceTree, ProbeBinarySHA256: binarySHA,
		ArtifactSHA256: b.profile.ArtifactSHA256(), Declaration: declaration, Admission: admission,
		ProducerAnalysisSHA: producerAnalysisSHA, FinalAnalysisSHA: finalAnalysisSHA, Lineage: lineage,
		NDArrayDescriptor: descriptor, BlobDescriptor: blobDescriptor, Publication: publication, MaterializationPlan: plan,
		ProducerAnalysis: producerAnalysisEvidence, ProducerRun: producerRunEvidence, FinalAnalysis: finalAnalysisEvidence,
		ConsumerRun: consumerRunEvidence, OriginalRun: originalRunEvidence, ConsumerResult: materialized.Value,
		OriginalResult: originalResult, ResultParity: parity, SupportedProducers: supported, Adversarial: adversarial, Store: store.Snapshot(),
	})
	fmt.Println(string(encoded))
}

func runSupportedProducers(ctx context.Context, b bundle, privateCOW bool, bindings numpyproducer.Bindings) []supportedProducerEvidence {
	cases := []numpyproducer.DeclarationInput{
		{Operation: numpyproducer.OperationZerosF64, InputElements: 8192},
		{Operation: numpyproducer.OperationAffineF64, InputElements: 131072},
		{Operation: numpyproducer.OperationSumI64, InputElements: 1048576},
		{Operation: numpyproducer.OperationMatmulF64, Rows: 256, Cols: 256},
	}
	rows := make([]supportedProducerEvidence, 0, len(cases))
	for _, input := range cases {
		raw, declaration, err := numpyproducer.SealDeclaration(input)
		if err != nil {
			fail(err)
		}
		source, _ := numpyproducer.RenderSource(declaration)
		analysis, analysisEvidence, err := analyzeFresh(ctx, b, privateCOW, "supported-analysis-"+declaration.Operation, source, bindings)
		if err != nil {
			fail(err)
		}
		admission, err := numpyproducer.Admit(raw, source, analysis, bindings)
		if err != nil {
			fail(err)
		}
		responseRaw, executionEvidence, err := runFresh(ctx, b, privateCOW, "supported-run-"+declaration.Operation, buildRequest("supported-run-"+declaration.Operation, source, []string{"base64", "hashlib", "numpy"}))
		if err != nil {
			fail(err)
		}
		producerValue, _, err := numpyproducer.ValidateExecutionResponse(responseRaw, admission)
		if err != nil {
			fail(fmt.Errorf("supported producer %s execution: %w response=%s", declaration.Operation, err, responseRaw))
		}
		codecBindings := numpycodec.Bindings{
			ArtifactSHA256: admission.ArtifactSHA256, ExecutionProfileID: admission.ExecutionProfileID,
			ExecutionProfileSHA256: admission.ExecutionProfileSHA256, ImportClosureSHA256: admission.ImportClosureSHA256,
			SourceSHA256: admission.SourceSHA256, InputsSHA256: admission.InputsSHA256,
			PassRegistrationSHA256: admission.PassRegistrationSHA256,
		}
		descriptor, body, err := numpycodec.DecodeProducerValue(producerValue, codecBindings, numpycodec.MaxBodyBytes)
		if err != nil || descriptor.NBytes != declaration.BodyBytes || uint64(len(body)) != declaration.BodyBytes {
			fail(fmt.Errorf("supported producer %s mismatch descriptor=%+v body=%d declaration=%+v err=%v", declaration.Operation, descriptor, len(body), declaration, err))
		}
		analysisSHA, _, _ := analysis.Identity()
		rows = append(rows, supportedProducerEvidence{
			Operation: declaration.Operation, DeclarationSHA: declaration.IdentitySHA256, AdmissionSHA: admission.IdentitySHA256,
			AnalysisSHA: analysisSHA, BodyBytes: descriptor.NBytes, Analysis: analysisEvidence, Execution: executionEvidence,
		})
	}
	return rows
}

func runAdversarial(ctx context.Context, b bundle, privateCOW bool, declarationRaw []byte, source string, base semantic.Analysis, bindings numpyproducer.Bindings) []adversarialEvidence {
	variants := []struct{ name, source string }{
		{"random", source + "\nnp.random.rand()"},
		{"time", source + "\nimport datetime\ndatetime.datetime.now()"},
		{"file", source + "\nopen('/tmp/forbidden','wb')"},
		{"dynamic_import", source + "\n__import__('os')"},
		{"object_dtype", stringReplaceOnce(source, "dtype=np.int64", "dtype=object")},
		{"unknown_call", source + "\ncallback(array)"},
	}
	rows := make([]adversarialEvidence, 0, len(variants)+3)
	for _, variant := range variants {
		analysis, engine, err := analyzeFresh(ctx, b, privateCOW, "adversarial-"+variant.name, variant.source, bindings)
		analysisSHA := ""
		if err == nil {
			analysisSHA, _, _ = analysis.Identity()
		}
		_, admitErr := numpyproducer.Admit(declarationRaw, variant.source, analysis, bindings)
		if admitErr == nil {
			fail(fmt.Errorf("adversarial source admitted: %s", variant.name))
		}
		rows = append(rows, adversarialEvidence{Name: variant.name, SourceSHA256: numpyproducer.Digest([]byte(variant.source)), AnalysisSHA256: analysisSHA, ExactGuestAnalyzed: err == nil, Engine: engine, Rejected: true})
	}
	profileDrift := bindings
	profileDrift.ExecutionProfileID = "base"
	_, profileErr := numpyproducer.Admit(declarationRaw, source, base, profileDrift)
	rows = append(rows, adversarialEvidence{Name: "profile", SourceSHA256: base.SourceSHA256, Rejected: profileErr != nil})
	analysisDrift := base
	analysisDrift.SourceSHA256 = digest("stale-source")
	_, staleErr := numpyproducer.Admit(declarationRaw, source, analysisDrift, bindings)
	rows = append(rows, adversarialEvidence{Name: "stale_source", SourceSHA256: analysisDrift.SourceSHA256, Rejected: staleErr != nil})
	inputDrift := append([]byte(nil), declarationRaw...)
	inputDrift[0] = ' '
	_, inputErr := numpyproducer.Admit(inputDrift, source, base, bindings)
	rows = append(rows, adversarialEvidence{Name: "inputs", SourceSHA256: base.SourceSHA256, Rejected: inputErr != nil})
	for _, row := range rows {
		if !row.Rejected {
			fail(fmt.Errorf("adversarial case not rejected: %s", row.Name))
		}
	}
	return rows
}

func analyzeFresh(ctx context.Context, b bundle, privateCOW bool, runID, source string, bindings numpyproducer.Bindings) (semantic.Analysis, engineEvidence, error) {
	engine, err := newEngine(ctx, b, privateCOW)
	if err != nil {
		return semantic.Analysis{}, engineEvidence{}, err
	}
	defer engine.Close(context.Background())
	if privateCOW {
		if err := requirePrepared(ctx, engine); err != nil {
			return semantic.Analysis{}, engineEvidence{}, err
		}
	}
	request, err := semantic.NewRequest(source, semantic.Bindings{
		ArtifactSHA256: bindings.ArtifactSHA256, ExecutionProfileSHA256: bindings.ExecutionProfileSHA256,
		ImportClosureSHA256: bindings.ImportClosureSHA256, CapabilityPlanSHA256: bindings.CapabilityPlanSHA256,
	}, nil)
	if err != nil {
		return semantic.Analysis{}, engineEvidence{}, err
	}
	analysis, err := semantic.Analyze(ctx, engine, request)
	evidence := engineEvidence{RunID: runID, COWProbe: engine.COWProbe(), Prepared: engine.PreparedState(), Image: engine.PreparedImageState()}
	return analysis, evidence, err
}

func runFresh(ctx context.Context, b bundle, privateCOW bool, runID string, request []byte) ([]byte, runEvidence, error) {
	engine, err := newEngine(ctx, b, privateCOW)
	if err != nil {
		return nil, runEvidence{}, err
	}
	defer engine.Close(context.Background())
	if privateCOW {
		if err := requirePrepared(ctx, engine); err != nil {
			return nil, runEvidence{}, err
		}
	}
	response, err := engine.Run(ctx, request, "")
	evidence := runEvidence{engineEvidence: engineEvidence{RunID: runID, COWProbe: engine.COWProbe(), Prepared: engine.PreparedState(), Image: engine.PreparedImageState()}}
	if err != nil {
		return nil, evidence, err
	}
	decoded := decodeEnvelope(response)
	evidence.CapabilityCalls = decoded.Metrics.CapabilityCalls
	if decoded.Metrics.CapabilityCalls != 0 {
		return nil, evidence, errors.New("unexpected capability call")
	}
	return response, evidence, nil
}

func newEngine(ctx context.Context, b bundle, privateCOW bool) (*wazeroengine.Engine, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 5 * time.Minute
	config.MaxRequestBytes = 16 * 1024 * 1024
	config.MaxResponseBytes = 16 * 1024 * 1024
	config.MemoryLimitPages = 16384
	profile := b.profile
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	if privateCOW {
		config.Mechanisms.PreparedRuntime = true
		config.Mechanisms.MemoryCOW = true
	}
	return wazeroengine.New(ctx, b.wasm, config)
}

func requirePrepared(ctx context.Context, engine *wazeroengine.Engine) error {
	if err := engine.PrepareSemanticRuntime(ctx); err != nil {
		return err
	}
	probe := engine.COWProbe()
	if !probe.COWSelected || probe.Fallback || !probe.MemoryCOWCandidate {
		return fmt.Errorf("private COW unavailable: %+v", probe)
	}
	return nil
}

func analysisBindings(profile runtimeconfig.ExecutionProfile, privateCOW bool) (numpyproducer.Bindings, error) {
	config := runtimeconfig.DefaultRunConfig()
	bound := profile
	config.ExecutionProfile = &bound
	config.Mechanisms.SemanticAnalysis = true
	if privateCOW {
		config.Mechanisms.PreparedRuntime = true
		config.Mechanisms.MemoryCOW = true
	}
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		return numpyproducer.Bindings{}, err
	}
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	importsRaw, _ := json.Marshal(imports)
	return numpyproducer.Bindings{
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileID: profile.ID(), ExecutionProfileSHA256: profileSHA,
		ImportClosureSHA256: digestBytes(importsRaw), CapabilityPlanSHA256: digest("pysolate.empty-capability-plan.v1"),
	}, nil
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
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return bundle{}, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return bundle{wasm: wasm, profile: profile}, err
}

func buildRequest(runID, source string, imports []string) []byte {
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
	}{Profile: "numpy-core", Imports: imports}})
	if err != nil {
		panic(err)
	}
	return request
}

func requestSource(raw []byte) string {
	var request struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &request) != nil || request.Code == "" {
		fail(errors.New("materialization request source missing"))
	}
	return request.Code
}

func renderOriginal(declaration numpyproducer.Declaration, consumer string) string {
	return fmt.Sprintf("import numpy as np\nassert np.__version__ == '1.26.0b1'\narray = (np.arange(%d, %d, %d, dtype=np.int64) + np.int64(%d)).reshape((%d, %d)).copy(order='C')\n%s",
		declaration.Start, declaration.Stop, declaration.Step, declaration.Add, declaration.Rows, declaration.Cols, consumer)
}

func decodeEnvelope(raw []byte) guestResponse {
	var response guestResponse
	if json.Unmarshal(raw, &response) != nil || (response.Status != "ok" && response.Status != "error") {
		fail(errors.New("invalid Guest response"))
	}
	return response
}
func decodeSuccess(raw []byte) guestResponse {
	response := decodeEnvelope(raw)
	if response.Status != "ok" || !response.ResultPresent || len(response.Result) == 0 {
		fail(fmt.Errorf("Guest failed: %s", raw))
	}
	return response
}

func verifyBuildIdentity(expectedCommit string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("missing build identity")
	}
	revision, modified := "", ""
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value
		}
	}
	if revision != expectedCommit || modified != "false" {
		return "", fmt.Errorf("source identity mismatch revision=%s modified=%s", revision, modified)
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

func stringReplaceOnce(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
func digest(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
