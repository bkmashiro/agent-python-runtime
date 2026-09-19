package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type provider struct {
	db        *sql.DB
	holdPoint string
}

type providerEffect struct {
	OperationKey string          `json:"operation_key"`
	Kind         string          `json:"kind"`
	Parameters   json.RawMessage `json:"parameters"`
	Result       json.RawMessage `json:"result"`
}

type providerStatus struct {
	ReadDispatches uint64           `json:"read_dispatches"`
	WriteRequests  uint64           `json:"write_requests"`
	EffectCount    uint64           `json:"effect_count"`
	Effects        []providerEffect `json:"effects"`
}

func openProvider(path, holdPoint string) (*provider, error) {
	if path == "" {
		return nil, fmt.Errorf("provider database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	if file, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		_ = file.Chmod(0o600)
		_ = file.Close()
	} else {
		return nil, err
	}
	q := url.Values{}
	for _, pragma := range []string{"journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)"} {
		q.Add("_pragma", pragma)
	}
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	p := &provider{db: db, holdPoint: holdPoint}
	if err := p.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

func (p *provider) configure() error {
	_, err := p.db.Exec(`CREATE TABLE IF NOT EXISTS provider_config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS provider_counts (kind TEXT PRIMARY KEY, count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS provider_effects (
 operation_key TEXT PRIMARY KEY NOT NULL,
 kind TEXT NOT NULL,
 parameters BLOB NOT NULL,
 result BLOB NOT NULL,
 committed_at TEXT NOT NULL
)`)
	return err
}

func (p *provider) close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

func (p *provider) setReadValue(value string) error {
	if value == "" {
		return nil
	}
	_, err := p.db.Exec(`INSERT INTO provider_config(key,value) VALUES('read_value',?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value)
	return err
}

func (p *provider) read(ctx context.Context, operationKey string, args json.RawMessage) (json.RawMessage, error) {
	if operationKey == "" {
		return nil, fmt.Errorf("read missing operation key")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := increment(ctx, tx, "read_dispatches"); err != nil {
		return nil, err
	}
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM provider_config WHERE key='read_value'`).Scan(&value); err != nil {
		return nil, fmt.Errorf("read fixture value: %w", err)
	}
	result, _ := json.Marshal(map[string]string{"value": value})
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = args
	return result, nil
}

func (p *provider) write(ctx context.Context, operationKey string, args json.RawMessage) (json.RawMessage, error) {
	if operationKey == "" {
		return nil, fmt.Errorf("write missing operation key")
	}
	var input struct {
		ReadValue   int `json:"read_value"`
		RandomValue int `json:"random_value"`
		Computed    int `json:"computed"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := increment(ctx, tx, "write_requests"); err != nil {
		return nil, err
	}
	result, _ := json.Marshal(map[string]any{"written": true, "computed": input.Computed, "operation_key": operationKey})
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_effects(operation_key,kind,parameters,result,committed_at)
VALUES(?,?,?,?,?) ON CONFLICT(operation_key) DO NOTHING`, operationKey, "write", args, result, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT result FROM provider_effects WHERE operation_key=?`, operationKey).Scan(&result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if p.holdPoint == "provider-committed" {
		event, _ := json.Marshal(map[string]any{"event": "provider-committed", "operation_key": operationKey, "kind": "write"})
		fmt.Println(string(event))
		_ = os.Stdout.Sync()
		time.Sleep(365 * 24 * time.Hour)
	}
	return result, nil
}

func increment(ctx context.Context, tx *sql.Tx, kind string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO provider_counts(kind,count) VALUES(?,1)
ON CONFLICT(kind) DO UPDATE SET count=count+1`, kind)
	return err
}

func (p *provider) status(ctx context.Context) (providerStatus, error) {
	var status providerStatus
	for kind, target := range map[string]*uint64{"read_dispatches": &status.ReadDispatches, "write_requests": &status.WriteRequests} {
		if err := p.db.QueryRowContext(ctx, `SELECT count FROM provider_counts WHERE kind=?`, kind).Scan(target); err != nil && err != sql.ErrNoRows {
			return status, err
		}
	}
	if err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_effects`).Scan(&status.EffectCount); err != nil {
		return status, err
	}
	rows, err := p.db.QueryContext(ctx, `SELECT operation_key,kind,parameters,result FROM provider_effects ORDER BY operation_key`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var effect providerEffect
		if err := rows.Scan(&effect.OperationKey, &effect.Kind, &effect.Parameters, &effect.Result); err != nil {
			return status, err
		}
		status.Effects = append(status.Effects, effect)
	}
	return status, rows.Err()
}
