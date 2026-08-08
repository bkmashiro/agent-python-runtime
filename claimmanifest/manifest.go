// Package claimmanifest defines Host-owned, claim-scoped verification results.
// It adapts Runtime and Harness metadata without expanding Runtime authority.
package claimmanifest

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const Version = "claim-manifest/v1"

type ClaimKind string

const (
	ClaimArtifact  ClaimKind = "artifact"
	ClaimBase      ClaimKind = "base"
	ClaimAuthority ClaimKind = "authority"
	ClaimExecution ClaimKind = "execution"
	ClaimEffect    ClaimKind = "effect"
	ClaimOutcome   ClaimKind = "outcome"
)

type Status string

const (
	StatusVerified     Status = "verified"
	StatusContradicted Status = "contradicted"
	StatusInsufficient Status = "insufficient"
	StatusStale        Status = "stale"
)

type Qualification string

const (
	QualificationStructuralOnly  Qualification = "structural-only"
	QualificationInputInjection  Qualification = "input-injection"
	QualificationStateEquivalent Qualification = "state-equivalent"
)

type ReplayLevel string

const (
	ReplayR0 ReplayLevel = "R0"
	ReplayR1 ReplayLevel = "R1"
	ReplayR2 ReplayLevel = "R2"
)

type EvidenceKind string

const (
	EvidenceExecutedCodeDigest EvidenceKind = "executed-code-digest"
	EvidenceTraceIntegrity     EvidenceKind = "trace-integrity"
)

var (
	ErrInvalidManifest      = errors.New("invalid claim manifest")
	ErrInsufficientEvidence = errors.New("insufficient evidence for replay level")
	ErrOverclaimedReplay    = errors.New("claim manifest overstates replay qualification")
	ErrExecutionNotObserved = errors.New("execution reference not observed in metadata playback")
	digestPattern           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	eventIDPattern          = regexp.MustCompile(`^evt_[0-9a-f]{32}$`)
)

type Evidence struct {
	Kind EvidenceKind `json:"kind"`
	Ref  string       `json:"ref"`
}

type Claim struct {
	ID        string     `json:"id"`
	Kind      ClaimKind  `json:"kind"`
	Status    Status     `json:"status"`
	DependsOn []string   `json:"depends_on,omitempty"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

type Manifest struct {
	Version          string                     `json:"version"`
	Source           string                     `json:"source"`
	ExecutionRef     runtimeconfig.ExecutionRef `json:"execution_ref"`
	PlaybackDigest   string                     `json:"playback_digest"`
	CompletedEventID string                     `json:"completed_event_id"`
	Qualification    Qualification              `json:"qualification"`
	Claims           []Claim                    `json:"claims"`
}

func (manifest Manifest) Claim(kind ClaimKind) (Claim, bool) {
	for _, claim := range manifest.Claims {
		if claim.Kind == kind {
			return claim, true
		}
	}
	return Claim{}, false
}

func (manifest Manifest) RequireReplay(level ReplayLevel) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	required, ok := replayRank(level)
	if !ok || required > qualificationRank(manifest.Qualification) {
		return ErrInsufficientEvidence
	}
	return nil
}

func (manifest Manifest) Validate() error {
	if manifest.Version != Version || manifest.Source != "metadata-only" || manifest.ExecutionRef.Validate() != nil ||
		!digestPattern.MatchString(manifest.PlaybackDigest) || !eventIDPattern.MatchString(manifest.CompletedEventID) {
		return ErrInvalidManifest
	}
	claims := make(map[ClaimKind]Claim, len(manifest.Claims))
	ids := make(map[string]struct{}, len(manifest.Claims))
	for _, claim := range manifest.Claims {
		if !validClaimKind(claim.Kind) || !validStatus(claim.Status) || claim.ID == "" {
			return ErrInvalidManifest
		}
		if _, exists := claims[claim.Kind]; exists {
			return ErrInvalidManifest
		}
		if _, exists := ids[claim.ID]; exists {
			return ErrInvalidManifest
		}
		claims[claim.Kind] = claim
		ids[claim.ID] = struct{}{}
		for _, evidence := range claim.Evidence {
			if !validEvidenceKind(evidence.Kind) || evidence.Ref == "" {
				return ErrInvalidManifest
			}
		}
	}
	for _, kind := range allClaimKinds() {
		if _, ok := claims[kind]; !ok {
			return ErrInvalidManifest
		}
	}
	for _, claim := range manifest.Claims {
		for _, dependency := range claim.DependsOn {
			if _, ok := ids[dependency]; !ok || dependency == claim.ID {
				return ErrInvalidManifest
			}
		}
	}
	if !reflect.DeepEqual(manifest.Claims, metadataClaims(manifest.ExecutionRef, manifest.PlaybackDigest, manifest.CompletedEventID)) {
		return ErrInvalidManifest
	}
	maximum := maximumQualification(claims)
	if qualificationRank(manifest.Qualification) > qualificationRank(maximum) {
		return ErrOverclaimedReplay
	}
	if qualificationRank(manifest.Qualification) == 0 {
		return ErrInvalidManifest
	}
	return nil
}

func maximumQualification(claims map[ClaimKind]Claim) Qualification {
	artifact := claims[ClaimArtifact]
	execution := claims[ClaimExecution]
	if artifact.Status != StatusVerified || execution.Status != StatusVerified ||
		!hasEvidence(artifact, EvidenceExecutedCodeDigest) || !hasEvidence(execution, EvidenceTraceIntegrity) {
		return ""
	}
	return QualificationStructuralOnly
}

func hasEvidence(claim Claim, kind EvidenceKind) bool {
	for _, evidence := range claim.Evidence {
		if evidence.Kind == kind {
			return true
		}
	}
	return false
}

func qualificationRank(qualification Qualification) int {
	switch qualification {
	case QualificationStructuralOnly:
		return 1
	case QualificationInputInjection:
		return 2
	case QualificationStateEquivalent:
		return 3
	default:
		return 0
	}
}

func replayRank(level ReplayLevel) (int, bool) {
	switch level {
	case ReplayR0:
		return 1, true
	case ReplayR1:
		return 2, true
	case ReplayR2:
		return 3, true
	default:
		return 0, false
	}
}

func validClaimKind(kind ClaimKind) bool {
	for _, candidate := range allClaimKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

func allClaimKinds() []ClaimKind {
	return []ClaimKind{ClaimArtifact, ClaimBase, ClaimAuthority, ClaimExecution, ClaimEffect, ClaimOutcome}
}

func validStatus(status Status) bool {
	switch status {
	case StatusVerified, StatusContradicted, StatusInsufficient, StatusStale:
		return true
	default:
		return false
	}
}

func validEvidenceKind(kind EvidenceKind) bool {
	switch kind {
	case EvidenceExecutedCodeDigest, EvidenceTraceIntegrity:
		return true
	default:
		return false
	}
}

func claimID(kind ClaimKind, executionID string) string {
	return fmt.Sprintf("%s:%s", kind, executionID)
}

func metadataClaims(ref runtimeconfig.ExecutionRef, playbackDigest, completedEventID string) []Claim {
	artifact := statusClaim(ClaimArtifact, ref.ExecutionID, StatusVerified)
	artifact.Evidence = []Evidence{{Kind: EvidenceExecutedCodeDigest, Ref: ref.ExecutedCodeSHA256}}
	base := statusClaim(ClaimBase, ref.ExecutionID, StatusInsufficient)
	authority := statusClaim(ClaimAuthority, ref.ExecutionID, StatusInsufficient)
	execution := statusClaim(ClaimExecution, ref.ExecutionID, StatusVerified, ClaimArtifact, ClaimBase, ClaimAuthority)
	execution.Evidence = []Evidence{{Kind: EvidenceTraceIntegrity, Ref: playbackDigest + "#" + completedEventID}}
	effect := statusClaim(ClaimEffect, ref.ExecutionID, StatusInsufficient, ClaimAuthority, ClaimExecution)
	outcome := statusClaim(ClaimOutcome, ref.ExecutionID, StatusInsufficient, ClaimExecution, ClaimEffect)
	return []Claim{artifact, base, authority, execution, effect, outcome}
}

func statusClaim(kind ClaimKind, executionID string, status Status, dependencies ...ClaimKind) Claim {
	claim := Claim{ID: claimID(kind, executionID), Kind: kind, Status: status}
	for _, dependency := range dependencies {
		claim.DependsOn = append(claim.DependsOn, claimID(dependency, executionID))
	}
	return claim
}
