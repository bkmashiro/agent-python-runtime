// Package fsjournal stores bounded, optional filesystem operation recordings.
package fsjournal

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/fsrecord"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

var ErrClosed = errors.New("fsjournal: store closed")
var ErrNotFound = errors.New("fsjournal: run not found")

// Options limits one run. Zero fields select documented defaults.
type Options struct {
	CaptureContents  bool
	MaxEvents        int
	MaxQueuedBytes   int64
	MaxCapturedBytes int64
	BatchSize        int
	MaxDetailBytes   int
}

func (o Options) normalized() Options {
	if o.MaxEvents == 0 {
		o.MaxEvents = 100000
	}
	if o.MaxQueuedBytes == 0 {
		o.MaxQueuedBytes = 4 << 20
	}
	if o.MaxCapturedBytes == 0 {
		o.MaxCapturedBytes = 64 << 20
	}
	if o.BatchSize == 0 {
		o.BatchSize = 128
	}
	if o.MaxDetailBytes == 0 {
		o.MaxDetailBytes = 16 << 10
	}
	return o
}

// Event is shared with recording-free runtime consumers; SQLite stays optional.
type Event = fsrecord.Event

// Run reports recording completeness separately from the program's own outcome.
type Run struct {
	RunID          string `json:"run_id"`
	ViewRevision   string `json:"view_revision"`
	Complete       bool   `json:"complete"`
	RunError       string `json:"run_error,omitempty"`
	RecordingError string `json:"recording_error,omitempty"`
	EventCount     uint64 `json:"event_count"`
}
type Reference struct {
	RunID    string `json:"run_id"`
	Seq      uint64 `json:"seq"`
	Resource string `json:"resource"`
	Version  uint64 `json:"version"`
}

type Store struct {
	mu        sync.Mutex
	contentMu sync.Mutex
	db        *sql.DB
	closed    bool
	sessions  map[*Session]bool
}
type Session struct {
	mu           sync.Mutex
	store        *Store
	runID        string
	viewRevision string
	opts         Options
	next         uint64
	queued       []Event
	queuedBytes  int64
	captured     int64
	sticky       error
	finished     bool
}

// Open opens an independent SQLite journal. The database file is created privately when new.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("fsjournal: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil && !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		if f != nil {
			_ = f.Close()
		}
	} else if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	db, err := sql.Open("sqlite3", u.String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema := `CREATE TABLE IF NOT EXISTS runs(run_id TEXT PRIMARY KEY, view_revision TEXT NOT NULL, complete INTEGER NOT NULL DEFAULT 0, run_error TEXT NOT NULL DEFAULT '', recording_error TEXT NOT NULL DEFAULT '', event_count INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS events(run_id TEXT NOT NULL, seq INTEGER NOT NULL, operation TEXT NOT NULL, path TEXT NOT NULL, resource TEXT NOT NULL, version INTEGER NOT NULL, offset INTEGER NOT NULL, count INTEGER NOT NULL, error TEXT NOT NULL, detail BLOB, PRIMARY KEY(run_id,seq));
 CREATE TABLE IF NOT EXISTS file_versions(resource TEXT NOT NULL, version INTEGER NOT NULL, content BLOB NOT NULL, PRIMARY KEY(resource,version));
 CREATE INDEX IF NOT EXISTS events_resource_version_run_seq ON events(resource,version,run_id,seq);`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, sessions: map[*Session]bool{}}, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	sessions := s.sessions
	s.sessions = nil
	err := s.db.Close()
	s.mu.Unlock()
	// Session methods take their lock before Store.mu. Release Store.mu before
	// dropping their queued tails; committed prefixes remain incomplete on disk.
	for x := range sessions {
		x.mu.Lock()
		x.queued = nil
		x.queuedBytes = 0
		if x.sticky == nil {
			x.sticky = ErrClosed
		}
		x.mu.Unlock()
	}
	return err
}
func (s *Store) Begin(runID, viewRevision string, options Options) (*Session, error) {
	if runID == "" {
		return nil, errors.New("fsjournal: empty run id")
	}
	o := options.normalized()
	if o.MaxEvents < 0 || o.MaxQueuedBytes < 0 || o.MaxCapturedBytes < 0 || o.BatchSize < 1 || o.MaxDetailBytes < 0 {
		return nil, errors.New("fsjournal: limits must be non-negative and batch size positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if _, err := s.db.Exec(`INSERT INTO runs(run_id,view_revision) VALUES(?,?)`, runID, viewRevision); err != nil {
		return nil, fmt.Errorf("fsjournal begin: %w", err)
	}
	x := &Session{store: s, runID: runID, viewRevision: viewRevision, opts: o}
	s.sessions[x] = true
	return x, nil
}
func (x *Session) fail(err error) error {
	if x.sticky == nil {
		x.sticky = err
		x.store.mu.Lock()
		if !x.store.closed {
			_, _ = x.store.db.Exec(`UPDATE runs SET recording_error=? WHERE run_id=?`, err.Error(), x.runID)
		}
		x.store.mu.Unlock()
	}
	return x.sticky
}
func (x *Session) ViewRevision() string { return x.viewRevision }

func (x *Session) Append(e Event) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.finished {
		return errors.New("fsjournal: session finished")
	}
	if x.sticky != nil {
		return x.sticky
	}
	x.store.mu.Lock()
	closed := x.store.closed
	x.store.mu.Unlock()
	if closed {
		return x.fail(ErrClosed)
	}
	if e.Operation == "" {
		return x.fail(errors.New("fsjournal: empty operation"))
	}
	if e.Seq != 0 {
		return x.fail(errors.New("fsjournal: sequence is assigned automatically"))
	}
	if len(e.Detail) > x.opts.MaxDetailBytes {
		return x.fail(errors.New("fsjournal: detail limit exceeded"))
	}
	if len(e.Detail) > 0 && !json.Valid(e.Detail) {
		return x.fail(errors.New("fsjournal: detail is not valid JSON"))
	}
	size := int64(len(e.Operation) + len(e.Path) + len(e.Resource) + len(e.Error) + len(e.Detail) + 48)
	if x.next >= uint64(x.opts.MaxEvents) {
		return x.fail(errors.New("fsjournal: event limit exceeded"))
	}
	if size > x.opts.MaxQueuedBytes-x.queuedBytes {
		return x.fail(errors.New("fsjournal: queued metadata limit exceeded"))
	}
	x.next++
	e.Seq = x.next
	e.Detail = append(json.RawMessage(nil), e.Detail...)
	x.queued = append(x.queued, e)
	x.queuedBytes += size
	if len(x.queued) >= x.opts.BatchSize {
		if err := x.flushLocked(); err != nil {
			return x.fail(err)
		}
	}
	return nil
}
func (x *Session) flushLocked() error {
	if len(x.queued) == 0 {
		return nil
	}
	s := x.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO events(run_id,seq,operation,path,resource,version,offset,count,error,detail) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, e := range x.queued {
		if _, err = stmt.Exec(x.runID, e.Seq, e.Operation, e.Path, e.Resource, e.Version, e.Offset, e.Count, e.Error, []byte(e.Detail)); err != nil {
			break
		}
	}
	_ = stmt.Close()
	if err != nil {
		tx.Rollback()
		return err
	}
	if _, err = tx.Exec(`UPDATE runs SET event_count=? WHERE run_id=?`, x.next, x.runID); err != nil {
		tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	x.queued = nil
	x.queuedBytes = 0
	return nil
}

// EnsureContent stores immutable bytes once for a resource/version, when capture is enabled.
func (x *Session) EnsureContent(resource string, version uint64, load func() ([]byte, error)) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.finished {
		return errors.New("fsjournal: session finished")
	}
	if x.sticky != nil {
		return x.sticky
	}
	x.store.mu.Lock()
	closed := x.store.closed
	x.store.mu.Unlock()
	if closed {
		return x.fail(ErrClosed)
	}
	if !x.opts.CaptureContents {
		return nil
	}
	if resource == "" || load == nil {
		return x.fail(errors.New("fsjournal: resource and loader required"))
	}
	x.store.contentMu.Lock()
	defer x.store.contentMu.Unlock()
	s := x.store
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return x.fail(ErrClosed)
	}
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM file_versions WHERE resource=? AND version=?`, resource, version).Scan(&exists)
	s.mu.Unlock()
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return x.fail(err)
	}
	data, err := load()
	if err != nil {
		return x.fail(fmt.Errorf("fsjournal load content: %w", err))
	}
	if int64(len(data)) > x.opts.MaxCapturedBytes-x.captured {
		return x.fail(errors.New("fsjournal: captured content limit exceeded"))
	}
	if data == nil {
		data = []byte{}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return x.fail(ErrClosed)
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO file_versions(resource,version,content) VALUES(?,?,?)`, resource, version, data)
	var n int64
	if err == nil {
		_ = s.db.QueryRow(`SELECT length(content) FROM file_versions WHERE resource=? AND version=?`, resource, version).Scan(&n)
	}
	s.mu.Unlock()
	if err != nil {
		return x.fail(err)
	}
	x.captured += n
	return nil
}

// Finish flushes all accepted events; recording failures never mark the run complete.
func (x *Session) Finish(runError error) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.finished {
		return errors.New("fsjournal: session already finished")
	}
	if x.sticky == nil {
		if err := x.flushLocked(); err != nil {
			x.fail(err)
		}
	}
	x.finished = true
	s := x.store
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, x)
	if s.closed {
		if x.sticky == nil {
			x.sticky = ErrClosed
		}
		return x.sticky
	}
	re := ""
	if runError != nil {
		re = runError.Error()
	}
	complete := 0
	if x.sticky == nil {
		complete = 1
	}
	_, err := s.db.Exec(`UPDATE runs SET run_error=?,recording_error=?,complete=? WHERE run_id=?`, re, errorString(x.sticky), complete, x.runID)
	if err != nil {
		return err
	}
	return x.sticky
}
func errorString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// Events returns an error for a missing run, never an empty-success result.
func (s *Store) Events(runID string) (Run, []Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Run{}, nil, ErrClosed
	}
	var r Run
	var complete int
	err := s.db.QueryRow(`SELECT run_id,view_revision,complete,run_error,recording_error,event_count FROM runs WHERE run_id=?`, runID).Scan(&r.RunID, &r.ViewRevision, &complete, &r.RunError, &r.RecordingError, &r.EventCount)
	if err == sql.ErrNoRows {
		return Run{}, nil, ErrNotFound
	}
	if err != nil {
		return Run{}, nil, err
	}
	r.Complete = complete == 1
	rows, err := s.db.Query(`SELECT seq,operation,path,resource,version,offset,count,error,detail FROM events WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var d []byte
		if err = rows.Scan(&e.Seq, &e.Operation, &e.Path, &e.Resource, &e.Version, &e.Offset, &e.Count, &e.Error, &d); err != nil {
			return r, nil, err
		}
		e.Detail = append(json.RawMessage(nil), d...)
		out = append(out, e)
	}
	return r, out, rows.Err()
}

// Find returns references in deterministic run/sequence order; nil version matches all versions.
func (s *Store) Find(resource string, version *uint64) ([]Reference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	q := `SELECT resource,version,run_id,seq FROM events WHERE resource=?`
	args := []any{resource}
	if version != nil {
		q += ` AND version=?`
		args = append(args, *version)
	}
	q += ` ORDER BY run_id,seq`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reference
	for rows.Next() {
		var r Reference
		if err = rows.Scan(&r.Resource, &r.Version, &r.RunID, &r.Seq); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Content returns a copy of one captured resource version; absent content returns sql.ErrNoRows.
func (s *Store) Content(resource string, version uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	var content []byte
	err := s.db.QueryRow(`SELECT content FROM file_versions WHERE resource=? AND version=?`, resource, version).Scan(&content)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), content...), nil
}
