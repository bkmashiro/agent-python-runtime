package agenttrace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqliteSchemaVersion = 1
	maxPlaybackEvents   = 4096
	maxPlaybackBytes    = 8 * 1024 * 1024
	playbackPageSize    = 128
)

type SQLiteStore struct {
	db       *sql.DB
	path     string
	readOnly bool
}

type RunSummary struct {
	AgentRunID             string    `json:"agent_run_id"`
	FirstObservedAt        time.Time `json:"first_observed_at"`
	LastObservedAt         time.Time `json:"last_observed_at"`
	EventCount             uint64    `json:"event_count"`
	CheckpointCount        uint64    `json:"checkpoint_count"`
	RuntimeInvocationCount uint64    `json:"runtime_invocation_count"`
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

func OpenSQLiteStoreReadOnly(path string) (*SQLiteStore, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidPlugin
	}
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidPlugin
	}
	location := url.URL{Scheme: "file", Path: path}
	query := location.Query()
	query.Set("mode", "ro")
	for _, pragma := range []string{"query_only(1)", "busy_timeout(5000)"} {
		query.Add("_pragma", pragma)
	}
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, fmt.Errorf("open trace store read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &SQLiteStore{db: db, path: path, readOnly: true}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping trace store read-only: %w", err)
	}
	if err := store.validateSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (store *SQLiteStore) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	err := store.db.Close()
	if !store.readOnly {
		store.secureFiles()
	}
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

func (store *SQLiteStore) validateSchema() error {
	tx, err := store.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrIntegrity
	}
	defer tx.Rollback()
	var version int
	var stored string
	if err := tx.QueryRow(`SELECT version,schema_digest FROM trace_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &stored); err != nil || version != sqliteSchemaVersion {
		return ErrIntegrity
	}
	actual, err := traceSchemaDigest(tx)
	if err != nil || stored != actual {
		return ErrIntegrity
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
	if store == nil || store.db == nil || store.readOnly {
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

func (store *SQLiteStore) Runs(ctx context.Context, limit uint32) ([]RunSummary, error) {
	if store == nil || store.db == nil || limit == 0 || limit > 1000 {
		return nil, ErrInvalidEvent
	}
	rows, err := store.db.QueryContext(ctx, `SELECT agent_run_id,MIN(observed_at_ns),MAX(observed_at_ns),COUNT(*),
		SUM(CASE WHEN event_type=? THEN 1 ELSE 0 END),SUM(CASE WHEN event_type=? THEN 1 ELSE 0 END)
		FROM trace_events GROUP BY agent_run_id ORDER BY MAX(observed_at_ns) DESC,agent_run_id LIMIT ?`, string(EventCheckpointCreated), string(EventRuntimeStarted), limit)
	if err != nil {
		return nil, fmt.Errorf("query trace runs: %w", err)
	}
	defer rows.Close()
	summaries := make([]RunSummary, 0)
	for rows.Next() {
		var summary RunSummary
		var first, last int64
		if err := rows.Scan(&summary.AgentRunID, &first, &last, &summary.EventCount, &summary.CheckpointCount, &summary.RuntimeInvocationCount); err != nil {
			return nil, fmt.Errorf("scan trace run: %w", err)
		}
		summary.FirstObservedAt = time.Unix(0, first).UTC()
		summary.LastObservedAt = time.Unix(0, last).UTC()
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trace runs: %w", err)
	}
	return summaries, nil
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
	totalBytes := len(agentRunID)
	for {
		page, err := store.Events(ctx, agentRunID, after, uint32(playbackPageSize))
		if err != nil {
			return Playback{}, err
		}
		if len(all)+len(page) > maxPlaybackEvents {
			return Playback{}, ErrIntegrity
		}
		for _, event := range page {
			var ok bool
			totalBytes, ok = addPlaybackEventBytes(totalBytes, event)
			if !ok {
				return Playback{}, ErrIntegrity
			}
		}
		all = append(all, page...)
		if len(page) < playbackPageSize {
			break
		}
		after = page[len(page)-1].Sequence
	}
	playback := Playback{AgentRunID: agentRunID, Events: all}
	if _, err := playback.IntegrityDigest(); err != nil {
		return Playback{}, err
	}
	return playback, nil
}

func (playback Playback) ValidateBounds() error {
	if playback.AgentRunID == "" || len(playback.Events) == 0 {
		return ErrInvalidEvent
	}
	if len(playback.Events) > maxPlaybackEvents || len(playback.AgentRunID) > maxPlaybackBytes {
		return ErrIntegrity
	}
	totalBytes := len(playback.AgentRunID)
	for _, event := range playback.Events {
		var ok bool
		totalBytes, ok = addPlaybackEventBytes(totalBytes, event)
		if !ok {
			return ErrIntegrity
		}
	}
	return nil
}

func addPlaybackEventBytes(total int, event Event) (int, bool) {
	fields := []string{
		event.Version, event.EventID, event.AgentRunID, string(event.EventType), event.ParentEventID,
		event.PayloadDigest, event.StateFingerprint,
	}
	for _, field := range fields {
		if total > maxPlaybackBytes || len(field) > maxPlaybackBytes-total {
			return 0, false
		}
		total += len(field)
	}
	if total > maxPlaybackBytes || len(event.Payload) > maxPlaybackBytes-total {
		return 0, false
	}
	return total + len(event.Payload), true
}

func (playback Playback) IntegrityDigest() (string, error) {
	if err := playback.ValidateBounds(); err != nil {
		return "", err
	}
	hash := sha256.New()
	seen := make(map[string]struct{}, len(playback.Events))
	for index, event := range playback.Events {
		if event.AgentRunID != playback.AgentRunID || event.Sequence != uint64(index+1) || event.Validate() != nil {
			return "", ErrIntegrity
		}
		if _, duplicate := seen[event.EventID]; duplicate {
			return "", ErrIntegrity
		}
		if event.ParentEventID != "" {
			if _, exists := seen[event.ParentEventID]; !exists {
				return "", ErrIntegrity
			}
		}
		seen[event.EventID] = struct{}{}
		if err := writeIntegrityEvent(hash, event); err != nil {
			return "", ErrIntegrity
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func writeIntegrityEvent(writer io.Writer, event Event) error {
	encoded, err := json.Marshal(struct {
		Version          string    `json:"schema_version"`
		EventID          string    `json:"event_id"`
		AgentRunID       string    `json:"agent_run_id"`
		Sequence         uint64    `json:"sequence"`
		EventType        EventType `json:"event_type"`
		ObservedAtNS     int64     `json:"observed_at_ns"`
		ParentEventID    string    `json:"parent_event_id"`
		PayloadDigest    string    `json:"payload_digest"`
		StateFingerprint string    `json:"state_fingerprint"`
	}{
		Version: event.Version, EventID: event.EventID, AgentRunID: event.AgentRunID, Sequence: event.Sequence,
		EventType: event.EventType, ObservedAtNS: event.ObservedAt.UnixNano(), ParentEventID: event.ParentEventID,
		PayloadDigest: event.PayloadDigest, StateFingerprint: event.StateFingerprint,
	})
	if err != nil {
		return err
	}
	if _, err := writer.Write(encoded); err != nil {
		return err
	}
	_, err = writer.Write([]byte{'\n'})
	return err
}

func (playback Playback) ForkAt(sequence uint64, agentRunID string) (ForkPlan, error) {
	if _, err := playback.IntegrityDigest(); err != nil {
		return ForkPlan{}, err
	}
	if playback.AgentRunID == "" || agentRunID == playback.AgentRunID || !boundedIdentifier(agentRunID, 128) || sequence == 0 || sequence > uint64(len(playback.Events)) {
		return ForkPlan{}, ErrInvalidEvent
	}
	checkpoint := playback.Events[sequence-1]
	if checkpoint.Sequence != sequence || checkpoint.EventType != EventCheckpointCreated || checkpoint.StateFingerprint == "" {
		return ForkPlan{}, ErrInvalidEvent
	}
	hash := sha256.New()
	for _, event := range playback.Events[:sequence] {
		if err := writeIntegrityEvent(hash, event); err != nil {
			return ForkPlan{}, ErrIntegrity
		}
	}
	return ForkPlan{
		SourceAgentRunID: playback.AgentRunID, AgentRunID: agentRunID, ThroughSequence: sequence,
		ParentEventID: checkpoint.EventID, StateFingerprint: checkpoint.StateFingerprint,
		PrefixDigest: fmt.Sprintf("sha256:%x", hash.Sum(nil)),
	}, nil
}

func (plan ForkPlan) Validate() error {
	if !boundedIdentifier(plan.SourceAgentRunID, 128) || !boundedIdentifier(plan.AgentRunID, 128) ||
		plan.SourceAgentRunID == plan.AgentRunID || plan.ThroughSequence == 0 ||
		!boundedIdentifier(plan.ParentEventID, 160) || !validDigest(plan.StateFingerprint) || !validDigest(plan.PrefixDigest) {
		return ErrInvalidEvent
	}
	return nil
}
