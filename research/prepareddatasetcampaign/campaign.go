package prepareddatasetcampaign

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	fanout "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetfanout"
)

const (
	EagerSchemaVersion          = "pysolate.prepared-data-eager.v1"
	CampaignSchemaVersion       = "pysolate.prepared-data-campaign.v1"
	frozenPreregistrationSHA256 = "sha256:9f7baa064eff8e19c93651b41decf4f855673fcc5ae767716f023d3de4702bd6"
	frozenSourceCommit          = "ccef2d9875ab2f289434012bbdfb4015b99db6b1"
	frozenSourceTree            = "6ecc38d09d37be9eaaab51d2765ca8930592faa9"
	frozenArtifactSHA256        = "sha256:2753cde560f3961a483df53aec334c8fdbb084934e5a62a56d436aea1ae557ad"
)

var ErrInvalidCampaign = errors.New("invalid prepared-data campaign")

type EagerRecord struct {
	Consumers        int    `json:"consumers"`
	ConsumerNanos    uint64 `json:"consumer_nanos"`
	BodyCopyBytes    uint64 `json:"body_copy_bytes"`
	EncodedBytes     uint64 `json:"encoded_bytes"`
	LogicalConsumers uint64 `json:"logical_consumers"`
	FreshGuests      uint64 `json:"fresh_guests"`
	ResultSum        uint64 `json:"result_sum"`
	Parity           bool   `json:"parity"`
	Cleanup          bool   `json:"cleanup"`
}

type EagerReport struct {
	SchemaVersion     string        `json:"schema_version"`
	ArtifactSHA256    string        `json:"artifact_sha256"`
	FixtureBodySHA256 string        `json:"fixture_body_sha256"`
	HostReadNanos     uint64        `json:"host_read_nanos"`
	HostDecodeNanos   uint64        `json:"host_decode_nanos"`
	ShardPrepareNanos uint64        `json:"shard_prepare_nanos"`
	WarmupFreshGuests uint64        `json:"warmup_fresh_guests"`
	Records           []EagerRecord `json:"records"`
}

func ValidateEager(report EagerReport) error {
	if report.SchemaVersion != EagerSchemaVersion || report.ArtifactSHA256 == "" || report.FixtureBodySHA256 != researchdata.CanonicalBodySHA256 ||
		report.HostReadNanos == 0 || report.HostDecodeNanos == 0 || report.ShardPrepareNanos == 0 || report.WarmupFreshGuests != 1 || len(report.Records) != 3 {
		return ErrInvalidCampaign
	}
	seen := map[int]bool{}
	for _, r := range report.Records {
		if (r.Consumers != 1 && r.Consumers != 2 && r.Consumers != 4) || seen[r.Consumers] || r.LogicalConsumers != uint64(r.Consumers) || r.FreshGuests != 1 ||
			r.ResultSum != fanout.ExpectedSum || !r.Parity || !r.Cleanup || r.BodyCopyBytes != researchdata.CanonicalBodyBytes || r.EncodedBytes <= r.BodyCopyBytes {
			return ErrInvalidCampaign
		}
		seen[r.Consumers] = true
	}
	return nil
}

type Manifest struct {
	SchemaVersion                 string `json:"schema_version"`
	PreregistrationSHA256         string `json:"preregistration_sha256"`
	SourceCommit                  string `json:"source_commit"`
	SourceTree                    string `json:"source_tree"`
	ArtifactSHA256                string `json:"artifact_sha256"`
	FixtureFileSHA256             string `json:"fixture_file_sha256"`
	FixtureBodySHA256             string `json:"fixture_body_sha256"`
	PayloadBytes                  uint64 `json:"payload_bytes"`
	Trials                        int    `json:"trials"`
	Consumers                     []int  `json:"consumers"`
	LeadGapMS                     []int  `json:"lead_gap_ms"`
	TrialOrder                    string `json:"trial_order"`
	FanoutWarmupFreshGuests       uint64 `json:"fanout_warmup_fresh_guests"`
	EagerWarmupFreshGuests        uint64 `json:"eager_warmup_fresh_guests"`
	PayloadExtensionsEntered      bool   `json:"payload_extensions_entered"`
	LinuxPlatform                 string `json:"linux_platform"`
	LinuxKernel                   string `json:"linux_kernel"`
	LinuxCPU                      string `json:"linux_cpu"`
	LinuxMemoryKiB                uint64 `json:"linux_memory_kib"`
	DarwinPlatform                string `json:"darwin_platform"`
	DarwinOS                      string `json:"darwin_os"`
	DarwinCPU                     string `json:"darwin_cpu"`
	DarwinMemoryBytes             uint64 `json:"darwin_memory_bytes"`
	DarwinMechanismEvidenceSHA256 string `json:"darwin_mechanism_evidence_sha256"`
}

type Trial struct {
	ID     int           `json:"id"`
	Fanout fanout.Report `json:"fanout"`
	Eager  EagerReport   `json:"eager"`
}

type Record struct {
	Trial                int    `json:"trial"`
	Treatment            string `json:"treatment"`
	Consumers            int    `json:"consumers"`
	LeadGapMS            int    `json:"lead_gap_ms"`
	CriticalPathNanos    uint64 `json:"critical_path_nanos"`
	ShardPrepareNanos    uint64 `json:"shard_prepare_nanos"`
	PhysicalPrepareNanos uint64 `json:"physical_prepare_nanos"`
	ConsumerNanos        uint64 `json:"consumer_nanos"`
	BodyCopyBytes        uint64 `json:"body_copy_bytes"`
	EncodedBytes         uint64 `json:"encoded_bytes"`
	MappedBodyBytes      uint64 `json:"mapped_body_bytes"`
	PhysicalProducers    uint64 `json:"physical_producers"`
	LogicalConsumers     uint64 `json:"logical_consumers"`
	FreshGuests          uint64 `json:"fresh_guests"`
	FreshAuthority       bool   `json:"fresh_authority"`
	Parity               bool   `json:"parity"`
	Cleanup              bool   `json:"cleanup"`
}

type Aggregate struct {
	Treatment               string `json:"treatment"`
	Consumers               int    `json:"consumers"`
	LeadGapMS               int    `json:"lead_gap_ms"`
	MedianCriticalPathNanos uint64 `json:"median_critical_path_nanos"`
	VersusSerialNanos       int64  `json:"versus_serial_nanos"`
}

type Report struct {
	SchemaVersion string      `json:"schema_version"`
	Manifest      Manifest    `json:"manifest"`
	Records       []Record    `json:"records"`
	Aggregates    []Aggregate `json:"aggregates"`
}

func Build(manifest Manifest, trials []Trial) (Report, error) {
	if !validManifest(manifest) || len(trials) != manifest.Trials {
		return Report{}, ErrInvalidCampaign
	}
	seen := map[int]bool{}
	records := make([]Record, 0, manifest.Trials*54)
	for _, trial := range trials {
		if seen[trial.ID] || trial.ID < 1 || trial.ID > manifest.Trials || fanout.Validate(trial.Fanout) != nil || ValidateEager(trial.Eager) != nil || trial.Fanout.ArtifactSHA256 != manifest.ArtifactSHA256 || trial.Eager.ArtifactSHA256 != manifest.ArtifactSHA256 {
			return Report{}, ErrInvalidCampaign
		}
		seen[trial.ID] = true
		for _, n := range manifest.Consumers {
			for _, gap := range manifest.LeadGapMS {
				generated, err := recordsFor(trial, n, gap)
				if err != nil {
					return Report{}, err
				}
				records = append(records, generated...)
			}
		}
	}
	aggregates, err := aggregate(records, manifest)
	if err != nil {
		return Report{}, err
	}
	return Report{CampaignSchemaVersion, manifest, records, aggregates}, nil
}

func validManifest(m Manifest) bool {
	return m.SchemaVersion == "pysolate.prepared-data-campaign-manifest.v1" && m.PreregistrationSHA256 == frozenPreregistrationSHA256 &&
		m.SourceCommit == frozenSourceCommit && m.SourceTree == frozenSourceTree && m.ArtifactSHA256 == frozenArtifactSHA256 &&
		m.FixtureFileSHA256 == researchdata.CanonicalFileSHA256 && m.FixtureBodySHA256 == researchdata.CanonicalBodySHA256 &&
		m.PayloadBytes == researchdata.CanonicalBodyBytes && m.Trials == 3 && equalInts(m.Consumers, []int{1, 2, 4}) &&
		equalInts(m.LeadGapMS, []int{0, 250, 1000}) && m.TrialOrder == "deterministic_alternating" &&
		m.FanoutWarmupFreshGuests == 4 && m.EagerWarmupFreshGuests == 1 && !m.PayloadExtensionsEntered &&
		m.LinuxPlatform == "linux_amd64" && m.LinuxKernel != "" && m.LinuxCPU != "" && m.LinuxMemoryKiB > 0 &&
		m.DarwinPlatform == "darwin_arm64" && m.DarwinOS != "" && m.DarwinCPU != "" && m.DarwinMemoryBytes > 0 &&
		m.DarwinMechanismEvidenceSHA256 != ""
}

func recordsFor(trial Trial, n, gapMS int) ([]Record, error) {
	private, ok := findFanout(trial.Fanout, "private_copy", n)
	if !ok {
		return nil, ErrInvalidCampaign
	}
	cow, ok := findFanout(trial.Fanout, "private_cow_pages", n)
	if !ok {
		return nil, ErrInvalidCampaign
	}
	local, ok := findFanout(trial.Fanout, "data_local_compute", n)
	if !ok {
		return nil, ErrInvalidCampaign
	}
	eager, ok := findEager(trial.Eager, n)
	if !ok {
		return nil, ErrInvalidCampaign
	}
	read, decode := trial.Fanout.HostReadNanos, trial.Fanout.HostDecodeNanos
	gap := uint64(gapMS) * 1_000_000
	makeRecord := func(t string, prep, consumer, critical, shard, copyBytes, encoded, mapped, producers, logical, fresh uint64, freshAuthority bool) Record {
		return Record{trial.ID, t, n, gapMS, critical, shard, prep, consumer, copyBytes, encoded, mapped, producers, logical, fresh, freshAuthority, true, true}
	}
	serialPrep := read + decode
	return []Record{
		makeRecord("serial_whole_source", serialPrep, private.ConsumerNanos, serialPrep+private.ConsumerNanos, private.ShardPrepareNanos, private.BodyCopyBytes, private.EncodedBytes, 0, 1, uint64(n), uint64(n), true),
		makeRecord("EAGER_style_persistent_interpreter", read+decode, eager.ConsumerNanos, overlap(read+decode, gap)+eager.ConsumerNanos, trial.Eager.ShardPrepareNanos, eager.BodyCopyBytes, eager.EncodedBytes, 0, 1, uint64(n), 1, false),
		makeRecord("raw_read_only_pre_dispatch", read, decode+private.ConsumerNanos, overlap(read, gap)+decode+private.ConsumerNanos, private.ShardPrepareNanos, private.BodyCopyBytes, private.EncodedBytes, 0, 1, uint64(n), uint64(n), true),
		makeRecord("prepared_data_private_copy", read+decode, private.ConsumerNanos, overlap(read+decode, gap)+private.ConsumerNanos, private.ShardPrepareNanos, private.BodyCopyBytes, private.EncodedBytes, 0, 1, uint64(n), uint64(n), true),
		makeRecord("prepared_data_private_cow_pages", cow.DatasetPrepareNanos, cow.ConsumerNanos, overlap(cow.DatasetPrepareNanos, gap)+cow.ConsumerNanos, cow.ShardPrepareNanos, 0, 0, cow.MappedBodyBytes, 1, uint64(n), uint64(n), true),
		makeRecord("prepared_data_data_local_compute", local.DatasetPrepareNanos, local.ConsumerNanos, overlap(local.DatasetPrepareNanos, gap)+local.ConsumerNanos, local.ShardPrepareNanos, 0, 0, local.MappedBodyBytes, 1, uint64(n), local.FreshGuests, true),
	}, nil
}

func aggregate(records []Record, m Manifest) ([]Aggregate, error) {
	groups := map[string][]uint64{}
	serial := map[string]uint64{}
	for _, r := range records {
		if !r.Parity || !r.Cleanup || r.CriticalPathNanos > r.PhysicalPrepareNanos+r.ConsumerNanos {
			return nil, ErrInvalidCampaign
		}
		groups[coord(r.Treatment, r.Consumers, r.LeadGapMS)] = append(groups[coord(r.Treatment, r.Consumers, r.LeadGapMS)], r.CriticalPathNanos)
	}
	for key, values := range groups {
		if len(values) != m.Trials {
			return nil, ErrInvalidCampaign
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		if len(values) != 3 {
			return nil, ErrInvalidCampaign
		}
		if strings.HasPrefix(key, "serial_whole_source/") {
			serial[strings.TrimPrefix(key, "serial_whole_source/")] = values[1]
		}
	}
	out := make([]Aggregate, 0, len(groups))
	for key, values := range groups {
		t, n, g := parseCoord(key)
		base, ok := serial[coordSuffix(n, g)]
		if !ok {
			return nil, ErrInvalidCampaign
		}
		out = append(out, Aggregate{t, n, g, values[1], int64(values[1]) - int64(base)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Consumers != out[j].Consumers {
			return out[i].Consumers < out[j].Consumers
		}
		if out[i].LeadGapMS != out[j].LeadGapMS {
			return out[i].LeadGapMS < out[j].LeadGapMS
		}
		return out[i].Treatment < out[j].Treatment
	})
	return out, nil
}

func overlap(prep, gap uint64) uint64 {
	if gap >= prep {
		return 0
	}
	return prep - gap
}
func findFanout(r fanout.Report, t string, n int) (fanout.Record, bool) {
	for _, x := range r.Records {
		if x.Treatment == t && x.Consumers == n {
			return x, true
		}
	}
	return fanout.Record{}, false
}
func findEager(r EagerReport, n int) (EagerRecord, bool) {
	for _, x := range r.Records {
		if x.Consumers == n {
			return x, true
		}
	}
	return EagerRecord{}, false
}
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func coord(t string, n, g int) string { return t + "/" + strconv.Itoa(n) + "/" + strconv.Itoa(g) }
func coordSuffix(n, g int) string     { return strconv.Itoa(n) + "/" + strconv.Itoa(g) }
func parseCoord(value string) (string, int, int) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return "", 0, 0
	}
	n, _ := strconv.Atoi(parts[1])
	g, _ := strconv.Atoi(parts[2])
	return parts[0], n, g
}
