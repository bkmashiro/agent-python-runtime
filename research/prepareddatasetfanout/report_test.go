package prepareddatasetfanout

import (
	"errors"
	"testing"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
)

func TestValidateAndSummaries(t *testing.T) {
	report := validReport()
	if err := Validate(report); err != nil {
		t.Fatal(err)
	}
	legacy := report
	legacy.SchemaVersion = SchemaVersionV1
	legacy.HostReadNanos, legacy.HostDecodeNanos, legacy.WarmupFreshGuests = 0, 0, 0
	if err := Validate(legacy); err != nil {
		t.Fatalf("legacy v1: %v", err)
	}
	summaries, err := Summaries(report)
	if err != nil || len(summaries) != 16 {
		t.Fatalf("summaries=%d err=%v", len(summaries), err)
	}
	for _, summary := range summaries {
		if summary.Treatment == "recompute" && summary.VersusRecomputeNanos != 0 {
			t.Fatalf("baseline=%+v", summary)
		}
	}
}

func TestValidateFailsClosed(t *testing.T) {
	cases := map[string]func(*Report){
		"schema":    func(r *Report) { r.SchemaVersion = "v0" },
		"artifact":  func(r *Report) { r.ArtifactSHA256 = "" },
		"duplicate": func(r *Report) { r.Records[1] = r.Records[0] },
		"missing":   func(r *Report) { r.Records = r.Records[:15] },
		"parity":    func(r *Report) { r.Records[1].Parity = false },
		"critical":  func(r *Report) { r.Records[1].CriticalPathNanos++ },
		"copy":      func(r *Report) { find(r, "private_copy", 2).BodyCopyBytes++ },
		"encoded":   func(r *Report) { find(r, "private_copy", 1).EncodedBytes = researchdata.CanonicalBodyBytes },
		"mapped":    func(r *Report) { find(r, "private_cow_pages", 4).MappedBodyBytes = 1 },
		"orphan":    func(r *Report) { find(r, "data_local_compute", 0).OrphanBytes = 0 },
		"fresh":     func(r *Report) { find(r, "data_local_compute", 2).FreshGuests = 2 },
		"producer":  func(r *Report) { find(r, "recompute", 1).PhysicalProducers = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := validReport()
			mutate(&candidate)
			if !errors.Is(Validate(candidate), ErrInvalidReport) {
				t.Fatal("accepted")
			}
		})
	}
}

func validReport() Report {
	report := Report{SchemaVersion: SchemaVersion, ArtifactSHA256: "sha256:artifact", FixtureBodySHA256: researchdata.CanonicalBodySHA256, FixtureBodyBytes: researchdata.CanonicalBodyBytes, PackagePrepareOnce: true, HostReadNanos: 1, HostDecodeNanos: 1, WarmupFreshGuests: 4, MutationIsolated: true}
	body := uint64(researchdata.CanonicalBodyBytes)
	for _, treatment := range []string{"recompute", "private_copy", "private_cow_pages", "data_local_compute"} {
		for _, consumers := range []int{0, 1, 2, 4} {
			r := Record{Treatment: treatment, Consumers: consumers, ShardPrepareNanos: 100, DatasetPrepareNanos: 10, ConsumerNanos: uint64(consumers), CriticalPathNanos: 10 + uint64(consumers), LogicalConsumers: uint64(consumers), FreshGuests: uint64(consumers), ResultSum: ExpectedSum, Parity: true, Cleanup: true}
			switch treatment {
			case "recompute":
				r.DatasetPrepareNanos = 0
				r.CriticalPathNanos = uint64(consumers)
			case "private_copy":
				r.PhysicalProducers = 1
				r.BodyCopyBytes = body * uint64(consumers)
				r.EncodedBytes = (body + 1) * uint64(consumers)
				if consumers == 0 {
					r.OrphanBytes = body
				}
			case "private_cow_pages":
				r.PhysicalProducers = 1
				r.MappedBodyBytes = body * uint64(consumers)
				if consumers == 0 {
					r.OrphanBytes = body
				}
			case "data_local_compute":
				r.PhysicalProducers = 1
				if consumers > 0 {
					r.FreshGuests = uint64(consumers + 1)
					r.MappedBodyBytes = body
				} else {
					r.OrphanBytes = body
				}
			}
			report.Records = append(report.Records, r)
		}
	}
	return report
}

func find(report *Report, treatment string, consumers int) *Record {
	for i := range report.Records {
		if report.Records[i].Treatment == treatment && report.Records[i].Consumers == consumers {
			return &report.Records[i]
		}
	}
	panic("missing")
}
