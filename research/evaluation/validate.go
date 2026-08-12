package evaluation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,95}$`)
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

var requiredClaims = []string{
	"arbitrary_determinism",
	"computer_replacement",
	"economic_advantage",
	"model_quality",
	"placement_share",
	"production_readiness",
	"token_or_latency_benefit",
}

const (
	maxCapabilitiesPerWorkload = 32
	maxEvaluationRows          = 100_000
	maxEvidenceRefsPerRow      = 128
)

func RequiredProhibitedClaims() []string { return append([]string(nil), requiredClaims...) }

func RowIdentity(workloadID string, treatment Treatment, repetition uint32) string {
	digest := sha256.New()
	digest.Write([]byte("pysolate.evaluation-row.v1"))
	digest.Write([]byte{0})
	digest.Write([]byte(workloadID))
	digest.Write([]byte{0})
	digest.Write([]byte(treatment))
	digest.Write([]byte{0})
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], repetition)
	digest.Write(encoded[:])
	return fmt.Sprintf("row-%x", digest.Sum(nil))
}

func validEvidenceClass(value EvidenceClass) bool {
	switch value {
	case EvidenceCurrent, EvidenceMechanismOnly, EvidenceQualifiedWorkload, EvidenceExperimentalPartial, EvidenceNotMeasured:
		return true
	default:
		return false
	}
}

func validFamily(value Family) bool {
	return value == FamilyStructuredSource || value == FamilyStatefulLocal || value == FamilyBoundedPlanning
}
func validTreatment(value Treatment) bool {
	return value == TreatmentLiveCapture || value == TreatmentOfflineReplay || value == TreatmentCounterfactualBranch || value == TreatmentDeterministicVerify
}
func validOracleKind(value OracleKind) bool {
	return value == OracleResultOnly || value == OracleResultAndWorkspace
}
func validRowStatus(value RowStatus) bool {
	return value == RowCompleted || value == RowFailed || value == RowTimedOut || value == RowUnsupported
}
func validOracleStatus(value OracleStatus) bool {
	return value == OraclePassed || value == OracleFailed || value == OracleNotRun
}

func validateCorpus(corpus Corpus) error {
	if corpus.SchemaVersion != CorpusSchemaVersion || corpus.EvidenceClass != EvidenceMechanismOnly || len(corpus.Workloads) != 3 {
		return ErrInvalid
	}
	ids := map[string]struct{}{}
	families := map[Family]struct{}{}
	for _, workload := range corpus.Workloads {
		if !identifierPattern.MatchString(workload.ID) || workload.Version == 0 || !validFamily(workload.Family) || workload.RequiredCapabilities == nil ||
			!digestPattern.MatchString(workload.CodeSHA256) || !digestPattern.MatchString(workload.InputSHA256) ||
			len(workload.Treatments) == 0 || !validOracleKind(workload.Oracle.Kind) || !digestPattern.MatchString(workload.Oracle.ExpectedResultSHA256) {
			return ErrInvalid
		}
		if _, exists := ids[workload.ID]; exists {
			return ErrInvalid
		}
		if _, exists := families[workload.Family]; exists {
			return ErrInvalid
		}
		ids[workload.ID], families[workload.Family] = struct{}{}, struct{}{}
		if workload.WorkspaceSeedSHA256 != "" && !digestPattern.MatchString(workload.WorkspaceSeedSHA256) {
			return ErrInvalid
		}
		if workload.Oracle.Kind == OracleResultAndWorkspace {
			if !digestPattern.MatchString(workload.Oracle.ExpectedWorkspaceSHA256) {
				return ErrInvalid
			}
		} else if workload.Oracle.ExpectedWorkspaceSHA256 != "" {
			return ErrInvalid
		}
		if len(workload.RequiredCapabilities) > maxCapabilitiesPerWorkload {
			return ErrInvalid
		}
		capabilities := map[string]struct{}{}
		for _, capability := range workload.RequiredCapabilities {
			if !capabilityPattern.MatchString(capability.Name) || capability.EffectClass != EffectExternalRead || capability.Playback != PlaybackCaptured {
				return ErrInvalid
			}
			if _, exists := capabilities[capability.Name]; exists {
				return ErrInvalid
			}
			capabilities[capability.Name] = struct{}{}
		}
		treatments := map[Treatment]struct{}{}
		for _, treatment := range workload.Treatments {
			if !validTreatment(treatment) {
				return ErrInvalid
			}
			if _, exists := treatments[treatment]; exists {
				return ErrInvalid
			}
			treatments[treatment] = struct{}{}
		}
		if _, branch := treatments[TreatmentCounterfactualBranch]; branch && len(capabilities) == 0 {
			return ErrInvalid
		}
		if _, deterministic := treatments[TreatmentDeterministicVerify]; deterministic && workload.WorkspaceSeedSHA256 != "" {
			return ErrInvalid
		}
		switch workload.Family {
		case FamilyStructuredSource:
			if workload.WorkspaceSeedSHA256 != "" || len(capabilities) == 0 {
				return ErrInvalid
			}
		case FamilyStatefulLocal:
			if !digestPattern.MatchString(workload.WorkspaceSeedSHA256) || len(capabilities) != 0 {
				return ErrInvalid
			}
			if _, branch := treatments[TreatmentCounterfactualBranch]; branch {
				return ErrInvalid
			}
			if _, deterministic := treatments[TreatmentDeterministicVerify]; deterministic {
				return ErrInvalid
			}
		case FamilyBoundedPlanning:
			if workload.WorkspaceSeedSHA256 != "" || len(capabilities) == 0 {
				return ErrInvalid
			}
		}
		if workload.Oracle.ExpectedCapabilityCalls != uint32(len(workload.RequiredCapabilities)) {
			return ErrInvalid
		}
	}
	for _, family := range []Family{FamilyStructuredSource, FamilyStatefulLocal, FamilyBoundedPlanning} {
		if _, exists := families[family]; !exists {
			return ErrInvalid
		}
	}
	return nil
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion || plan.EvidenceClass != EvidenceMechanismOnly || !commitPattern.MatchString(plan.HostCommit) ||
		!digestPattern.MatchString(plan.GuestArtifactSHA256) || !digestPattern.MatchString(plan.GuestManifestSHA256) ||
		!digestPattern.MatchString(plan.CorpusSHA256) || !digestPattern.MatchString(plan.RuntimeProfileSHA256) ||
		plan.Repetitions == 0 || plan.Repetitions > 100 || plan.Ceilings.MaxRows == 0 || plan.Ceilings.MaxRows > maxEvaluationRows || plan.Ceilings.MaxWallMillisPerRow == 0 || plan.Ceilings.MaxEvidenceBytesPerRow == 0 ||
		!slices.Equal(plan.ProhibitedClaims, requiredClaims) || len(plan.TreatmentOrder) == 0 {
		return ErrInvalid
	}
	seen := map[Treatment]struct{}{}
	for _, treatment := range plan.TreatmentOrder {
		if !validTreatment(treatment) {
			return ErrInvalid
		}
		if _, exists := seen[treatment]; exists {
			return ErrInvalid
		}
		seen[treatment] = struct{}{}
	}
	return nil
}

func validateReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion || report.EvidenceClass != EvidenceMechanismOnly || !digestPattern.MatchString(report.CorpusSHA256) ||
		!digestPattern.MatchString(report.PlanSHA256) || !slices.Equal(report.ProhibitedClaims, requiredClaims) || len(report.Rows) == 0 || len(report.Rows) > maxEvaluationRows || uint32(len(report.Rows)) != report.Summary.Offered ||
		report.Summary.Offered != report.Summary.Completed+report.Summary.Failed+report.Summary.TimedOut+report.Summary.Unsupported {
		return ErrInvalid
	}
	seen := map[string]struct{}{}
	counts := map[RowStatus]uint32{}
	for _, row := range report.Rows {
		if row.RowID != RowIdentity(row.WorkloadID, row.Treatment, row.Repetition) || !identifierPattern.MatchString(row.WorkloadID) || !validTreatment(row.Treatment) || !validRowStatus(row.Status) || !validOracleStatus(row.OracleStatus) || row.EvidenceRefs == nil || len(row.EvidenceRefs) > maxEvidenceRefsPerRow ||
			row.CorpusSHA256 != report.CorpusSHA256 || row.PlanSHA256 != report.PlanSHA256 {
			return ErrInvalid
		}
		if _, exists := seen[row.RowID]; exists {
			return ErrInvalid
		}
		seen[row.RowID] = struct{}{}
		counts[row.Status]++
		if row.Status == RowCompleted {
			if row.OracleStatus != OraclePassed || !row.EvidenceComplete || len(row.EvidenceRefs) == 0 || row.ProblemCode != "" {
				return ErrInvalid
			}
		} else {
			if row.ProblemCode == "" {
				return ErrInvalid
			}
			switch row.Status {
			case RowFailed:
				if row.OracleStatus != OracleFailed || (row.EvidenceComplete && len(row.EvidenceRefs) == 0) {
					return ErrInvalid
				}
			case RowTimedOut, RowUnsupported:
				if row.OracleStatus != OracleNotRun || row.EvidenceComplete {
					return ErrInvalid
				}
			}
		}
		for _, ref := range row.EvidenceRefs {
			if !digestPattern.MatchString(ref) {
				return ErrInvalid
			}
		}
		if row.ProblemCode != "" && (!identifierPattern.MatchString(row.ProblemCode) || strings.Contains(row.ProblemCode, ".")) {
			return ErrInvalid
		}
	}
	if counts[RowCompleted] != report.Summary.Completed || counts[RowFailed] != report.Summary.Failed || counts[RowTimedOut] != report.Summary.TimedOut || counts[RowUnsupported] != report.Summary.Unsupported {
		return fmt.Errorf("%w: summary counts", ErrInvalid)
	}
	return nil
}
