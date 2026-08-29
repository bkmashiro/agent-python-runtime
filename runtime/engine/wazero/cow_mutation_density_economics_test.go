package wazero

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"testing"
)

const cowMutationDensitySchema = "pysolate.cow-mutation-density-economics.v1"

var cowMutationDensityPages = []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048}

type cowMutationDensityCell struct {
	MutationPages  int                                `json:"mutation_pages"`
	ExpectedResult int64                              `json:"expected_result"`
	Treatments     []preparedFamilyEconomicsTreatment `json:"treatments"`
}

type cowMutationDensityEvidence struct {
	SchemaVersion  string                   `json:"schema_version"`
	SourceCommit   string                   `json:"source_commit"`
	SourceTree     string                   `json:"source_tree"`
	HostID         string                   `json:"host_id"`
	ArtifactSHA256 string                   `json:"artifact_sha256"`
	InputSHA256    string                   `json:"input_sha256"`
	InputBytes     uint64                   `json:"input_bytes"`
	Fanout         int                      `json:"fanout"`
	Runs           int                      `json:"runs"`
	PageSizeBytes  int                      `json:"page_size_bytes"`
	MutationPages  []int                    `json:"mutation_pages"`
	Cells          []cowMutationDensityCell `json:"cells"`
}

func TestCOWMutationDensityEconomicsFixture(t *testing.T) {
	output := os.Getenv("PYSOLATE_COW_MUTATION_DENSITY_OUTPUT")
	if output == "" {
		t.Skip("set PYSOLATE_COW_MUTATION_DENSITY_OUTPUT to run")
	}
	if goruntime.GOOS != "linux" || os.Getpagesize() != 4096 {
		t.Fatal("COW density requires Linux 4096-byte pages")
	}
	runs := 3
	var err error
	if raw := os.Getenv("PYSOLATE_COW_MUTATION_DENSITY_RUNS"); raw != "" {
		runs, err = strconv.Atoi(raw)
		if err != nil || runs < 1 || runs > 10 {
			t.Fatal("runs must be in [1,10]")
		}
	}
	orderOffset := 0
	if raw := os.Getenv("PYSOLATE_COW_MUTATION_DENSITY_ORDER_OFFSET"); raw != "" {
		orderOffset, err = strconv.Atoi(raw)
		if err != nil || orderOffset < 0 || orderOffset > 1 {
			t.Fatal("order offset must be 0 or 1")
		}
	}
	artifact, profile := realPreparedGuest(t)
	input := preparedFamilyEconomicsInput(t, profile)
	artifactDigest := sha256.Sum256(artifact)
	evidence := cowMutationDensityEvidence{
		SchemaVersion: cowMutationDensitySchema, SourceCommit: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_COMMIT"), SourceTree: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_TREE"),
		HostID: os.Getenv("EVALUATION_HOST_ID"), ArtifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]), InputSHA256: input.IdentitySHA256(),
		InputBytes: 8 << 20, Fanout: 4, Runs: runs, PageSizeBytes: os.Getpagesize(), MutationPages: append([]int(nil), cowMutationDensityPages...),
	}
	work := t.TempDir()
	for cellIndex, pages := range cowMutationDensityPages {
		byMode := map[string][]preparedFamilyEconomicsSample{"private_copy": {}, "private_cow": {}}
		for iteration := 0; iteration < runs; iteration++ {
			order := []string{"private_copy", "private_cow"}
			if (cellIndex+iteration+orderOffset)%2 == 1 {
				order[0], order[1] = order[1], order[0]
			}
			for _, mode := range order {
				workerOutput := filepath.Join(work, fmt.Sprintf("%04d-%02d-%s.json", pages, iteration, mode))
				command := exec.Command(os.Args[0], "-test.run=^TestPreparedFamilyEconomicsWorker$", "-test.count=1")
				command.Env = append(os.Environ(),
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER=1",
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_MODE="+mode,
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_ITERATION="+strconv.Itoa(iteration),
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT=4",
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_MUTATION_PAGES="+strconv.Itoa(pages),
					"PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER_OUTPUT="+workerOutput,
				)
				var trace bytes.Buffer
				command.Stdout, command.Stderr = &trace, &trace
				if err := command.Run(); err != nil {
					t.Fatalf("pages=%d iteration=%d mode=%s: %v\n%s", pages, iteration, mode, err, trace.String())
				}
				raw, err := os.ReadFile(workerOutput)
				if err != nil {
					t.Fatal(err)
				}
				var sample preparedFamilyEconomicsSample
				if err := json.Unmarshal(raw, &sample); err != nil {
					t.Fatal(err)
				}
				if sample.Mode != mode || sample.Iteration != iteration || sample.Fanout != 4 || sample.MutationPages != pages || sample.PageSizeBytes != 4096 || sample.Result != int64((8<<20)/8+pages) {
					t.Fatalf("invalid sample: %+v", sample)
				}
				byMode[mode] = append(byMode[mode], sample)
			}
		}
		cell := cowMutationDensityCell{MutationPages: pages, ExpectedResult: int64((8<<20)/8 + pages)}
		for _, mode := range []string{"private_copy", "private_cow"} {
			samples := byMode[mode]
			cell.Treatments = append(cell.Treatments, preparedFamilyEconomicsTreatment{Mode: mode, Samples: samples})
		}
		evidence.Cells = append(evidence.Cells, cell)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
