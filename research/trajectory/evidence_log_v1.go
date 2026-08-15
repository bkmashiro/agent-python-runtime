package trajectory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

const maxEvidenceLogLineBytes = 2 << 20

type evidenceLogRecord struct {
	Kind   string         `json:"kind"`
	Header *TraceHeader   `json:"header,omitempty"`
	Event  *EvidenceEvent `json:"event,omitempty"`
}

type EvidenceLog struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	builder *Builder
	closed  bool
}

func CreateEvidenceLog(path string, store *labstore.Store, header TraceHeader, limits EvidenceLimits) (*EvidenceLog, error) {
	if path == "" {
		return nil, errors.New("invalid causal evidence log path")
	}
	builder, err := NewBoundedBuilder(header, store, limits)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	log := &EvidenceLog{path: path, file: file, builder: builder}
	record := evidenceLogRecord{Kind: "header", Header: &builder.header}
	if err := appendEvidenceLogRecord(file, record); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return log, nil
}

func OpenEvidenceLog(path string, store *labstore.Store, limits EvidenceLimits) (*EvidenceLog, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("invalid causal evidence log")
	}
	read, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(read)
	scanner.Buffer(make([]byte, 4096), maxEvidenceLogLineBytes)
	var builder *Builder
	line := 0
	for scanner.Scan() {
		line++
		record, err := decodeEvidenceLogRecord(scanner.Bytes())
		if err != nil {
			_ = read.Close()
			return nil, err
		}
		if line == 1 {
			if record.Kind != "header" || record.Header == nil || record.Event != nil {
				_ = read.Close()
				return nil, errors.New("invalid causal evidence log header")
			}
			header := *record.Header
			claimed := header.HeaderSHA256
			header.HeaderSHA256 = ""
			builder, err = NewBoundedBuilder(header, store, limits)
			if err != nil || builder.header.HeaderSHA256 != claimed {
				_ = read.Close()
				return nil, errors.New("invalid causal evidence log header")
			}
			continue
		}
		if builder == nil || record.Kind != "event" || record.Event == nil || record.Header != nil {
			_ = read.Close()
			return nil, errors.New("invalid causal evidence log event")
		}
		decoded, err := decodeEvidencePayload(record.Event.Type, record.Event.Payload)
		if err != nil {
			_ = read.Close()
			return nil, err
		}
		input := EvidenceInput{
			Type: record.Event.Type, ActorID: record.Event.ActorID, OccurredNanos: record.Event.OccurredNanos,
			ParentEventIDs: record.Event.ParentEventIDs, Payload: dereferenceEvidencePayload(decoded), Body: record.Event.Body,
		}
		var rebuilt EvidenceEvent
		if record.Event.Type == EventEvidenceTruncated {
			value, ok := input.Payload.(TruncationPayload)
			if !ok {
				_ = read.Close()
				return nil, errors.New("invalid causal evidence truncation")
			}
			rebuilt, err = builder.MarkTruncated(input.ActorID, value)
		} else {
			rebuilt, err = builder.Append(input)
		}
		if err != nil || !evidenceEventsEqual(rebuilt, *record.Event) {
			_ = read.Close()
			return nil, errors.New("invalid causal evidence log event")
		}
	}
	if err := scanner.Err(); err != nil || line == 0 || builder == nil {
		_ = read.Close()
		return nil, errors.New("invalid causal evidence log")
	}
	if err := read.Close(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &EvidenceLog{path: path, file: file, builder: builder}, nil
}

func (log *EvidenceLog) Append(input EvidenceInput) (EvidenceEvent, error) {
	if log == nil {
		return EvidenceEvent{}, errors.New("causal evidence log is closed")
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed || log.file == nil || log.builder == nil {
		return EvidenceEvent{}, errors.New("causal evidence log is closed")
	}
	trial := cloneBuilder(log.builder)
	event, err := trial.Append(input)
	if err != nil {
		return EvidenceEvent{}, err
	}
	if err := appendEvidenceLogRecord(log.file, evidenceLogRecord{Kind: "event", Event: &event}); err != nil {
		return EvidenceEvent{}, err
	}
	log.builder = trial
	return event, nil
}

func (log *EvidenceLog) MarkTruncated(actorID string, payload TruncationPayload) (EvidenceEvent, error) {
	if log == nil {
		return EvidenceEvent{}, errors.New("causal evidence log is closed")
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed || log.file == nil || log.builder == nil {
		return EvidenceEvent{}, errors.New("causal evidence log is closed")
	}
	trial := cloneBuilder(log.builder)
	event, err := trial.MarkTruncated(actorID, payload)
	if err != nil {
		return EvidenceEvent{}, err
	}
	if err := appendEvidenceLogRecord(log.file, evidenceLogRecord{Kind: "event", Event: &event}); err != nil {
		return EvidenceEvent{}, err
	}
	log.builder = trial
	return event, nil
}

func (log *EvidenceLog) Export(profile Profile, privacy labstore.Privacy) (Export, error) {
	if log == nil {
		return Export{}, errors.New("causal evidence log is closed")
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.builder == nil {
		return Export{}, errors.New("causal evidence log is closed")
	}
	return log.builder.Export(profile, privacy)
}

func (log *EvidenceLog) Close() error {
	if log == nil {
		return nil
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return nil
	}
	log.closed = true
	if log.file == nil {
		return nil
	}
	err := log.file.Close()
	log.file = nil
	return err
}

func appendEvidenceLogRecord(file *os.File, record evidenceLogRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > maxEvidenceLogLineBytes {
		return errors.New("invalid causal evidence log record")
	}
	encoded = append(encoded, '\n')
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	return file.Sync()
}

func decodeEvidenceLogRecord(raw []byte) (evidenceLogRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record evidenceLogRecord
	if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return evidenceLogRecord{}, errors.New("invalid causal evidence log record")
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, raw) {
		return evidenceLogRecord{}, errors.New("noncanonical causal evidence log record")
	}
	return record, nil
}

func cloneBuilder(builder *Builder) *Builder {
	cloned := *builder
	cloned.events = make([]EvidenceEvent, len(builder.events))
	for index, event := range builder.events {
		cloned.events[index] = cloneEvidenceEvent(event)
	}
	return &cloned
}

func evidenceEventsEqual(left, right EvidenceEvent) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
