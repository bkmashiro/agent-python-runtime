// Package streaming contains the narrow S1 append-only source-admission
// protocol. It deliberately does not parse Python: GuestCompiler is the
// semantic oracle for the target Guest's parser/compiler.
package streaming

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

const (
	// SourceDigestPrefix is used for all source and suite digests emitted by the
	// stream. Digests are over the exact UTF-8/source bytes supplied in chunks.
	SourceDigestPrefix = "sha256:"
	// MaxSourceBytes is the shared aggregate source and staged-result bound.
	MaxSourceBytes = 1 << 20
)

var (
	ErrCompilerRequired  = errors.New("GuestCompiler is required")
	ErrStreamNotBegun    = errors.New("source stream has not begun")
	ErrStreamEnded       = errors.New("source stream has already ended")
	ErrStreamCancelled   = errors.New("source stream has been cancelled")
	ErrStreamFailed      = errors.New("source stream has failed")
	ErrGuestRejected     = errors.New("Guest rejected source")
	ErrGuestIncomplete   = errors.New("Guest reports incomplete source")
	ErrGuestCompiler     = errors.New("Guest compiler oracle failed")
	ErrInvalidProtocol   = errors.New("invalid source stream protocol")
	ErrInvalidSuiteRange = errors.New("Guest returned an invalid suite range")
	ErrSuiteRevoked      = errors.New("Guest revoked an admitted suite")
	ErrPreambleChanged   = errors.New("Guest changed the frozen preamble")
	ErrPreambleInvalid   = errors.New("Guest returned an invalid preamble")
	ErrPreambleUnfrozen  = errors.New("Guest did not freeze the source preamble")
	ErrSourceDigest      = errors.New("source digest mismatch")
	ErrSourceTooLarge    = errors.New("source stream exceeds one MiB")
)

// CompileStatus is the target Guest compiler's semantic verdict for the
// complete source prefix passed to GuestCompiler. The Host must not infer any
// of these values from Python text.
type CompileStatus uint8

const (
	CompileIncomplete CompileStatus = iota
	CompileComplete
	CompileInvalid
)

// ByteRange is a half-open byte range [Start, End) in the append-only source.
type ByteRange struct {
	Start int
	End   int
}

func (r ByteRange) valid(sourceLength int) bool {
	return r.Start >= 0 && r.End > r.Start && r.End <= sourceLength
}

// Preamble is the Guest-reported module preamble boundary. End is retained as
// a convenience for Guest adapters; Range is the canonical half-open range.
// A frozen preamble may never move after the Guest reports Frozen=true.
type Preamble struct {
	Range  ByteRange
	End    int
	Digest string
	Frozen bool
}

// CompileResult is returned by the injected target-Guest compiler oracle.
// CompleteSuites must describe all complete top-level suites in the current
// source prefix, in source order. The state machine validates only protocol
// invariants and byte identities; Python completeness and validity remain the
// Guest's decision.
type CompileResult struct {
	Status          CompileStatus
	CompleteSuites  []ByteRange
	Preamble        Preamble
	RejectionReason string
}

// GuestCompiler is the semantic oracle. Implementations should invoke the
// exact target Guest parser/compiler and must not be replaced by Host parsing.
type GuestCompiler interface {
	Compile(source []byte) (CompileResult, error)
}

// SuiteRecord is an admitted complete suite bound to its exact source bytes.
type SuiteRecord struct {
	Range  ByteRange
	Digest string
}

// SourceSeal is the immutable S1 admission record produced by End.
type SourceSeal struct {
	Digest   string
	Suites   []SuiteRecord
	Preamble SuiteRecord
}

// BindObservation permits final-source binding only when the observation's
// suite identity was admitted by this exact source seal.
func (seal SourceSeal) BindObservation(record *StagedObservation) error {
	if record == nil || !validObservationDigest(seal.Digest) {
		return ErrStagedObservationMismatch
	}
	record.mu.Lock()
	identity := record.identity
	disposition := record.disposition
	record.mu.Unlock()
	if disposition != ObservationReady || identity.SourceSHA256 != "" || identity.BindingKind != ObservationBindingStreamSuite {
		return ErrStagedObservationTerminal
	}
	admitted := false
	for _, suite := range seal.Suites {
		if suite.Range == identity.SuiteRange && suite.Digest == identity.SuiteSHA256 {
			admitted = true
			break
		}
	}
	if !admitted {
		return ErrStagedObservationMismatch
	}
	_, err := record.BindSource(seal.Digest)
	return err
}

type streamState uint8

const (
	streamNew streamState = iota
	streamOpen
	streamEnded
	streamCancelled
	streamFailed
)

// SourceStream is a bounded append-only source state machine. It has no Python
// parser and no execution authority.
type SourceStream struct {
	compiler       GuestCompiler
	state          streamState
	source         []byte
	last           CompileResult
	seen           bool
	records        []SuiteRecord
	preamble       SuiteRecord
	hasPreamble    bool
	preambleFrozen bool
}

// NewSourceStream constructs a stream. Begin is explicit so callers cannot
// accidentally admit chunks without an event boundary.
func NewSourceStream(compiler GuestCompiler) (*SourceStream, error) {
	if compiler == nil {
		return nil, ErrCompilerRequired
	}
	return &SourceStream{compiler: compiler, state: streamNew}, nil
}

// Begin opens the append-only stream. It may be called exactly once.
func (stream *SourceStream) Begin() error {
	if stream == nil {
		return ErrInvalidProtocol
	}
	switch stream.state {
	case streamNew:
		stream.state = streamOpen
		return nil
	case streamEnded:
		return ErrStreamEnded
	case streamCancelled:
		return ErrStreamCancelled
	case streamFailed:
		return ErrStreamFailed
	default:
		return fmt.Errorf("%w: duplicate begin", ErrInvalidProtocol)
	}
}

// Chunk appends bytes and asks the Guest oracle to classify the complete
// prefix. Empty chunks are protocol-safe no-ops; End performs the final oracle
// call regardless.
func (stream *SourceStream) Chunk(chunk []byte) error {
	if err := stream.requireOpen(); err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}
	if len(stream.source) > MaxSourceBytes || len(chunk) > MaxSourceBytes-len(stream.source) {
		stream.state = streamFailed
		return ErrSourceTooLarge
	}
	stream.source = append(stream.source, chunk...)
	if err := stream.observe(); err != nil {
		return err
	}
	return nil
}

// End performs a final target-Guest oracle call and seals only a Guest-complete
// source. A failed or incomplete stream cannot be sealed.
func (stream *SourceStream) End() (SourceSeal, error) {
	if stream == nil {
		return SourceSeal{}, ErrInvalidProtocol
	}
	switch stream.state {
	case streamNew:
		return SourceSeal{}, ErrStreamNotBegun
	case streamEnded:
		return SourceSeal{}, ErrStreamEnded
	case streamCancelled:
		return SourceSeal{}, ErrStreamCancelled
	case streamFailed:
		return SourceSeal{}, ErrStreamFailed
	}
	if err := stream.observe(); err != nil {
		return SourceSeal{}, err
	}
	if stream.last.Status == CompileIncomplete {
		stream.state = streamFailed
		return SourceSeal{}, ErrGuestIncomplete
	}
	if stream.last.Status != CompileComplete {
		stream.state = streamFailed
		return SourceSeal{}, ErrGuestRejected
	}
	if !stream.preambleFrozen {
		stream.state = streamFailed
		return SourceSeal{}, ErrPreambleUnfrozen
	}
	stream.state = streamEnded
	return stream.seal(), nil
}

// Cancel abandons the stream. It never invokes the Guest oracle and produces
// no seal or publication record.
func (stream *SourceStream) Cancel() error {
	if stream == nil {
		return ErrInvalidProtocol
	}
	switch stream.state {
	case streamNew, streamOpen:
		stream.state = streamCancelled
		return nil
	case streamCancelled:
		return ErrStreamCancelled
	case streamEnded:
		return ErrStreamEnded
	case streamFailed:
		return ErrStreamFailed
	default:
		return ErrInvalidProtocol
	}
}

// Source returns a defensive copy of the bytes accepted so far. It is useful
// to adapters that bind a sealed source to a later execution phase.
func (stream *SourceStream) Source() []byte {
	if stream == nil {
		return nil
	}
	return append([]byte(nil), stream.source...)
}

// State is a stable diagnostic label, not a semantic Python verdict.
func (stream *SourceStream) State() string {
	if stream == nil {
		return "invalid"
	}
	switch stream.state {
	case streamNew:
		return "new"
	case streamOpen:
		return "open"
	case streamEnded:
		return "ended"
	case streamCancelled:
		return "cancelled"
	case streamFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func (stream *SourceStream) requireOpen() error {
	if stream == nil {
		return ErrInvalidProtocol
	}
	switch stream.state {
	case streamNew:
		return ErrStreamNotBegun
	case streamOpen:
		return nil
	case streamEnded:
		return ErrStreamEnded
	case streamCancelled:
		return ErrStreamCancelled
	case streamFailed:
		return ErrStreamFailed
	default:
		return ErrInvalidProtocol
	}
}

func (stream *SourceStream) observe() error {
	result, err := stream.compiler.Compile(append([]byte(nil), stream.source...))
	if err != nil {
		stream.state = streamFailed
		return fmt.Errorf("%w: %v", ErrGuestCompiler, err)
	}
	if result.Status != CompileIncomplete && result.Status != CompileComplete && result.Status != CompileInvalid {
		stream.state = streamFailed
		return fmt.Errorf("%w: unknown compile status %d", ErrInvalidProtocol, result.Status)
	}
	if result.Status == CompileInvalid {
		stream.state = streamFailed
		if result.RejectionReason == "" {
			return ErrGuestRejected
		}
		return fmt.Errorf("%w: %s", ErrGuestRejected, result.RejectionReason)
	}
	if err := stream.observePreamble(result.Preamble); err != nil {
		stream.state = streamFailed
		return err
	}
	if err := stream.observeSuites(result.CompleteSuites); err != nil {
		stream.state = streamFailed
		return err
	}
	stream.last = result
	stream.seen = true
	return nil
}

func (stream *SourceStream) observePreamble(preamble Preamble) error {
	rangeValue, err := preambleRange(preamble)
	if err != nil {
		return err
	}
	if rangeValue.End > len(stream.source) {
		return fmt.Errorf("%w: range %+v exceeds source length %d", ErrPreambleInvalid, rangeValue, len(stream.source))
	}
	expectedDigest := digest(stream.source[rangeValue.Start:rangeValue.End])
	if preamble.Digest != "" && preamble.Digest != expectedDigest {
		return fmt.Errorf("%w: preamble=%q expected=%q", ErrSourceDigest, preamble.Digest, expectedDigest)
	}
	if stream.hasPreamble {
		if stream.preambleFrozen {
			if !preamble.Frozen || rangeValue != stream.preamble.Range {
				return fmt.Errorf("%w: prior=%+v next=%+v", ErrPreambleChanged, stream.preamble.Range, rangeValue)
			}
		} else if rangeValue.Start != stream.preamble.Range.Start || rangeValue.End < stream.preamble.Range.End {
			return fmt.Errorf("%w: prior=%+v next=%+v", ErrPreambleChanged, stream.preamble.Range, rangeValue)
		}
		if stream.preamble.Range == rangeValue && stream.preamble.Digest != expectedDigest {
			return ErrSourceDigest
		}
	} else if rangeValue.Start != 0 {
		return fmt.Errorf("%w: preamble must begin at byte zero", ErrPreambleInvalid)
	}
	if preamble.Frozen {
		if stream.hasPreamble && stream.preamble.Range != rangeValue {
			return ErrPreambleChanged
		}
		stream.preamble = SuiteRecord{Range: rangeValue, Digest: expectedDigest}
		stream.hasPreamble = true
		stream.preambleFrozen = true
	} else if rangeValue.End > 0 {
		// Keep an unfrozen boundary as a monotonic candidate. It is not emitted
		// as a seal until the Guest freezes it.
		stream.preamble.Range = rangeValue
		stream.preamble.Digest = expectedDigest
		stream.hasPreamble = true
	}
	return nil
}

func preambleRange(preamble Preamble) (ByteRange, error) {
	if preamble.End < 0 || (preamble.Range.End != 0 && preamble.End != 0 && preamble.Range.End != preamble.End) {
		return ByteRange{}, fmt.Errorf("%w: conflicting range=%+v end=%d", ErrPreambleInvalid, preamble.Range, preamble.End)
	}
	rangeValue := preamble.Range
	if rangeValue == (ByteRange{}) && preamble.End != 0 {
		rangeValue = ByteRange{End: preamble.End}
	}
	if rangeValue.Start < 0 || rangeValue.End < 0 || rangeValue.Start != 0 || rangeValue.End < rangeValue.Start {
		return ByteRange{}, fmt.Errorf("%w: %+v", ErrPreambleInvalid, rangeValue)
	}
	return rangeValue, nil
}

func (stream *SourceStream) observeSuites(suites []ByteRange) error {
	previous := ByteRange{}
	for index, suite := range suites {
		if !suite.valid(len(stream.source)) {
			return fmt.Errorf("%w: suite[%d]=%+v source_length=%d", ErrInvalidSuiteRange, index, suite, len(stream.source))
		}
		if index > 0 && suite.Start <= previous.Start {
			return fmt.Errorf("%w: suites are not in source order: %+v then %+v", ErrInvalidSuiteRange, previous, suite)
		}
		if index > 0 && suite.Start < previous.End {
			return fmt.Errorf("%w: overlapping suites %+v and %+v", ErrInvalidSuiteRange, previous, suite)
		}
		previous = suite
	}
	if stream.seen {
		for _, admitted := range stream.records {
			found := false
			for _, suite := range suites {
				if suite == admitted.Range {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: suite=%+v", ErrSuiteRevoked, admitted.Range)
			}
		}
	}
	for _, suite := range suites {
		for _, admitted := range stream.records {
			if suite == admitted.Range {
				if admitted.Digest != digest(stream.source[suite.Start:suite.End]) {
					return fmt.Errorf("%w: suite=%+v", ErrSourceDigest, suite)
				}
				continue
			}
			if suite.Start < admitted.Range.End && admitted.Range.Start < suite.End {
				return fmt.Errorf("%w: overlapping admitted suite=%+v next=%+v", ErrInvalidSuiteRange, admitted.Range, suite)
			}
		}
		found := false
		for _, admitted := range stream.records {
			if admitted.Range == suite {
				found = true
				break
			}
		}
		if !found {
			stream.records = append(stream.records, SuiteRecord{Range: suite, Digest: digest(stream.source[suite.Start:suite.End])})
		}
	}
	sort.Slice(stream.records, func(left, right int) bool {
		return stream.records[left].Range.Start < stream.records[right].Range.Start
	})
	return nil
}

func (stream *SourceStream) seal() SourceSeal {
	return SourceSeal{
		Digest:   digest(stream.source),
		Suites:   append([]SuiteRecord(nil), stream.records...),
		Preamble: stream.preamble,
	}
}

func digest(source []byte) string {
	sum := sha256.Sum256(source)
	return fmt.Sprintf("%s%x", SourceDigestPrefix, sum[:])
}
