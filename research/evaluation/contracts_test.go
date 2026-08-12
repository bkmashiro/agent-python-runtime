package evaluation_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestCanonicalCorpusRoundTripAndIdentity(t *testing.T) {
	corpus := validCorpus()
	encoded, identity, err := evaluation.EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := evaluation.DecodeCorpus(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decodedIdentity != identity || decoded.SchemaVersion != evaluation.CorpusSchemaVersion || len(decoded.Workloads) != 3 {
		t.Fatalf("decoded=%+v identity=%s/%s", decoded, identity, decodedIdentity)
	}
	reencoded, _, err := evaluation.EncodeCorpus(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("non-canonical round trip err=%v\n%s\n%s", err, encoded, reencoded)
	}
}

func TestCorpusRejectsUnknownTrailingDuplicateMissingOracleAndTreatmentMismatch(t *testing.T) {
	encoded, _, err := evaluation.EncodeCorpus(validCorpus())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"unknown":  bytes.Replace(encoded, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1),
		"trailing": append(append([]byte(nil), encoded...), []byte(` {}`)...),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := evaluation.DecodeCorpus(raw); !errors.Is(err, evaluation.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}

	corpus := validCorpus()
	corpus.Workloads[1].ID = corpus.Workloads[0].ID
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("duplicate id err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[0].Oracle = evaluation.Oracle{}
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("missing oracle err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[1].Treatments = append(corpus.Workloads[1].Treatments, evaluation.TreatmentDeterministicVerify)
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("mounted deterministic mismatch err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[1].RequiredCapabilities = nil
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("null required capabilities err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[0].RequiredCapabilities[0].EffectClass = evaluation.EffectClass("external_write")
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("branch write capability err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[0].RequiredCapabilities[0].Playback = evaluation.PlaybackPolicy("forbidden")
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("branch uncaptured capability err=%v", err)
	}
	corpus = validCorpus()
	corpus.Workloads[1].WorkspaceSeedSHA256 = ""
	corpus.Workloads[1].Treatments = append(corpus.Workloads[1].Treatments, evaluation.TreatmentDeterministicVerify)
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("stateful seed omission err=%v", err)
	}
}

func TestPlanRequiresExactIdentitiesCeilingsAndProhibitedClaims(t *testing.T) {
	plan := validPlan()
	encoded, identity, err := evaluation.EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := evaluation.DecodePlan(encoded)
	if err != nil || decodedIdentity != identity || decoded.Repetitions != 2 {
		t.Fatalf("decoded=%+v identity=%s/%s err=%v", decoded, identity, decodedIdentity, err)
	}
	for name, mutate := range map[string]func(*evaluation.Plan){
		"identity drift":   func(value *evaluation.Plan) { value.CorpusSHA256 = "sha256:deadbeef" },
		"zero repetitions": func(value *evaluation.Plan) { value.Repetitions = 0 },
		"missing ceiling":  func(value *evaluation.Plan) { value.Ceilings.MaxRows = 0 },
		"weakened claims":  func(value *evaluation.Plan) { value.ProhibitedClaims = value.ProhibitedClaims[1:] },
		"duplicate treatment": func(value *evaluation.Plan) {
			value.TreatmentOrder = append(value.TreatmentOrder, value.TreatmentOrder[0])
		},
		"excess rows": func(value *evaluation.Plan) { value.Ceilings.MaxRows = 100_001 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validPlan()
			mutate(&candidate)
			if _, _, err := evaluation.EncodePlan(candidate); !errors.Is(err, evaluation.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestContractCollectionAndDocumentBounds(t *testing.T) {
	corpus := validCorpus()
	for len(corpus.Workloads) <= 256 {
		workload := corpus.Workloads[0]
		workload.ID = fmt.Sprintf("extra-%03d", len(corpus.Workloads))
		corpus.Workloads = append(corpus.Workloads, workload)
	}
	if _, _, err := evaluation.EncodeCorpus(corpus); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("oversized corpus err=%v", err)
	}
	report := validReport()
	report.Rows[0].EvidenceRefs = make([]string, 129)
	for index := range report.Rows[0].EvidenceRefs {
		report.Rows[0].EvidenceRefs[index] = digestA
	}
	if _, _, err := evaluation.EncodeReport(report); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("oversized evidence refs err=%v", err)
	}
	oversized := bytes.Repeat([]byte{' '}, (16<<20)+1)
	if _, _, err := evaluation.DecodeCorpus(oversized); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("oversized document err=%v", err)
	}
}

func TestReportConservationRowIdentityAndEvidenceRules(t *testing.T) {
	report := validReport()
	encoded, identity, err := evaluation.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := evaluation.DecodeReport(encoded)
	if err != nil || decodedIdentity != identity || decoded.Summary.Offered != 4 {
		t.Fatalf("decoded=%+v identity=%s/%s err=%v", decoded, identity, decodedIdentity, err)
	}
	for name, mutate := range map[string]func(*evaluation.Report){
		"bad conservation":          func(value *evaluation.Report) { value.Summary.Completed++ },
		"duplicate row":             func(value *evaluation.Report) { value.Rows[1].RowID = value.Rows[0].RowID },
		"derived row id drift":      func(value *evaluation.Report) { value.Rows[0].RowID = "row-arbitrary" },
		"row identity drift":        func(value *evaluation.Report) { value.Rows[0].CorpusSHA256 = digestA },
		"complete missing refs":     func(value *evaluation.Report) { value.Rows[0].EvidenceRefs = nil },
		"unsupported passed oracle": func(value *evaluation.Report) { value.Rows[3].OracleStatus = evaluation.OraclePassed },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validReport()
			mutate(&candidate)
			if _, _, err := evaluation.EncodeReport(candidate); !errors.Is(err, evaluation.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestFailedOracleMayStillHaveCompleteEvidence(t *testing.T) {
	report := validReport()
	report.Rows[2].EvidenceComplete = true
	report.Rows[2].EvidenceRefs = []string{digestA}
	if _, _, err := evaluation.EncodeReport(report); err != nil {
		t.Fatalf("complete evidence for failed oracle rejected: %v", err)
	}
}

func TestCanonicalFixtureSetIsDeterministic(t *testing.T) {
	fixtures, err := evaluation.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 3 {
		t.Fatalf("fixtures=%d", len(fixtures))
	}
	for name, fixture := range fixtures {
		if len(fixture.Bytes) == 0 || fixture.SHA256 == "" {
			t.Fatalf("fixture %s incomplete", name)
		}
		digest := sha256.Sum256(fixture.Bytes)
		if fixture.SHA256 != fmt.Sprintf("sha256:%x", digest) {
			t.Fatalf("fixture %s identity mismatch", name)
		}
	}
	second, err := evaluation.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	for name := range fixtures {
		if fixtures[name].SHA256 != second[name].SHA256 || !bytes.Equal(fixtures[name].Bytes, second[name].Bytes) {
			t.Fatalf("fixture %s drifted", name)
		}
	}
}

func TestCheckedInCanonicalFixturesMatchProducer(t *testing.T) {
	fixtures, err := evaluation.CanonicalFixtures()
	if err != nil {
		t.Fatal(err)
	}
	for name, fixture := range fixtures {
		actual, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, fixture.Bytes) {
			t.Fatalf("checked-in fixture %s drifted", name)
		}
	}
}

func TestCheckedInAdversarialFixturesAreDeterministicAndRejected(t *testing.T) {
	fixtures, err := evaluation.AdversarialFixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 8 {
		t.Fatalf("fixtures=%d", len(fixtures))
	}
	for name, fixture := range fixtures {
		actual, err := os.ReadFile(filepath.Join("testdata", "invalid", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, fixture.Bytes) {
			t.Fatalf("checked-in adversarial fixture %s drifted", name)
		}
		digest := sha256.Sum256(fixture.Bytes)
		if fixture.SHA256 != fmt.Sprintf("sha256:%x", digest) {
			t.Fatalf("adversarial fixture %s identity mismatch", name)
		}
		var decodeErr error
		switch fixture.Contract {
		case evaluation.ContractCorpus:
			_, _, decodeErr = evaluation.DecodeCorpus(fixture.Bytes)
		case evaluation.ContractReport:
			_, _, decodeErr = evaluation.DecodeReport(fixture.Bytes)
		default:
			t.Fatalf("fixture %s contract=%s", name, fixture.Contract)
		}
		if !errors.Is(decodeErr, evaluation.ErrInvalid) {
			t.Fatalf("fixture %s accepted err=%v", name, decodeErr)
		}
	}
}

func validCorpus() evaluation.Corpus {
	return evaluation.Corpus{SchemaVersion: evaluation.CorpusSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, Workloads: []evaluation.Workload{
		{ID: "structured-source-v1", Version: 1, Family: evaluation.FamilyStructuredSource, CodeSHA256: digestA, InputSHA256: digestB, RequiredCapabilities: []evaluation.CapabilityRequirement{{Name: "sources.benchmark_manifest", EffectClass: evaluation.EffectExternalRead, Playback: evaluation.PlaybackCaptured}, {Name: "sources.demo_catalog", EffectClass: evaluation.EffectExternalRead, Playback: evaluation.PlaybackCaptured}}, Treatments: []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch}, Oracle: evaluation.Oracle{Kind: evaluation.OracleResultAndWorkspace, ExpectedResultSHA256: digestA, ExpectedWorkspaceSHA256: digestB, ExpectedCapabilityCalls: 2}},
		{ID: "stateful-local-v1", Version: 1, Family: evaluation.FamilyStatefulLocal, CodeSHA256: digestB, InputSHA256: digestC, WorkspaceSeedSHA256: digestA, RequiredCapabilities: []evaluation.CapabilityRequirement{}, Treatments: []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay}, Oracle: evaluation.Oracle{Kind: evaluation.OracleResultAndWorkspace, ExpectedResultSHA256: digestB, ExpectedWorkspaceSHA256: digestC, ExpectedCapabilityCalls: 0}},
		{ID: "bounded-planning-v1", Version: 1, Family: evaluation.FamilyBoundedPlanning, CodeSHA256: digestC, InputSHA256: digestA, RequiredCapabilities: []evaluation.CapabilityRequirement{{Name: "sources.demo_catalog", EffectClass: evaluation.EffectExternalRead, Playback: evaluation.PlaybackCaptured}}, Treatments: []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch, evaluation.TreatmentDeterministicVerify}, Oracle: evaluation.Oracle{Kind: evaluation.OracleResultOnly, ExpectedResultSHA256: digestC, ExpectedCapabilityCalls: 1}},
	}}
}

func validPlan() evaluation.Plan {
	return evaluation.Plan{SchemaVersion: evaluation.PlanSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, HostCommit: "0123456789abcdef0123456789abcdef01234567", GuestArtifactSHA256: digestA, GuestManifestSHA256: digestB, CorpusSHA256: digestC, RuntimeProfileSHA256: digestA, TreatmentOrder: []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch, evaluation.TreatmentDeterministicVerify}, Repetitions: 2, Ceilings: evaluation.Ceilings{MaxRows: 64, MaxWallMillisPerRow: 120000, MaxEvidenceBytesPerRow: 8 << 20}, ProhibitedClaims: evaluation.RequiredProhibitedClaims()}
}

func validReport() evaluation.Report {
	rows := []evaluation.Row{
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Repetition: 0, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: digestC, PlanSHA256: digestB, EvidenceRefs: []string{digestA}},
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentOfflineReplay, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentOfflineReplay, Repetition: 0, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: digestC, PlanSHA256: digestB, EvidenceRefs: []string{digestB}},
		{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentCounterfactualBranch, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentCounterfactualBranch, Repetition: 0, Status: evaluation.RowFailed, OracleStatus: evaluation.OracleFailed, EvidenceComplete: false, CorpusSHA256: digestC, PlanSHA256: digestB, EvidenceRefs: []string{}, ProblemCode: "oracle_mismatch"},
		{RowID: evaluation.RowIdentity("stateful-local-v1", evaluation.TreatmentDeterministicVerify, 0), WorkloadID: "stateful-local-v1", Treatment: evaluation.TreatmentDeterministicVerify, Repetition: 0, Status: evaluation.RowUnsupported, OracleStatus: evaluation.OracleNotRun, EvidenceComplete: false, CorpusSHA256: digestC, PlanSHA256: digestB, EvidenceRefs: []string{}, ProblemCode: "mounted_workspace_unsupported"},
	}
	return evaluation.Report{SchemaVersion: evaluation.ReportSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, CorpusSHA256: digestC, PlanSHA256: digestB, ProhibitedClaims: evaluation.RequiredProhibitedClaims(), Summary: evaluation.Summary{Offered: 4, Completed: 2, Failed: 1, TimedOut: 0, Unsupported: 1}, Rows: rows}
}
