package semantic

import (
	"errors"
	"sync"
)

var (
	ErrPreparedRegionMissing  = errors.New("prepared region decision is missing")
	ErrPreparedRegionUnready  = errors.New("prepared region decision is not ready")
	ErrPreparedRegionConsumed = errors.New("prepared region decision is already consumed")
	ErrPreparedRegionClosed   = errors.New("prepared region table is closed")
)

const PreparedRegionTableEvidenceSchemaVersion = "pysolate.prepared-region-table-evidence.v1"

type PreparedRegionEntry struct {
	Decision PreparedRegionDecision
	Capsule  PreparedRegionCapsule
}

type PreparedRegionTableEvidence struct {
	SchemaVersion  string `json:"schema_version"`
	Ready          uint32 `json:"ready"`
	Unready        uint32 `json:"unready"`
	Consumed       uint32 `json:"consumed"`
	Discarded      uint32 `json:"discarded"`
	Claims         uint32 `json:"claims"`
	RejectedClaims uint32 `json:"rejected_claims"`
	PayloadBytes   uint64 `json:"payload_bytes"`
}

type preparedRegionState uint8

const (
	preparedRegionUnready preparedRegionState = iota
	preparedRegionReady
	preparedRegionConsumed
	preparedRegionDiscarded
)

type preparedRegionTableEntry struct {
	decision PreparedRegionDecision
	capsule  PreparedRegionCapsule
	state    preparedRegionState
}

type PreparedRegionTable struct {
	mu       sync.Mutex
	entries  map[string]*preparedRegionTableEntry
	closed   bool
	claims   uint32
	rejected uint32
}

func NewPreparedRegionTable(entries []PreparedRegionEntry) (*PreparedRegionTable, error) {
	table := &PreparedRegionTable{entries: make(map[string]*preparedRegionTableEntry, len(entries))}
	for _, candidate := range entries {
		if !candidate.Decision.valid() {
			return nil, ErrInvalidPreparedRegion
		}
		identity := candidate.Decision.IdentitySHA256
		if _, exists := table.entries[identity]; exists {
			return nil, ErrInvalidPreparedRegion
		}
		entry := &preparedRegionTableEntry{decision: candidate.Decision, state: preparedRegionUnready}
		if candidate.Capsule.IdentitySHA256 != "" {
			if err := candidate.Capsule.ValidateDecision(candidate.Decision); err != nil {
				return nil, err
			}
			entry.capsule = candidate.Capsule
			entry.state = preparedRegionReady
		}
		table.entries[identity] = entry
	}
	return table, nil
}

func (table *PreparedRegionTable) Claim(decisionSHA256 string) ([]byte, error) {
	if table == nil {
		return nil, ErrPreparedRegionMissing
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if table.closed {
		table.rejected++
		return nil, ErrPreparedRegionClosed
	}
	entry, ok := table.entries[decisionSHA256]
	if !ok {
		table.rejected++
		return nil, ErrPreparedRegionMissing
	}
	switch entry.state {
	case preparedRegionUnready:
		table.rejected++
		return nil, ErrPreparedRegionUnready
	case preparedRegionConsumed:
		table.rejected++
		return nil, ErrPreparedRegionConsumed
	case preparedRegionDiscarded:
		table.rejected++
		return nil, ErrPreparedRegionClosed
	case preparedRegionReady:
	default:
		table.rejected++
		return nil, ErrInvalidPreparedRegion
	}
	payload := append([]byte(nil), entry.capsule.Payload...)
	entry.capsule.Payload = nil
	entry.state = preparedRegionConsumed
	table.claims++
	return payload, nil
}

func (table *PreparedRegionTable) Close() error {
	if table == nil {
		return nil
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if table.closed {
		return nil
	}
	for _, entry := range table.entries {
		if entry.state == preparedRegionReady || entry.state == preparedRegionUnready {
			entry.capsule.Payload = nil
			entry.state = preparedRegionDiscarded
		}
	}
	table.closed = true
	return nil
}

func (table *PreparedRegionTable) Evidence() PreparedRegionTableEvidence {
	evidence := PreparedRegionTableEvidence{SchemaVersion: PreparedRegionTableEvidenceSchemaVersion}
	if table == nil {
		return evidence
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	evidence.Claims = table.claims
	evidence.RejectedClaims = table.rejected
	for _, entry := range table.entries {
		switch entry.state {
		case preparedRegionReady:
			evidence.Ready++
		case preparedRegionUnready:
			evidence.Unready++
		case preparedRegionConsumed:
			evidence.Consumed++
		case preparedRegionDiscarded:
			evidence.Discarded++
		}
		evidence.PayloadBytes += uint64(entry.capsule.PayloadBytes)
	}
	return evidence
}
