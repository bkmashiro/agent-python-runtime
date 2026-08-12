package capability

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type BranchMode string

const (
	BranchOverride       BranchMode = "override"
	BranchRecordedSuffix BranchMode = "recorded_suffix"
	BranchLiveSuffix     BranchMode = "live_suffix"
)

// BranchConfig is Host-owned routing policy sealed before a child Guest
// starts. Prefix records are consumed strictly; suffix policy never comes from
// Guest arguments and never expands a Plan during execution.
type BranchConfig struct {
	ForkOperation uint32
	PrefixEntries []TranscriptEntry
	Mode          BranchMode
	SuffixEntries []TranscriptEntry
}

type branchState struct {
	fork           uint32
	mode           BranchMode
	prefix         map[uint32]TranscriptEntry
	suffix         map[uint32]TranscriptEntry
	consumed       map[uint32]bool
	failed         bool
	suffixObserved bool
}

func newBranchState(config BranchConfig, plan *Plan) (*branchState, error) {
	if plan == nil || config.ForkOperation >= plan.MaxCalls() {
		return nil, ErrInvalidBroker
	}
	prefix, err := normalizePlaybackEntries(config.PrefixEntries)
	if err != nil || uint32(len(prefix)) != config.ForkOperation {
		return nil, ErrInvalidBroker
	}
	state := &branchState{
		fork: config.ForkOperation, mode: config.Mode, prefix: make(map[uint32]TranscriptEntry, len(prefix)),
		suffix: make(map[uint32]TranscriptEntry), consumed: make(map[uint32]bool, len(prefix)+len(config.SuffixEntries)),
	}
	for index, entry := range prefix {
		if entry.OperationIndex != uint32(index) || !branchCompatible(plan, entry) {
			return nil, ErrInvalidBroker
		}
		state.prefix[entry.OperationIndex] = entry
	}
	switch config.Mode {
	case BranchOverride:
		if len(config.SuffixEntries) == 0 {
			return nil, ErrInvalidBroker
		}
		for index, entry := range cloneTranscript(config.SuffixEntries) {
			if entry.OperationIndex != config.ForkOperation+uint32(index) || !branchCompatible(plan, entry) || normalizeBranchOverride(&entry) != nil {
				return nil, ErrInvalidBroker
			}
			state.suffix[entry.OperationIndex] = entry
		}
	case BranchRecordedSuffix:
		suffix, err := normalizePlaybackEntries(config.SuffixEntries)
		if err != nil || len(suffix) == 0 {
			return nil, ErrInvalidBroker
		}
		for index, entry := range suffix {
			if entry.OperationIndex != config.ForkOperation+uint32(index) || !branchCompatible(plan, entry) {
				return nil, ErrInvalidBroker
			}
			state.suffix[entry.OperationIndex] = entry
		}
	case BranchLiveSuffix:
		if len(config.SuffixEntries) != 0 {
			return nil, ErrInvalidBroker
		}
	default:
		return nil, ErrInvalidBroker
	}
	return state, nil
}

func branchCompatible(plan *Plan, entry TranscriptEntry) bool {
	registered, ok := plan.lookup(entry.Capability)
	return ok && registered.spec.EffectClass == EffectExternalRead && registered.spec.Playback == PlaybackCaptured && entry.OperationIndex < plan.MaxCalls()
}

func normalizeBranchOverride(entry *TranscriptEntry) error {
	if entry == nil || !validName(entry.Capability) || entry.Evidence.Kind != "branch_override" || entry.Evidence.Status != 200 ||
		entry.Evidence.MediaType != "application/json" || entry.Evidence.BodyBytes > maxCallBytes || !validSHA256Identity(entry.Evidence.BodySHA256) {
		return ErrPlaybackMismatch
	}
	argumentsDocument, arguments, err := canonicalJSON(entry.Arguments)
	if err != nil || argumentsDocument == nil || !bytes.Equal(arguments, entry.Arguments) {
		return ErrPlaybackMismatch
	}
	resultDocument, result, err := canonicalJSON(entry.Result)
	if err != nil || resultDocument == nil || !bytes.Equal(result, entry.Result) || len(result) > maxCallBytes {
		return ErrPlaybackMismatch
	}
	argumentsDigest := sha256.Sum256(arguments)
	resultDigest := sha256.Sum256(result)
	if entry.ArgumentsSHA256 != fmt.Sprintf("sha256:%x", argumentsDigest[:]) || entry.ResultSHA256 != fmt.Sprintf("sha256:%x", resultDigest[:]) ||
		entry.Evidence.BodySHA256 != entry.ResultSHA256 || entry.Evidence.BodyBytes != uint32(len(result)) {
		return ErrPlaybackMismatch
	}
	entry.Arguments, entry.Result = arguments, result
	return nil
}

func (broker *Broker) matchBranch(operation uint32, capabilityName string, arguments json.RawMessage) (TranscriptEntry, bool, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.branch == nil || broker.branch.failed {
		return TranscriptEntry{}, false, ErrPlaybackMismatch
	}
	if operation < broker.branch.fork {
		entry, ok := broker.branch.prefix[operation]
		if !ok || broker.branch.consumed[operation] || entry.Capability != capabilityName || !bytes.Equal(entry.Arguments, arguments) {
			broker.branch.failed = true
			return TranscriptEntry{}, false, ErrPlaybackMismatch
		}
		return cloneTranscript([]TranscriptEntry{entry})[0], false, nil
	}
	broker.branch.suffixObserved = true
	if broker.branch.mode == BranchLiveSuffix {
		return TranscriptEntry{}, true, nil
	}
	entry, ok := broker.branch.suffix[operation]
	if !ok || broker.branch.consumed[operation] || entry.Capability != capabilityName || !bytes.Equal(entry.Arguments, arguments) {
		broker.branch.failed = true
		return TranscriptEntry{}, false, ErrPlaybackMismatch
	}
	return cloneTranscript([]TranscriptEntry{entry})[0], false, nil
}

func (broker *Broker) consumeBranch(entry TranscriptEntry) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.branch == nil || broker.branch.failed || broker.branch.consumed[entry.OperationIndex] {
		return ErrPlaybackMismatch
	}
	broker.branch.consumed[entry.OperationIndex] = true
	broker.transcript = append(broker.transcript, cloneTranscript([]TranscriptEntry{entry})[0])
	return nil
}

func (broker *Broker) failBranch() {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	if broker.branch != nil {
		broker.branch.failed = true
	}
	broker.mu.Unlock()
}

func (broker *Broker) finalizeBranch() error {
	if broker.branch == nil {
		return nil
	}
	if broker.branch.failed || !broker.branch.suffixObserved || len(broker.branch.consumed) != len(broker.branch.prefix)+len(broker.branch.suffix) {
		return ErrPlaybackIncomplete
	}
	return nil
}
