package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	goruntime "runtime"
	"runtime/debug"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/numpyreuse"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpyproducer"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
)

type caseExecutionSpec struct {
	Declaration numpyproducer.DeclarationInput
	Consumer    string
	Expected    json.RawMessage
}

type materializedEnvelope struct {
	SchemaVersion                string          `json:"schema_version"`
	Value                        json.RawMessage `json:"value"`
	MaterializationDurationNanos uint64          `json:"materialization_duration_ns"`
}

type placementCounts struct{ prepared, ordinary, fallback uint32 }

func main() {
	artifactRoot := flag.String("artifact-root", "", "verified numpy-core bundle root")
	platform := flag.String("platform", "", "frozen platform coordinate")
	outputPath := flag.String("output", "", "append-only JSONL checkpoint")
	sourceCommit := flag.String("source-commit", "", "signed campaign binary source commit")
	sourceTree := flag.String("source-tree", "", "signed campaign binary source tree")
	maxRecords := flag.Int("max-records", 0, "bounded smoke prefix; zero runs all remaining frozen coordinates")
	caseID := flag.String("case-id", "", "optional exact case filter for bounded smoke runs")
	flag.Parse()
	if *artifactRoot == "" || *outputPath == "" || *sourceCommit == "" || *sourceTree == "" || (*platform != "darwin_arm64" && *platform != "linux_amd64") ||
		*platform != goruntime.GOOS+"_"+goruntime.GOARCH || *maxRecords < 0 {
		fail(errors.New("invalid campaign flags"))
	}
	binarySHA256, err := verifyBuildIdentity(*sourceCommit)
	if err != nil {
		fail(err)
	}
	bundle, err := loadArtifactBundle(*artifactRoot)
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	runners := map[string]*engineRunner{}
	bindings := map[string]numpyproducer.Bindings{}
	for _, profile := range []string{"cold_end_to_end", "preprovisioned_numpy_ready_equivalent_capacity"} {
		warm := profile == "preprovisioned_numpy_ready_equivalent_capacity"
		linuxCOW := warm && *platform == "linux_amd64"
		runner, err := newEngineRunner(ctx, bundle, warm, linuxCOW)
		if err != nil {
			fail(err)
		}
		runners[profile] = runner
		binding, err := analysisBindings(bundle.profile, linuxCOW)
		if err != nil {
			fail(err)
		}
		bindings[profile] = binding
	}
	defer func() {
		for _, runner := range runners {
			_ = runner.Close()
		}
	}()

	existing := map[numpyreuse.CampaignCoordinate]bool{}
	if raw, err := os.ReadFile(*outputPath); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		records, err := numpyreuse.DecodeTrialJSONL(raw)
		if err != nil {
			fail(err)
		}
		for _, record := range records {
			if record.Coordinate.Platform != *platform || existing[record.Coordinate] {
				fail(errors.New("resume file platform/duplicate mismatch"))
			}
			existing[record.Coordinate] = true
		}
	} else if err != nil && !os.IsNotExist(err) {
		fail(err)
	}
	file, err := os.OpenFile(*outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	completed := 0
	for _, coordinate := range numpyreuse.CampaignCoordinates() {
		if coordinate.Platform != *platform || (*caseID != "" && coordinate.CaseID != *caseID) || existing[coordinate] {
			continue
		}
		candidate, ok := numpyreuse.CaseByID(coordinate.CaseID)
		if !ok {
			fail(errors.New("frozen case missing"))
		}
		record, err := executeTrial(ctx, runners[coordinate.Profile], bindings[coordinate.Profile], bundle, coordinate, candidate, *platform, *sourceCommit, *sourceTree, binarySHA256)
		if err != nil {
			fail(fmt.Errorf("coordinate %+v: %w", coordinate, err))
		}
		raw, err := numpyreuse.EncodeTrialRecord(record)
		if err != nil {
			fail(err)
		}
		if _, err := file.Write(append(raw, '\n')); err != nil {
			fail(err)
		}
		if err := file.Sync(); err != nil {
			fail(err)
		}
		completed++
		fmt.Fprintf(os.Stderr, "completed=%d coordinate=%s/%s/%s/%d\n", completed, coordinate.Profile, coordinate.CaseID, coordinate.Treatment, coordinate.TrialIndex)
		if *maxRecords > 0 && completed >= *maxRecords {
			break
		}
	}
}

func executeTrial(ctx context.Context, runner *engineRunner, bindings numpyproducer.Bindings, bundle artifactBundle, coordinate numpyreuse.CampaignCoordinate, candidate numpyreuse.Case, platform, harnessCommit, harnessTree, binarySHA256 string) (numpyreuse.TrialRecord, error) {
	spec, err := executionSpec(candidate)
	if err != nil {
		return numpyreuse.TrialRecord{}, err
	}
	sampler := startRSSSampler()
	defer sampler.Stop()
	started := time.Now()
	stages := numpyreuse.StageMetrics{UnavailableStageReason: "NumPy import, compute and producer encode are one authority-preserving Guest execution interval; splitting them would instrument or observe the admitted source"}
	placements := placementCounts{}
	artifactSHA := bundle.profile.ArtifactSHA256()
	profileSHA := bindings.ExecutionProfileSHA256
	notApplicable := digestString("pysolate.numpy-reuse.not-applicable.v1")
	declarationSHA, sourceSHA, inputsSHA, passSHA := notApplicable, candidate.SourceSHA256, notApplicable, notApplicable
	resultSHA := ""
	physical := candidate.Consumers
	blobBytes := uint64(0)
	blobDisposition := "not_applicable"
	leaseDispositions := []string{}
	trialID := digestString(fmt.Sprintf("%s\x00%s\x00%s\x00%d", coordinate.Platform, coordinate.Profile, coordinate.CaseID+coordinate.Treatment, coordinate.TrialIndex))[7:23]

	if coordinate.Treatment == "original_recompute" {
		if candidate.LeadGapMillis > 0 {
			time.Sleep(time.Duration(candidate.LeadGapMillis) * time.Millisecond)
		}
		var canonical []byte
		for index := uint32(0); index < candidate.Consumers; index++ {
			request := buildRunRequest("original-"+trialID+fmt.Sprint(index), candidate.Source, []string{"numpy"})
			_, envelope, timing, err := runner.Run(request)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			stages.ConsumerGuestProvisionNanos += timing.ProvisionNanos
			stages.ConsumerExecutionNanos += timing.GuestNanos
			addPlacement(&placements, timing)
			current, err := canonicalResult(envelope.Result)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			if !bytes.Equal(current, spec.Expected) {
				return numpyreuse.TrialRecord{}, errors.New("original result mismatch")
			}
			if canonical == nil {
				canonical = current
			} else if !bytes.Equal(canonical, current) {
				return numpyreuse.TrialRecord{}, errors.New("original consumers diverged")
			}
		}
		resultSHA = digestBytes(canonical)
	} else {
		declarationRaw, declaration, err := numpyproducer.SealDeclaration(spec.Declaration)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		producerSource, err := numpyproducer.RenderSource(declaration)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		analysis, timing, err := runner.Analyze(producerSource, bindings)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		stages.AnalysisNanos += timing.ExecutionNanos
		stages.ProducerGuestProvisionNanos += timing.ProvisionNanos
		addPlacement(&placements, timing)
		admission, err := numpyproducer.Admit(declarationRaw, producerSource, analysis.Verified, bindings)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		declarationSHA, sourceSHA, inputsSHA, passSHA = declaration.IdentitySHA256, admission.SourceSHA256, admission.InputsSHA256, admission.PassRegistrationSHA256
		producerRequest := buildRunRequest("producer-"+trialID, producerSource, []string{"base64", "hashlib", "numpy"})
		producerExecution, _, timing, err := runner.RunProducer(producerRequest, admission)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		stages.ProducerGuestProvisionNanos += timing.ProvisionNanos
		stages.ProducerExecutionNanos += timing.ExecutionNanos
		addPlacement(&placements, timing)
		producerValue, guard, err := numpyproducer.ValidateExecutionResponse(producerExecution, admission)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		store, err := resultblob.NewStore("campaign-"+trialID, resultblob.Limits{MaxEntries: 1, MaxBodyBytes: numpycodec.MaxBodyBytes, MaxRetainedBytes: numpycodec.MaxBodyBytes + 65536, MaxMetadataBytes: 65536, MaxLeases: candidate.Consumers})
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		codecBindings := numpycodec.Bindings{ArtifactSHA256: admission.ArtifactSHA256, ExecutionProfileID: admission.ExecutionProfileID, ExecutionProfileSHA256: admission.ExecutionProfileSHA256, ImportClosureSHA256: admission.ImportClosureSHA256, SourceSHA256: admission.SourceSHA256, InputsSHA256: admission.InputsSHA256, PassRegistrationSHA256: admission.PassRegistrationSHA256}
		ndarrayDescriptor, blob, publication, err := numpycodec.Publish(ctx, store, "campaign-"+trialID, producerValue, codecBindings, guard, numpycodec.MaxBodyBytes)
		if err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		if publication.DecodeSealDurationNS > 0 {
			stages.HostBlobStoreNanos += uint64(publication.DecodeSealDurationNS)
		}
		blobBytes = blob.BodyBytes
		if blobBytes != candidate.ExpectedNBytes {
			return numpyreuse.TrialRecord{}, errors.New("published bytes mismatch")
		}
		if candidate.LeadGapMillis > 0 {
			time.Sleep(time.Duration(candidate.LeadGapMillis) * time.Millisecond)
		}
		leaseDispositions = make([]string, 0, candidate.Consumers)
		var canonical []byte
		for index := uint32(0); index < candidate.Consumers; index++ {
			consumerSHA, err := numpycodec.ConsumerIdentity(ndarrayDescriptor, "array", spec.Consumer)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			lease, err := store.NewLease(blob.IdentitySHA256, consumerSHA)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			claim, err := store.Claim(lease)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			plan, err := numpycodec.BuildMaterializationRequest("consumer-"+trialID+fmt.Sprint(index), "array", claim, spec.Consumer)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			finalSource := requestSource(plan.Request)
			finalAnalysis, timing, err := runner.Analyze(finalSource, bindings)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			stages.AnalysisNanos += timing.ExecutionNanos
			stages.ConsumerGuestProvisionNanos += timing.ProvisionNanos
			addPlacement(&placements, timing)
			_, lineage, err := numpyproducer.SealPreparedLineage(admission, declaration, plan, finalAnalysis.Verified)
			if err != nil || lineage.Validate(admission, plan) != nil {
				return numpyreuse.TrialRecord{}, errors.New("prepared lineage rejected")
			}
			_, envelope, timing, err := runner.Run(plan.Request)
			if err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			stages.ConsumerGuestProvisionNanos += timing.ProvisionNanos
			addPlacement(&placements, timing)
			var materialized materializedEnvelope
			if json.Unmarshal(envelope.Result, &materialized) != nil || materialized.SchemaVersion != "pysolate.numpy-materialization-result.v1" {
				return numpyreuse.TrialRecord{}, errors.New("materialized envelope rejected")
			}
			current, err := canonicalResult(materialized.Value)
			if err != nil || !bytes.Equal(current, spec.Expected) {
				return numpyreuse.TrialRecord{}, errors.New("derived result mismatch")
			}
			if canonical == nil {
				canonical = current
			} else if !bytes.Equal(canonical, current) {
				return numpyreuse.TrialRecord{}, errors.New("derived consumers diverged")
			}
			if plan.RequestBuildDurationNS > 0 {
				stages.ConsumerCopyMaterializeNanos += materialized.MaterializationDurationNanos + uint64(plan.RequestBuildDurationNS)
			} else {
				stages.ConsumerCopyMaterializeNanos += materialized.MaterializationDurationNanos
			}
			if timing.GuestNanos > materialized.MaterializationDurationNanos {
				stages.ConsumerExecutionNanos += timing.GuestNanos - materialized.MaterializationDurationNanos
			}
			if err := store.Consume(lease); err != nil {
				return numpyreuse.TrialRecord{}, err
			}
			leaseDispositions = append(leaseDispositions, "consumed")
		}
		teardown := time.Now()
		if err := store.Close(); err != nil {
			return numpyreuse.TrialRecord{}, err
		}
		stages.TeardownNanos = uint64(time.Since(teardown))
		if snapshot := store.Snapshot(); !snapshot.Closed || snapshot.RetainedBytes != 0 {
			return numpyreuse.TrialRecord{}, errors.New("store teardown incomplete")
		}
		blobDisposition = "consumed"
		resultSHA = digestBytes(canonical)
		physical = 2 + 2*candidate.Consumers
	}
	stages.CriticalWallNanos = uint64(time.Since(started))
	stages.PeakResidentMemoryBytes = sampler.Stop()
	if stages.PeakResidentMemoryBytes == 0 {
		stages.PeakResidentMemoryBytes = 1
	}
	record := numpyreuse.TrialRecord{
		SchemaVersion: numpyreuse.TrialRecordSchemaVersion, Coordinate: coordinate, CaseSourceSHA256: candidate.SourceSHA256,
		ArtifactSHA256: artifactSHA, HarnessSourceCommit: harnessCommit, HarnessSourceTree: harnessTree, HarnessBinarySHA256: binarySHA256,
		ExecutionProfileSHA256: profileSHA, PassRegistrationSHA256: passSHA,
		DeclarationSHA256: declarationSHA, SourceSHA256: sourceSHA, InputsSHA256: inputsSHA,
		ProcessExit: "success", ProtocolStatus: "ok", ResultSHA256: resultSHA, ResultParity: true,
		PhysicalGuests: physical, RuntimeInitializations: physical, PreparedCOWGuests: placements.prepared, OrdinaryFreshGuests: placements.ordinary, PlacementFallbacks: placements.fallback,
		HostBlobBytes: blobBytes, BlobDisposition: blobDisposition, LeaseDispositions: leaseDispositions,
		NoAuthorityExpansion: true, NoReplay: true, FreshGuests: true, Stages: stages,
	}
	return numpyreuse.SealTrialRecord(record)
}

func executionSpec(candidate numpyreuse.Case) (caseExecutionSpec, error) {
	var input numpyproducer.DeclarationInput
	var consumer string
	var expected any
	switch candidate.ID {
	case "numpy_import_small_gap0_c1":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationZerosF64, InputElements: 8192}
		consumer = "result = int(array.size)"
		expected = 8192
	case "numpy_elementwise_small_gap0_c1":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationAffineF64, InputElements: 8192}
		consumer = "result = float(array[-1])"
		expected = 12288.5
	case "numpy_elementwise_medium_gap10000_c1":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationAffineF64, InputElements: 131072}
		consumer = "result = float(array[-1])"
		expected = 196608.5
	case "numpy_elementwise_large_gap45000_c1", "numpy_elementwise_large_gap0_c4":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationAffineF64, InputElements: 1048576}
		consumer = "result = float(array[-1])"
		expected = 1572864.5
	case "numpy_reduction_small_gap0_c1", "numpy_reduction_small_gap10000_c2":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationSumI64, InputElements: 1048576}
		consumer = "result = int(array[0])"
		expected = 549755289600
	case "numpy_matrix_medium_gap0_c1", "numpy_matrix_medium_gap10000_c2", "numpy_matrix_medium_gap45000_c4":
		input = numpyproducer.DeclarationInput{Operation: numpyproducer.OperationMatmulF64, Rows: 256, Cols: 256}
		consumer = "result = float(array[0, 0])"
		return caseExecutionSpec{Declaration: input, Consumer: consumer, Expected: json.RawMessage("5559680.0")}, nil
	default:
		return caseExecutionSpec{}, errors.New("unsupported frozen case")
	}
	raw, _ := json.Marshal(expected)
	canonical, _ := canonicalResult(raw)
	return caseExecutionSpec{Declaration: input, Consumer: consumer, Expected: canonical}, nil
}

func addPlacement(placements *placementCounts, timing callTiming) {
	if timing.COWSelected {
		placements.prepared++
	} else {
		placements.ordinary++
	}
	if timing.COWRequired && (!timing.COWSelected || timing.Fallback) {
		placements.fallback++
	}
}

func buildRunRequest(runID, source string, imports []string) []byte {
	request, _ := json.Marshal(struct {
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
	}{"numpy-core", imports}})
	return request
}
func requestSource(raw []byte) string {
	var value struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value.Code
}
func canonicalResult(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, errors.New("result JSON invalid")
	}
	return json.Marshal(value)
}
func verifyBuildIdentity(expected string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("missing build identity")
	}
	revision, modified := "", ""
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			revision = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value
		}
	}
	if revision != expected || modified != "false" {
		return "", fmt.Errorf("source identity mismatch revision=%s modified=%s", revision, modified)
	}
	binary, err := os.Executable()
	if err != nil {
		return "", err
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}
func digestString(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func init() { sort.Strings([]string{}) }
