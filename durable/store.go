package durable

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const (
	schemaVersion = 3
	busyTimeoutMS = 5000
	maxRunIDBytes = 128
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS runs (
		run_id TEXT PRIMARY KEY NOT NULL,
		code TEXT NOT NULL,
		seed TEXT NOT NULL,
		artifact_sha256 TEXT NOT NULL,
		environment_version TEXT NOT NULL,
		inputs BLOB,
		tools BLOB,
		status TEXT NOT NULL,
		outcome BLOB,
		reason TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS calls (
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		call_id TEXT NOT NULL,
		tool TEXT NOT NULL,
		operation_key TEXT NOT NULL UNIQUE,
		state TEXT NOT NULL,
		arguments BLOB,
		outcome BLOB,
		PRIMARY KEY (run_id, sequence),
		FOREIGN KEY (run_id) REFERENCES runs(run_id)
	)`,
	`CREATE TABLE IF NOT EXISTS waits (
		wait_id TEXT PRIMARY KEY NOT NULL,
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		kind TEXT NOT NULL,
		request BLOB,
		deadline TEXT,
		resolved INTEGER NOT NULL DEFAULT 0,
		decision_result BLOB,
		decision_error TEXT NOT NULL DEFAULT '',
		UNIQUE (run_id, sequence),
		FOREIGN KEY (run_id, sequence) REFERENCES calls(run_id, sequence)
	)`,
}

func Open(path string) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, fmt.Errorf("open durable store: path must be a filesystem database")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open durable store path: %w", err)
	}
	parent := filepath.Dir(absolute)
	if err := makePrivateParent(parent); err != nil {
		return nil, fmt.Errorf("open durable store directory: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open durable database: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protect durable database: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat durable database: %w", err)
	}
	if err := rejectHardLinkedDatabase(file, fileInfo); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close durable database probe: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve durable database path: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, fmt.Errorf("resolve durable database absolute path: %w", err)
	}
	canonical = filepath.Clean(canonical)
	canonicalParent := filepath.Dir(canonical)
	if err := makePrivateParent(canonicalParent); err != nil {
		return nil, fmt.Errorf("open durable canonical directory: %w", err)
	}

	pragmas := url.Values{}
	for _, pragma := range []string{
		"busy_timeout(" + strconv.Itoa(busyTimeoutMS) + ")",
		"journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)",
	} {
		pragmas.Add("_pragma", pragma)
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(canonical), RawQuery: pragmas.Encode()}).String()
	lockDir := canonical + ".lock"
	if err := makePrivateParent(lockDir); err != nil {
		return nil, fmt.Errorf("open durable lock directory: %w", err)
	}
	initLock, err := openInitializationLock(filepath.Join(lockDir, ".pysolate-init.lock"))
	if err != nil {
		return nil, fmt.Errorf("initialize durable store lock: %w", err)
	}
	defer releaseRunLock(initLock)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open durable sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	store := &Store{db: db, lockDir: lockDir}
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping durable sqlite: %w", err)
	}
	if err := store.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func openInitializationLock(path string) (*os.File, error) {
	const attempts = 200
	for attempt := 0; attempt < attempts; attempt++ {
		file, err := openRunLock(path)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, ErrBusy) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, ErrBusy
}

func makePrivateParent(parent string) error {
	info, err := os.Stat(parent)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
		return os.Chmod(parent, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", parent)
	}
	// Do not chmod an existing caller-owned parent (in particular, never chmod
	// /tmp). Lock and database files are still created with private modes.
	return nil
}

func (s *Store) configure(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin durable schema transaction: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read durable schema version: %w", err)
	}
	if version != 0 && version != schemaVersion {
		return fmt.Errorf("unsupported durable schema version %d", version)
	}
	if version == 0 {
		for _, statement := range schema {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create durable schema: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version="+strconv.Itoa(schemaVersion)); err != nil {
			return fmt.Errorf("write durable schema version: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit durable schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Create(ctx context.Context, definition Definition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM runs WHERE run_id=?", definition.ID).Scan(&exists)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runs
		(run_id, code, seed, artifact_sha256, environment_version, inputs, tools, status, outcome, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, '')`,
		definition.ID, definition.Code, definition.Seed,
		definition.ArtifactSHA256, definition.EnvironmentVersion, rawBytes(definition.Inputs), rawBytes(definition.Tools), StatusActive)
	if err != nil {
		if isConstraint(err) {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func validateDefinition(definition Definition) error {
	if definition.ID == "" {
		return fmt.Errorf("durable definition ID is required")
	}
	if len([]byte(definition.ID)) > maxRunIDBytes {
		return fmt.Errorf("durable definition ID is too long")
	}
	if definitionPayload(definition) > MaxRunPayloadBytes {
		return ErrLimit
	}
	if len(definition.Inputs) != 0 && !json.Valid(definition.Inputs) {
		return fmt.Errorf("durable definition inputs are not valid JSON")
	}
	if len(definition.Tools) != 0 && !json.Valid(definition.Tools) {
		return fmt.Errorf("durable definition tools are not valid JSON")
	}
	return nil
}

func (s *Store) Get(ctx context.Context, runID string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT run_id, code, seed, artifact_sha256,
		environment_version, inputs, tools, status, outcome, reason FROM runs WHERE run_id=?`, runID)
	var run Run
	var inputs, tools, outcome []byte
	if err := row.Scan(&run.Definition.ID, &run.Definition.Code, &run.Definition.Seed,
		&run.Definition.ArtifactSHA256, &run.Definition.EnvironmentVersion, &inputs, &tools,
		&run.Status, &outcome, &run.Reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	run.Definition.Inputs, run.Definition.Tools, run.Outcome = cloneRaw(inputs), cloneRaw(tools), cloneRaw(outcome)
	return run, nil
}

func (s *Store) Claim(runID string) (func() error, error) {
	if runID == "" || len([]byte(runID)) > maxRunIDBytes {
		return nil, ErrNotFound
	}
	if _, err := s.Get(context.Background(), runID); err != nil {
		return nil, err
	}
	path := s.lockPath(runID)
	file, err := openRunLock(path)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() { releaseErr = releaseRunLock(file) })
		return releaseErr
	}, nil
}

func (s *Store) lockPath(runID string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(runID))
	return filepath.Join(s.lockDir, ".pysolate-run-"+encoded+".lock")
}

func (s *Store) BeginCall(ctx context.Context, runID string, call LoggedCall) (Call, bool, error) {
	if len([]byte(runID)) > maxRunIDBytes {
		return Call{}, false, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Call{}, false, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE run_id=?", runID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Call{}, false, ErrNotFound
		}
		return Call{}, false, err
	}
	var record Call
	var args, outcome []byte
	err = tx.QueryRowContext(ctx, `SELECT call_id, tool, operation_key, state, arguments, outcome
		FROM calls WHERE run_id=? AND sequence=?`, runID, call.Sequence).
		Scan(&record.CallID, &record.Tool, &record.OperationKey, &record.State, &args, &outcome)
	if err == nil {
		record.RunID, record.Sequence = runID, call.Sequence
		record.Arguments, record.Outcome = cloneRaw(args), cloneRaw(outcome)
		if record.CallID != call.CallID || record.Tool != call.Capability || !bytes.Equal(record.Arguments, call.Arguments) {
			return Call{}, false, ErrHistoryMismatch
		}
		if (record.State == CallPending || record.State == CallWaiting) && (terminalStatus(status) || status == StatusBlocked) {
			return Call{}, false, ErrConflict
		}
		return record, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Call{}, false, err
	}
	if terminalStatus(status) || status == StatusBlocked || status == StatusWaiting {
		return Call{}, false, ErrConflict
	}
	var count uint64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence)+1,0) FROM calls WHERE run_id=?", runID).Scan(&count); err != nil {
		return Call{}, false, err
	}
	if count != uint64(call.Sequence) {
		return Call{}, false, ErrHistoryMismatch
	}
	opKey := operationKey(runID, call.Sequence)
	current, err := payloadSizeTx(ctx, tx, runID)
	if err != nil {
		return Call{}, false, err
	}
	if exceeds(current, callPayload(call, opKey)) {
		return Call{}, false, ErrLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO calls
		(run_id, sequence, call_id, tool, operation_key, state, arguments, outcome)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, runID, call.Sequence, call.CallID,
		call.Capability, opKey, CallPending, rawBytes(call.Arguments))
	if err != nil {
		if isConstraint(err) {
			return Call{}, false, ErrHistoryMismatch
		}
		return Call{}, false, err
	}
	record = Call{RunID: runID, Sequence: call.Sequence, CallID: call.CallID,
		Tool: call.Capability, OperationKey: opKey, State: CallPending,
		Arguments: cloneRaw(call.Arguments)}
	if err := tx.Commit(); err != nil {
		return Call{}, false, err
	}
	return record, true, nil
}

func (s *Store) CompleteCall(ctx context.Context, runID string, sequence uint32, outcome json.RawMessage) (json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state string
	var persisted []byte
	err = tx.QueryRowContext(ctx, "SELECT state, outcome FROM calls WHERE run_id=? AND sequence=?", runID, sequence).Scan(&state, &persisted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if state == CallCompleted {
		winner := cloneRaw(persisted)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return winner, nil
	}
	if state != CallPending && state != CallWaiting {
		return nil, ErrConflict
	}
	current, err := payloadSizeTx(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if exceeds(current, replacementIncrease(persisted, outcome, "", "")) {
		return nil, ErrLimit
	}
	if _, err := tx.ExecContext(ctx, "UPDATE calls SET state=?, outcome=? WHERE run_id=? AND sequence=?", CallCompleted, rawBytes(outcome), runID, sequence); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cloneRaw(outcome), nil
}

func (s *Store) EnsureWait(ctx context.Context, runID string, sequence uint32, spec WaitSpec) (Wait, error) {
	// Replay callers should GetWait first; an existing row is authoritative.
	waitID := waitKey(runID, sequence)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Wait{}, err
	}
	defer tx.Rollback()
	wait, found, err := queryWaitTx(ctx, tx, waitID)
	if err != nil {
		return Wait{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return Wait{}, err
		}
		return wait, nil
	}
	deadline := encodeDeadline(spec.Deadline)
	var callState, status string
	var args, outcome []byte
	err = tx.QueryRowContext(ctx, `SELECT state, arguments, outcome FROM calls WHERE run_id=? AND sequence=?`, runID, sequence).
		Scan(&callState, &args, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return Wait{}, ErrNotFound
	}
	if err != nil {
		return Wait{}, err
	}
	if callState == CallCompleted {
		return Wait{}, ErrConflict
	}
	if callState != CallPending && callState != CallWaiting {
		return Wait{}, ErrConflict
	}
	if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE run_id=?", runID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Wait{}, ErrNotFound
		}
		return Wait{}, err
	}
	if terminalStatus(status) || status == StatusBlocked {
		return Wait{}, ErrConflict
	}
	current, err := payloadSizeTx(ctx, tx, runID)
	if err != nil {
		return Wait{}, err
	}
	if exceeds(current, waitPayload(waitID, spec, deadline)) {
		return Wait{}, ErrLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO waits
		(wait_id, run_id, sequence, kind, request, deadline, resolved, decision_result, decision_error)
		VALUES (?, ?, ?, ?, ?, ?, 0, NULL, '')`, waitID, runID, sequence, spec.Kind,
		rawBytes(spec.Request), deadline)
	if err != nil {
		if isConstraint(err) {
			return Wait{}, ErrHistoryMismatch
		}
		return Wait{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE calls SET state=? WHERE run_id=? AND sequence=?", CallWaiting, runID, sequence); err != nil {
		return Wait{}, err
	}
	if status == StatusActive {
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET status=? WHERE run_id=?", StatusWaiting, runID); err != nil {
			return Wait{}, err
		}
	}
	wait = Wait{ID: waitID, RunID: runID, Sequence: sequence, Spec: copyWaitSpec(spec)}
	if err := tx.Commit(); err != nil {
		return Wait{}, err
	}
	return wait, nil
}

func (s *Store) GetWait(ctx context.Context, waitID string) (Wait, error) {
	wait, found, err := queryWait(ctx, s.db, waitID)
	if err != nil {
		return Wait{}, err
	}
	if !found {
		return Wait{}, ErrNotFound
	}
	return wait, nil
}

func (s *Store) ResolveWait(ctx context.Context, waitID string, decision Decision) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	wait, found, err := queryWaitTx(ctx, tx, waitID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if wait.Decision != nil {
		if bytes.Equal(wait.Decision.Result, decision.Result) && wait.Decision.Error == decision.Error {
			return tx.Commit()
		}
		return ErrConflict
	}
	current, err := payloadSizeTx(ctx, tx, wait.RunID)
	if err != nil {
		return err
	}
	if exceeds(current, decisionPayload(decision)) {
		return ErrLimit
	}
	if _, err := tx.ExecContext(ctx, `UPDATE waits SET resolved=1, decision_result=?, decision_error=? WHERE wait_id=? AND resolved=0`,
		rawBytes(decision.Result), decision.Error, waitID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET status=? WHERE run_id=? AND status=?", StatusActive, wait.RunID, StatusWaiting); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CallCount(ctx context.Context, runID string) (uint32, error) {
	var count uint64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM calls WHERE run_id=?", runID).Scan(&count)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM runs WHERE run_id=?", runID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if count > uint64(^uint32(0)) {
		return 0, ErrLimit
	}
	return uint32(count), nil
}

func (s *Store) SetState(ctx context.Context, runID, status string, outcome json.RawMessage, reason string) error {
	if !validStatus(status) {
		return fmt.Errorf("invalid durable run status %q", status)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentStatus, currentReason string
	var currentOutcome []byte
	err = tx.QueryRowContext(ctx, "SELECT status, outcome, reason FROM runs WHERE run_id=?", runID).Scan(&currentStatus, &currentOutcome, &currentReason)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if terminalStatus(currentStatus) {
		if currentStatus == status && bytes.Equal(currentOutcome, outcome) && currentReason == reason {
			return tx.Commit()
		}
		return ErrConflict
	}
	current, err := payloadSizeTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if exceeds(current, replacementIncrease(currentOutcome, outcome, currentReason, reason)) {
		return ErrLimit
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET status=?, outcome=?, reason=? WHERE run_id=?", status, rawBytes(outcome), reason, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func validStatus(status string) bool {
	switch status {
	case StatusActive, StatusWaiting, StatusCompleted, StatusFailed, StatusCancelled, StatusBlocked:
		return true
	default:
		return false
	}
}

func terminalStatus(status string) bool {
	return status == StatusCompleted || status == StatusFailed || status == StatusCancelled
}

func operationKey(runID string, sequence uint32) string {
	return runID + "/" + strconv.FormatUint(uint64(sequence), 10)
}

func waitKey(runID string, sequence uint32) string {
	return runID + "/wait/" + strconv.FormatUint(uint64(sequence), 10)
}

func rawBytes(raw json.RawMessage) []byte {
	if raw == nil {
		return nil
	}
	return append([]byte(nil), raw...)
}

func cloneRaw(raw []byte) json.RawMessage {
	if raw == nil {
		return nil
	}
	return json.RawMessage(append([]byte(nil), raw...))
}

func encodeDeadline(deadline *time.Time) any {
	if deadline == nil {
		return nil
	}
	return deadline.UTC().Format(time.RFC3339Nano)
}

func decodeDeadline(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func copyWaitSpec(spec WaitSpec) WaitSpec {
	copy := spec
	copy.Request = cloneRaw(spec.Request)
	if spec.Deadline != nil {
		deadline := *spec.Deadline
		copy.Deadline = &deadline
	}
	return copy
}

func queryWait(ctx context.Context, db *sql.DB, waitID string) (Wait, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT wait_id, run_id, sequence, kind, request, deadline,
		resolved, decision_result, decision_error FROM waits WHERE wait_id=?`, waitID)
	return scanWait(row)
}

func queryWaitTx(ctx context.Context, tx *sql.Tx, waitID string) (Wait, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT wait_id, run_id, sequence, kind, request, deadline,
		resolved, decision_result, decision_error FROM waits WHERE wait_id=?`, waitID)
	return scanWait(row)
}

func scanWait(row interface{ Scan(...any) error }) (Wait, bool, error) {
	var wait Wait
	var request, result []byte
	var deadline sql.NullString
	var resolved int
	var decisionError string
	if err := row.Scan(&wait.ID, &wait.RunID, &wait.Sequence, &wait.Spec.Kind, &request,
		&deadline, &resolved, &result, &decisionError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Wait{}, false, nil
		}
		return Wait{}, false, err
	}
	wait.Spec.Request = cloneRaw(request)
	wait.Spec.Deadline = decodeDeadline(deadline)
	if resolved != 0 {
		wait.Decision = &Decision{Result: cloneRaw(result), Error: decisionError}
	}
	return wait, true, nil
}

func exceeds(current, additional uint64) bool {
	return current > MaxRunPayloadBytes || additional > MaxRunPayloadBytes-current
}

func definitionPayload(definition Definition) uint64 {
	return uint64(len(definition.ID) + len(definition.Code) + len(definition.Seed) +
		len(definition.ArtifactSHA256) + len(definition.EnvironmentVersion) + len(definition.Inputs) + len(definition.Tools))
}

func callPayload(call LoggedCall, operationKey string) uint64 {
	return uint64(len(call.CallID) + len(call.Capability) + len(operationKey) + len(call.Arguments))
}

func waitPayload(waitID string, spec WaitSpec, deadline any) uint64 {
	size := uint64(len(waitID) + len(spec.Kind) + len(spec.Request))
	if value, ok := deadline.(string); ok {
		size += uint64(len(value))
	}
	return size
}

func decisionPayload(decision Decision) uint64 {
	return uint64(len(decision.Result) + len(decision.Error))
}

func replacementIncrease(oldOutcome, newOutcome []byte, oldReason, newReason string) uint64 {
	oldSize := uint64(len(oldOutcome) + len(oldReason))
	newSize := uint64(len(newOutcome) + len(newReason))
	if newSize <= oldSize {
		return 0
	}
	return newSize - oldSize
}

func payloadSizeTx(ctx context.Context, tx *sql.Tx, runID string) (uint64, error) {
	const sizeQuery = `
		SELECT
			COALESCE((SELECT SUM(
				length(CAST(run_id AS BLOB)) + length(CAST(code AS BLOB)) +
				length(CAST(seed AS BLOB)) + length(CAST(artifact_sha256 AS BLOB)) +
				length(CAST(environment_version AS BLOB)) + COALESCE(length(CAST(inputs AS BLOB)), 0) +
				COALESCE(length(CAST(tools AS BLOB)), 0) +
				COALESCE(length(CAST(outcome AS BLOB)), 0) + length(CAST(reason AS BLOB)))
				FROM runs WHERE run_id = ?), 0) +
			COALESCE((SELECT SUM(
				length(CAST(call_id AS BLOB)) + length(CAST(tool AS BLOB)) +
				length(CAST(operation_key AS BLOB)) + COALESCE(length(CAST(arguments AS BLOB)), 0) +
				COALESCE(length(CAST(outcome AS BLOB)), 0))
				FROM calls WHERE run_id = ?), 0) +
			COALESCE((SELECT SUM(
				length(CAST(wait_id AS BLOB)) + length(CAST(kind AS BLOB)) +
				COALESCE(length(CAST(request AS BLOB)), 0) + COALESCE(length(CAST(deadline AS BLOB)), 0) +
				COALESCE(length(CAST(decision_result AS BLOB)), 0) +
				length(CAST(decision_error AS BLOB)))
				FROM waits WHERE run_id = ?), 0)`
	var total int64
	if err := tx.QueryRowContext(ctx, sizeQuery, runID, runID, runID).Scan(&total); err != nil {
		return 0, err
	}
	if total < 0 || uint64(total) > MaxRunPayloadBytes {
		return 0, ErrLimit
	}
	return uint64(total), nil
}

func isConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique")
}

// Every public append is contiguous; the next primary-key entry detects an
// unconsumed tail without counting all historical rows.
func (s *Store) hasCall(ctx context.Context, runID string, sequence uint32) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM calls WHERE run_id=? AND sequence=?)", runID, sequence).Scan(&exists)
	return exists, err
}

// readCompleted reads a small immutable prefix window, not a mutable Store cache.
// A pending row stops read-ahead so its state is observed by BeginCall normally.
func (s *Store) readCompleted(ctx context.Context, runID string, from uint32) ([]Call, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,call_id,tool,state,arguments,outcome FROM calls
 WHERE run_id=? AND sequence>=? ORDER BY sequence LIMIT 64`, runID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []Call
	size := 0
	for rows.Next() {
		var call Call
		var args, outcome []byte
		if err := rows.Scan(&call.Sequence, &call.CallID, &call.Tool, &call.State, &args, &outcome); err != nil {
			return nil, err
		}
		if call.Sequence != from+uint32(len(calls)) || call.State != CallCompleted {
			break
		}
		call.Arguments, call.Outcome = args, outcome
		calls = append(calls, call)
		size += len(args) + len(outcome)
		if size >= 1<<20 {
			break
		} // Bound read-ahead; a single large row still must be read.
	}
	return calls, rows.Err()
}
