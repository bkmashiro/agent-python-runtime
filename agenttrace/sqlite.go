package agenttrace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = 1

type SQLiteStore struct {
	db   *sql.DB
	path string
}

type Playback struct {
	AgentRunID string
	Events     []Event
}

type ForkPlan struct {
	SourceAgentRunID string `json:"source_agent_run_id"`
	AgentRunID       string `json:"agent_run_id"`
	ThroughSequence  uint64 `json:"through_sequence"`
	ParentEventID    string `json:"parent_event_id"`
	StateFingerprint string `json:"state_fingerprint"`
	PrefixDigest     string `json:"prefix_digest"`
}

var sqliteSchema = []string{
	`CREATE TABLE trace_schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at_ns INTEGER NOT NULL,
		schema_digest TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE trace_events (
		event_id TEXT PRIMARY KEY,
		agent_run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL CHECK(sequence > 0),
		event_type TEXT NOT NULL,
		observed_at_ns INTEGER NOT NULL,
		parent_event_id TEXT NOT NULL,
		payload BLOB NOT NULL,
		payload_digest TEXT NOT NULL,
		state_fingerprint TEXT NOT NULL,
		version TEXT NOT NULL,
		UNIQUE(agent_run_id, sequence)
	) STRICT`,
	`CREATE INDEX trace_events_run_order ON trace_events(agent_run_id, sequence)`,
}

func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidPlugin
	}
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return nil, fmt.Errorf("create trace store: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("close new trace store: %w", closeErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect trace store: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidPlugin
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure trace store: %w", err)
	}
	location := url.URL{Scheme: "file", Path: path}
	query := location.Query()
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, fmt.Errorf("open trace store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &SQLiteStore{db: db, path: path}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping trace store: %w", err)
	}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	store.secureFiles()
	return store, nil
}

func (store *SQLiteStore) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	err := store.db.Close()
	store.secureFiles()
	return err
}

func (store *SQLiteStore) secureFiles() {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(store.path+suffix, 0o600)
	}
}

func (store *SQLiteStore) migrate() error {
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin trace migration: %w", err)
	}
	defer tx.Rollback()
	var migrationTable int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='trace_schema_migrations'`).Scan(&migrationTable); err != nil {
		return fmt.Errorf("inspect trace schema: %w", err)
	}
	if migrationTable == 0 {
		var unknown int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&unknown); err != nil || unknown != 0 {
			return ErrIntegrity
		}
		for _, statement := range sqliteSchema {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("create trace schema: %w", err)
			}
		}
		digest, err := traceSchemaDigest(tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO trace_schema_migrations(version,applied_at_ns,schema_digest) VALUES(?,?,?)`, sqliteSchemaVersion, time.Now().UTC().UnixNano(), digest); err != nil {
			return fmt.Errorf("record trace migration: %w", err)
		}
	} else {
		var version int
		var stored string
		if err := tx.QueryRow(`SELECT version,schema_digest FROM trace_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &stored); err != nil || version != sqliteSchemaVersion {
			return ErrIntegrity
		}
		actual, err := traceSchemaDigest(tx)
		if err != nil || stored != actual {
			return ErrIntegrity
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit trace migration: %w", err)
	}
	return nil
}

func traceSchemaDigest(tx *sql.Tx) (string, error) {
	rows, err := tx.Query(`SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var objectType, name, table, statement string
		if err := rows.Scan(&objectType, &name, &table, &statement); err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\n", objectType, name, table, statement)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func (store *SQLiteStore) Append(ctx context.Context, event Event) error {
	if store == nil || store.db == nil {
		return ErrAppend
	}
	if err := event.Validate(); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAppend, err)
	}
	defer tx.Rollback()
	if event.ParentEventID != "" {
		var parentRunID string
		var parentSequence uint64
		if err := tx.QueryRowContext(ctx, `SELECT agent_run_id,sequence FROM trace_events WHERE event_id=?`, event.ParentEventID).Scan(&parentRunID, &parentSequence); err != nil || parentRunID != event.AgentRunID || parentSequence >= event.Sequence {
			return ErrIntegrity
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO trace_events(
		event_id,agent_run_id,sequence,event_type,observed_at_ns,parent_event_id,payload,payload_digest,state_fingerprint,version
	) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.EventID, event.AgentRunID, event.Sequence, string(event.EventType), event.ObservedAt.UnixNano(),
		event.ParentEventID, []byte(event.Payload), event.PayloadDigest, event.StateFingerprint, event.Version)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return fmt.Errorf("%w: %v", ErrAppend, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %v", ErrAppend, err)
	}
	store.secureFiles()
	return nil
}

func (store *SQLiteStore) Events(ctx context.Context, agentRunID string, after uint64, limit uint32) ([]Event, error) {
	if store == nil || store.db == nil || !boundedIdentifier(agentRunID, 128) || limit == 0 || limit > 1000 {
		return nil, ErrInvalidEvent
	}
	rows, err := store.db.QueryContext(ctx, `SELECT event_id,sequence,event_type,observed_at_ns,parent_event_id,payload,payload_digest,state_fingerprint,version
		FROM trace_events WHERE agent_run_id=? AND sequence>? ORDER BY sequence LIMIT ?`, agentRunID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query trace events: %w", err)
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var observedAt int64
		var eventType string
		var payload []byte
		if err := rows.Scan(&event.EventID, &event.Sequence, &eventType, &observedAt, &event.ParentEventID, &payload,
			&event.PayloadDigest, &event.StateFingerprint, &event.Version); err != nil {
			return nil, fmt.Errorf("scan trace event: %w", err)
		}
		event.AgentRunID = agentRunID
		event.EventType = EventType(eventType)
		event.ObservedAt = time.Unix(0, observedAt).UTC()
		event.Payload = append([]byte(nil), payload...)
		if err := event.Validate(); err != nil {
			return nil, ErrIntegrity
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trace events: %w", err)
	}
	return events, nil
}

func (store *SQLiteStore) LoadPlayback(ctx context.Context, agentRunID string) (Playback, error) {
	var all []Event
	var after uint64
	for {
		page, err := store.Events(ctx, agentRunID, after, 1000)
		if err != nil {
			return Playback{}, err
		}
		all = append(all, page...)
		if len(page) < 1000 {
			break
		}
		after = page[len(page)-1].Sequence
	}
	if len(all) == 0 {
		return Playback{}, ErrInvalidEvent
	}
	seen := make(map[string]struct{}, len(all))
	for index, event := range all {
		if event.Sequence != uint64(index+1) {
			return Playback{}, ErrIntegrity
		}
		if event.ParentEventID != "" {
			if _, exists := seen[event.ParentEventID]; !exists {
				return Playback{}, ErrIntegrity
			}
		}
		seen[event.EventID] = struct{}{}
	}
	return Playback{AgentRunID: agentRunID, Events: all}, nil
}

func (playback Playback) ForkAt(sequence uint64, agentRunID string) (ForkPlan, error) {
	if playback.AgentRunID == "" || agentRunID == playback.AgentRunID || !boundedIdentifier(agentRunID, 128) || sequence == 0 || sequence > uint64(len(playback.Events)) {
		return ForkPlan{}, ErrInvalidEvent
	}
	checkpoint := playback.Events[sequence-1]
	if checkpoint.Sequence != sequence || checkpoint.EventType != EventCheckpointCreated || checkpoint.StateFingerprint == "" {
		return ForkPlan{}, ErrInvalidEvent
	}
	hash := sha256.New()
	for _, event := range playback.Events[:sequence] {
		fmt.Fprintf(hash, "%s\x00%s\n", event.EventID, event.PayloadDigest)
	}
	return ForkPlan{
		SourceAgentRunID: playback.AgentRunID, AgentRunID: agentRunID, ThroughSequence: sequence,
		ParentEventID: checkpoint.EventID, StateFingerprint: checkpoint.StateFingerprint,
		PrefixDigest: fmt.Sprintf("sha256:%x", hash.Sum(nil)),
	}, nil
}
