package transaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const (
	sqliteSchemaVersion        = 4
	sqliteSchemaV1ExpectedHash = "sha256:46d6a0239a4dbf91166ad517810eed112d67cb2d05fe0fb4f0b9ef1294055eec"
	sqliteSchemaV3ExpectedHash = "sha256:819cf3b5cb29f1d23d7f481d48473a0a6f46eb3ae1f39dff9f30d9a65dd2d708"
	sqliteSchemaV4ExpectedHash = "sha256:845db336575a9e5d602877aaf40d65e4da9f99bb6422654b143c81871358151c"
)

var ErrUnsupportedSchema = errors.New("unsupported transaction ledger schema")

type SQLiteLedger struct {
	db   *sql.DB
	path string
}

func OpenSQLiteLedger(path string) (*SQLiteLedger, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidInput
	}
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return nil, fmt.Errorf("create SQLite ledger: %w", createErr)
		}
		if createErr == nil {
			if closeErr := file.Close(); closeErr != nil {
				return nil, fmt.Errorf("close new SQLite ledger: %w", closeErr)
			}
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect SQLite ledger: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidInput
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure SQLite ledger: %w", err)
	}

	location := url.URL{Scheme: "file", Path: path}
	query := location.Query()
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite ledger: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ledger := &SQLiteLedger{db: db, path: path}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping SQLite ledger: %w", err)
	}
	if err := ledger.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	ledger.secureFiles()
	return ledger, nil
}

func (ledger *SQLiteLedger) Close() error {
	if ledger == nil || ledger.db == nil {
		return nil
	}
	err := ledger.db.Close()
	ledger.secureFiles()
	return err
}

func (ledger *SQLiteLedger) secureFiles() {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(ledger.path+suffix, 0o600)
	}
}

func (ledger *SQLiteLedger) migrate() error {
	tx, err := ledger.db.Begin()
	if err != nil {
		return sqliteFailure("begin migration", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at_ns INTEGER NOT NULL,
		schema_digest TEXT NOT NULL
	) STRICT`); err != nil {
		return sqliteFailure("create migration table", err)
	}
	var version int
	var storedDigest string
	err = tx.QueryRow(`SELECT version,schema_digest FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &storedDigest)
	if errors.Is(err, sql.ErrNoRows) {
		version = 0
	} else if err != nil {
		return sqliteFailure("read migration version", err)
	}
	if version > sqliteSchemaVersion {
		return ErrUnsupportedSchema
	}
	if version == 0 {
		var unknownObjects int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`).Scan(&unknownObjects); err != nil {
			return sqliteFailure("inspect unmigrated schema", err)
		}
		if unknownObjects != 0 {
			return ErrUnsupportedSchema
		}
		for _, statement := range sqliteSchemaV1 {
			if _, err := tx.Exec(statement); err != nil {
				return sqliteFailure("apply migration v1", err)
			}
		}
		digest, err := computeSQLiteSchemaDigest(tx)
		if err != nil {
			return err
		}
		if digest != sqliteSchemaV1ExpectedHash {
			return ErrUnsupportedSchema
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns, schema_digest) VALUES(1, ?, ?)`, time.Now().UTC().UnixNano(), sqliteSchemaV1ExpectedHash); err != nil {
			return sqliteFailure("record migration v1", err)
		}
		version = 1
		storedDigest = sqliteSchemaV1ExpectedHash
	}
	actualDigest, err := computeSQLiteSchemaDigest(tx)
	if err != nil {
		return err
	}
	expectedDigest := sqliteSchemaV1ExpectedHash
	if version == 3 {
		expectedDigest = sqliteSchemaV3ExpectedHash
	} else if version == 4 {
		expectedDigest = sqliteSchemaV4ExpectedHash
	}
	if storedDigest != expectedDigest || actualDigest != expectedDigest {
		return ErrUnsupportedSchema
	}
	if version == 1 {
		if _, err := tx.Exec(`UPDATE operations SET updated_at_ns=created_at_ns WHERE updated_at_ns=0`); err != nil {
			return sqliteFailure("backfill operation timestamps v2", err)
		}
		if _, err := tx.Exec(`UPDATE attempts SET updated_at_ns=created_at_ns WHERE updated_at_ns=0`); err != nil {
			return sqliteFailure("backfill attempt timestamps v2", err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns, schema_digest) VALUES(2, ?, ?)`, time.Now().UTC().UnixNano(), sqliteSchemaV1ExpectedHash); err != nil {
			return sqliteFailure("record migration v2", err)
		}
		version = 2
	}
	if version == 2 {
		if _, err := tx.Exec(`ALTER TABLE attempts ADD COLUMN reconciliation_digest TEXT NOT NULL DEFAULT ''`); err != nil {
			return sqliteFailure("add reconciliation digest v3", err)
		}
		actualDigest, err = computeSQLiteSchemaDigest(tx)
		if err != nil || actualDigest != sqliteSchemaV3ExpectedHash {
			return ErrUnsupportedSchema
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns, schema_digest) VALUES(3, ?, ?)`, time.Now().UTC().UnixNano(), sqliteSchemaV3ExpectedHash); err != nil {
			return sqliteFailure("record migration v3", err)
		}
		version = 3
	}
	if version == 3 {
		for _, statement := range sqliteSchemaV4 {
			if _, err := tx.Exec(statement); err != nil {
				return sqliteFailure("apply migration v4", err)
			}
		}
		actualDigest, err = computeSQLiteSchemaDigest(tx)
		if err != nil || actualDigest != sqliteSchemaV4ExpectedHash {
			return ErrUnsupportedSchema
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns, schema_digest) VALUES(4, ?, ?)`, time.Now().UTC().UnixNano(), sqliteSchemaV4ExpectedHash); err != nil {
			return sqliteFailure("record migration v4", err)
		}
		version = 4
	}
	if version != sqliteSchemaVersion {
		return ErrUnsupportedSchema
	}
	if err := tx.Commit(); err != nil {
		return sqliteFailure("commit migration", err)
	}
	return nil
}

var sqliteSchemaV1 = []string{
	`CREATE TABLE transactions (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		catalog_digest TEXT NOT NULL,
		mode TEXT NOT NULL,
		state TEXT NOT NULL,
		version INTEGER NOT NULL CHECK(version > 0),
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	) STRICT`,
	`CREATE TABLE operations (
		id TEXT PRIMARY KEY,
		transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
		operation_index INTEGER NOT NULL CHECK(operation_index > 0),
		tool_id TEXT NOT NULL,
		handler_version TEXT NOT NULL,
		effect_class TEXT NOT NULL,
		policy TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		state TEXT NOT NULL,
		argument_digest TEXT NOT NULL,
		manifest_digest TEXT NOT NULL,
		version INTEGER NOT NULL CHECK(version > 0),
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL,
		UNIQUE(transaction_id, operation_index)
	) STRICT`,
	`CREATE TABLE attempts (
		id TEXT PRIMARY KEY,
		transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
		operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
		kind TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK(ordinal > 0),
		state TEXT NOT NULL,
		expected_operation_state TEXT NOT NULL,
		lease_id TEXT NOT NULL,
		lease_expires_at_ns INTEGER NOT NULL,
		provider_request_digest TEXT NOT NULL,
		version INTEGER NOT NULL CHECK(version > 0),
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL,
		UNIQUE(transaction_id, operation_id, kind, ordinal),
		UNIQUE(transaction_id, provider_request_digest)
	) STRICT`,
	`CREATE TABLE transitions (
		transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
		sequence INTEGER NOT NULL CHECK(sequence > 0),
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		observed_at_ns INTEGER NOT NULL,
		PRIMARY KEY(transaction_id, sequence)
	) STRICT`,
	`CREATE INDEX operations_transaction_order ON operations(transaction_id, operation_index)`,
	`CREATE INDEX attempts_transaction_order ON attempts(transaction_id, operation_id, kind, ordinal, id)`,
}

var sqliteSchemaV4 = []string{
	`ALTER TABLE attempts ADD COLUMN provider_receipt_digest TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE approvals (
		authority_id TEXT PRIMARY KEY,
		token_digest TEXT NOT NULL UNIQUE,
		transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
		operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
		manifest_digest TEXT NOT NULL,
		source TEXT NOT NULL,
		source_run_id TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		phase_grant_id TEXT NOT NULL,
		expires_at_ns INTEGER NOT NULL,
		registered_at_ns INTEGER NOT NULL,
		consumed_at_ns INTEGER NOT NULL DEFAULT 0
	) STRICT`,
	`CREATE INDEX approvals_transaction_order ON approvals(transaction_id, registered_at_ns, authority_id)`,
}

func computeSQLiteSchemaDigest(tx *sql.Tx) (string, error) {
	rows, err := tx.Query(`SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		return "", sqliteFailure("read SQLite schema", err)
	}
	defer rows.Close()
	hash := sha256.New()
	objects := 0
	for rows.Next() {
		var objectType, name, tableName, statement string
		if err := rows.Scan(&objectType, &name, &tableName, &statement); err != nil {
			return "", sqliteFailure("scan SQLite schema", err)
		}
		for _, value := range []string{objectType, name, tableName, statement} {
			hash.Write([]byte{0})
			hash.Write([]byte(value))
		}
		objects++
	}
	if err := rows.Err(); err != nil {
		return "", sqliteFailure("iterate SQLite schema", err)
	}
	if objects != 7 && objects != 9 {
		return "", ErrUnsupportedSchema
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (ledger *SQLiteLedger) createTransaction(value Transaction) error {
	if !validIdentifier(value.ID) || !validIdentifier(value.RunID) || !digestPattern.MatchString(value.CatalogDigest) ||
		(value.Mode != TransactionModeDirect && value.Mode != TransactionModeWorkflow) || value.State != TransactionOpen || value.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	value.Version = 1
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
	tx, err := ledger.db.Begin()
	if err != nil {
		return sqliteFailure("begin create transaction", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO transactions(id,run_id,catalog_digest,mode,state,version,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?,?,?,?)`,
		value.ID, value.RunID, value.CatalogDigest, value.Mode, value.State, value.Version, timeNS(value.CreatedAt), timeNS(value.UpdatedAt)); err != nil {
		return sqliteConstraintOrFailure("create transaction", err)
	}
	if err := appendSQLiteTransition(tx, value.ID, "transaction", value.ID, "", string(value.State), value.CreatedAt); err != nil {
		return err
	}
	return commitSQLite(tx, "create transaction")
}

func (ledger *SQLiteLedger) GetTransaction(id string) (Transaction, error) {
	if !validIdentifier(id) {
		return Transaction{}, ErrInvalidInput
	}
	return scanSQLiteTransaction(ledger.db.QueryRow(`SELECT id,run_id,catalog_digest,mode,state,version,created_at_ns,updated_at_ns FROM transactions WHERE id=?`, id))
}

func (ledger *SQLiteLedger) transitionTransaction(id string, version uint64, from, to TransactionState, observedAt time.Time) (Transaction, error) {
	if !validIdentifier(id) || observedAt.IsZero() {
		return Transaction{}, ErrInvalidInput
	}
	if err := ValidateTransactionTransition(from, to); err != nil {
		return Transaction{}, err
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Transaction{}, sqliteFailure("begin transaction CAS", err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE transactions SET state=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=?`, to, timeNS(observedAt), id, version, from)
	if err != nil {
		return Transaction{}, sqliteFailure("transaction CAS", err)
	}
	if err := requireSQLiteUpdate(tx, result, "transactions", id); err != nil {
		return Transaction{}, err
	}
	if err := appendSQLiteTransition(tx, id, "transaction", id, string(from), string(to), observedAt); err != nil {
		return Transaction{}, err
	}
	value, err := scanSQLiteTransaction(tx.QueryRow(`SELECT id,run_id,catalog_digest,mode,state,version,created_at_ns,updated_at_ns FROM transactions WHERE id=?`, id))
	if err != nil {
		return Transaction{}, err
	}
	if err := commitSQLite(tx, "transaction CAS"); err != nil {
		return Transaction{}, err
	}
	return value, nil
}

func (ledger *SQLiteLedger) createOperation(value Operation) error {
	if !validIdentifier(value.ID) || !validIdentifier(value.TransactionID) || value.Index == 0 || !validIdentifier(value.ToolID) ||
		!validIdentifier(value.HandlerVersion) || !validIdentifier(value.PolicyVersion) || !validEffectClass(value.EffectClass) ||
		!validPolicy(value.Policy) || !validInitialOperationState(value.State) || !digestPattern.MatchString(value.ArgumentDigest) ||
		!digestPattern.MatchString(value.ManifestDigest) || value.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return sqliteFailure("begin create operation", err)
	}
	defer tx.Rollback()
	if !sqliteEntityExists(tx, "transactions", value.TransactionID) {
		return ErrNotFound
	}
	value.Version = 1
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
	if _, err := tx.Exec(`INSERT INTO operations(id,transaction_id,operation_index,tool_id,handler_version,effect_class,policy,policy_version,state,argument_digest,manifest_digest,version,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.TransactionID, value.Index, value.ToolID, value.HandlerVersion, value.EffectClass, value.Policy, value.PolicyVersion,
		value.State, value.ArgumentDigest, value.ManifestDigest, value.Version, timeNS(value.CreatedAt), timeNS(value.UpdatedAt)); err != nil {
		return sqliteConstraintOrFailure("create operation", err)
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "operation", value.ID, "", string(value.State), value.CreatedAt); err != nil {
		return err
	}
	return commitSQLite(tx, "create operation")
}

func (ledger *SQLiteLedger) GetOperation(id string) (Operation, error) {
	if !validIdentifier(id) {
		return Operation{}, ErrInvalidInput
	}
	return scanSQLiteOperation(ledger.db.QueryRow(operationSelect+` WHERE id=?`, id))
}

const operationSelect = `SELECT id,transaction_id,operation_index,tool_id,handler_version,effect_class,policy,policy_version,state,argument_digest,manifest_digest,version,created_at_ns,updated_at_ns FROM operations`

func (ledger *SQLiteLedger) ListOperations(transactionID string) ([]Operation, error) {
	if !validIdentifier(transactionID) {
		return nil, ErrInvalidInput
	}
	if !sqliteEntityExists(ledger.db, "transactions", transactionID) {
		return nil, ErrNotFound
	}
	rows, err := ledger.db.Query(operationSelect+` WHERE transaction_id=? ORDER BY operation_index`, transactionID)
	if err != nil {
		return nil, sqliteFailure("list operations", err)
	}
	defer rows.Close()
	values := make([]Operation, 0)
	for rows.Next() {
		value, err := scanSQLiteOperation(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, sqliteFailure("iterate operations", rows.Err())
}

func (ledger *SQLiteLedger) transitionOperation(id string, version uint64, from, to OperationState, observedAt time.Time) (Operation, error) {
	if !validIdentifier(id) || observedAt.IsZero() {
		return Operation{}, ErrInvalidInput
	}
	if err := ValidateOperationTransition(from, to); err != nil {
		return Operation{}, err
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Operation{}, sqliteFailure("begin operation CAS", err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE operations SET state=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=?`, to, timeNS(observedAt), id, version, from)
	if err != nil {
		return Operation{}, sqliteFailure("operation CAS", err)
	}
	if err := requireSQLiteUpdate(tx, result, "operations", id); err != nil {
		return Operation{}, err
	}
	value, err := scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, id))
	if err != nil {
		return Operation{}, err
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "operation", id, string(from), string(to), observedAt); err != nil {
		return Operation{}, err
	}
	if err := commitSQLite(tx, "operation CAS"); err != nil {
		return Operation{}, err
	}
	return value, nil
}

const approvalSelect = `SELECT authority_id,token_digest,transaction_id,operation_id,manifest_digest,source,source_run_id,actor_id,phase_grant_id,expires_at_ns,registered_at_ns,consumed_at_ns FROM approvals`

func (ledger *SQLiteLedger) registerApproval(record approvalRecord) error {
	if err := validateApprovalRecord(record); err != nil {
		return err
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return sqliteFailure("begin register approval", err)
	}
	defer tx.Rollback()
	prior, priorErr := scanSQLiteApproval(tx.QueryRow(approvalSelect+` WHERE token_digest=? OR authority_id=? LIMIT 1`, record.TokenDigest, record.AuthorityID))
	if priorErr == nil {
		if sameApprovalGrant(prior, record) {
			return nil
		}
		return ErrAlreadyExists
	}
	if !errors.Is(priorErr, ErrNotFound) {
		return priorErr
	}
	operation, err := scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, record.OperationID))
	if err != nil || operation.TransactionID != record.TransactionID || operation.ManifestDigest != record.ManifestDigest || operation.State != approvalExpectedState(record.Source) {
		return ErrConflict
	}
	_, err = tx.Exec(`INSERT INTO approvals(authority_id,token_digest,transaction_id,operation_id,manifest_digest,source,source_run_id,actor_id,phase_grant_id,expires_at_ns,registered_at_ns,consumed_at_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,0)`,
		record.AuthorityID, record.TokenDigest, record.TransactionID, record.OperationID, record.ManifestDigest, record.Source,
		record.SourceRunID, record.ActorID, record.PhaseGrantID, timeNS(record.ExpiresAt), timeNS(record.RegisteredAt))
	if err != nil {
		prior, scanErr := scanSQLiteApproval(tx.QueryRow(approvalSelect+` WHERE token_digest=? OR authority_id=? LIMIT 1`, record.TokenDigest, record.AuthorityID))
		if scanErr == nil && sameApprovalGrant(prior, record) {
			return nil
		}
		return sqliteConstraintOrFailure("register approval", err)
	}
	return commitSQLite(tx, "register approval")
}

func (ledger *SQLiteLedger) findApproval(tokenDigest string) (approvalRecord, error) {
	if !digestPattern.MatchString(tokenDigest) {
		return approvalRecord{}, ErrInvalidInput
	}
	return scanSQLiteApproval(ledger.db.QueryRow(approvalSelect+` WHERE token_digest=?`, tokenDigest))
}

func (ledger *SQLiteLedger) ListApprovals(transactionID string) ([]ApprovalEvidence, error) {
	if !validIdentifier(transactionID) {
		return nil, ErrInvalidInput
	}
	if !sqliteEntityExists(ledger.db, "transactions", transactionID) {
		return nil, ErrNotFound
	}
	rows, err := ledger.db.Query(approvalSelect+` WHERE transaction_id=? ORDER BY registered_at_ns,authority_id`, transactionID)
	if err != nil {
		return nil, sqliteFailure("list approvals", err)
	}
	defer rows.Close()
	values := make([]ApprovalEvidence, 0)
	for rows.Next() {
		record, err := scanSQLiteApproval(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, record.ApprovalEvidence)
	}
	return values, sqliteFailure("iterate approvals", rows.Err())
}

func (ledger *SQLiteLedger) consumeApprovalAndAuthorize(tokenDigest string, observedAt time.Time) (Operation, approvalRecord, error) {
	if !digestPattern.MatchString(tokenDigest) || observedAt.IsZero() {
		return Operation{}, approvalRecord{}, ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Operation{}, approvalRecord{}, sqliteFailure("begin consume approval", err)
	}
	defer tx.Rollback()
	record, err := scanSQLiteApproval(tx.QueryRow(approvalSelect+` WHERE token_digest=?`, tokenDigest))
	if err != nil {
		return Operation{}, approvalRecord{}, err
	}
	operation, err := scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, record.OperationID))
	if err != nil || operation.TransactionID != record.TransactionID || operation.ManifestDigest != record.ManifestDigest {
		return Operation{}, approvalRecord{}, ErrConflict
	}
	if !record.ConsumedAt.IsZero() {
		if operation.State != OperationReady {
			return Operation{}, approvalRecord{}, ErrConflict
		}
		return operation, record, nil
	}
	if !record.ExpiresAt.After(observedAt) {
		return Operation{}, approvalRecord{}, ErrExpired
	}
	from := approvalExpectedState(record.Source)
	if operation.State != from || ValidateOperationTransition(from, OperationReady) != nil {
		return Operation{}, approvalRecord{}, ErrConflict
	}
	result, err := tx.Exec(`UPDATE approvals SET consumed_at_ns=? WHERE authority_id=? AND consumed_at_ns=0`, timeNS(observedAt), record.AuthorityID)
	if err != nil {
		return Operation{}, approvalRecord{}, sqliteFailure("consume approval", err)
	}
	if err := requireSQLiteUpdate(tx, result, "approvals", record.AuthorityID); err != nil {
		return Operation{}, approvalRecord{}, err
	}
	result, err = tx.Exec(`UPDATE operations SET state=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=?`, OperationReady, timeNS(observedAt), operation.ID, operation.Version, from)
	if err != nil {
		return Operation{}, approvalRecord{}, sqliteFailure("authorize operation", err)
	}
	if err := requireSQLiteUpdate(tx, result, "operations", operation.ID); err != nil {
		return Operation{}, approvalRecord{}, err
	}
	operation, err = scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, operation.ID))
	if err != nil {
		return Operation{}, approvalRecord{}, err
	}
	if err := appendSQLiteTransition(tx, operation.TransactionID, "operation", operation.ID, string(from), string(OperationReady), observedAt); err != nil {
		return Operation{}, approvalRecord{}, err
	}
	record.ConsumedAt = observedAt.UTC()
	if err := commitSQLite(tx, "consume approval"); err != nil {
		return Operation{}, approvalRecord{}, err
	}
	return operation, record, nil
}

func scanSQLiteApproval(scanner sqliteScanner) (approvalRecord, error) {
	var record approvalRecord
	var source string
	var expiresAt, registeredAt, consumedAt int64
	if err := scanner.Scan(&record.AuthorityID, &record.TokenDigest, &record.TransactionID, &record.OperationID, &record.ManifestDigest,
		&source, &record.SourceRunID, &record.ActorID, &record.PhaseGrantID, &expiresAt, &registeredAt, &consumedAt); err != nil {
		return approvalRecord{}, sqliteScanFailure("approval", err)
	}
	record.Source = CommitSource(source)
	record.ExpiresAt, record.RegisteredAt, record.ConsumedAt = timeFromNS(expiresAt), timeFromNS(registeredAt), timeFromNS(consumedAt)
	return record, nil
}

func (ledger *SQLiteLedger) createAttempt(value Attempt) error {
	if !validIdentifier(value.ID) || !validIdentifier(value.TransactionID) || !validIdentifier(value.OperationID) ||
		!validIdentifier(value.LeaseID) || value.Ordinal == 0 || value.State != AttemptLeased || !validAttemptKind(value.Kind) ||
		!validAttemptPriorState(value.Kind, value.ExpectedOperationState) || !digestPattern.MatchString(value.ProviderRequestDigest) ||
		value.CreatedAt.IsZero() || !value.LeaseExpiresAt.After(value.CreatedAt) {
		return ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return sqliteFailure("begin create attempt", err)
	}
	defer tx.Rollback()
	operation, err := scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, value.OperationID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if operation.TransactionID != value.TransactionID {
		return ErrNotFound
	}
	if operation.State != value.ExpectedOperationState {
		return ErrConflict
	}
	value.Version = 1
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
	value.LeaseExpiresAt = value.LeaseExpiresAt.UTC()
	if _, err := tx.Exec(`INSERT INTO attempts(id,transaction_id,operation_id,kind,ordinal,state,expected_operation_state,lease_id,lease_expires_at_ns,provider_request_digest,version,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.TransactionID, value.OperationID, value.Kind, value.Ordinal, value.State, value.ExpectedOperationState, value.LeaseID,
		timeNS(value.LeaseExpiresAt), value.ProviderRequestDigest, value.Version, timeNS(value.CreatedAt), timeNS(value.UpdatedAt)); err != nil {
		return sqliteConstraintOrFailure("create attempt", err)
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "attempt", value.ID, "", string(value.State), value.CreatedAt); err != nil {
		return err
	}
	return commitSQLite(tx, "create attempt")
}

const attemptSelect = `SELECT id,transaction_id,operation_id,kind,ordinal,state,expected_operation_state,lease_id,lease_expires_at_ns,provider_request_digest,provider_receipt_digest,reconciliation_digest,version,created_at_ns,updated_at_ns FROM attempts`

func (ledger *SQLiteLedger) GetAttempt(id string) (Attempt, error) {
	if !validIdentifier(id) {
		return Attempt{}, ErrInvalidInput
	}
	return scanSQLiteAttempt(ledger.db.QueryRow(attemptSelect+` WHERE id=?`, id))
}

func (ledger *SQLiteLedger) ListAttempts(transactionID string) ([]Attempt, error) {
	if !validIdentifier(transactionID) {
		return nil, ErrInvalidInput
	}
	if !sqliteEntityExists(ledger.db, "transactions", transactionID) {
		return nil, ErrNotFound
	}
	rows, err := ledger.db.Query(attemptSelect+` WHERE transaction_id=? ORDER BY operation_id,kind,ordinal,id`, transactionID)
	if err != nil {
		return nil, sqliteFailure("list attempts", err)
	}
	defer rows.Close()
	values := make([]Attempt, 0)
	for rows.Next() {
		value, err := scanSQLiteAttempt(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, sqliteFailure("iterate attempts", err)
	}
	if err := rows.Close(); err != nil {
		return nil, sqliteFailure("close attempt rows", err)
	}
	operations, err := ledger.ListOperations(transactionID)
	if err != nil {
		return nil, err
	}
	indexes := make(map[string]uint32, len(operations))
	for _, operation := range operations {
		indexes[operation.ID] = operation.Index
	}
	sort.Slice(values, func(left, right int) bool {
		if indexes[values[left].OperationID] != indexes[values[right].OperationID] {
			return indexes[values[left].OperationID] < indexes[values[right].OperationID]
		}
		if values[left].Kind != values[right].Kind {
			return values[left].Kind < values[right].Kind
		}
		if values[left].Ordinal != values[right].Ordinal {
			return values[left].Ordinal < values[right].Ordinal
		}
		return values[left].ID < values[right].ID
	})
	return values, nil
}

func (ledger *SQLiteLedger) findAttemptByProviderRequest(transactionID, digest string) (Attempt, error) {
	if !validIdentifier(transactionID) || !digestPattern.MatchString(digest) {
		return Attempt{}, ErrInvalidInput
	}
	return scanSQLiteAttempt(ledger.db.QueryRow(attemptSelect+` WHERE transaction_id=? AND provider_request_digest=?`, transactionID, digest))
}

func (ledger *SQLiteLedger) transitionAttempt(id string, version uint64, from, to AttemptState, observedAt time.Time) (Attempt, error) {
	if !validIdentifier(id) || observedAt.IsZero() {
		return Attempt{}, ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Attempt{}, sqliteFailure("begin attempt CAS", err)
	}
	defer tx.Rollback()
	current, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if current.Version != version || current.State != from {
		return Attempt{}, ErrConflict
	}
	if from == to && attemptTerminal(from) {
		return current, nil
	}
	if from == AttemptLeased && to == AttemptDispatching {
		if !observedAt.Before(current.LeaseExpiresAt) {
			return Attempt{}, ErrExpired
		}
		operation, err := scanSQLiteOperation(tx.QueryRow(operationSelect+` WHERE id=?`, current.OperationID))
		if err != nil || operation.TransactionID != current.TransactionID || operation.State != current.ExpectedOperationState {
			return Attempt{}, ErrConflict
		}
	}
	if err := ValidateAttemptTransition(from, to); err != nil {
		return Attempt{}, err
	}
	result, err := tx.Exec(`UPDATE attempts SET state=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=?`, to, timeNS(observedAt), id, version, from)
	if err != nil {
		return Attempt{}, sqliteFailure("attempt CAS", err)
	}
	if err := requireSQLiteUpdate(tx, result, "attempts", id); err != nil {
		return Attempt{}, err
	}
	value, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "attempt", id, string(from), string(to), observedAt); err != nil {
		return Attempt{}, err
	}
	if err := commitSQLite(tx, "attempt CAS"); err != nil {
		return Attempt{}, err
	}
	return value, nil
}

func (ledger *SQLiteLedger) completeAttempt(id string, version uint64, target AttemptState, receiptDigest string, observedAt time.Time) (Attempt, error) {
	if !validIdentifier(id) || observedAt.IsZero() || (receiptDigest != "" && !digestPattern.MatchString(receiptDigest)) ||
		(target != AttemptSucceeded && target != AttemptFailed && target != AttemptAmbiguous) || (target != AttemptSucceeded && receiptDigest != "") {
		return Attempt{}, ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Attempt{}, sqliteFailure("begin complete attempt", err)
	}
	defer tx.Rollback()
	current, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if current.State == target && attemptTerminal(target) {
		if current.ProviderReceiptDigest == receiptDigest {
			return current, nil
		}
		return Attempt{}, ErrConflict
	}
	if current.Version != version || current.State != AttemptDispatching || ValidateAttemptTransition(AttemptDispatching, target) != nil {
		return Attempt{}, ErrConflict
	}
	result, err := tx.Exec(`UPDATE attempts SET state=?,provider_receipt_digest=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=?`, target, receiptDigest, timeNS(observedAt), id, version, AttemptDispatching)
	if err != nil {
		return Attempt{}, sqliteFailure("complete attempt", err)
	}
	if err := requireSQLiteUpdate(tx, result, "attempts", id); err != nil {
		return Attempt{}, err
	}
	value, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "attempt", id, string(AttemptDispatching), string(target), observedAt); err != nil {
		return Attempt{}, err
	}
	if err := commitSQLite(tx, "complete attempt"); err != nil {
		return Attempt{}, err
	}
	return value, nil
}

func (ledger *SQLiteLedger) reconcileAttempt(id string, version uint64, target AttemptState, observationDigest string, observedAt time.Time) (Attempt, error) {
	if !validIdentifier(id) || !digestPattern.MatchString(observationDigest) || observedAt.IsZero() ||
		(target != AttemptSucceeded && target != AttemptFailed) {
		return Attempt{}, ErrInvalidInput
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		return Attempt{}, sqliteFailure("begin attempt reconciliation", err)
	}
	defer tx.Rollback()
	current, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if current.State == target && current.ReconciliationDigest == observationDigest {
		return current, nil
	}
	if current.Version != version || current.State != AttemptAmbiguous || current.ReconciliationDigest != "" {
		return Attempt{}, ErrConflict
	}
	result, err := tx.Exec(`UPDATE attempts SET state=?,reconciliation_digest=?,version=version+1,updated_at_ns=? WHERE id=? AND version=? AND state=? AND reconciliation_digest=''`,
		target, observationDigest, timeNS(observedAt), id, version, AttemptAmbiguous)
	if err != nil {
		return Attempt{}, sqliteFailure("reconcile attempt", err)
	}
	if err := requireSQLiteUpdate(tx, result, "attempts", id); err != nil {
		return Attempt{}, err
	}
	value, err := scanSQLiteAttempt(tx.QueryRow(attemptSelect+` WHERE id=?`, id))
	if err != nil {
		return Attempt{}, err
	}
	if err := appendSQLiteTransition(tx, value.TransactionID, "attempt", id, string(AttemptAmbiguous), string(target), observedAt); err != nil {
		return Attempt{}, err
	}
	if err := commitSQLite(tx, "attempt reconciliation"); err != nil {
		return Attempt{}, err
	}
	return value, nil
}

func (ledger *SQLiteLedger) Snapshot(transactionID string) (JournalSnapshot, error) {
	if !validIdentifier(transactionID) {
		return JournalSnapshot{}, ErrInvalidInput
	}
	tx, err := ledger.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return JournalSnapshot{}, sqliteFailure("begin journal snapshot", err)
	}
	defer tx.Rollback()
	snapshot := JournalSnapshot{Operations: make([]Operation, 0), Attempts: make([]Attempt, 0), Approvals: make([]ApprovalEvidence, 0), Transitions: make([]Transition, 0)}
	snapshot.Transaction, err = scanSQLiteTransaction(tx.QueryRow(`SELECT id,run_id,catalog_digest,mode,state,version,created_at_ns,updated_at_ns FROM transactions WHERE id=?`, transactionID))
	if err != nil {
		return JournalSnapshot{}, err
	}
	operationRows, err := tx.Query(operationSelect+` WHERE transaction_id=? ORDER BY operation_index`, transactionID)
	if err != nil {
		return JournalSnapshot{}, sqliteFailure("snapshot operations", err)
	}
	for operationRows.Next() {
		operation, scanErr := scanSQLiteOperation(operationRows)
		if scanErr != nil {
			operationRows.Close()
			return JournalSnapshot{}, scanErr
		}
		snapshot.Operations = append(snapshot.Operations, operation)
	}
	if err := operationRows.Err(); err != nil {
		operationRows.Close()
		return JournalSnapshot{}, sqliteFailure("iterate snapshot operations", err)
	}
	if err := operationRows.Close(); err != nil {
		return JournalSnapshot{}, sqliteFailure("close snapshot operations", err)
	}
	attemptRows, err := tx.Query(attemptSelect+` WHERE transaction_id=? ORDER BY operation_id,kind,ordinal,id`, transactionID)
	if err != nil {
		return JournalSnapshot{}, sqliteFailure("snapshot attempts", err)
	}
	for attemptRows.Next() {
		attempt, scanErr := scanSQLiteAttempt(attemptRows)
		if scanErr != nil {
			attemptRows.Close()
			return JournalSnapshot{}, scanErr
		}
		snapshot.Attempts = append(snapshot.Attempts, attempt)
	}
	if err := attemptRows.Err(); err != nil {
		attemptRows.Close()
		return JournalSnapshot{}, sqliteFailure("iterate snapshot attempts", err)
	}
	if err := attemptRows.Close(); err != nil {
		return JournalSnapshot{}, sqliteFailure("close snapshot attempts", err)
	}
	approvalRows, err := tx.Query(approvalSelect+` WHERE transaction_id=? ORDER BY registered_at_ns,authority_id`, transactionID)
	if err != nil {
		return JournalSnapshot{}, sqliteFailure("snapshot approvals", err)
	}
	for approvalRows.Next() {
		record, scanErr := scanSQLiteApproval(approvalRows)
		if scanErr != nil {
			approvalRows.Close()
			return JournalSnapshot{}, scanErr
		}
		snapshot.Approvals = append(snapshot.Approvals, record.ApprovalEvidence)
	}
	if err := approvalRows.Err(); err != nil {
		approvalRows.Close()
		return JournalSnapshot{}, sqliteFailure("iterate snapshot approvals", err)
	}
	if err := approvalRows.Close(); err != nil {
		return JournalSnapshot{}, sqliteFailure("close snapshot approvals", err)
	}
	transitionRows, err := tx.Query(`SELECT sequence,transaction_id,entity_type,entity_id,from_state,to_state,observed_at_ns FROM transitions WHERE transaction_id=? ORDER BY sequence`, transactionID)
	if err != nil {
		return JournalSnapshot{}, sqliteFailure("snapshot transitions", err)
	}
	for transitionRows.Next() {
		var transition Transition
		var observed int64
		if err := transitionRows.Scan(&transition.Sequence, &transition.TransactionID, &transition.EntityType, &transition.EntityID, &transition.From, &transition.To, &observed); err != nil {
			transitionRows.Close()
			return JournalSnapshot{}, sqliteFailure("scan snapshot transition", err)
		}
		transition.ObservedAt = timeFromNS(observed)
		snapshot.Transitions = append(snapshot.Transitions, transition)
	}
	if err := transitionRows.Err(); err != nil {
		transitionRows.Close()
		return JournalSnapshot{}, sqliteFailure("iterate snapshot transitions", err)
	}
	if err := transitionRows.Close(); err != nil {
		return JournalSnapshot{}, sqliteFailure("close snapshot transitions", err)
	}
	operationIndexes := make(map[string]uint32, len(snapshot.Operations))
	for _, operation := range snapshot.Operations {
		operationIndexes[operation.ID] = operation.Index
	}
	sort.Slice(snapshot.Attempts, func(left, right int) bool {
		if operationIndexes[snapshot.Attempts[left].OperationID] != operationIndexes[snapshot.Attempts[right].OperationID] {
			return operationIndexes[snapshot.Attempts[left].OperationID] < operationIndexes[snapshot.Attempts[right].OperationID]
		}
		if snapshot.Attempts[left].Kind != snapshot.Attempts[right].Kind {
			return snapshot.Attempts[left].Kind < snapshot.Attempts[right].Kind
		}
		if snapshot.Attempts[left].Ordinal != snapshot.Attempts[right].Ordinal {
			return snapshot.Attempts[left].Ordinal < snapshot.Attempts[right].Ordinal
		}
		return snapshot.Attempts[left].ID < snapshot.Attempts[right].ID
	})
	if err := tx.Commit(); err != nil {
		return JournalSnapshot{}, sqliteFailure("commit journal snapshot", err)
	}
	return snapshot, nil
}

func (ledger *SQLiteLedger) ListTransitions(transactionID string) ([]Transition, error) {
	if !validIdentifier(transactionID) {
		return nil, ErrInvalidInput
	}
	if !sqliteEntityExists(ledger.db, "transactions", transactionID) {
		return nil, ErrNotFound
	}
	rows, err := ledger.db.Query(`SELECT sequence,transaction_id,entity_type,entity_id,from_state,to_state,observed_at_ns FROM transitions WHERE transaction_id=? ORDER BY sequence`, transactionID)
	if err != nil {
		return nil, sqliteFailure("list transitions", err)
	}
	defer rows.Close()
	values := make([]Transition, 0)
	for rows.Next() {
		var value Transition
		var observed int64
		if err := rows.Scan(&value.Sequence, &value.TransactionID, &value.EntityType, &value.EntityID, &value.From, &value.To, &observed); err != nil {
			return nil, sqliteFailure("scan transition", err)
		}
		value.ObservedAt = timeFromNS(observed)
		values = append(values, value)
	}
	return values, sqliteFailure("iterate transitions", rows.Err())
}

type sqliteScanner interface {
	Scan(...any) error
}

type sqliteQueryer interface {
	QueryRow(string, ...any) *sql.Row
}

func scanSQLiteTransaction(scanner sqliteScanner) (Transaction, error) {
	var value Transaction
	var created, updated int64
	if err := scanner.Scan(&value.ID, &value.RunID, &value.CatalogDigest, &value.Mode, &value.State, &value.Version, &created, &updated); err != nil {
		return Transaction{}, sqliteScanFailure("transaction", err)
	}
	value.CreatedAt, value.UpdatedAt = timeFromNS(created), timeFromNS(updated)
	return value, nil
}

func scanSQLiteOperation(scanner sqliteScanner) (Operation, error) {
	var value Operation
	var created, updated int64
	if err := scanner.Scan(&value.ID, &value.TransactionID, &value.Index, &value.ToolID, &value.HandlerVersion, &value.EffectClass,
		&value.Policy, &value.PolicyVersion, &value.State, &value.ArgumentDigest, &value.ManifestDigest, &value.Version, &created, &updated); err != nil {
		return Operation{}, sqliteScanFailure("operation", err)
	}
	value.CreatedAt, value.UpdatedAt = timeFromNS(created), timeFromNS(updated)
	return value, nil
}

func scanSQLiteAttempt(scanner sqliteScanner) (Attempt, error) {
	var value Attempt
	var lease, created, updated int64
	if err := scanner.Scan(&value.ID, &value.TransactionID, &value.OperationID, &value.Kind, &value.Ordinal, &value.State,
		&value.ExpectedOperationState, &value.LeaseID, &lease, &value.ProviderRequestDigest, &value.ProviderReceiptDigest, &value.ReconciliationDigest, &value.Version, &created, &updated); err != nil {
		return Attempt{}, sqliteScanFailure("attempt", err)
	}
	value.LeaseExpiresAt, value.CreatedAt, value.UpdatedAt = timeFromNS(lease), timeFromNS(created), timeFromNS(updated)
	return value, nil
}

func appendSQLiteTransition(tx *sql.Tx, transactionID, entityType, entityID, from, to string, observedAt time.Time) error {
	if _, err := tx.Exec(`INSERT INTO transitions(transaction_id,sequence,entity_type,entity_id,from_state,to_state,observed_at_ns)
		SELECT ?,COALESCE(MAX(sequence),0)+1,?,?,?,?,? FROM transitions WHERE transaction_id=?`,
		transactionID, entityType, entityID, from, to, timeNS(observedAt), transactionID); err != nil {
		return sqliteConstraintOrFailure("append transition", err)
	}
	return nil
}

func requireSQLiteUpdate(tx *sql.Tx, result sql.Result, table, id string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return sqliteFailure("read CAS result", err)
	}
	if count == 1 {
		return nil
	}
	if sqliteEntityExists(tx, table, id) {
		return ErrConflict
	}
	return ErrNotFound
}

func sqliteEntityExists(queryer sqliteQueryer, table, id string) bool {
	column := "id"
	if table == "approvals" {
		column = "authority_id"
	} else if table != "transactions" && table != "operations" && table != "attempts" {
		return false
	}
	var marker int
	return queryer.QueryRow(`SELECT 1 FROM `+table+` WHERE `+column+`=?`, id).Scan(&marker) == nil
}

func commitSQLite(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return sqliteFailure("commit "+operation, err)
	}
	return nil
}

func sqliteScanFailure(entity string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return sqliteFailure("scan "+entity, err)
}

func sqliteConstraintOrFailure(operation string, err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 19 {
		return ErrAlreadyExists
	}
	return sqliteFailure(operation, err)
}

func sqliteFailure(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6:
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func timeNS(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func timeFromNS(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ coordinatorLedger = (*SQLiteLedger)(nil)
