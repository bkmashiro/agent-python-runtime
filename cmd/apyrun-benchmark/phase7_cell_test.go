package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

func testP7CellArtifact() (artifactIdentity, []byte) {
	body := []byte("fixture-wasm")
	digest := sha256.Sum256(body)
	return artifactIdentity{Filename: "numpy.wasm", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(body)), SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "numpy-core", Target: "wasm32-wasip1", Execution: "reactor"}, body
}

func testP7Allocation(task uint32, order string) phase7CellAllocationIdentity {
	return phase7CellAllocationIdentity{JobID: fmt.Sprintf("%d", 900000+task), ArrayJobID: "900", ArrayTaskID: task, CgroupPathSHA256: fmt.Sprintf("%064x", task+1), ArmOrder: order, Partition: "t4", CPUsPerTask: 4, MemoryPerNodeMiB: 16384, GPUType: "tesla_t4", GPUs: 1}
}

func testP7Fragment(t *testing.T, order string, repeats, slots, repeat uint32, task uint32) phase7PairedCellFragment {
	t.Helper()
	artifact, body := testP7CellArtifact()
	digest := sha256.New()
	artifactDigest := sha256.Sum256(body)
	_, _ = digest.Write(artifactDigest[:])
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("numpy-ready-v1"))
	generation := hex.EncodeToString(digest.Sum(nil))
	invocations := 0
	fragment, _, err := assemblePhase7PairedCellFragment(context.Background(), artifact, body, hostSourceIdentity{Revision: strings.Repeat("b", 40)}, slots, repeat, repeats, order, testP7Allocation(task, order), []byte("01234567890123456789012345678901"), func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
		invocations++
		process := boundedChildResult{PID: 100 + int(spec.SampleIndex) + invocations, StartedAtUnixNS: int64(spec.SampleIndex+1)*10 + int64(invocations), MaxObservedRSSBytes: 1024}
		if spec.Strategy == "single-use-preinitialized" && spec.RequestedSlots == 64 {
			process.MaxObservedRSSBytes = spec.MaxRSSBytes + 4096
			return densityChildInvocation{Process: process}, &processRSSGuardError{Observed: process.MaxObservedRSSBytes, Limit: spec.MaxRSSBytes}
		}
		envelope := validDensityChildEnvelope(spec, artifact)
		envelope.Warmup.GenerationSHA256 = generation
		envelope.Environment.CgroupVersion = "v2"
		skipped := runtimeevidence.Metric{Status: runtimeevidence.MetricSkipped, ReasonCode: runtimeevidence.ReasonNonisolatedScope}
		envelope.Sample.Cgroup = runtimeevidence.CgroupMetrics{
			Version: "v2", Scope: "shared", MembershipSHA256: strings.Repeat("f", 64),
			MemoryCurrentBytes: skipped, MemoryPeakBytes: skipped, MemorySwapCurrentBytes: skipped,
			MemoryEventsHighTotal: skipped, MemoryEventsOOMTotal: skipped, MemoryEventsOOMKillTotal: skipped,
			PressureSomeTotalUS: skipped, PressureFullTotalUS: skipped,
		}
		return densityChildInvocation{Envelope: envelope, Process: process}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fragment
}

func TestPhase7PairedCellFragmentBindsAllocationAndExecutionOrder(t *testing.T) {
	fragment := testP7Fragment(t, "cow-first", 3, 8, 1, 10)
	if fragment.SchemaVersion != 1 || fragment.EvidenceClass != "phase7-paired-density-cell" {
		t.Fatalf("identity drift: %#v", fragment)
	}
	if fragment.Cell.RequestedSlots != 8 || fragment.Cell.RepeatIndex != 1 || fragment.Cell.SampleIndex != 10 {
		t.Fatalf("cell drift: %#v", fragment.Cell)
	}
	if fragment.Allocation.ArrayTaskID != 10 || fragment.Allocation.ArmOrder != "cow-first" {
		t.Fatalf("allocation drift: %#v", fragment.Allocation)
	}
	if len(fragment.Outcomes) != 2 || fragment.Outcomes[0].Arm != "cow" || fragment.Outcomes[1].Arm != "non_cow" {
		t.Fatalf("order drift: %#v", fragment.Outcomes)
	}
	if err := validatePhase7PairedCellFragment(fragment); err != nil {
		t.Fatal(err)
	}

	for key, mutate := range map[string]func(*phase7PairedCellFragment){
		"compound job":       func(value *phase7PairedCellFragment) { value.Allocation.JobID = "900_10" },
		"leading-zero array": func(value *phase7PairedCellFragment) { value.Allocation.ArrayJobID = "0900" },
	} {
		copy := fragment
		mutate(&copy)
		if err := validatePhase7PairedCellFragment(copy); err == nil {
			t.Fatalf("%s identity accepted", key)
		}
	}

	fragment.Allocation.CgroupPathSHA256 = ""
	if err := validatePhase7PairedCellFragment(fragment); err == nil {
		t.Fatal("missing cgroup identity accepted")
	}
}

func TestPhase7PairedCellFragmentRetainsOnlyCanonicalNonCOW64Boundary(t *testing.T) {
	fragment := testP7Fragment(t, "non-cow-first", 3, 64, 2, 20)
	if fragment.Outcomes[0].Arm != "non_cow" || fragment.Outcomes[0].Status != "rss_guard" || fragment.Outcomes[0].Boundary == nil || fragment.Outcomes[0].Sample != nil {
		t.Fatalf("boundary drift: %#v", fragment.Outcomes[0])
	}
	if fragment.Outcomes[1].Arm != "cow" || fragment.Outcomes[1].Status != "measured" {
		t.Fatalf("COW outcome drift: %#v", fragment.Outcomes[1])
	}
	if err := validatePhase7PairedCellFragment(fragment); err != nil {
		t.Fatal(err)
	}

	fragment.Outcomes[0].Arm = "cow"
	if err := validatePhase7PairedCellFragment(fragment); err == nil {
		t.Fatal("COW guard boundary accepted")
	}
}

func TestPhase7PairedCellFragmentRejectsSchemaValidSampleSemanticDrift(t *testing.T) {
	for name, mutate := range map[string]func(*phase7PairedCellFragment){
		"under ready": func(fragment *phase7PairedCellFragment) {
			sample := fragment.Outcomes[0].Sample
			sample.Pool.TargetCapacity--
			sample.Pool.Ready--
			sample.Pool.AccountedSlots--
		},
		"runtime shards": func(fragment *phase7PairedCellFragment) { fragment.Outcomes[0].Sample.RuntimeShards++ },
		"missing warmup": func(fragment *phase7PairedCellFragment) { fragment.Outcomes[0].Sample.Phases.WarmupNS = nil },
		"duplicate process": func(fragment *phase7PairedCellFragment) {
			fragment.Outcomes[1].Sample.ProcessInstanceSHA256 = fragment.Outcomes[0].Sample.ProcessInstanceSHA256
		},
	} {
		t.Run(name, func(t *testing.T) {
			fragment := testP7Fragment(t, "cow-first", 1, 8, 0, 3)
			mutate(&fragment)
			if err := validatePhase7PairedCellFragment(fragment); err == nil {
				t.Fatal("schema-valid semantic drift accepted")
			}
		})
	}
}

func TestAggregatePhase7PairedCellFragmentsRequiresExactCoverageAndUniqueAllocations(t *testing.T) {
	slots := []uint32{1, 2, 4, 8, 16, 32, 64}
	fragments := make([]phase7PairedCellFragment, 0, 21)
	for slotIndex, requested := range slots {
		for repeat := uint32(0); repeat < 3; repeat++ {
			task := uint32(slotIndex)*3 + repeat
			fragments = append(fragments, testP7Fragment(t, "cow-first", 3, requested, repeat, task))
		}
	}
	cow, cowJSON, err := aggregatePhase7PairedCellFragments(fragments, "cow-ready-single-use", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(cow.Samples) != 21 || len(cow.Boundaries) != 0 || len(cowJSON) == 0 {
		t.Fatalf("COW aggregate drift: %d/%d", len(cow.Samples), len(cow.Boundaries))
	}
	nonCOW, _, err := aggregatePhase7PairedCellFragments(fragments, "single-use-preinitialized", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonCOW.Samples) != 18 || len(nonCOW.Boundaries) != 3 {
		t.Fatalf("non-COW aggregate drift: %d/%d", len(nonCOW.Samples), len(nonCOW.Boundaries))
	}
	multiArray := append([]phase7PairedCellFragment(nil), fragments...)
	for index := range multiArray {
		array := 900 + index/7
		multiArray[index].Allocation.ArrayJobID = fmt.Sprintf("%d", array)
		multiArray[index].Allocation.JobID = fmt.Sprintf("%d", 901000+index)
	}
	if _, _, err := aggregatePhase7PairedCellFragments(multiArray, "cow-ready-single-use", 3); err != nil {
		t.Fatalf("QOS-bounded multi-array campaign rejected: %v", err)
	}

	if _, _, err := aggregatePhase7PairedCellFragments(fragments[:20], "cow-ready-single-use", 3); err == nil {
		t.Fatal("missing cell accepted")
	}
	duplicate := append([]phase7PairedCellFragment(nil), fragments...)
	duplicate[1].Allocation = duplicate[0].Allocation
	if _, _, err := aggregatePhase7PairedCellFragments(duplicate, "cow-ready-single-use", 3); err == nil {
		t.Fatal("duplicate allocation/cgroup accepted")
	}
}

func TestValidatePhase7PairedCellOptionsRequiresCanonicalExperiment(t *testing.T) {
	options := benchmarkOptions{Kind: "phase7-paired-density-cell", ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "cell.json", Class: "profile-candidate", Strategy: "fresh", PreparedWarmupProfile: "numpy-ready-v1", DensitySlots: 32, DensityRepeat: 2, ArmOrder: "cow-first", Samples: 3, MaxRSSBytes: 8589934592, ChildTimeout: phase7CellChildTimeout}
	if err := validatePhase7PairedCellOptions(options, "linux"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*benchmarkOptions){
		"shared matrix": func(o *benchmarkOptions) { o.DensitySlots = 0 },
		"guard":         func(o *benchmarkOptions) { o.MaxRSSBytes++ },
		"timeout":       func(o *benchmarkOptions) { o.ChildTimeout++ },
		"repeat":        func(o *benchmarkOptions) { o.DensityRepeat = 3 },
		"order":         func(o *benchmarkOptions) { o.ArmOrder = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := options
			mutate(&candidate)
			if err := validatePhase7PairedCellOptions(candidate, "linux"); err == nil {
				t.Fatal("noncanonical options accepted")
			}
		})
	}
}

func TestPhase7AllocationIdentityRequiresExactSlurmShapeAndCgroupV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cgroup")
	if err := os.WriteFile(path, []byte("0::/slurm/job_900/step_batch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SLURM_JOB_ID": "901", "SLURM_ARRAY_JOB_ID": "900", "SLURM_ARRAY_TASK_ID": "10", "SLURM_JOB_PARTITION": "t4", "SLURM_CPUS_PER_TASK": "4", "SLURM_MEM_PER_NODE": "16384", "SLURM_GPUS_ON_NODE": "1", "P7_ARM_ORDER": "cow-first"}
	allocation, err := phase7AllocationIdentityFromEnvironment(func(key string) string { return env[key] }, path)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.ArrayTaskID != 10 || !lowerHexString(allocation.CgroupPathSHA256, 64) {
		t.Fatalf("allocation drift: %#v", allocation)
	}
	for key, invalid := range map[string]string{"SLURM_JOB_ID": "900_10", "SLURM_ARRAY_JOB_ID": "0900", "SLURM_ARRAY_TASK_ID": "010"} {
		original := env[key]
		env[key] = invalid
		if _, err := phase7AllocationIdentityFromEnvironment(func(key string) string { return env[key] }, path); err == nil {
			t.Fatalf("noncanonical %s accepted", key)
		}
		env[key] = original
	}
	env["SLURM_MEM_PER_NODE"] = "16G"
	if _, err := phase7AllocationIdentityFromEnvironment(func(key string) string { return env[key] }, path); err != nil {
		t.Fatalf("canonical 16G memory form rejected: %v", err)
	}
	env["SLURM_MEM_PER_NODE"] = "32768"
	if _, err := phase7AllocationIdentityFromEnvironment(func(key string) string { return env[key] }, path); err == nil {
		t.Fatal("resource drift accepted")
	}
}

func TestReadPhase7CgroupV2PathBoundedRejectsEmptyAndOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cgroup")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPhase7CgroupV2PathBounded(path); err == nil {
		t.Fatal("empty cgroup pseudo-file accepted")
	}
	content := []byte("0::/slurm/uid_1/job_2/step_batch\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readPhase7CgroupV2PathBounded(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("cgroup content drift: %q", got)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 16*1024+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPhase7CgroupV2PathBounded(path); err == nil {
		t.Fatal("oversized cgroup pseudo-file accepted")
	}
}

func TestReadPhase7CgroupV2PathBoundedReadsLinuxProcfs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs only")
	}
	document, err := readPhase7CgroupV2PathBounded("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(document, []byte("0::/")) {
		t.Fatalf("unexpected cgroup v2 document: %q", document)
	}
}

func TestPhase7PairedCellSchemaRejectsUnknownAndDuplicateKeys(t *testing.T) {
	fragment := testP7Fragment(t, "cow-first", 1, 4, 0, 2)
	encoded, err := json.Marshal(fragment)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "..", "benchmark", "v1", "phase7-paired-density-cell.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateJSONSchemaDocument(encoded, schema, "phase7-paired-density-cell"); err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(",\"unknown\":true}")...)
	if err := validateJSONSchemaDocument(unknown, schema, "phase7-paired-density-cell"); err == nil {
		t.Fatal("unknown field accepted")
	}
	duplicate := bytes.Replace(encoded, []byte("\"schema_version\":1"), []byte("\"schema_version\":1,\"schema_version\":1"), 1)
	if err := validateJSONSchemaDocument(duplicate, schema, "phase7-paired-density-cell"); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestDecodePhase7CellCampaignManifestRejectsCoverageUnknownAndDuplicateKeys(t *testing.T) {
	manifest := phase7CellCampaignManifest{SchemaVersion: 1, EvidenceClass: "phase7-cell-campaign", ArmOrder: "cow-first", Repeats: 1, Entries: make([]phase7CellCampaignEntry, 7)}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePhase7CellCampaignManifest(encoded); err != nil {
		t.Fatal(err)
	}
	missing := manifest
	missing.Entries = missing.Entries[:6]
	bad, _ := json.Marshal(missing)
	if _, err := decodePhase7CellCampaignManifest(bad); err == nil {
		t.Fatal("missing campaign cell accepted")
	}
	unknown := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(",\"unknown\":true}")...)
	if _, err := decodePhase7CellCampaignManifest(unknown); err == nil {
		t.Fatal("unknown campaign field accepted")
	}
	duplicate := bytes.Replace(encoded, []byte("\"schema_version\":1"), []byte("\"schema_version\":1,\"schema_version\":1"), 1)
	if _, err := decodePhase7CellCampaignManifest(duplicate); err == nil {
		t.Fatal("duplicate campaign key accepted")
	}
}

func TestPhase7CampaignEntryUsesWrapperReceiptNamesAcrossArrays(t *testing.T) {
	for _, test := range []struct {
		index int
		array string
	}{{0, "900"}, {7, "901"}, {20, "902"}} {
		entry := phase7CellCampaignEntry{
			SampleIndex: uint32(test.index), FragmentFilename: fmt.Sprintf("cell-%02d.json", test.index), FragmentSHA256: strings.Repeat("a", 64),
			ArchiveFilename: fmt.Sprintf("cell-%d-result-%s_%d.tar.gz", test.index, test.array, test.index), ArchiveSHA256: strings.Repeat("b", 64),
			READYFilename: fmt.Sprintf("READY-%s_%d", test.array, test.index), ACKEDFilename: fmt.Sprintf("ACKED-%s_%d", test.array, test.index),
			SlurmFilename: fmt.Sprintf("slurm-%02d.json", test.index), JobID: fmt.Sprintf("%d", 1000+test.index), ArrayJobID: test.array, ArrayTaskID: uint32(test.index), CgroupSHA256: strings.Repeat("c", 64),
		}
		if err := validatePhase7CampaignEntryIdentity(entry, test.index); err != nil {
			t.Fatalf("wrapper receipt identity rejected for task %d: %v", test.index, err)
		}
		entry.READYFilename = fmt.Sprintf("READY-%02d", test.index)
		if err := validatePhase7CampaignEntryIdentity(entry, test.index); err == nil {
			t.Fatalf("controller-renamed receipt accepted for task %d", test.index)
		}
		if canonicalPhase7SlurmDecimal("0" + test.array) {
			t.Fatalf("leading-zero array job accepted for task %d", test.index)
		}
	}
}

func testP7CellArchive(t *testing.T, fragment []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	compressed := gzip.NewWriter(&encoded)
	writer := tar.NewWriter(compressed)
	members := []struct {
		name string
		kind byte
		body []byte
	}{
		{"ENVIRONMENT.txt", tar.TypeReg, []byte("env\n")}, {"RUN_COMPLETE", tar.TypeReg, []byte("source\n")},
		{"result", tar.TypeDir, nil}, {"result/SHA256SUMS", tar.TypeReg, []byte("sums\n")},
		{"result/cell.json", tar.TypeReg, fragment}, {"result/cell.json.validation.json", tar.TypeReg, []byte("{}\n")},
	}
	for _, member := range members {
		header := &tar.Header{Name: member.name, Typeflag: member.kind, Mode: 0600, Size: int64(len(member.body)), Uid: 0, Gid: 0, ModTime: time.Unix(0, 0)}
		if member.kind == tar.TypeDir {
			header.Mode = 0700
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(member.body) != 0 {
			if _, err := writer.Write(member.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPhase7CampaignVerifiesArchiveACKEDAndSlurmReceipts(t *testing.T) {
	fragment := []byte("{\"cell\":1}\n")
	digest := sha256.Sum256(fragment)
	archive := testP7CellArchive(t, fragment)
	if err := validatePhase7CellArchive(archive, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if err := validatePhase7CellArchive(archive, strings.Repeat("0", 64)); err == nil {
		t.Fatal("archive unrelated to fragment accepted")
	}
	archiveDigest := sha256.Sum256(archive)
	archiveSHA := hex.EncodeToString(archiveDigest[:])
	if err := validatePhase7ACKEDReceipt([]byte(archiveSHA+"  2026-08-09T09:00:00Z\n"), archiveSHA); err != nil {
		t.Fatal(err)
	}
	if err := validatePhase7ACKEDReceipt([]byte(strings.Repeat("0", 64)+"  2026-08-09T09:00:00Z\n"), archiveSHA); err == nil {
		t.Fatal("wrong ACKED digest accepted")
	}
	snapshot := []byte("JobId=901 ArrayJobId=900 ArrayTaskId=0 JobState=COMPLETED ExitCode=0:0 Partition=t4 NumCPUs=4 NumNodes=1 MinMemoryNode=16G Restarts=0 TresPerNode=gres/gpu:tesla_t4:1\n")
	snapshotDigest := sha256.Sum256(snapshot)
	receipt := phase7CellSlurmReceipt{JobID: "901", ArrayJobID: "900", State: "COMPLETED", ExitCode: "0:0", Partition: "t4", CPUs: 4, MemoryPerNodeMiB: 16384, GPUType: "tesla_t4", GPUs: 1, SnapshotFilename: "slurm-00.scontrol.txt", SnapshotSHA256: hex.EncodeToString(snapshotDigest[:])}
	receiptJSON, _ := json.Marshal(receipt)
	if _, err := decodePhase7SlurmReceipt(receiptJSON); err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(receiptJSON, []byte("\"state\":\"COMPLETED\""), []byte("\"state\":\"COMPLETED\",\"state\":\"COMPLETED\""), 1)
	if _, err := decodePhase7SlurmReceipt(duplicate); err == nil {
		t.Fatal("duplicate Slurm receipt key accepted")
	}
	if err := validatePhase7SlurmSnapshot(snapshot, receipt); err != nil {
		t.Fatal(err)
	}
	for _, drift := range [][]byte{
		bytes.Replace(snapshot, []byte("ArrayJobId=900"), []byte("ArrayJobId=999"), 1),
		bytes.Replace(snapshot, []byte("ArrayTaskId=0"), []byte("ArrayTaskId=1"), 1),
		bytes.Replace(snapshot, []byte("NumCPUs=4"), []byte("NumCPUs=8"), 1),
		bytes.Replace(snapshot, []byte("TresPerNode=gres/gpu:tesla_t4:1"), []byte("TresPerNode=gres/gpu:tesla_a100:1"), 1),
	} {
		if err := validatePhase7SlurmSnapshot(drift, receipt); err == nil {
			t.Fatal("drifted raw Slurm snapshot accepted")
		}
	}
}
