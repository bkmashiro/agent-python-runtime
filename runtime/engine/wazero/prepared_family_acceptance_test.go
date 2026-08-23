package wazero

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

func TestPreparedFamilyAcceptanceFixtureDigests(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/examples/prepared-family-acceptance-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion string `json:"schema_version"`
		Arrays        []struct {
			ID         string   `json:"id"`
			DType      string   `json:"dtype"`
			BodySHA256 string   `json:"body_sha256"`
			Shape      []uint64 `json:"shape"`
			NBytes     uint64   `json:"nbytes"`
		} `json:"arrays"`
		Fanout []uint32 `json:"fanout"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.SchemaVersion != "pysolate.prepared-family-acceptance-fixture.v1" || len(fixture.Arrays) != 3 || len(fixture.Fanout) != 4 {
		t.Fatalf("fixture=%+v", fixture)
	}
	bodies := map[string][]byte{
		"i8-vector": make([]byte, 32),
		"f4-matrix": make([]byte, 16),
		"u1-tensor": {0, 1, 2, 3, 4, 5, 6, 7},
	}
	for index, value := range []uint64{1, 2, 3, 4} {
		binary.LittleEndian.PutUint64(bodies["i8-vector"][index*8:], value)
	}
	for index, value := range []float32{0.25, 1.25, 2.25, 3.25} {
		binary.LittleEndian.PutUint32(bodies["f4-matrix"][index*4:], math.Float32bits(value))
	}
	for _, array := range fixture.Arrays {
		body, exists := bodies[array.ID]
		digest := sha256.Sum256(body)
		if !exists || uint64(len(body)) != array.NBytes || "sha256:"+hex.EncodeToString(digest[:]) != array.BodySHA256 {
			t.Fatalf("array=%+v", array)
		}
	}
	for index, expected := range []uint32{0, 1, 2, 4} {
		if fixture.Fanout[index] != expected {
			t.Fatalf("fanout=%v", fixture.Fanout)
		}
	}
}

func TestPreparedFamilyAcceptanceReportIsBodyFreeAndRejectsDrift(t *testing.T) {
	digest := func(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }
	record := PreparedMemberRecord{
		SchemaVersion: "pysolate.prepared-family-member.v1", FamilySHA256: digest('a'), InputSHA256: digest('b'), MemberID: 1,
		RunID: "run", InvocationID: "invocation", ExecutionID: "run", PlanSHA256: digest('c'), GrantsSHA256: digest('d'),
		PhysicalDisposition: PreparedDispositionPrivateCopy, Outcome: PreparedMemberOK, FinalWorkspaceSHA256: digest('e'),
	}
	state := PreparedFamilyState{Created: 1, Terminal: 1, FamilySHA256: digest('a'), InputSHA256: digest('b'), Disposition: PreparedDispositionPrivateCopy}
	encoded, err := EncodePreparedFamilyAcceptanceReport(digest('f'), digest('1'), digest('2'), state, []PreparedMemberRecord{record}, digest('e'))
	if err != nil || strings.Contains(string(encoded), "body") || strings.Contains(string(encoded), "response") {
		t.Fatalf("report=%s err=%v", encoded, err)
	}
	if _, err := EncodePreparedFamilyAcceptanceReport(digest('f'), digest('1'), digest('2'), state, []PreparedMemberRecord{record}, digest('9')); err == nil {
		t.Fatal("accepted unobserved selected root")
	}
	zero := PreparedFamilyState{FamilySHA256: digest('a'), InputSHA256: digest('b'), Disposition: PreparedDispositionPrivateCopy}
	if _, err := EncodePreparedFamilyAcceptanceReport(digest('f'), digest('1'), digest('2'), zero, nil, ""); err != nil {
		t.Fatalf("zero fanout: %v", err)
	}
}
