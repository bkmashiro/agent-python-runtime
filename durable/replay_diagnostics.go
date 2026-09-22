package durable

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// DefaultReplayHistoryPageSize is used when a caller omits a page size.
	DefaultReplayHistoryPageSize uint32 = 32
	// MaxReplayHistoryPageSize bounds rows returned by one diagnostics query.
	MaxReplayHistoryPageSize uint32 = 64
)

// ReplayHistoryOptions controls one read-only page of persisted call records.
// FromSequence is inclusive. Payloads are excluded unless IncludePayloads is
// explicitly true because arguments and outcomes may contain secrets.
type ReplayHistoryOptions struct {
	FromSequence    uint32
	Limit           uint32
	IncludePayloads bool
}

// ReplayHistoryPage is an ordered, bounded view of a Run's existing call
// journal. NextSequence is meaningful only when HasMore is true.
type ReplayHistoryPage struct {
	Calls        []ReplayCall `json:"calls"`
	NextSequence uint32       `json:"next_sequence,omitempty"`
	HasMore      bool         `json:"has_more"`
}

// ReplayCall is a diagnostic projection of one persisted journal call. The
// journal does not persist which recovery route handled a call, so history
// deliberately does not claim to know whether it was dispatched, replayed,
// looked up, or waited.
type ReplayCall struct {
	Sequence     uint32          `json:"sequence"`
	CallID       string          `json:"call_id"`
	Capability   string          `json:"capability"`
	Version      string          `json:"version,omitempty"`
	State        string          `json:"state"`
	OperationKey string          `json:"operation_key"`
	OutcomeClass OutcomeClass    `json:"outcome_class"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Outcome      json.RawMessage `json:"outcome,omitempty"`
}

// OutcomeClass is a coarse classification that does not expose the persisted
// result. Unknown is used for malformed or otherwise unclassified outcomes.
type OutcomeClass string

const (
	OutcomeUnknown OutcomeClass = "unknown"
	OutcomePending OutcomeClass = "pending"
	OutcomeSuccess OutcomeClass = "success"
	OutcomeError   OutcomeClass = "error"
)

// HistoryMismatchField identifies the first call-history field that differed.
type HistoryMismatchField string

const (
	HistoryMismatchSequence     HistoryMismatchField = "sequence"
	HistoryMismatchCallID       HistoryMismatchField = "call_id"
	HistoryMismatchCapability   HistoryMismatchField = "capability"
	HistoryMismatchOperationKey HistoryMismatchField = "operation_key"
	HistoryMismatchArguments    HistoryMismatchField = "arguments"
)

// HistoryMismatchError preserves errors.Is(ErrHistoryMismatch) while exposing
// safe structural details. It intentionally carries no expected or received
// payloads; use ReplayHistory with IncludePayloads only when raw data is needed.
type HistoryMismatchError struct {
	RunID    string
	Sequence uint32
	Field    HistoryMismatchField
}

func (err *HistoryMismatchError) Error() string {
	if err == nil {
		return ErrHistoryMismatch.Error()
	}
	return fmt.Sprintf("%s: run=%q sequence=%d field=%s", ErrHistoryMismatch, err.RunID, err.Sequence, err.Field)
}

func (err *HistoryMismatchError) Unwrap() error { return ErrHistoryMismatch }

func historyMismatch(runID string, sequence uint32, field HistoryMismatchField) error {
	return &HistoryMismatchError{RunID: runID, Sequence: sequence, Field: field}
}

// ReplayHistory returns one bounded, ordered page from the existing calls
// table. It never changes durable state or executes a tool.
func (s *Store) ReplayHistory(ctx context.Context, runID string, options ReplayHistoryOptions) (ReplayHistoryPage, error) {
	if s == nil || s.db == nil || runID == "" {
		return ReplayHistoryPage{}, ErrNotFound
	}
	run, err := s.Get(ctx, runID)
	if err != nil {
		return ReplayHistoryPage{}, err
	}
	limit := options.Limit
	if limit == 0 {
		limit = DefaultReplayHistoryPageSize
	}
	if limit > MaxReplayHistoryPageSize {
		limit = MaxReplayHistoryPageSize
	}
	versions, err := toolVersionsForRun(run.Definition.Tools)
	if err != nil {
		return ReplayHistoryPage{}, err
	}

	query := `SELECT sequence, call_id, tool, operation_key, state,
			CASE
				WHEN state <> 'completed' THEN 'pending'
				WHEN outcome IS NULL OR length(outcome)=0 OR json_valid(outcome) <> 1 THEN 'unknown'
				WHEN json_type(outcome) <> 'object' THEN 'unknown'
				WHEN json_type(outcome, '$.error') IN ('integer', 'real', 'true', 'false', 'array', 'object') THEN 'unknown'
				WHEN json_type(outcome, '$.error') = 'text' AND json_extract(outcome, '$.error') <> '' THEN 'error'
				ELSE 'success'
			END AS outcome_class`
	argsColumn := ""
	if options.IncludePayloads {
		argsColumn = ", arguments, outcome"
	}
	query += argsColumn + `
		FROM calls WHERE run_id=? AND sequence>=? ORDER BY sequence LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, runID, options.FromSequence, limit+1)
	if err != nil {
		return ReplayHistoryPage{}, err
	}
	defer rows.Close()

	page := ReplayHistoryPage{Calls: make([]ReplayCall, 0, limit)}
	for rows.Next() {
		var (
			call ReplayCall
		)
		var scanArgs []any
		scanArgs = append(scanArgs, &call.Sequence, &call.CallID, &call.Capability, &call.OperationKey,
			&call.State, &call.OutcomeClass)
		var arguments, outcome []byte
		if options.IncludePayloads {
			scanArgs = append(scanArgs, &arguments, &outcome)
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return ReplayHistoryPage{}, err
		}
		if uint32(len(page.Calls)) == limit {
			page.HasMore = true
			break
		}
		call.Version = versions[call.Capability]
		if options.IncludePayloads {
			call.Arguments = cloneRaw(arguments)
			call.Outcome = cloneRaw(outcome)
		}
		page.Calls = append(page.Calls, call)
	}
	if err := rows.Err(); err != nil {
		return ReplayHistoryPage{}, err
	}
	if page.HasMore && len(page.Calls) != 0 {
		last := page.Calls[len(page.Calls)-1].Sequence
		if last == ^uint32(0) {
			page.HasMore = false
		} else {
			page.NextSequence = last + 1
		}
	}
	return page, nil
}

// ReplayHistory exposes the same read-only diagnostics through the Runner Host
// API without adding another persistence path.
func (runner *Runner) ReplayHistory(ctx context.Context, runID string, options ReplayHistoryOptions) (ReplayHistoryPage, error) {
	if runner == nil || runner.store == nil || runID == "" {
		return ReplayHistoryPage{}, ErrInvalidRunner
	}
	return runner.store.ReplayHistory(ctx, runID, options)
}

func toolVersionsForRun(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	var declarations []toolDeclaration
	if err := json.Unmarshal(raw, &declarations); err != nil {
		return nil, fmt.Errorf("decode durable tool declarations: %w", err)
	}
	versions := make(map[string]string, len(declarations))
	for _, declaration := range declarations {
		if declaration.Name != "" {
			versions[declaration.Name] = declaration.Version
		}
	}
	return versions, nil
}
