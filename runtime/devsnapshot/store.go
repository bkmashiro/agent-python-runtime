package devsnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	schemaVersion     = 1
	payloadVersion    = 1
	maxComponents     = 32
	maxComponentBytes = 16 << 20
	maxPayloadBytes   = 64 << 20
)

var (
	ErrInvalidInput      = errors.New("invalid development snapshot input")
	ErrNotFound          = errors.New("development snapshot not found")
	ErrUnsupportedSchema = errors.New("unsupported development snapshot schema")
	ErrIntegrity         = errors.New("development snapshot integrity failure")
)

const snapshotTableSQL = `CREATE TABLE dev_snapshots (
	id TEXT PRIMARY KEY,
	payload BLOB NOT NULL CHECK(length(payload) > 0 AND length(payload) <= 67108864),
	digest TEXT NOT NULL CHECK(length(digest) = 71),
	version INTEGER NOT NULL CHECK(version > 0),
	updated_at_ns INTEGER NOT NULL
) STRICT`

type Store struct {
	db   *sql.DB
	path string
}
type Snapshot struct {
	ID         string                     `json:"id"`
	Digest     string                     `json:"digest"`
	Version    uint64                     `json:"version"`
	Components map[string]json.RawMessage `json:"components"`
}
type payload struct {
	SchemaVersion uint32                     `json:"schema_version"`
	Components    map[string]json.RawMessage `json:"components"`
}

func Open(path string) (*Store, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidInput
	}
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return nil, fmt.Errorf("create dev snapshot db: %w", createErr)
		}
		if createErr == nil {
			if err := file.Close(); err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidInput
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: path}
	query := location.Query()
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, path: path}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	store.secureFiles()
	return store, nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	err := store.db.Close()
	store.secureFiles()
	return err
}
func (store *Store) secureFiles() {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(store.path+suffix, 0o600)
	}
}
func (store *Store) migrate() error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return ErrUnsupportedSchema
	}
	if version == 0 {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrUnsupportedSchema
		}
		if _, err := tx.Exec(snapshotTableSQL); err != nil {
			return err
		}
		if _, err := tx.Exec(`PRAGMA user_version=1`); err != nil {
			return err
		}
		version = 1
	}
	if version != schemaVersion {
		return ErrUnsupportedSchema
	}
	var actual string
	if err := tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='dev_snapshots'`).Scan(&actual); err != nil || actual != snapshotTableSQL {
		return ErrUnsupportedSchema
	}
	return tx.Commit()
}

func (store *Store) Put(ctx context.Context, id string, components map[string]json.RawMessage) (Snapshot, error) {
	if store == nil || store.db == nil || !validID(id) {
		return Snapshot{}, ErrInvalidInput
	}
	encoded, digest, cloned, err := encodeComponents(components)
	if err != nil {
		return Snapshot{}, err
	}
	var next uint64
	err = store.db.QueryRowContext(ctx, `INSERT INTO dev_snapshots(id,payload,digest,version,updated_at_ns) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,digest=excluded.digest,version=dev_snapshots.version+1,updated_at_ns=excluded.updated_at_ns RETURNING version`, id, encoded, digest, 1, time.Now().UTC().UnixNano()).Scan(&next)
	if err != nil {
		return Snapshot{}, err
	}
	store.secureFiles()
	return Snapshot{ID: id, Digest: digest, Version: next, Components: cloned}, nil
}

func (store *Store) Get(ctx context.Context, id string) (Snapshot, error) {
	if store == nil || store.db == nil || !validID(id) {
		return Snapshot{}, ErrInvalidInput
	}
	var encoded []byte
	var digest string
	var version uint64
	err := store.db.QueryRowContext(ctx, `SELECT payload,digest,version FROM dev_snapshots WHERE id=?`, id).Scan(&encoded, &digest, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	actual := digestBytes(encoded)
	if actual != digest {
		return Snapshot{}, ErrIntegrity
	}
	var decoded payload
	if json.Unmarshal(encoded, &decoded) != nil || decoded.SchemaVersion != payloadVersion {
		return Snapshot{}, ErrIntegrity
	}
	canonical, computed, cloned, err := encodeComponents(decoded.Components)
	if err != nil || computed != digest || !bytes.Equal(canonical, encoded) {
		return Snapshot{}, ErrIntegrity
	}
	return Snapshot{ID: id, Digest: digest, Version: version, Components: cloned}, nil
}

func encodeComponents(components map[string]json.RawMessage) ([]byte, string, map[string]json.RawMessage, error) {
	if len(components) == 0 || len(components) > maxComponents {
		return nil, "", nil, ErrInvalidInput
	}
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	cloned := make(map[string]json.RawMessage, len(components))
	total := 0
	for _, name := range names {
		value := components[name]
		if !validComponent(name) || len(value) == 0 || len(value) > maxComponentBytes || !json.Valid(value) {
			return nil, "", nil, ErrInvalidInput
		}
		total += len(value)
		if total > maxPayloadBytes {
			return nil, "", nil, ErrInvalidInput
		}
		var canonical any
		if json.Unmarshal(value, &canonical) != nil {
			return nil, "", nil, ErrInvalidInput
		}
		normalized, err := json.Marshal(canonical)
		if err != nil {
			return nil, "", nil, ErrInvalidInput
		}
		cloned[name] = normalized
	}
	encoded, err := json.Marshal(payload{SchemaVersion: payloadVersion, Components: cloned})
	if err != nil || len(encoded) > maxPayloadBytes {
		return nil, "", nil, ErrInvalidInput
	}
	return encoded, digestBytes(encoded), cloneComponents(cloned), nil
}
func cloneComponents(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for name, value := range source {
		result[name] = append(json.RawMessage(nil), value...)
	}
	return result
}
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validID(value string) bool        { return validToken(value, 256) }
func validComponent(value string) bool { return validToken(value, 64) }
func validToken(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-/", r)) {
			return false
		}
	}
	return true
}
