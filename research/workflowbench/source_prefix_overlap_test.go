package workflowbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func validSourcePrefixSchedule() SourcePrefixSchedule {
	return SourcePrefixSchedule{
		SchemaVersion: SourcePrefixScheduleSchema,
		CaseID:        "early-read-overlap-v1",
		Chunks: []TimedSourceChunk{
			{OffsetMS: 0, Source: "record = slow.lookup('alpha')\n"},
			{OffsetMS: 700, Source: "label = record['label'].upper()\n"},
			{OffsetMS: 1400, Source: "result = {'label': label}\n"},
		},
		MaxBufferedChunks: 32,
		MaxBufferedBytes:  64 * 1024,
	}
}

func testSourcePrefixSHA(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func validSourcePrefixContract(t *testing.T) SourcePrefixExperimentContract {
	t.Helper()
	schedule := validSourcePrefixSchedule()
	result, err := json.Marshal(map[string]any{"label": "ALPHA"})
	if err != nil {
		t.Fatal(err)
	}
	return SourcePrefixExperimentContract{
		SchemaVersion:        SourcePrefixExperimentContractSchema,
		ExperimentID:         "source-prefix-overlap-v1",
		Schedule:             schedule,
		Repetitions:          2,
		ToolDelayMS:          1500,
		ExpectedResultSHA256: testSourcePrefixSHA(string(result)),
		OracleSHA256:         testSourcePrefixSHA("independent-result-and-call-oracle-v1"),
		LaneConfigSHA256:     testSourcePrefixSHA("matched-fresh-streaming-lanes-v1"),
		ClaimBoundary:        SourcePrefixClaimBoundary,
	}
}

func validSourcePrefixEvidence(t *testing.T) SourcePrefixEvidence {
	t.Helper()
	contract := validSourcePrefixContract(t)
	contractSHA, err := contract.Identity()
	if err != nil {
		t.Fatal(err)
	}
	rows := []SourcePrefixRow{
		{Pair: 0, LaneOrder: 0, Treatment: SourcePrefixBaseline, WallNS: 3_010_000_000, GenerationCompleteNS: 1_400_000_000, ToolStartedNS: 1_450_000_000, ToolEndedNS: 2_950_000_000, RunEndedNS: 3_010_000_000, ResultSHA256: contract.ExpectedResultSHA256, OraclePassed: true, LogicalCalls: 1, PhysicalDispatches: 1, GuestStarts: 1, WorkspaceBeforeSHA256: testSourcePrefixSHA("workspace"), WorkspaceAfterSHA256: testSourcePrefixSHA("workspace"), WorkspaceDisposition: "published"},
		{Pair: 0, LaneOrder: 1, Treatment: SourcePrefixStreaming, WallNS: 1_620_000_000, GenerationCompleteNS: 1_400_000_000, ToolStartedNS: 50_000_000, ToolEndedNS: 1_550_000_000, RunEndedNS: 1_620_000_000, ResultSHA256: contract.ExpectedResultSHA256, OraclePassed: true, LogicalCalls: 1, PhysicalDispatches: 1, GuestStarts: 1, WorkspaceBeforeSHA256: testSourcePrefixSHA("workspace"), WorkspaceAfterSHA256: testSourcePrefixSHA("workspace"), WorkspaceDisposition: "published"},
		{Pair: 1, LaneOrder: 0, Treatment: SourcePrefixStreaming, WallNS: 1_640_000_000, GenerationCompleteNS: 1_400_000_000, ToolStartedNS: 60_000_000, ToolEndedNS: 1_560_000_000, RunEndedNS: 1_640_000_000, ResultSHA256: contract.ExpectedResultSHA256, OraclePassed: true, LogicalCalls: 1, PhysicalDispatches: 1, GuestStarts: 1, WorkspaceBeforeSHA256: testSourcePrefixSHA("workspace"), WorkspaceAfterSHA256: testSourcePrefixSHA("workspace"), WorkspaceDisposition: "published"},
		{Pair: 1, LaneOrder: 1, Treatment: SourcePrefixBaseline, WallNS: 3_030_000_000, GenerationCompleteNS: 1_400_000_000, ToolStartedNS: 1_470_000_000, ToolEndedNS: 2_970_000_000, RunEndedNS: 3_030_000_000, ResultSHA256: contract.ExpectedResultSHA256, OraclePassed: true, LogicalCalls: 1, PhysicalDispatches: 1, GuestStarts: 1, WorkspaceBeforeSHA256: testSourcePrefixSHA("workspace"), WorkspaceAfterSHA256: testSourcePrefixSHA("workspace"), WorkspaceDisposition: "published"},
	}
	return SourcePrefixEvidence{
		SchemaVersion:          SourcePrefixEvidenceSchema,
		ExperimentSHA256:       contractSHA,
		ArtifactSHA256:         testSourcePrefixSHA("artifact"),
		ArtifactSourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		HarnessSourceCommit:    "89abcdef0123456789abcdef0123456789abcdef",
		ExecutionProfileSHA256: testSourcePrefixSHA("profile"),
		CapabilityPlanSHA256:   testSourcePrefixSHA("plan"),
		CapabilitySpecSHA256:   testSourcePrefixSHA("spec"),
		HandlerSHA256:          testSourcePrefixSHA("handler"),
		Rows:                   rows,
		BaselineMedianNS:       3_020_000_000,
		StreamingMedianNS:      1_630_000_000,
		MedianSpeedupMilli:     1852,
		SpeedupSupported:       true,
		ClaimBoundary:          SourcePrefixClaimBoundary,
	}
}

func TestExecuteSourcePrefixPairsAlternatesAndValidates(t *testing.T) {
	contract := validSourcePrefixContract(t)
	identities := SourcePrefixRuntimeIdentities{
		ArtifactSHA256: testSourcePrefixSHA("artifact"), ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		HarnessSourceCommit:    "89abcdef0123456789abcdef0123456789abcdef",
		ExecutionProfileSHA256: testSourcePrefixSHA("profile"), CapabilityPlanSHA256: testSourcePrefixSHA("plan"),
		CapabilitySpecSHA256: testSourcePrefixSHA("spec"), HandlerSHA256: testSourcePrefixSHA("handler"),
	}
	rows := validSourcePrefixEvidence(t).Rows
	var seen []SourcePrefixTreatment
	evidence, err := ExecuteSourcePrefixPairs(context.Background(), contract, identities, func(_ context.Context, pair, order uint32, treatment SourcePrefixTreatment) (SourcePrefixRow, error) {
		seen = append(seen, treatment)
		for _, row := range rows {
			if row.Pair == pair && row.LaneOrder == order {
				return row, nil
			}
		}
		return SourcePrefixRow{}, errors.New("missing fixture row")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []SourcePrefixTreatment{SourcePrefixBaseline, SourcePrefixStreaming, SourcePrefixStreaming, SourcePrefixBaseline}
	if !reflect.DeepEqual(seen, want) || evidence.BaselineMedianNS != 3_020_000_000 || evidence.StreamingMedianNS != 1_630_000_000 || !evidence.SpeedupSupported {
		t.Fatalf("seen=%v evidence=%+v", seen, evidence)
	}
}

func TestExecuteSourcePrefixPairsFailsClosed(t *testing.T) {
	contract := validSourcePrefixContract(t)
	identities := SourcePrefixRuntimeIdentities{}
	if _, err := ExecuteSourcePrefixPairs(context.Background(), contract, identities, func(context.Context, uint32, uint32, SourcePrefixTreatment) (SourcePrefixRow, error) {
		return SourcePrefixRow{}, nil
	}); err == nil {
		t.Fatal("invalid runtime identities accepted")
	}
	identities = SourcePrefixRuntimeIdentities{
		ArtifactSHA256: testSourcePrefixSHA("artifact"), ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		HarnessSourceCommit:    "89abcdef0123456789abcdef0123456789abcdef",
		ExecutionProfileSHA256: testSourcePrefixSHA("profile"), CapabilityPlanSHA256: testSourcePrefixSHA("plan"), CapabilitySpecSHA256: testSourcePrefixSHA("spec"), HandlerSHA256: testSourcePrefixSHA("handler"),
	}
	if _, err := ExecuteSourcePrefixPairs(context.Background(), contract, identities, func(context.Context, uint32, uint32, SourcePrefixTreatment) (SourcePrefixRow, error) {
		return SourcePrefixRow{}, errors.New("lane failed")
	}); err == nil {
		t.Fatal("lane failure was swallowed")
	}
	if _, err := ExecuteSourcePrefixPairs(context.Background(), contract, identities, func(_ context.Context, pair, order uint32, treatment SourcePrefixTreatment) (SourcePrefixRow, error) {
		return SourcePrefixRow{Pair: pair, LaneOrder: order, Treatment: treatment}, nil
	}); err == nil {
		t.Fatal("zero-duration lane was accepted")
	}
}

func TestDecodeSourcePrefixContractRejectsUnknownAndDuplicateFields(t *testing.T) {
	contract := validSourcePrefixContract(t)
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSourcePrefixExperimentContract(encoded)
	if err != nil || !reflect.DeepEqual(decoded, contract) {
		t.Fatalf("decode err=%v contract=%+v", err, decoded)
	}
	unknown := append(encoded[:len(encoded)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeSourcePrefixExperimentContract(unknown); err == nil {
		t.Fatal("unknown contract field accepted")
	}
	duplicate := append([]byte(`{"schema_version":"duplicate",`), encoded[1:]...)
	if _, err := DecodeSourcePrefixExperimentContract(duplicate); err == nil {
		t.Fatal("duplicate contract field accepted")
	}
}

func TestSourcePrefixExperimentContractBindsScheduleAndOracle(t *testing.T) {
	contract := validSourcePrefixContract(t)
	first, err := contract.Identity()
	if err != nil {
		t.Fatal(err)
	}
	mutated := contract
	mutated.ToolDelayMS++
	second, err := mutated.Identity()
	if err != nil || first == second {
		t.Fatalf("contract identity did not bind tool delay: %q %q err=%v", first, second, err)
	}
	mutated = contract
	mutated.ClaimBoundary = "natural_speedup"
	if mutated.Validate() == nil {
		t.Fatal("unsupported claim boundary accepted")
	}
}

func TestSourcePrefixEvidenceValidatesMatchedPairs(t *testing.T) {
	contract := validSourcePrefixContract(t)
	evidence := validSourcePrefixEvidence(t)
	if err := ValidateSourcePrefixEvidence(contract, evidence); err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSourcePrefixEvidence(contract, evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSourcePrefixEvidence(encoded, contract)
	if err != nil || !reflect.DeepEqual(decoded, evidence) {
		t.Fatalf("decode err=%v evidence=%+v", err, decoded)
	}
}

func TestSourcePrefixEvidenceRejectsDriftAndFalseSpeedup(t *testing.T) {
	contract := validSourcePrefixContract(t)
	for name, mutate := range map[string]func(*SourcePrefixEvidence){
		"result drift":              func(value *SourcePrefixEvidence) { value.Rows[0].ResultSHA256 = testSourcePrefixSHA("wrong") },
		"physical call drift":       func(value *SourcePrefixEvidence) { value.Rows[1].PhysicalDispatches = 2 },
		"oracle failure":            func(value *SourcePrefixEvidence) { value.Rows[2].OraclePassed = false },
		"fallback":                  func(value *SourcePrefixEvidence) { value.Rows[0].Fallback = true },
		"baseline dispatched early": func(value *SourcePrefixEvidence) { value.Rows[0].ToolStartedNS = 100 },
		"stream failed to overlap":  func(value *SourcePrefixEvidence) { value.Rows[1].ToolStartedNS = value.Rows[1].GenerationCompleteNS },
		"tool delay shorter than preregistered": func(value *SourcePrefixEvidence) {
			value.Rows[0].ToolEndedNS = value.Rows[0].ToolStartedNS + int64(time.Millisecond)
		},
		"workspace changed":          func(value *SourcePrefixEvidence) { value.Rows[0].WorkspaceAfterSHA256 = testSourcePrefixSHA("changed") },
		"workspace identity missing": func(value *SourcePrefixEvidence) { value.Rows[0].WorkspaceBeforeSHA256 = "" },
		"summary drift":              func(value *SourcePrefixEvidence) { value.BaselineMedianNS++ },
		"identity drift":             func(value *SourcePrefixEvidence) { value.CapabilityPlanSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			evidence := validSourcePrefixEvidence(t)
			mutate(&evidence)
			if ValidateSourcePrefixEvidence(contract, evidence) == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestDecodeSourcePrefixEvidenceRejectsUnknownAndDuplicateFields(t *testing.T) {
	contract := validSourcePrefixContract(t)
	evidence := validSourcePrefixEvidence(t)
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(encoded[:len(encoded)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeSourcePrefixEvidence(unknown, contract); err == nil {
		t.Fatal("unknown field accepted")
	}
	duplicate := append([]byte(`{"schema_version":"duplicate",`), encoded[1:]...)
	if _, err := DecodeSourcePrefixEvidence(duplicate, contract); err == nil {
		t.Fatal("duplicate field accepted")
	}
}

func TestSourcePrefixScheduleHasStableIdentityAndBounds(t *testing.T) {
	schedule := validSourcePrefixSchedule()
	if err := schedule.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := schedule.Identity()
	if err != nil || first == "" {
		t.Fatalf("identity=%q err=%v", first, err)
	}
	second, _ := schedule.Identity()
	if first != second {
		t.Fatalf("unstable identity: %q != %q", first, second)
	}

	tooMany := schedule
	tooMany.Chunks = make([]TimedSourceChunk, 33)
	for index := range tooMany.Chunks {
		tooMany.Chunks[index] = TimedSourceChunk{OffsetMS: uint32(index), Source: "x = 1\n"}
	}
	if tooMany.Validate() == nil {
		t.Fatal("chunk overflow accepted")
	}
	tooLarge := schedule
	tooLarge.Chunks = []TimedSourceChunk{{Source: string(make([]byte, 64*1024+1))}}
	if tooLarge.Validate() == nil {
		t.Fatal("byte overflow accepted")
	}
	outOfOrder := schedule
	outOfOrder.Chunks[1].OffsetMS = 0
	if outOfOrder.Validate() == nil {
		t.Fatal("non-increasing release offsets accepted")
	}
}

func TestSourcePrefixTreatmentOrderAlternates(t *testing.T) {
	if got := SourcePrefixTreatmentOrder(0); !reflect.DeepEqual(got, []SourcePrefixTreatment{SourcePrefixBaseline, SourcePrefixStreaming}) {
		t.Fatalf("even order=%v", got)
	}
	if got := SourcePrefixTreatmentOrder(1); !reflect.DeepEqual(got, []SourcePrefixTreatment{SourcePrefixStreaming, SourcePrefixBaseline}) {
		t.Fatalf("odd order=%v", got)
	}
}

func TestProduceTimedSourceUsesMatchedSchedule(t *testing.T) {
	for _, test := range []struct {
		name      string
		treatment SourcePrefixTreatment
		waits     []time.Duration
	}{
		{name: "baseline waits until generation completes", treatment: SourcePrefixBaseline, waits: []time.Duration{1400 * time.Millisecond}},
		{name: "streaming releases each chunk", treatment: SourcePrefixStreaming, waits: []time.Duration{0, 700 * time.Millisecond, 1400 * time.Millisecond}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var waits []time.Duration
			wait := func(_ context.Context, offset time.Duration) error {
				waits = append(waits, offset)
				return nil
			}
			events, failures, err := ProduceTimedSource(context.Background(), validSourcePrefixSchedule(), test.treatment, wait)
			if err != nil {
				t.Fatal(err)
			}
			var sources []string
			for event := range events {
				sources = append(sources, event.Source)
			}
			if failure := <-failures; failure != nil {
				t.Fatal(failure)
			}
			if !reflect.DeepEqual(waits, test.waits) {
				t.Fatalf("waits=%v want=%v", waits, test.waits)
			}
			want := validSourcePrefixSchedule().Chunks
			for index := range want {
				if sources[index] != want[index].Source {
					t.Fatalf("source[%d]=%q", index, sources[index])
				}
			}
		})
	}
}

func TestProduceTimedSourcePropagatesCancellation(t *testing.T) {
	cancelled := errors.New("cancelled by fixture")
	wait := func(context.Context, time.Duration) error { return cancelled }
	events, failures, err := ProduceTimedSource(context.Background(), validSourcePrefixSchedule(), SourcePrefixStreaming, wait)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := <-events; ok {
		t.Fatal("cancelled producer emitted a chunk")
	}
	if failure := <-failures; !errors.Is(failure, cancelled) {
		t.Fatalf("failure=%v", failure)
	}
}
