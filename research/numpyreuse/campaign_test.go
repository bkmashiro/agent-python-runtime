package numpyreuse

import (
	"bytes"
	"encoding/json"
	"testing"
)

func syntheticRecord(coordinate CampaignCoordinate, nanos uint64) TrialRecord {
	candidate, _ := CaseByID(coordinate.CaseID)
	record := TrialRecord{
		SchemaVersion:          TrialRecordSchemaVersion,
		Coordinate:             coordinate,
		CaseSourceSHA256:       candidate.SourceSHA256,
		ArtifactSHA256:         "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExecutionProfileSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PassRegistrationSHA256: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		DeclarationSHA256:      "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		SourceSHA256:           "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		InputsSHA256:           "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		ProcessExit:            "success", ProtocolStatus: "ok", ResultSHA256: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ResultParity: true, PhysicalGuests: candidate.Consumers, RuntimeInitializations: candidate.Consumers,
		BlobDisposition: "not_applicable", LeaseDispositions: []string{},
		NoAuthorityExpansion: true, NoReplay: true, FreshGuests: true,
		Stages: StageMetrics{CriticalWallNanos: nanos, PeakResidentMemoryBytes: 1000},
	}
	if coordinate.Treatment == "prepared_ndarray_reuse" {
		record.PhysicalGuests++
		record.RuntimeInitializations++
		record.BlobDisposition = "consumed"
		record.LeaseDispositions = make([]string, candidate.Consumers)
		for index := range record.LeaseDispositions {
			record.LeaseDispositions[index] = "consumed"
		}
		record.HostBlobBytes = candidate.ExpectedNBytes
	}
	sealed, err := SealTrialRecord(record)
	if err != nil {
		panic(coordinate.CaseID + "/" + coordinate.Treatment + ": " + err.Error())
	}
	return sealed
}

func TestTrialRecordsAreCanonicalBodyFreeAndIdentityBound(t *testing.T) {
	coordinate := CampaignCoordinates()[0]
	record := syntheticRecord(coordinate, 100)
	raw, err := EncodeTrialRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTrialRecord(raw)
	if err != nil || decoded.IdentitySHA256 != record.IdentitySHA256 {
		t.Fatalf("decode=%+v err=%v", decoded, err)
	}
	for _, forbidden := range [][]byte{[]byte("body_base64"), []byte("ndarray_body"), []byte("result_payload"), []byte("traceback")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("body-unsafe field %q", forbidden)
		}
	}
	mutated := record
	mutated.Coordinate.TrialIndex++
	if _, err := SealTrialRecord(mutated); err == nil {
		t.Fatal("identity-bearing sealed record was resealed after mutation")
	}
}

func TestCampaignAggregationRequiresExactFrozenScheduleAndReportsMixedEconomics(t *testing.T) {
	coordinates := CampaignCoordinates()
	records := make([]TrialRecord, 0, len(coordinates))
	for _, coordinate := range coordinates {
		nanos := uint64(100)
		if coordinate.Treatment == "prepared_ndarray_reuse" {
			if len(records)%4 == 0 {
				nanos = 80
			} else {
				nanos = 120
			}
		}
		records = append(records, syntheticRecord(coordinate, nanos))
	}
	report, err := SealCampaignReport(records, []AdversarialControl{{ID: "all", Passed: true, EvidenceSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Records) != 240 || len(report.Cells) != 80 || len(report.Economics) != 40 || report.RequireUniversalPositiveEconomics {
		t.Fatalf("report counts records=%d cells=%d economics=%d", len(report.Records), len(report.Cells), len(report.Economics))
	}
	positive, nonPositive := 0, 0
	for _, summary := range report.Economics {
		if summary.NetSavedNanos > 0 {
			positive++
		} else {
			nonPositive++
		}
	}
	if positive == 0 || nonPositive == 0 || report.Interpretation != "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure" {
		t.Fatalf("economics positive=%d nonpositive=%d interpretation=%q", positive, nonPositive, report.Interpretation)
	}
	duplicate := append(append([]TrialRecord(nil), records...), records[0])
	if _, err := SealCampaignReport(duplicate, nil); err == nil {
		t.Fatal("duplicate schedule admitted")
	}
	missing := records[:len(records)-1]
	if _, err := SealCampaignReport(missing, nil); err == nil {
		t.Fatal("incomplete schedule admitted")
	}
}

func TestJSONLResumeRejectsUnknownDuplicateAndDrift(t *testing.T) {
	coordinate := CampaignCoordinates()[0]
	record := syntheticRecord(coordinate, 100)
	raw, _ := EncodeTrialRecord(record)
	joined := append(append([]byte(nil), raw...), '\n')
	decoded, err := DecodeTrialJSONL(joined)
	if err != nil || len(decoded) != 1 {
		t.Fatalf("decoded=%d err=%v", len(decoded), err)
	}
	if _, err := DecodeTrialJSONL(append(joined, joined...)); err == nil {
		t.Fatal("duplicate accepted")
	}
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	generic["unknown"] = true
	unknown, _ := json.Marshal(generic)
	if _, err := DecodeTrialJSONL(append(unknown, '\n')); err == nil {
		t.Fatal("unknown field accepted")
	}
}
