package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrPlaybackMismatch   = errors.New("playback transcript mismatch")
	ErrPlaybackIncomplete = errors.New("playback transcript has unused records")
)

type PlaybackConfig struct {
	Entries []TranscriptEntry
}

type playbackHandler struct{}

// NewPlaybackHandler returns a non-network placeholder required only to keep
// the sealed Spec/Grant registration identical in offline mode. Broker
// playback intercepts every call before this handler can run.
func NewPlaybackHandler() Handler { return playbackHandler{} }

func (playbackHandler) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("offline playback handler must not execute")
}

func (playbackHandler) CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, TransportEvidence, error) {
	return nil, TransportEvidence{}, errors.New("offline playback handler must not execute")
}

func playbackDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func normalizePlaybackEntries(entries []TranscriptEntry) ([]TranscriptEntry, error) {
	cloned := cloneTranscript(entries)
	var previous uint32
	for index := range cloned {
		entry := &cloned[index]
		if !validName(entry.Capability) || !validTransportEvidence(entry.Evidence) || (index > 0 && entry.OperationIndex <= previous) {
			return nil, ErrPlaybackMismatch
		}
		argumentsDocument, arguments, err := canonicalJSON(entry.Arguments)
		if err != nil || argumentsDocument == nil || !bytes.Equal(arguments, entry.Arguments) {
			return nil, ErrPlaybackMismatch
		}
		resultDocument, result, err := canonicalJSON(entry.Result)
		if err != nil || resultDocument == nil || !bytes.Equal(result, entry.Result) {
			return nil, ErrPlaybackMismatch
		}
		argumentsDigest := sha256.Sum256(arguments)
		resultDigest := sha256.Sum256(result)
		if entry.ArgumentsSHA256 != fmt.Sprintf("sha256:%x", argumentsDigest[:]) || entry.ResultSHA256 != fmt.Sprintf("sha256:%x", resultDigest[:]) {
			return nil, ErrPlaybackMismatch
		}
		previous = entry.OperationIndex
	}
	return cloned, nil
}

func (broker *Broker) failPlayback() {
	if broker == nil || broker.playbackEntries == nil {
		return
	}
	broker.mu.Lock()
	broker.playbackFailed = true
	broker.mu.Unlock()
}

func (broker *Broker) matchPlayback(operation uint32, capabilityName string, arguments json.RawMessage) (TranscriptEntry, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	entry, ok := broker.playbackEntries[operation]
	if !ok || broker.playbackConsumed[operation] || entry.Capability != capabilityName || !bytes.Equal(entry.Arguments, arguments) {
		return TranscriptEntry{}, ErrPlaybackMismatch
	}
	digest := sha256.Sum256(arguments)
	if entry.ArgumentsSHA256 != fmt.Sprintf("sha256:%x", digest[:]) {
		return TranscriptEntry{}, ErrPlaybackMismatch
	}
	return cloneTranscript([]TranscriptEntry{entry})[0], nil
}

func (broker *Broker) consumePlayback(operation uint32) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if _, exists := broker.playbackEntries[operation]; !exists || broker.playbackConsumed[operation] {
		return ErrPlaybackMismatch
	}
	broker.playbackConsumed[operation] = true
	return nil
}
