package prepareddatasetcampaign

import (
	"errors"
	"testing"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	fanout "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetfanout"
)

func TestBuildCampaignCoordinatesAndAuthority(t *testing.T) {
	manifest := validManifestForTest()
	trials := []Trial{{1, fanoutForTest(1), eagerForTest(1)}, {2, fanoutForTest(2), eagerForTest(2)}, {3, fanoutForTest(3), eagerForTest(3)}}
	report, err := Build(manifest, trials)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Records) != 162 || len(report.Aggregates) != 54 {
		t.Fatalf("records=%d aggregates=%d", len(report.Records), len(report.Aggregates))
	}
	for _, record := range report.Records {
		if record.Treatment == "EAGER_style_persistent_interpreter" && record.FreshAuthority {
			t.Fatal("EAGER mislabeled fresh")
		}
		if record.Treatment != "EAGER_style_persistent_interpreter" && !record.FreshAuthority {
			t.Fatalf("fresh treatment mislabeled: %s", record.Treatment)
		}
	}
}

func TestBuildCampaignFailsClosed(t *testing.T) {
	base := []Trial{{1, fanoutForTest(1), eagerForTest(1)}, {2, fanoutForTest(2), eagerForTest(2)}, {3, fanoutForTest(3), eagerForTest(3)}}
	cases := map[string]func(*Manifest, []Trial){"manifest": func(m *Manifest, _ []Trial) { m.Trials = 2 }, "duplicate": func(_ *Manifest, t []Trial) { t[1].ID = 1 }, "artifact": func(_ *Manifest, t []Trial) { t[1].Eager.ArtifactSHA256 = "drift" }, "parity": func(_ *Manifest, t []Trial) { t[2].Fanout.Records[1].Parity = false }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validManifestForTest()
			trials := cloneTrials(base)
			mutate(&m, trials)
			if _, err := Build(m, trials); !errors.Is(err, ErrInvalidCampaign) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func validManifestForTest() Manifest {
	return Manifest{SchemaVersion: "pysolate.prepared-data-campaign-manifest.v1", PreregistrationSHA256: "sha256:pre", SourceCommit: "commit", SourceTree: "tree", ArtifactSHA256: "sha256:artifact", FixtureFileSHA256: researchdata.CanonicalFileSHA256, FixtureBodySHA256: researchdata.CanonicalBodySHA256, PayloadBytes: researchdata.CanonicalBodyBytes, Trials: 3, Consumers: []int{1, 2, 4}, LeadGapMS: []int{0, 250, 1000}, LinuxPlatform: "linux_amd64", DarwinMechanismEvidenceSHA256: "sha256:darwin"}
}
func fanoutForTest(trial int) fanout.Report {
	r := fanout.Report{SchemaVersion: fanout.SchemaVersion, ArtifactSHA256: "sha256:artifact", FixtureBodySHA256: researchdata.CanonicalBodySHA256, FixtureBodyBytes: researchdata.CanonicalBodyBytes, PackagePrepareOnce: true, HostReadNanos: uint64(10 + trial), HostDecodeNanos: uint64(20 + trial), WarmupFreshGuests: 4, MutationIsolated: true}
	body := uint64(researchdata.CanonicalBodyBytes)
	for _, t := range []string{"recompute", "private_copy", "private_cow_pages", "data_local_compute"} {
		for _, n := range []int{0, 1, 2, 4} {
			x := fanout.Record{Treatment: t, Consumers: n, ShardPrepareNanos: 100, DatasetPrepareNanos: 30, ConsumerNanos: uint64(50*n + trial), CriticalPathNanos: 30 + uint64(50*n+trial), LogicalConsumers: uint64(n), FreshGuests: uint64(n), ResultSum: fanout.ExpectedSum, Parity: true, Cleanup: true}
			switch t {
			case "recompute":
				x.DatasetPrepareNanos = 0
				x.CriticalPathNanos = x.ConsumerNanos
			case "private_copy":
				x.PhysicalProducers = 1
				x.BodyCopyBytes = body * uint64(n)
				x.EncodedBytes = (body + 1) * uint64(n)
				if n == 0 {
					x.OrphanBytes = body
				}
			case "private_cow_pages":
				x.PhysicalProducers = 1
				x.MappedBodyBytes = body * uint64(n)
				if n == 0 {
					x.OrphanBytes = body
				}
			case "data_local_compute":
				x.PhysicalProducers = 1
				if n > 0 {
					x.FreshGuests = uint64(n + 1)
					x.MappedBodyBytes = body
				} else {
					x.OrphanBytes = body
				}
			}
			r.Records = append(r.Records, x)
		}
	}
	return r
}
func eagerForTest(trial int) EagerReport {
	r := EagerReport{SchemaVersion: EagerSchemaVersion, ArtifactSHA256: "sha256:artifact", FixtureBodySHA256: researchdata.CanonicalBodySHA256, HostReadNanos: uint64(10 + trial), HostDecodeNanos: uint64(20 + trial), ShardPrepareNanos: 100, WarmupFreshGuests: 1}
	for _, n := range []int{1, 2, 4} {
		r.Records = append(r.Records, EagerRecord{Consumers: n, ConsumerNanos: uint64(100*n + trial), BodyCopyBytes: researchdata.CanonicalBodyBytes, EncodedBytes: researchdata.CanonicalBodyBytes + 1, LogicalConsumers: uint64(n), FreshGuests: 1, ResultSum: fanout.ExpectedSum, Parity: true, Cleanup: true})
	}
	return r
}
func cloneTrials(in []Trial) []Trial {
	out := make([]Trial, len(in))
	for i, x := range in {
		out[i] = Trial{x.ID, fanoutForTest(x.ID), eagerForTest(x.ID)}
	}
	return out
}
