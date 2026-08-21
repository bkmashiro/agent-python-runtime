package prepareddatasetfanout

import (
	"errors"
	"fmt"
	"sort"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
)

const (
	SchemaVersion        = "pysolate.prepared-data-fanout.v1"
	ExpectedSum   uint64 = 549755289600
)

var ErrInvalidReport = errors.New("invalid prepared-data fanout report")

type Record struct {
	Treatment           string `json:"treatment"`
	Consumers           int    `json:"consumers"`
	ShardPrepareNanos   uint64 `json:"shard_prepare_nanos"`
	DatasetPrepareNanos uint64 `json:"dataset_prepare_nanos"`
	ConsumerNanos       uint64 `json:"consumer_nanos"`
	CriticalPathNanos   uint64 `json:"critical_path_nanos"`
	BodyCopyBytes       uint64 `json:"body_copy_bytes"`
	EncodedBytes        uint64 `json:"encoded_bytes"`
	MappedBodyBytes     uint64 `json:"mapped_body_bytes"`
	OrphanBytes         uint64 `json:"orphan_bytes"`
	PhysicalProducers   uint64 `json:"physical_producers"`
	LogicalConsumers    uint64 `json:"logical_consumers"`
	FreshGuests         uint64 `json:"fresh_guests"`
	MinorFaults         int64  `json:"minor_faults"`
	MajorFaults         int64  `json:"major_faults"`
	ResultSum           uint64 `json:"result_sum"`
	Parity              bool   `json:"parity"`
	Cleanup             bool   `json:"cleanup"`
}

type Report struct {
	SchemaVersion      string   `json:"schema_version"`
	ArtifactSHA256     string   `json:"artifact_sha256"`
	FixtureBodySHA256  string   `json:"fixture_body_sha256"`
	FixtureBodyBytes   uint64   `json:"fixture_body_bytes"`
	PackagePrepareOnce bool     `json:"package_prepare_once_per_treatment"`
	MutationIsolated   bool     `json:"mutation_isolated"`
	Records            []Record `json:"records"`
}

func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion || report.ArtifactSHA256 == "" ||
		report.FixtureBodySHA256 != researchdata.CanonicalBodySHA256 || report.FixtureBodyBytes != researchdata.CanonicalBodyBytes ||
		!report.PackagePrepareOnce || !report.MutationIsolated || len(report.Records) != 16 {
		return ErrInvalidReport
	}
	expected := map[string]bool{}
	for _, treatment := range []string{"recompute", "private_copy", "private_cow_pages", "data_local_compute"} {
		for _, consumers := range []int{0, 1, 2, 4} {
			expected[key(treatment, consumers)] = true
		}
	}
	seen := map[string]bool{}
	for _, record := range report.Records {
		coordinate := key(record.Treatment, record.Consumers)
		if !expected[coordinate] || seen[coordinate] || validateRecord(record) != nil {
			return ErrInvalidReport
		}
		seen[coordinate] = true
	}
	return nil
}

func validateRecord(record Record) error {
	if record.CriticalPathNanos != record.DatasetPrepareNanos+record.ConsumerNanos || !record.Parity || !record.Cleanup ||
		record.ResultSum != ExpectedSum || record.LogicalConsumers != uint64(record.Consumers) || record.MinorFaults < 0 || record.MajorFaults < 0 {
		return ErrInvalidReport
	}
	body := uint64(researchdata.CanonicalBodyBytes)
	if record.Consumers == 0 {
		if record.FreshGuests != 0 {
			return ErrInvalidReport
		}
	} else if record.Treatment == "data_local_compute" {
		if record.FreshGuests != uint64(record.Consumers+1) {
			return ErrInvalidReport
		}
	} else if record.FreshGuests != uint64(record.Consumers) {
		return ErrInvalidReport
	}
	switch record.Treatment {
	case "recompute":
		if record.PhysicalProducers != 0 || record.BodyCopyBytes != 0 || record.EncodedBytes != 0 || record.MappedBodyBytes != 0 || record.OrphanBytes != 0 {
			return ErrInvalidReport
		}
	case "private_copy":
		if record.PhysicalProducers != 1 || record.MappedBodyBytes != 0 || record.BodyCopyBytes != body*uint64(record.Consumers) ||
			(record.Consumers > 0 && (record.EncodedBytes <= record.BodyCopyBytes || record.OrphanBytes != 0)) || (record.Consumers == 0 && (record.EncodedBytes != 0 || record.OrphanBytes != body)) {
			return ErrInvalidReport
		}
	case "private_cow_pages":
		expectedOrphan := uint64(0)
		if record.Consumers == 0 {
			expectedOrphan = body
		}
		if record.PhysicalProducers != 1 || record.BodyCopyBytes != 0 || record.EncodedBytes != 0 || record.MappedBodyBytes != body*uint64(record.Consumers) ||
			record.OrphanBytes != expectedOrphan {
			return ErrInvalidReport
		}
	case "data_local_compute":
		expectedMapped, expectedOrphan := uint64(0), uint64(0)
		if record.Consumers > 0 {
			expectedMapped = body
		} else {
			expectedOrphan = body
		}
		if record.PhysicalProducers != 1 || record.BodyCopyBytes != 0 || record.EncodedBytes != 0 || record.MappedBodyBytes != expectedMapped ||
			record.OrphanBytes != expectedOrphan {
			return ErrInvalidReport
		}
	default:
		return ErrInvalidReport
	}
	return nil
}

type Summary struct {
	Treatment            string `json:"treatment"`
	Consumers            int    `json:"consumers"`
	CriticalPathNanos    uint64 `json:"critical_path_nanos"`
	VersusRecomputeNanos int64  `json:"versus_recompute_nanos"`
}

func Summaries(report Report) ([]Summary, error) {
	if err := Validate(report); err != nil {
		return nil, err
	}
	baseline := map[int]uint64{}
	for _, record := range report.Records {
		if record.Treatment == "recompute" {
			baseline[record.Consumers] = record.CriticalPathNanos
		}
	}
	out := make([]Summary, 0, len(report.Records))
	for _, record := range report.Records {
		out = append(out, Summary{record.Treatment, record.Consumers, record.CriticalPathNanos, int64(record.CriticalPathNanos) - int64(baseline[record.Consumers])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Consumers != out[j].Consumers {
			return out[i].Consumers < out[j].Consumers
		}
		return out[i].Treatment < out[j].Treatment
	})
	return out, nil
}

func key(treatment string, consumers int) string { return fmt.Sprintf("%s/%d", treatment, consumers) }
