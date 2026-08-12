package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxContractBytes = 16 << 20

func encodeCanonical[T any](value T, validate func(T) error) ([]byte, string, error) {
	if err := validate(value); err != nil {
		return nil, "", ErrInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	return encoded, fmt.Sprintf("sha256:%x", digest), nil
}

func decodeStrict[T any](data []byte, validate func(T) error) (T, string, error) {
	var zero T
	if len(data) == 0 || len(data) > maxContractBytes {
		return zero, "", ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, "", ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, "", ErrInvalid
	}
	if err := validate(value); err != nil {
		return zero, "", ErrInvalid
	}
	canonical, identity, err := encodeCanonical(value, validate)
	if err != nil || !bytes.Equal(data, canonical) {
		return zero, "", ErrInvalid
	}
	return value, identity, nil
}

func EncodeCorpus(value Corpus) ([]byte, string, error) {
	return encodeCanonical(value, validateCorpus)
}
func DecodeCorpus(data []byte) (Corpus, string, error) { return decodeStrict(data, validateCorpus) }
func EncodePlan(value Plan) ([]byte, string, error)    { return encodeCanonical(value, validatePlan) }
func DecodePlan(data []byte) (Plan, string, error)     { return decodeStrict(data, validatePlan) }
func EncodeReport(value Report) ([]byte, string, error) {
	return encodeCanonical(value, validateReport)
}
func DecodeReport(data []byte) (Report, string, error) { return decodeStrict(data, validateReport) }

func CanonicalFixtures() (map[string]Fixture, error) {
	corpus := Corpus{SchemaVersion: CorpusSchemaVersion, EvidenceClass: EvidenceMechanismOnly, Workloads: []Workload{
		{ID: "structured-source-v1", Version: 1, Family: FamilyStructuredSource, CodeSHA256: fixtureDigest('a'), InputSHA256: fixtureDigest('b'), RequiredCapabilities: []CapabilityRequirement{{Name: "sources.benchmark_manifest", EffectClass: EffectExternalRead, Playback: PlaybackCaptured}, {Name: "sources.demo_catalog", EffectClass: EffectExternalRead, Playback: PlaybackCaptured}}, Treatments: []Treatment{TreatmentLiveCapture, TreatmentOfflineReplay, TreatmentCounterfactualBranch}, Oracle: Oracle{Kind: OracleResultAndWorkspace, ExpectedResultSHA256: fixtureDigest('a'), ExpectedWorkspaceSHA256: fixtureDigest('b'), ExpectedCapabilityCalls: 2}},
		{ID: "stateful-local-v1", Version: 1, Family: FamilyStatefulLocal, CodeSHA256: fixtureDigest('b'), InputSHA256: fixtureDigest('c'), WorkspaceSeedSHA256: fixtureDigest('a'), RequiredCapabilities: []CapabilityRequirement{}, Treatments: []Treatment{TreatmentLiveCapture, TreatmentOfflineReplay}, Oracle: Oracle{Kind: OracleResultAndWorkspace, ExpectedResultSHA256: fixtureDigest('b'), ExpectedWorkspaceSHA256: fixtureDigest('c'), ExpectedCapabilityCalls: 0}},
		{ID: "bounded-planning-v1", Version: 1, Family: FamilyBoundedPlanning, CodeSHA256: fixtureDigest('c'), InputSHA256: fixtureDigest('a'), RequiredCapabilities: []CapabilityRequirement{{Name: "sources.demo_catalog", EffectClass: EffectExternalRead, Playback: PlaybackCaptured}}, Treatments: []Treatment{TreatmentLiveCapture, TreatmentOfflineReplay, TreatmentCounterfactualBranch, TreatmentDeterministicVerify}, Oracle: Oracle{Kind: OracleResultOnly, ExpectedResultSHA256: fixtureDigest('c'), ExpectedCapabilityCalls: 1}},
	}}
	corpusBytes, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		return nil, err
	}
	plan := Plan{SchemaVersion: PlanSchemaVersion, EvidenceClass: EvidenceMechanismOnly, HostCommit: "0123456789abcdef0123456789abcdef01234567", GuestArtifactSHA256: fixtureDigest('a'), GuestManifestSHA256: fixtureDigest('b'), CorpusSHA256: corpusID, RuntimeProfileSHA256: fixtureDigest('c'), TreatmentOrder: []Treatment{TreatmentLiveCapture, TreatmentOfflineReplay, TreatmentCounterfactualBranch, TreatmentDeterministicVerify}, Repetitions: 1, Ceilings: Ceilings{MaxRows: 32, MaxWallMillisPerRow: 120000, MaxEvidenceBytesPerRow: 8 << 20}, ProhibitedClaims: RequiredProhibitedClaims()}
	planBytes, planID, err := EncodePlan(plan)
	if err != nil {
		return nil, err
	}
	report := Report{SchemaVersion: ReportSchemaVersion, EvidenceClass: EvidenceMechanismOnly, CorpusSHA256: corpusID, PlanSHA256: planID, ProhibitedClaims: RequiredProhibitedClaims(), Summary: Summary{Offered: 1, Completed: 1}, Rows: []Row{{RowID: RowIdentity("structured-source-v1", TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: TreatmentLiveCapture, Status: RowCompleted, OracleStatus: OraclePassed, EvidenceComplete: true, CorpusSHA256: corpusID, PlanSHA256: planID, EvidenceRefs: []string{fixtureDigest('d')}}}}
	reportBytes, reportID, err := EncodeReport(report)
	if err != nil {
		return nil, err
	}
	return map[string]Fixture{"corpus.json": {Bytes: corpusBytes, SHA256: corpusID, Contract: ContractCorpus}, "plan.json": {Bytes: planBytes, SHA256: planID, Contract: ContractPlan}, "report.json": {Bytes: reportBytes, SHA256: reportID, Contract: ContractReport}}, nil
}

func AdversarialFixtures() (map[string]Fixture, error) {
	positive, err := CanonicalFixtures()
	if err != nil {
		return nil, err
	}
	corpus, _, err := DecodeCorpus(positive["corpus.json"].Bytes)
	if err != nil {
		return nil, err
	}
	report, _, err := DecodeReport(positive["report.json"].Bytes)
	if err != nil {
		return nil, err
	}

	unknown := bytes.Replace(positive["corpus.json"].Bytes, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1)
	trailing := append(append([]byte(nil), positive["corpus.json"].Bytes...), []byte(` {}`)...)
	duplicate := corpus
	duplicate.Workloads = append([]Workload(nil), corpus.Workloads...)
	duplicate.Workloads[1].ID = duplicate.Workloads[0].ID
	missingOracle := corpus
	missingOracle.Workloads = append([]Workload(nil), corpus.Workloads...)
	missingOracle.Workloads[0].Oracle = Oracle{}
	incompatible := corpus
	incompatible.Workloads = append([]Workload(nil), corpus.Workloads...)
	incompatible.Workloads[1].Treatments = append(append([]Treatment(nil), incompatible.Workloads[1].Treatments...), TreatmentDeterministicVerify)
	branchWrite := corpus
	branchWrite.Workloads = append([]Workload(nil), corpus.Workloads...)
	branchWrite.Workloads[0].RequiredCapabilities = append([]CapabilityRequirement(nil), corpus.Workloads[0].RequiredCapabilities...)
	branchWrite.Workloads[0].RequiredCapabilities[0].EffectClass = EffectClass("external_write")
	seedOmission := corpus
	seedOmission.Workloads = append([]Workload(nil), corpus.Workloads...)
	seedOmission.Workloads[1].WorkspaceSeedSHA256 = ""
	seedOmission.Workloads[1].Treatments = append(append([]Treatment(nil), corpus.Workloads[1].Treatments...), TreatmentDeterministicVerify)
	drift := report
	drift.Rows = append([]Row(nil), report.Rows...)
	drift.Rows[0].CorpusSHA256 = fixtureDigest('f')

	values := map[string]struct {
		value    any
		contract Contract
	}{
		"corpus-unknown-field.json":           {json.RawMessage(unknown), ContractCorpus},
		"corpus-trailing-json.json":           {json.RawMessage(trailing), ContractCorpus},
		"corpus-duplicate-id.json":            {duplicate, ContractCorpus},
		"corpus-missing-oracle.json":          {missingOracle, ContractCorpus},
		"corpus-incompatible-treatment.json":  {incompatible, ContractCorpus},
		"corpus-branch-write-capability.json": {branchWrite, ContractCorpus},
		"corpus-stateful-seed-omission.json":  {seedOmission, ContractCorpus},
		"report-identity-drift.json":          {drift, ContractReport},
	}
	fixtures := make(map[string]Fixture, len(values))
	for name, value := range values {
		var raw []byte
		if message, ok := value.value.(json.RawMessage); ok {
			raw = append([]byte(nil), message...)
		} else {
			raw, err = json.Marshal(value.value)
			if err != nil {
				return nil, err
			}
		}
		digest := sha256.Sum256(raw)
		fixtures[name] = Fixture{Bytes: raw, SHA256: fmt.Sprintf("sha256:%x", digest), Contract: value.contract}
	}
	return fixtures, nil
}

func fixtureDigest(value byte) string { return "sha256:" + string(bytes.Repeat([]byte{value}, 64)) }
