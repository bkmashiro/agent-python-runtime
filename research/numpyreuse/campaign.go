package numpyreuse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
)

const (
	TrialRecordSchemaVersion    = "pysolate.numpy-reuse-trial-record.v1"
	CampaignReportSchemaVersion = "pysolate.numpy-reuse-campaign-report.v1"
)

var ErrCampaignRecord = errors.New("invalid numpy reuse campaign record")

type StageMetrics struct {
	AnalysisNanos                uint64  `json:"analysis_nanos"`
	ProducerGuestProvisionNanos  uint64  `json:"producer_guest_provision_nanos"`
	ProducerImportNanos          *uint64 `json:"producer_import_nanos"`
	ProducerComputeNanos         *uint64 `json:"producer_compute_nanos"`
	ProducerEncodeCopyNanos      *uint64 `json:"producer_encode_copy_nanos"`
	ProducerExecutionNanos       uint64  `json:"producer_execution_nanos"`
	HostBlobStoreNanos           uint64  `json:"host_blob_store_nanos"`
	ConsumerGuestProvisionNanos  uint64  `json:"consumer_guest_provision_nanos"`
	ConsumerImportNanos          *uint64 `json:"consumer_import_nanos"`
	ConsumerCopyMaterializeNanos uint64  `json:"consumer_copy_materialize_nanos"`
	ConsumerExecutionNanos       uint64  `json:"consumer_execution_nanos"`
	TeardownNanos                uint64  `json:"teardown_nanos"`
	CriticalWallNanos            uint64  `json:"critical_wall_nanos"`
	PeakResidentMemoryBytes      uint64  `json:"peak_resident_memory_bytes"`
	UnavailableStageReason       string  `json:"unavailable_stage_reason"`
}

type TrialRecord struct {
	SchemaVersion          string             `json:"schema_version"`
	Coordinate             CampaignCoordinate `json:"coordinate"`
	CaseSourceSHA256       string             `json:"case_source_sha256"`
	ArtifactSHA256         string             `json:"artifact_sha256"`
	ExecutionProfileSHA256 string             `json:"execution_profile_sha256"`
	PassRegistrationSHA256 string             `json:"pass_registration_sha256"`
	DeclarationSHA256      string             `json:"declaration_sha256"`
	SourceSHA256           string             `json:"source_sha256"`
	InputsSHA256           string             `json:"inputs_sha256"`
	ProcessExit            string             `json:"process_exit"`
	ProtocolStatus         string             `json:"protocol_status"`
	ResultSHA256           string             `json:"result_sha256"`
	ResultParity           bool               `json:"result_parity"`
	PhysicalGuests         uint32             `json:"physical_guests"`
	RuntimeInitializations uint32             `json:"runtime_initializations"`
	PreparedCOWGuests      uint32             `json:"prepared_cow_guests"`
	OrdinaryFreshGuests    uint32             `json:"ordinary_fresh_guests"`
	PlacementFallbacks     uint32             `json:"placement_fallbacks"`
	HostBlobBytes          uint64             `json:"host_blob_bytes"`
	BlobDisposition        string             `json:"blob_disposition"`
	LeaseDispositions      []string           `json:"lease_dispositions"`
	NoAuthorityExpansion   bool               `json:"no_authority_expansion"`
	NoReplay               bool               `json:"no_replay"`
	FreshGuests            bool               `json:"fresh_guests"`
	Stages                 StageMetrics       `json:"stages"`
	IdentitySHA256         string             `json:"identity_sha256"`
}

type CellSummary struct {
	Platform                string `json:"platform"`
	Profile                 string `json:"profile"`
	CaseID                  string `json:"case_id"`
	Treatment               string `json:"treatment"`
	Trials                  uint32 `json:"trials"`
	MedianCriticalWallNanos uint64 `json:"median_critical_wall_nanos"`
	MADCriticalWallNanos    uint64 `json:"mad_critical_wall_nanos"`
	MedianPeakRSSBytes      uint64 `json:"median_peak_resident_memory_bytes"`
}

type EconomicSummary struct {
	Platform                 string   `json:"platform"`
	Profile                  string   `json:"profile"`
	CaseID                   string   `json:"case_id"`
	Consumers                uint32   `json:"consumers"`
	LeadGapMillis            uint32   `json:"lead_gap_millis"`
	PayloadBytes             uint64   `json:"payload_bytes"`
	OriginalMedianNanos      uint64   `json:"original_median_nanos"`
	SharedMedianNanos        uint64   `json:"shared_median_nanos"`
	NetSavedNanos            int64    `json:"net_saved_nanos"`
	SpeedupRatio             float64  `json:"speedup_ratio"`
	BytesPerSavedComputeNano *float64 `json:"bytes_copied_per_saved_compute_nano"`
	ObservedBreakEven        bool     `json:"observed_break_even"`
}

type AdversarialControl struct {
	ID             string `json:"id"`
	Passed         bool   `json:"passed"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type CampaignReport struct {
	SchemaVersion                     string               `json:"schema_version"`
	PreregistrationIdentity           string               `json:"preregistration_identity"`
	CaseMatrixIdentity                string               `json:"case_matrix_identity"`
	Records                           []TrialRecord        `json:"records"`
	Cells                             []CellSummary        `json:"cells"`
	Economics                         []EconomicSummary    `json:"economics"`
	AdversarialControls               []AdversarialControl `json:"adversarial_controls"`
	RequireUniversalPositiveEconomics bool                 `json:"require_universal_positive_economics"`
	Interpretation                    string               `json:"interpretation"`
	IdentitySHA256                    string               `json:"identity_sha256"`
}

func CaseByID(id string) (Case, bool) {
	for _, candidate := range Cases() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Case{}, false
}

func SealTrialRecord(record TrialRecord) (TrialRecord, error) {
	if record.IdentitySHA256 != "" || !validTrialRecord(record, false) {
		return TrialRecord{}, ErrCampaignRecord
	}
	record.IdentitySHA256 = campaignDigest(record)
	return record, nil
}

func EncodeTrialRecord(record TrialRecord) ([]byte, error) {
	if !validTrialRecord(record, true) {
		return nil, ErrCampaignRecord
	}
	return json.Marshal(record)
}

func DecodeTrialRecord(raw []byte) (TrialRecord, error) {
	var record TrialRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validTrialRecord(record, true) {
		return TrialRecord{}, ErrCampaignRecord
	}
	canonical, _ := json.Marshal(record)
	if !bytes.Equal(raw, canonical) {
		return TrialRecord{}, ErrCampaignRecord
	}
	return record, nil
}

func DecodeTrialJSONL(raw []byte) ([]TrialRecord, error) {
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return []TrialRecord{}, nil
	}
	seen := map[string]bool{}
	records := make([]TrialRecord, 0, len(lines))
	for _, line := range lines {
		record, err := DecodeTrialRecord(line)
		if err != nil || seen[record.IdentitySHA256] {
			return nil, ErrCampaignRecord
		}
		seen[record.IdentitySHA256] = true
		records = append(records, record)
	}
	return records, nil
}

func validTrialRecord(record TrialRecord, sealed bool) bool {
	candidate, ok := CaseByID(record.Coordinate.CaseID)
	if !ok || !candidate.EconomicsEligible || record.SchemaVersion != TrialRecordSchemaVersion || record.CaseSourceSHA256 != candidate.SourceSHA256 ||
		record.Coordinate.Platform == "" || record.Coordinate.Profile == "" || !containsString(platforms, record.Coordinate.Platform) || !containsString(profiles, record.Coordinate.Profile) ||
		record.Coordinate.TrialIndex == 0 || record.Coordinate.TrialIndex > TrialsPerTreatment ||
		(record.Coordinate.Treatment != "original_recompute" && record.Coordinate.Treatment != "prepared_ndarray_reuse") ||
		record.ProcessExit != "success" || record.ProtocolStatus != "ok" || !record.ResultParity || !record.NoAuthorityExpansion || !record.NoReplay || !record.FreshGuests ||
		record.PlacementFallbacks != 0 || record.PreparedCOWGuests+record.OrdinaryFreshGuests != record.PhysicalGuests ||
		record.Stages.CriticalWallNanos == 0 || record.Stages.PeakResidentMemoryBytes == 0 {
		return false
	}
	for _, value := range []string{record.CaseSourceSHA256, record.ArtifactSHA256, record.ExecutionProfileSHA256, record.PassRegistrationSHA256, record.DeclarationSHA256, record.SourceSHA256, record.InputsSHA256, record.ResultSHA256} {
		if !campaignDigestPattern(value) {
			return false
		}
	}
	if record.Coordinate.Treatment == "original_recompute" {
		if record.PhysicalGuests != candidate.Consumers || record.RuntimeInitializations != candidate.Consumers || record.HostBlobBytes != 0 || record.BlobDisposition != "not_applicable" || len(record.LeaseDispositions) != 0 {
			return false
		}
	} else {
		if record.PhysicalGuests != 2+2*candidate.Consumers || record.RuntimeInitializations != 2+2*candidate.Consumers || record.HostBlobBytes != candidate.ExpectedNBytes || record.BlobDisposition != "consumed" || len(record.LeaseDispositions) != int(candidate.Consumers) {
			return false
		}
		for _, disposition := range record.LeaseDispositions {
			if disposition != "consumed" {
				return false
			}
		}
	}
	if sealed {
		if !campaignDigestPattern(record.IdentitySHA256) {
			return false
		}
		identity := record
		identity.IdentitySHA256 = ""
		return record.IdentitySHA256 == campaignDigest(identity)
	}
	return record.IdentitySHA256 == ""
}

func SealCampaignReport(records []TrialRecord, controls []AdversarialControl) (CampaignReport, error) {
	coordinates := CampaignCoordinates()
	if len(records) != len(coordinates) || len(controls) == 0 {
		return CampaignReport{}, ErrCampaignRecord
	}
	byCoordinate := map[CampaignCoordinate]TrialRecord{}
	for _, record := range records {
		if !validTrialRecord(record, true) || byCoordinate[record.Coordinate].IdentitySHA256 != "" {
			return CampaignReport{}, ErrCampaignRecord
		}
		byCoordinate[record.Coordinate] = record
	}
	ordered := make([]TrialRecord, 0, len(coordinates))
	for _, coordinate := range coordinates {
		record, ok := byCoordinate[coordinate]
		if !ok {
			return CampaignReport{}, ErrCampaignRecord
		}
		ordered = append(ordered, record)
	}
	for _, control := range controls {
		if control.ID == "" || !control.Passed || !campaignDigestPattern(control.EvidenceSHA256) {
			return CampaignReport{}, ErrCampaignRecord
		}
	}
	cells := summarizeCells(ordered)
	economics := summarizeEconomics(cells)
	report := CampaignReport{SchemaVersion: CampaignReportSchemaVersion, PreregistrationIdentity: PreregistrationIdentity, CaseMatrixIdentity: CaseMatrixIdentity, Records: ordered, Cells: cells, Economics: economics, AdversarialControls: append([]AdversarialControl(nil), controls...), RequireUniversalPositiveEconomics: false, Interpretation: "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure"}
	report.IdentitySHA256 = campaignDigest(report)
	return report, nil
}

func summarizeCells(records []TrialRecord) []CellSummary {
	type key struct{ platform, profile, caseID, treatment string }
	groups := map[key][]TrialRecord{}
	for _, record := range records {
		k := key{record.Coordinate.Platform, record.Coordinate.Profile, record.Coordinate.CaseID, record.Coordinate.Treatment}
		groups[k] = append(groups[k], record)
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].platform != keys[j].platform {
			return keys[i].platform < keys[j].platform
		}
		if keys[i].profile != keys[j].profile {
			return keys[i].profile < keys[j].profile
		}
		if keys[i].caseID != keys[j].caseID {
			return keys[i].caseID < keys[j].caseID
		}
		return keys[i].treatment < keys[j].treatment
	})
	out := make([]CellSummary, 0, len(keys))
	for _, k := range keys {
		walls, rss := []uint64{}, []uint64{}
		for _, r := range groups[k] {
			walls = append(walls, r.Stages.CriticalWallNanos)
			rss = append(rss, r.Stages.PeakResidentMemoryBytes)
		}
		median := medianU64(walls)
		deviations := make([]uint64, len(walls))
		for i, v := range walls {
			if v > median {
				deviations[i] = v - median
			} else {
				deviations[i] = median - v
			}
		}
		out = append(out, CellSummary{k.platform, k.profile, k.caseID, k.treatment, uint32(len(walls)), median, medianU64(deviations), medianU64(rss)})
	}
	return out
}

func summarizeEconomics(cells []CellSummary) []EconomicSummary {
	type key struct{ platform, profile, caseID string }
	pairs := map[key]map[string]CellSummary{}
	for _, cell := range cells {
		k := key{cell.Platform, cell.Profile, cell.CaseID}
		if pairs[k] == nil {
			pairs[k] = map[string]CellSummary{}
		}
		pairs[k][cell.Treatment] = cell
	}
	keys := make([]key, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].platform != keys[j].platform {
			return keys[i].platform < keys[j].platform
		}
		if keys[i].profile != keys[j].profile {
			return keys[i].profile < keys[j].profile
		}
		return keys[i].caseID < keys[j].caseID
	})
	out := make([]EconomicSummary, 0, len(keys))
	for _, k := range keys {
		original := pairs[k]["original_recompute"]
		shared := pairs[k]["prepared_ndarray_reuse"]
		candidate, _ := CaseByID(k.caseID)
		net := signedDifference(original.MedianCriticalWallNanos, shared.MedianCriticalWallNanos)
		ratio := float64(original.MedianCriticalWallNanos) / float64(shared.MedianCriticalWallNanos)
		var bytesPer *float64
		if net > 0 {
			value := float64(candidate.ExpectedNBytes) / float64(net)
			bytesPer = &value
		}
		out = append(out, EconomicSummary{k.platform, k.profile, k.caseID, candidate.Consumers, candidate.LeadGapMillis, candidate.ExpectedNBytes, original.MedianCriticalWallNanos, shared.MedianCriticalWallNanos, net, ratio, bytesPer, net >= 0})
	}
	return out
}

func medianU64(values []uint64) uint64 {
	copied := append([]uint64(nil), values...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	return copied[len(copied)/2]
}
func signedDifference(left, right uint64) int64 {
	if left >= right {
		d := left - right
		if d > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(d)
	}
	d := right - left
	if d > math.MaxInt64 {
		return math.MinInt64
	}
	return -int64(d)
}
func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func campaignDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func campaignDigestPattern(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}
