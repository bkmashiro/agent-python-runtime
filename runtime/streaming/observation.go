package streaming

import (
	"errors"
	"regexp"
	"sync"
)

const ObservationIdentitySchemaVersion = "pysolate.staged-observation.v1"

var (
	ErrInvalidObservationIdentity   = errors.New("invalid staged observation identity")
	ErrStagedObservationMismatch    = errors.New("staged observation identity mismatch")
	ErrStagedObservationConsumed    = errors.New("staged observation already consumed")
	ErrStagedObservationTerminal    = errors.New("staged observation is terminal")
	ErrStagedObservationSourceBound = errors.New("staged observation source is already bound")
)

var observationTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// ObservationIdentity binds one staged read to its exact source occurrence and
// Host policy context. It contains identities only, never result bodies, paths,
// endpoints, credentials, or authority handles.
type ObservationIdentity struct {
	SchemaVersion       string    `json:"schema_version"`
	StreamEpoch         string    `json:"stream_epoch"`
	WorkflowEpoch       string    `json:"workflow_epoch"`
	SourceSHA256        string    `json:"source_sha256,omitempty"`
	SuiteRange          ByteRange `json:"suite_range"`
	SuiteSHA256         string    `json:"suite_sha256"`
	DynamicOccurrence   uint32    `json:"dynamic_occurrence"`
	ArgumentsSHA256     string    `json:"arguments_sha256"`
	Capability          string    `json:"capability"`
	SpecSHA256          string    `json:"spec_sha256"`
	HandlerIdentity     string    `json:"handler_identity"`
	PlanSHA256          string    `json:"plan_sha256"`
	GrantPolicySHA256   string    `json:"grant_policy_sha256"`
	FreshnessEpoch      string    `json:"freshness_epoch"`
	ExpiryEpoch         string    `json:"expiry_epoch"`
	PrivacyPartition    string    `json:"privacy_partition"`
	ParentLineageSHA256 string    `json:"parent_lineage_sha256"`
}

// Validate validates a provisional or sealed identity. Sealed identities must
// include the final full-source digest; provisional identities must not guess it.
func (identity ObservationIdentity) Validate(sealed bool) error {
	if identity.SchemaVersion != ObservationIdentitySchemaVersion ||
		!observationTokenPattern.MatchString(identity.StreamEpoch) ||
		!observationTokenPattern.MatchString(identity.WorkflowEpoch) ||
		identity.SuiteRange.Start < 0 || identity.SuiteRange.End <= identity.SuiteRange.Start ||
		identity.DynamicOccurrence == 0 ||
		!validObservationDigest(identity.SuiteSHA256) ||
		!validObservationDigest(identity.ArgumentsSHA256) ||
		!observationTokenPattern.MatchString(identity.Capability) ||
		!validObservationDigest(identity.SpecSHA256) ||
		!observationTokenPattern.MatchString(identity.HandlerIdentity) ||
		!validObservationDigest(identity.PlanSHA256) ||
		!validObservationDigest(identity.GrantPolicySHA256) ||
		!observationTokenPattern.MatchString(identity.FreshnessEpoch) ||
		!observationTokenPattern.MatchString(identity.ExpiryEpoch) ||
		!observationTokenPattern.MatchString(identity.PrivacyPartition) ||
		!validObservationDigest(identity.ParentLineageSHA256) {
		return ErrInvalidObservationIdentity
	}
	if sealed != validObservationDigest(identity.SourceSHA256) {
		return ErrInvalidObservationIdentity
	}
	return nil
}

func validObservationDigest(value string) bool {
	if len(value) != len(SourceDigestPrefix)+64 || value[:len(SourceDigestPrefix)] != SourceDigestPrefix {
		return false
	}
	for _, character := range value[len(SourceDigestPrefix):] {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

type ObservationDisposition string

const (
	ObservationReady     ObservationDisposition = "ready"
	ObservationConsumed  ObservationDisposition = "consumed"
	ObservationFailed    ObservationDisposition = "failed"
	ObservationTimedOut  ObservationDisposition = "timed_out"
	ObservationCancelled ObservationDisposition = "cancelled"
	ObservationLate      ObservationDisposition = "late"
	ObservationOrphaned  ObservationDisposition = "orphaned"
	ObservationFallback  ObservationDisposition = "fallback_playback"
)

// StagedObservation owns one private staged result. It is deliberately local
// and one-shot; durable/global memoization belongs to Agent Functions instead.
type StagedObservation struct {
	mu          sync.Mutex
	identity    ObservationIdentity
	result      []byte
	disposition ObservationDisposition
}

func NewStagedObservation(identity ObservationIdentity, result []byte) (*StagedObservation, error) {
	sealed := identity.SourceSHA256 != ""
	if identity.Validate(sealed) != nil || len(result) == 0 || len(result) > maxSourceBytes {
		return nil, ErrInvalidObservationIdentity
	}
	return &StagedObservation{
		identity: identity, result: append([]byte(nil), result...), disposition: ObservationReady,
	}, nil
}

// BindSource binds a provisional record to the final admitted source identity.
// It is idempotent only for the exact same final digest.
func (record *StagedObservation) BindSource(sourceSHA256 string) (*StagedObservation, error) {
	if record == nil || !validObservationDigest(sourceSHA256) {
		return nil, ErrInvalidObservationIdentity
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.disposition != ObservationReady {
		return nil, ErrStagedObservationTerminal
	}
	if record.identity.SourceSHA256 != "" {
		if record.identity.SourceSHA256 != sourceSHA256 {
			return nil, ErrStagedObservationSourceBound
		}
		return record, nil
	}
	record.identity.SourceSHA256 = sourceSHA256
	if err := record.identity.Validate(true); err != nil {
		record.identity.SourceSHA256 = ""
		return nil, err
	}
	return record, nil
}

func (record *StagedObservation) Consume(identity ObservationIdentity) ([]byte, error) {
	if record == nil || identity.Validate(true) != nil {
		return nil, ErrStagedObservationMismatch
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.disposition == ObservationConsumed {
		return nil, ErrStagedObservationConsumed
	}
	if record.disposition != ObservationReady || record.identity.SourceSHA256 == "" {
		return nil, ErrStagedObservationTerminal
	}
	if record.identity != identity {
		return nil, ErrStagedObservationMismatch
	}
	record.disposition = ObservationConsumed
	return append([]byte(nil), record.result...), nil
}

func (record *StagedObservation) Terminate(disposition ObservationDisposition) error {
	if record == nil || !terminalObservationDisposition(disposition) {
		return ErrStagedObservationTerminal
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.disposition != ObservationReady {
		return ErrStagedObservationTerminal
	}
	record.disposition = disposition
	record.result = nil
	return nil
}

func terminalObservationDisposition(disposition ObservationDisposition) bool {
	switch disposition {
	case ObservationFailed, ObservationTimedOut, ObservationCancelled, ObservationLate, ObservationOrphaned, ObservationFallback:
		return true
	default:
		return false
	}
}
