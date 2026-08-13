// Package agentfunction implements explicit, project-private whole-function
// result reuse. It is not a general Python purity detector or global cache.
package agentfunction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

const (
	InvocationSchemaVersion  = "pysolate.agent-function.v1"
	cacheRecordSchemaVersion = "pysolate.agent-function-cache.v1"
)

type Admission string

const (
	Cacheable    Admission = "cacheable"
	NotCacheable Admission = "not_cacheable"
)

var (
	ErrInvalidInvocation = errors.New("invalid agent function invocation")
	ErrInvalidStore      = errors.New("invalid agent function store")
	ErrProjectPartition  = errors.New("agent function project partition mismatch")
	ErrCachePurity       = errors.New("cacheable agent function attempted forbidden authority")
	ErrPhysicalExecution = errors.New("invalid physical execution identity")
	ErrResultTooLarge    = errors.New("agent function result exceeds store bound")
	ErrRetentionLimit    = errors.New("agent function store retention limit reached")
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var partitionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Invocation struct {
	SchemaVersion               string          `json:"schema_version"`
	Admission                   Admission       `json:"admission"`
	ProjectSHA256               string          `json:"project_sha256"`
	FunctionSourceSHA256        string          `json:"function_source_sha256"`
	ArtifactSHA256              string          `json:"artifact_sha256"`
	ExecutionProfileSHA256      string          `json:"execution_profile_sha256"`
	ImportClosureSHA256         string          `json:"import_closure_sha256"`
	CanonicalInputs             json.RawMessage `json:"canonical_inputs"`
	ImmutableRootSHA256         []string        `json:"immutable_root_sha256"`
	DeterministicSettingsSHA256 string          `json:"deterministic_settings_sha256"`
	OutputSchemaSHA256          string          `json:"output_schema_sha256"`
	PrivacyPartition            string          `json:"privacy_partition"`
	PolicyEpochSHA256           string          `json:"policy_epoch_sha256"`
}

func (invocation Invocation) Validate() error {
	if invocation.SchemaVersion != InvocationSchemaVersion ||
		(invocation.Admission != Cacheable && invocation.Admission != NotCacheable) ||
		!partitionPattern.MatchString(invocation.PrivacyPartition) || !canonicalJSON(invocation.CanonicalInputs) {
		return ErrInvalidInvocation
	}
	for _, digest := range []string{
		invocation.ProjectSHA256, invocation.FunctionSourceSHA256, invocation.ArtifactSHA256,
		invocation.ExecutionProfileSHA256, invocation.ImportClosureSHA256,
		invocation.DeterministicSettingsSHA256, invocation.OutputSchemaSHA256, invocation.PolicyEpochSHA256,
	} {
		if !digestPattern.MatchString(digest) {
			return ErrInvalidInvocation
		}
	}
	if !sort.StringsAreSorted(invocation.ImmutableRootSHA256) {
		return ErrInvalidInvocation
	}
	for index, digest := range invocation.ImmutableRootSHA256 {
		if !digestPattern.MatchString(digest) || (index > 0 && invocation.ImmutableRootSHA256[index-1] == digest) {
			return ErrInvalidInvocation
		}
	}
	return nil
}

func (invocation Invocation) Identity() (string, []byte, error) {
	if err := invocation.Validate(); err != nil {
		return "", nil, err
	}
	document, err := json.Marshal(invocation)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:]), document, nil
}

func canonicalJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	canonical, err := json.Marshal(value)
	return err == nil && bytes.Equal(canonical, raw)
}

type Guard struct {
	enforce             bool
	violated            bool
	physicalExecutionID string
}

func (guard *Guard) forbid() error {
	if guard == nil || !guard.enforce {
		return nil
	}
	guard.violated = true
	return ErrCachePurity
}
func (guard *Guard) HostCall(string) error       { return guard.forbid() }
func (guard *Guard) UndeclaredRead(string) error { return guard.forbid() }
func (guard *Guard) SharedWrite(string) error    { return guard.forbid() }
func (guard *Guard) Clock() error                { return guard.forbid() }
func (guard *Guard) Random() error               { return guard.forbid() }
func (guard *Guard) DynamicImport(string) error  { return guard.forbid() }

func (guard *Guard) BindPhysicalExecution(id string) error {
	if guard == nil || !partitionPattern.MatchString(id) || guard.physicalExecutionID != "" {
		return ErrPhysicalExecution
	}
	guard.physicalExecutionID = id
	return nil
}

type ComputeFunc func(context.Context, *Guard) ([]byte, error)

type Disposition string

const (
	Independent Disposition = "independent"
	Leader      Disposition = "leader"
	Waiter      Disposition = "waiter"
	Retained    Disposition = "retained"
)

type Result struct {
	Key                 string
	Value               []byte
	CacheHit            bool
	Shared              bool
	PhysicalExecutionID string
	Disposition         Disposition
}

type Engine struct {
	Store        *Store
	CacheEnabled bool
	Flights      *FlightGroup
}

func (engine Engine) Execute(ctx context.Context, invocation Invocation, compute ComputeFunc) (Result, error) {
	return engine.execute(ctx, invocation, compute, "callback")
}

func (engine Engine) execute(ctx context.Context, invocation Invocation, compute ComputeFunc, flightDomain string) (Result, error) {
	if compute == nil {
		return Result{}, ErrInvalidInvocation
	}
	key, _, err := invocation.Identity()
	if err != nil {
		return Result{}, err
	}
	if engine.Store != nil && engine.Store.projectSHA256 != invocation.ProjectSHA256 {
		return Result{}, ErrProjectPartition
	}
	useCache := invocation.Admission == Cacheable && engine.CacheEnabled
	if useCache && engine.Store == nil {
		return Result{}, ErrInvalidStore
	}
	execute := func() (Result, error) {
		if useCache {
			if value, hit := engine.Store.get(key); hit {
				return Result{Key: key, Value: value, CacheHit: true, Disposition: Retained}, nil
			}
		}
		guard := &Guard{enforce: invocation.Admission == Cacheable}
		value, err := compute(ctx, guard)
		if err != nil {
			return Result{}, err
		}
		if guard.violated {
			return Result{}, ErrCachePurity
		}
		value = append([]byte(nil), value...)
		if useCache {
			if err := engine.Store.put(key, value); err != nil {
				return Result{}, err
			}
		}
		return Result{Key: key, Value: value, PhysicalExecutionID: guard.physicalExecutionID}, nil
	}
	if engine.Flights != nil && invocation.Admission == Cacheable {
		return engine.Flights.Do(ctx, flightDomain+":"+key, execute)
	}
	result, err := execute()
	if err == nil && result.Disposition == "" {
		result.Disposition = Independent
	}
	return result, err
}

type Stats struct {
	Hits        uint64
	Misses      uint64
	Writes      uint64
	Evictions   uint64
	Corruptions uint64
	StoredBytes uint64
}

type Store struct {
	directory      string
	projectSHA256  string
	maxResultBytes uint64
	maxStoredBytes uint64
	mu             sync.Mutex
	stats          Stats
}

type cacheRecord struct {
	SchemaVersion string `json:"schema_version"`
	Key           string `json:"key"`
	Result        []byte `json:"result"`
	ResultSHA256  string `json:"result_sha256"`
}

func NewStore(directory, projectSHA256 string, maxResultBytes uint64) (*Store, error) {
	if maxResultBytes == 0 || maxResultBytes > ^uint64(0)/64 {
		return nil, ErrInvalidStore
	}
	return NewBoundedStore(directory, projectSHA256, maxResultBytes, maxResultBytes*64)
}

func NewBoundedStore(directory, projectSHA256 string, maxResultBytes, maxStoredBytes uint64) (*Store, error) {
	if directory == "" || !filepath.IsAbs(directory) || !digestPattern.MatchString(projectSHA256) || maxResultBytes == 0 || maxStoredBytes < maxResultBytes {
		return nil, ErrInvalidStore
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create agent function store: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect agent function store: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return nil, ErrInvalidStore
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect agent function store: %w", err)
	}
	var storedBytes uint64
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil || entry.Type()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() || entryInfo.Size() < 0 {
			return nil, ErrInvalidStore
		}
		storedBytes += uint64(entryInfo.Size())
		if storedBytes > maxStoredBytes {
			return nil, ErrRetentionLimit
		}
	}
	return &Store{
		directory: directory, projectSHA256: projectSHA256,
		maxResultBytes: maxResultBytes, maxStoredBytes: maxStoredBytes,
		stats: Stats{StoredBytes: storedBytes},
	}, nil
}

func (store *Store) Directory() string {
	if store == nil {
		return ""
	}
	return store.directory
}

func (store *Store) Stats() Stats {
	if store == nil {
		return Stats{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.stats
}

func (store *Store) get(key string) ([]byte, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	path, ok := store.path(key)
	if !ok {
		store.stats.Misses++
		return nil, false
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		store.stats.Misses++
		return nil, false
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		store.corruptLocked(path)
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		store.corruptLocked(path)
		return nil, false
	}
	var record cacheRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		record.SchemaVersion != cacheRecordSchemaVersion || record.Key != key || uint64(len(record.Result)) > store.maxResultBytes {
		store.corruptLocked(path)
		return nil, false
	}
	digest := sha256.Sum256(record.Result)
	if record.ResultSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		store.corruptLocked(path)
		return nil, false
	}
	store.stats.Hits++
	return append([]byte(nil), record.Result...), true
}

func (store *Store) corruptLocked(path string) {
	if info, err := os.Lstat(path); err == nil && info.Size() >= 0 && uint64(info.Size()) <= store.stats.StoredBytes {
		store.stats.StoredBytes -= uint64(info.Size())
	}
	_ = os.Remove(path)
	store.stats.Corruptions++
	store.stats.Misses++
}

func (store *Store) put(key string, result []byte) error {
	if uint64(len(result)) > store.maxResultBytes {
		return ErrResultTooLarge
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	path, ok := store.path(key)
	if !ok {
		return ErrInvalidInvocation
	}
	digest := sha256.Sum256(result)
	record := cacheRecord{
		SchemaVersion: cacheRecordSchemaVersion, Key: key, Result: append(json.RawMessage(nil), result...),
		ResultSHA256: "sha256:" + hex.EncodeToString(digest[:]),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	var replacedBytes uint64
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 {
			return ErrInvalidStore
		}
		replacedBytes = uint64(info.Size())
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if uint64(len(raw))+store.stats.StoredBytes-replacedBytes > store.maxStoredBytes {
		return ErrRetentionLimit
	}
	temporary, err := os.CreateTemp(store.directory, ".write-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	store.stats.Writes++
	store.stats.StoredBytes = store.stats.StoredBytes - replacedBytes + uint64(len(raw))
	return nil
}

func (store *Store) Evict(key string) error {
	if store == nil {
		return ErrInvalidStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	path, ok := store.path(key)
	if !ok {
		return ErrInvalidInvocation
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	store.stats.Evictions++
	if uint64(info.Size()) <= store.stats.StoredBytes {
		store.stats.StoredBytes -= uint64(info.Size())
	}
	return nil
}

func (store *Store) path(key string) (string, bool) {
	if store == nil || !digestPattern.MatchString(key) {
		return "", false
	}
	return filepath.Join(store.directory, key+".json"), true
}
