package runtime

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	DeterministicVerificationSchemaVersion       = "pysolate.deterministic-verification.v1"
	DeterministicVerificationExperimentalPartial = "experimental_partial"
	deterministicWalltimeUnixNano                = int64(1700000000000000000)
	deterministicMonotonicStartNano              = int64(1000000000)
	deterministicClockStepNano                   = int64(1000)
)

var ErrDeterministicVerificationAdmission = errors.New("deterministic verification workload is unsupported")

// DeterministicVerificationProfile is Host-owned, artifact-bound policy for a
// deliberately narrow reproducibility experiment. It is not an unqualified
// claim that arbitrary Python or an entire Agent is deterministic.
type DeterministicVerificationProfile struct {
	artifactSHA256 string
	randomSeed     []byte
	identity       string
}

type deterministicVerificationDescriptor struct {
	SchemaVersion        string   `json:"schema_version"`
	Status               string   `json:"status"`
	ArtifactSHA256       string   `json:"artifact_sha256"`
	RandomAlgorithm      string   `json:"random_algorithm"`
	RandomSeedSHA256     string   `json:"random_seed_sha256"`
	WallClockPolicy      string   `json:"wall_clock_policy"`
	WalltimeUnixNano     int64    `json:"walltime_unix_nano"`
	MonotonicStartNano   int64    `json:"monotonic_start_nano"`
	ClockStepNano        int64    `json:"clock_step_nano"`
	LocalePolicy         string   `json:"locale_policy"`
	DirectoryOrderPolicy string   `json:"directory_order_policy"`
	Unsupported          []string `json:"unsupported"`
}

func NewDeterministicVerificationProfile(artifactSHA256, randomSeed string) (DeterministicVerificationProfile, error) {
	if !sha256DigestPattern.MatchString(artifactSHA256) || !boundedIdentifier(randomSeed, 128) {
		return DeterministicVerificationProfile{}, ErrDeterministicVerificationAdmission
	}
	profile := DeterministicVerificationProfile{
		artifactSHA256: artifactSHA256,
		randomSeed:     append([]byte(nil), randomSeed...),
	}
	identity, err := profile.computeIdentity()
	if err != nil {
		return DeterministicVerificationProfile{}, ErrDeterministicVerificationAdmission
	}
	profile.identity = identity
	return profile, nil
}

func (profile DeterministicVerificationProfile) Validate() error {
	if !sha256DigestPattern.MatchString(profile.artifactSHA256) || len(profile.randomSeed) == 0 || !boundedIdentifier(string(profile.randomSeed), 128) || !sha256DigestPattern.MatchString(profile.identity) {
		return ErrDeterministicVerificationAdmission
	}
	identity, err := profile.computeIdentity()
	if err != nil || identity != profile.identity {
		return ErrDeterministicVerificationAdmission
	}
	return nil
}

func (profile DeterministicVerificationProfile) SchemaVersion() string {
	return DeterministicVerificationSchemaVersion
}

func (profile DeterministicVerificationProfile) Status() string {
	return DeterministicVerificationExperimentalPartial
}

func (profile DeterministicVerificationProfile) Identity() string { return profile.identity }

func (profile DeterministicVerificationProfile) ArtifactSHA256() string {
	return profile.artifactSHA256
}

func (profile DeterministicVerificationProfile) RandomSeed() []byte {
	return append([]byte(nil), profile.randomSeed...)
}

func (profile DeterministicVerificationProfile) WalltimeUnixNano() int64 {
	return deterministicWalltimeUnixNano
}

func (profile DeterministicVerificationProfile) MonotonicStartNano() int64 {
	return deterministicMonotonicStartNano
}

func (profile DeterministicVerificationProfile) ClockStepNano() int64 {
	return deterministicClockStepNano
}

func (profile DeterministicVerificationProfile) descriptor() deterministicVerificationDescriptor {
	seedDigest := sha256.Sum256(profile.randomSeed)
	unsupported := []string{
		"concurrent-scheduling",
		"floating-point-cross-platform-equivalence",
		"locale-mutation",
		"mounted-wasi-directory-enumeration",
	}
	sort.Strings(unsupported)
	return deterministicVerificationDescriptor{
		SchemaVersion: DeterministicVerificationSchemaVersion, Status: DeterministicVerificationExperimentalPartial,
		ArtifactSHA256: profile.artifactSHA256, RandomAlgorithm: "sha256-counter-v1",
		RandomSeedSHA256: fmt.Sprintf("sha256:%x", seedDigest[:]), WallClockPolicy: "virtual-fixed-step",
		WalltimeUnixNano: deterministicWalltimeUnixNano, MonotonicStartNano: deterministicMonotonicStartNano,
		ClockStepNano: deterministicClockStepNano, LocalePolicy: "wasi-no-env-locale-import-denied",
		DirectoryOrderPolicy: "no-mounted-wasi-filesystem", Unsupported: unsupported,
	}
}

func (profile DeterministicVerificationProfile) computeIdentity() (string, error) {
	descriptor := profile.descriptor()
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write([]byte("pysolate-deterministic-verification-v1"))
	digest.Write([]byte{0})
	digest.Write(encoded)
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// AdmitDeterministicVerification validates only workload-source classes. Host
// execution paths should call AdmitDeterministicVerificationExecution so the
// execution environment is admitted in the same fail-closed decision.
func AdmitDeterministicVerification(request RunRequest) error {
	return admitDeterministicVerificationWorkload(request)
}

// AdmitDeterministicVerificationExecution rejects workload or environment
// classes whose behavior this bounded profile does not claim to control. The
// ordinary import/source admission path still runs independently and rejects
// dynamic or late imports.
func AdmitDeterministicVerificationExecution(request RunRequest, workspaceMounted bool) error {
	if workspaceMounted {
		return ErrDeterministicVerificationAdmission
	}
	return admitDeterministicVerificationWorkload(request)
}

func admitDeterministicVerificationWorkload(request RunRequest) error {
	imports, err := InferStaticImportRoots(request.Code)
	if err != nil {
		return ErrDeterministicVerificationAdmission
	}
	for _, root := range imports {
		switch root {
		case "asyncio", "concurrent", "locale", "multiprocessing", "threading":
			return ErrDeterministicVerificationAdmission
		}
	}
	return nil
}
