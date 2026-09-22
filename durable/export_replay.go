package durable

import (
	"bytes"
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
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

const (
	// RunBundleVersion is the on-disk format version. Bundles are deliberately
	// small, local artifacts rather than a general export/ledger format.
	RunBundleVersion = "pysolate-durable-run/v1"
	// RunBundlePrivacyWarning is included in every bundle because it contains
	// source, inputs and complete tool outcomes.
	RunBundlePrivacyWarning = "SENSITIVE LOCAL EXPORT: contains full source, inputs, ordered tool arguments and outcomes. Do not upload or share."
	maxBundleBytes          = int64(128 << 20)
)

var (
	ErrBundleInvalid  = errors.New("invalid durable run bundle")
	ErrBundleMismatch = errors.New("offline replay mismatch")
)

// RunBundle is a versioned, JSON-only local export of one ended durable Run.
// It grants no provider or workspace capability. Payloads may still contain
// credentials or other secrets returned by tools.
type RunBundle struct {
	Version        string       `json:"version"`
	PrivacyWarning string       `json:"privacy_warning"`
	Run            BundleRun    `json:"run"`
	Calls          []BundleCall `json:"calls"`
	Waits          []BundleWait `json:"waits,omitempty"`
}

// BundleRun is the persisted definition and terminal result needed to compare
// a fresh offline Guest execution with the original Run.
type BundleRun struct {
	ID                 string          `json:"id"`
	Code               string          `json:"code"`
	Seed               string          `json:"seed"`
	ArtifactSHA256     string          `json:"artifact_sha256"`
	EnvironmentVersion string          `json:"environment_version"`
	Inputs             json.RawMessage `json:"inputs"`
	Tools              json.RawMessage `json:"tools"`
	Status             string          `json:"status"`
	Outcome            json.RawMessage `json:"outcome"`
	Reason             string          `json:"reason,omitempty"`
}

// BundleCall retains every ordered call, including duplicate tool/argument
// pairs. Outcome is the exact wire response delivered to Python.
type BundleCall struct {
	Sequence     uint32          `json:"sequence"`
	CallID       string          `json:"call_id"`
	Tool         string          `json:"tool"`
	OperationKey string          `json:"operation_key"`
	State        string          `json:"state"`
	Arguments    json.RawMessage `json:"arguments"`
	Outcome      json.RawMessage `json:"outcome"`
}

// BundleWait proves that every durable wait belonging to the exported Run was
// resolved before export. Replay consumes the completed call outcome and does
// not recreate a live wait.
type BundleWait struct {
	ID             string          `json:"id"`
	Sequence       uint32          `json:"sequence"`
	Kind           string          `json:"kind"`
	Request        json.RawMessage `json:"request,omitempty"`
	Deadline       string          `json:"deadline,omitempty"`
	DecisionResult json.RawMessage `json:"decision_result,omitempty"`
	DecisionError  string          `json:"decision_error,omitempty"`
}

// Bundle is kept as a short alias for callers that prefer the generic name.
type Bundle = RunBundle

// ExportRun opens an existing database read-only, without migrations or chmod.
func ExportRun(ctx context.Context, databasePath, runID, outputPath string) error {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: database must exist", ErrBundleInvalid)
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != schemaVersion {
		return fmt.Errorf("%w: database schema differs", ErrBundleInvalid)
	}
	return (&Store{db: db}).ExportTo(ctx, runID, outputPath)
}

// Export reads one terminal, fully resolved Run without claiming it or
// changing any durable state. Statuses that could hide unfinished work are
// rejected, including cancelled Runs.
func (s *Store) Export(ctx context.Context, runID string) (RunBundle, error) {
	if s == nil || s.db == nil || runID == "" {
		return RunBundle{}, ErrNotFound
	}
	run, err := s.Get(ctx, runID)
	if err != nil {
		return RunBundle{}, err
	}
	if run.Status != StatusCompleted && run.Status != StatusFailed {
		return RunBundle{}, fmt.Errorf("%w: run is not an ended successful or failed run", ErrBundleInvalid)
	}
	if err := validateBundleRun(BundleRun{
		ID: run.Definition.ID, Code: run.Definition.Code, Seed: run.Definition.Seed,
		ArtifactSHA256: run.Definition.ArtifactSHA256, EnvironmentVersion: run.Definition.EnvironmentVersion,
		Inputs: run.Definition.Inputs, Tools: run.Definition.Tools, Status: run.Status,
		Outcome: run.Outcome, Reason: run.Reason,
	}); err != nil {
		return RunBundle{}, err
	}

	calls, err := s.exportCalls(ctx, runID)
	if err != nil {
		return RunBundle{}, err
	}
	waits, err := s.exportWaits(ctx, runID)
	if err != nil {
		return RunBundle{}, err
	}
	bundle := RunBundle{
		Version: RunBundleVersion, PrivacyWarning: RunBundlePrivacyWarning,
		Run: BundleRun{
			ID: run.Definition.ID, Code: run.Definition.Code, Seed: run.Definition.Seed,
			ArtifactSHA256: run.Definition.ArtifactSHA256, EnvironmentVersion: run.Definition.EnvironmentVersion,
			Inputs: cloneRaw(run.Definition.Inputs), Tools: cloneRaw(run.Definition.Tools), Status: run.Status,
			Outcome: cloneRaw(run.Outcome), Reason: run.Reason,
		},
		Calls: calls, Waits: waits,
	}
	if err := validateRunBundle(bundle); err != nil {
		return RunBundle{}, err
	}
	return bundle, nil
}

// ExportTo writes one explicit local export with mode 0600 and O_EXCL. It
// never overwrites an existing path and does not create or upload anything.
func (s *Store) ExportTo(ctx context.Context, runID, outputPath string) error {
	if outputPath == "" {
		return fmt.Errorf("%w: output path is required", ErrBundleInvalid)
	}
	bundle, err := s.Export(ctx, runID)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("%w: invalid output path", ErrBundleInvalid)
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write local bundle: %w", err)
	}
	created := true
	defer func() {
		_ = file.Close()
		if created {
			_ = os.Remove(absolute)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("protect local bundle: %w", err)
	}
	encoder := json.NewEncoder(&bundleWriter{Writer: file, remaining: maxBundleBytes})
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(bundle); err != nil {
		return fmt.Errorf("write local bundle: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local bundle: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close local bundle: %w", err)
	}
	created = false
	return nil
}

type bundleWriter struct {
	io.Writer
	remaining int64
}

func (w *bundleWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("%w: encoded bundle is too large", ErrBundleInvalid)
	}
	n, err := w.Writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// ReadBundle reads and validates a local bundle without opening a Store.
func ReadBundle(path string) (RunBundle, error) {
	if path == "" {
		return RunBundle{}, fmt.Errorf("%w: bundle path is required", ErrBundleInvalid)
	}
	info, err := os.Stat(path)
	if err != nil {
		return RunBundle{}, fmt.Errorf("read local bundle: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBundleBytes {
		return RunBundle{}, fmt.Errorf("%w: bundle size or file type is unsupported", ErrBundleInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return RunBundle{}, fmt.Errorf("read local bundle: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBundleBytes+1))
	if err != nil || int64(len(data)) > maxBundleBytes {
		return RunBundle{}, fmt.Errorf("%w: bundle is too large", ErrBundleInvalid)
	}
	var bundle RunBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return RunBundle{}, fmt.Errorf("%w: bundle JSON is invalid", ErrBundleInvalid)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return RunBundle{}, fmt.Errorf("%w: bundle has trailing data", ErrBundleInvalid)
	}
	if err := validateRunBundle(bundle); err != nil {
		return RunBundle{}, err
	}
	return bundle, nil
}

// ReplayBundle reconstructs a fresh Guest from the supplied artifact and runs
// only against the exported ordered journal. The tool catalog consists of
// non-dispatching stubs; all outcomes must come from the bundle. This function
// does not open or mutate a Store.
func ReplayBundle(ctx context.Context, bundle RunBundle, guest []byte) (pysolate.Output, error) {
	if err := validateRunBundle(bundle); err != nil {
		return pysolate.Output{}, err
	}
	if len(guest) == 0 {
		return pysolate.Output{}, fmt.Errorf("%w: Guest artifact is empty", ErrBundleInvalid)
	}
	actualArtifact := fmt.Sprintf("sha256:%x", sha256.Sum256(guest))
	if actualArtifact != bundle.Run.ArtifactSHA256 {
		return pysolate.Output{}, fmt.Errorf("%w: Guest artifact identity differs", ErrBundleMismatch)
	}

	manifest, err := offlineManifest(bundle.Run.Tools)
	if err != nil {
		return pysolate.Output{}, err
	}
	runner, err := pysolate.New(ctx, guest, manifest)
	if err != nil {
		return pysolate.Output{}, fmt.Errorf("%w: Guest could not be reconstructed", ErrBundleInvalid)
	}
	defer runner.Close(context.Background())
	journal := &offlineJournal{runID: bundle.Run.ID, calls: bundle.Calls}
	output, runErr := runner.RunRecorded(ctx, bundle.Run.Code, json.RawMessage(bundle.Run.Inputs), bundle.Run.Seed, journal)
	if journal.index != len(bundle.Calls) {
		return output, fmt.Errorf("%w: recorded call history was not fully consumed", ErrBundleMismatch)
	}
	if err := compareReplay(bundle.Run, output, runErr); err != nil {
		return output, err
	}
	return output, nil
}

func (s *Store) exportCalls(ctx context.Context, runID string) ([]BundleCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence, call_id, tool, operation_key, state, arguments, outcome
		FROM calls WHERE run_id=? ORDER BY sequence`, runID)
	if err != nil {
		return nil, fmt.Errorf("read durable call history: %w", err)
	}
	defer rows.Close()
	calls := make([]BundleCall, 0)
	for rows.Next() {
		var call BundleCall
		var args, outcome []byte
		if err := rows.Scan(&call.Sequence, &call.CallID, &call.Tool, &call.OperationKey, &call.State, &args, &outcome); err != nil {
			return nil, fmt.Errorf("read durable call history: %w", err)
		}
		call.Arguments, call.Outcome = cloneRaw(args), cloneRaw(outcome)
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read durable call history: %w", err)
	}
	return calls, nil
}

func (s *Store) exportWaits(ctx context.Context, runID string) ([]BundleWait, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT wait_id, sequence, kind, request, deadline, resolved, decision_result, decision_error
		FROM waits WHERE run_id=? ORDER BY sequence`, runID)
	if err != nil {
		return nil, fmt.Errorf("read durable wait history: %w", err)
	}
	defer rows.Close()
	waits := make([]BundleWait, 0)
	for rows.Next() {
		var wait BundleWait
		var request, result []byte
		var deadline sql.NullString
		var resolved int
		if err := rows.Scan(&wait.ID, &wait.Sequence, &wait.Kind, &request, &deadline, &resolved, &result, &wait.DecisionError); err != nil {
			return nil, fmt.Errorf("read durable wait history: %w", err)
		}
		if resolved == 0 {
			return nil, fmt.Errorf("%w: unresolved durable wait", ErrBundleInvalid)
		}
		wait.Request, wait.DecisionResult = cloneRaw(request), cloneRaw(result)
		if deadline.Valid {
			wait.Deadline = deadline.String
		}
		waits = append(waits, wait)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read durable wait history: %w", err)
	}
	return waits, nil
}

func validateRunBundle(bundle RunBundle) error {
	if bundle.Version != RunBundleVersion || bundle.PrivacyWarning != RunBundlePrivacyWarning {
		return fmt.Errorf("%w: unsupported bundle version or privacy marker", ErrBundleInvalid)
	}
	if err := validateBundleRun(bundle.Run); err != nil {
		return err
	}
	if len(bundle.Calls) > 0 && bundle.Calls[0].Sequence != 0 {
		return fmt.Errorf("%w: call sequence does not start at zero", ErrBundleInvalid)
	}
	for index, call := range bundle.Calls {
		if err := validateBundleCall(bundle.Run.ID, uint32(index), call); err != nil {
			return err
		}
	}
	var payload uint64 = definitionPayload(Definition{
		ID: bundle.Run.ID, Code: bundle.Run.Code, Seed: bundle.Run.Seed,
		ArtifactSHA256: bundle.Run.ArtifactSHA256, EnvironmentVersion: bundle.Run.EnvironmentVersion,
		Inputs: bundle.Run.Inputs, Tools: bundle.Run.Tools,
	})
	payload += uint64(len(bundle.Run.Outcome) + len(bundle.Run.Reason))
	for _, call := range bundle.Calls {
		payload += uint64(len(call.CallID) + len(call.Tool) + len(call.OperationKey) + len(call.Arguments) + len(call.Outcome))
	}
	for _, wait := range bundle.Waits {
		payload += uint64(len(wait.ID) + len(wait.Kind) + len(wait.Deadline) + len(wait.Request) + len(wait.DecisionResult) + len(wait.DecisionError))
	}
	if payload > MaxRunPayloadBytes {
		return fmt.Errorf("%w: bundle exceeds durable payload limit", ErrBundleInvalid)
	}
	seenWaits := make(map[uint32]bool, len(bundle.Waits))
	for _, wait := range bundle.Waits {
		if wait.ID != waitKey(bundle.Run.ID, wait.Sequence) || wait.Kind == "" || wait.Sequence >= uint32(len(bundle.Calls)) || seenWaits[wait.Sequence] {
			return fmt.Errorf("%w: invalid durable wait metadata", ErrBundleInvalid)
		}
		seenWaits[wait.Sequence] = true
		if wait.Deadline != "" {
			if _, err := time.Parse(time.RFC3339Nano, wait.Deadline); err != nil {
				return fmt.Errorf("%w: invalid durable wait deadline", ErrBundleInvalid)
			}
		}
		if len(wait.Request) != 0 && !json.Valid(wait.Request) {
			return fmt.Errorf("%w: invalid durable wait request", ErrBundleInvalid)
		}
		if len(wait.DecisionResult) != 0 && !json.Valid(wait.DecisionResult) {
			return fmt.Errorf("%w: invalid durable wait decision", ErrBundleInvalid)
		}
	}
	return nil
}

func validateBundleRun(run BundleRun) error {
	if run.ID == "" || run.Code == "" || run.Seed == "" || run.ArtifactSHA256 == "" || run.EnvironmentVersion == "" {
		return fmt.Errorf("%w: incomplete run definition", ErrBundleInvalid)
	}
	if run.Status != StatusCompleted && run.Status != StatusFailed {
		return fmt.Errorf("%w: run status is not replayable", ErrBundleInvalid)
	}
	if len(run.Inputs) == 0 || !json.Valid(run.Inputs) || len(run.Tools) != 0 && !json.Valid(run.Tools) {
		return fmt.Errorf("%w: run definition JSON is invalid", ErrBundleInvalid)
	}
	if len(run.Outcome) == 0 || !json.Valid(run.Outcome) {
		return fmt.Errorf("%w: terminal outcome is invalid", ErrBundleInvalid)
	}
	if run.Status == StatusCompleted && run.Reason != "" {
		return fmt.Errorf("%w: completed run has a failure reason", ErrBundleInvalid)
	}
	if run.Status == StatusFailed && run.Reason == "" {
		return fmt.Errorf("%w: failed run has no failure reason", ErrBundleInvalid)
	}
	var declarations []toolDeclaration
	if len(run.Tools) != 0 {
		if err := json.Unmarshal(run.Tools, &declarations); err != nil {
			return fmt.Errorf("%w: tool metadata is invalid", ErrBundleInvalid)
		}
	}
	for _, declaration := range declarations {
		if declaration.Name == "" || declaration.Version == "" || declaration.PythonPath == "" {
			return fmt.Errorf("%w: tool Python path metadata is missing", ErrBundleInvalid)
		}
	}
	return nil
}

func validateBundleCall(runID string, sequence uint32, call BundleCall) error {
	if call.Sequence != sequence || call.CallID != fmt.Sprintf("call-%d", sequence) || call.Tool == "" ||
		call.OperationKey != operationKey(runID, sequence) || call.State != CallCompleted || len(call.Outcome) == 0 {
		return fmt.Errorf("%w: call history identity is invalid", ErrBundleInvalid)
	}
	if len(call.Arguments) == 0 || !json.Valid(call.Arguments) || !json.Valid(call.Outcome) {
		return fmt.Errorf("%w: call history JSON is invalid", ErrBundleInvalid)
	}
	var wire struct {
		Value json.RawMessage `json:"value"`
		Error *string         `json:"error"`
	}
	if err := json.Unmarshal(call.Outcome, &wire); err != nil || wire.Value == nil {
		return fmt.Errorf("%w: call outcome is incomplete", ErrBundleInvalid)
	}
	return nil
}

func offlineManifest(raw json.RawMessage) (pysolate.Manifest, error) {
	var declarations []toolDeclaration
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &declarations); err != nil {
			return nil, fmt.Errorf("%w: tool metadata is invalid", ErrBundleInvalid)
		}
	}
	manifest := make(pysolate.Manifest, len(declarations))
	for _, declaration := range declarations {
		name := declaration.Name
		if _, exists := manifest[name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool metadata", ErrBundleInvalid)
		}
		manifest[name] = pysolate.ToolSpec{
			PythonPath: declaration.PythonPath,
			Call: func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("offline replay tool dispatch denied")
			},
		}
	}
	return manifest, nil
}

type offlineJournal struct {
	runID string
	calls []BundleCall
	index int
}

func (journal *offlineJournal) Call(_ context.Context, tool string, args json.RawMessage, _ func(context.Context) []byte) ([]byte, error) {
	if journal.index >= len(journal.calls) {
		return nil, fmt.Errorf("%w: excess tool call", ErrBundleMismatch)
	}
	call := journal.calls[journal.index]
	sequence := uint32(journal.index)
	if call.Sequence != sequence || call.Tool != tool || !sameJSON(call.Arguments, args) || call.OperationKey != operationKey(journal.runID, sequence) {
		return nil, fmt.Errorf("%w: call identity differs", ErrBundleMismatch)
	}
	journal.index++
	return append([]byte(nil), call.Outcome...), nil
}

func compareReplay(run BundleRun, output pysolate.Output, runErr error) error {
	var expected pysolate.Output
	if err := json.Unmarshal(run.Outcome, &expected); err != nil {
		return fmt.Errorf("%w: terminal output is invalid", ErrBundleInvalid)
	}
	if !sameJSON(expected.Value, output.Value) || expected.Stdout != output.Stdout || expected.Transformed != output.Transformed {
		return fmt.Errorf("%w: result or stdout differs", ErrBundleMismatch)
	}
	switch run.Status {
	case StatusCompleted:
		if runErr != nil {
			return fmt.Errorf("%w: replay ended with an error", ErrBundleMismatch)
		}
	case StatusFailed:
		var pythonErr *pysolate.PythonError
		if runErr == nil || !errors.As(runErr, &pythonErr) || pythonErr.Message != run.Reason {
			return fmt.Errorf("%w: replay error differs", ErrBundleMismatch)
		}
	default:
		return fmt.Errorf("%w: unsupported terminal status", ErrBundleInvalid)
	}
	return nil
}

func sameJSON(left, right json.RawMessage) bool {
	if len(left) == 0 {
		left = json.RawMessage("null")
	}
	if len(right) == 0 {
		right = json.RawMessage("null")
	}
	// RawMessage marshaling normalizes whitespace and Go's HTML escaping
	// without parsing numbers through float64 or reordering object members.
	encodedLeft, leftErr := json.Marshal(left)
	encodedRight, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(encodedLeft, encodedRight)
}
