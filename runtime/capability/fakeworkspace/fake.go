package fakeworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"

	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

const (
	RepoOpenToolID          = "repo.open"
	WorkspaceSearchToolID   = "workspace.search"
	WorkspaceReadManyToolID = "workspace.read_many"
	RepoManifestToolID      = "repo.manifest"
	WorkspaceListToolID     = "workspace.list"
	WorkspaceGlobToolID     = "workspace.glob"
	WorkspaceStatManyToolID = "workspace.stat_many"
	HandlerVersion          = "fake-workspace-v1"
)

var (
	ErrFixtureDenied    = errors.New("fake repository fixture denied")
	ErrWorkspaceDenied  = errors.New("fake workspace access denied")
	ErrPathDenied       = errors.New("fake workspace path denied")
	ErrQuotaExceeded    = errors.New("fake workspace quota exceeded")
	ErrWorkspaceExpired = errors.New("fake workspace expired")
)

type Limits struct {
	MaxWorkspaces      uint32
	MaxFixtureFiles    uint32
	MaxFixtureBytes    uint64
	MaxFileBytes       uint64
	MaxReadPaths       uint32
	MaxReadBytes       uint64
	MaxSearchResults   uint32
	MaxSearchLineBytes uint32
}

func DefaultLimits() Limits {
	return Limits{MaxWorkspaces: 4096, MaxFixtureFiles: 4096, MaxFixtureBytes: 16 << 20, MaxFileBytes: 1 << 20, MaxReadPaths: 32, MaxReadBytes: 1 << 20, MaxSearchResults: 100, MaxSearchLineBytes: 512}
}

type Fixture struct {
	Alias    string
	Revision string
	Files    map[string][]byte
}

type file struct {
	content []byte
	digest  string
}

type repository struct {
	alias          string
	revision       string
	files          map[string]file
	manifestDigest string
	totalBytes     uint64
}

type workspace struct {
	id           string
	runIdentity  string
	taskIdentity string
	repository   *repository
	expiresAt    time.Time
}

type Store struct {
	mu         sync.RWMutex
	limits     Limits
	fixtures   map[string]*repository
	workspaces map[string]workspace
	now        func() time.Time
	ttl        time.Duration
}

func NewStore(fixtures []Fixture, limits Limits) (*Store, error) {
	return NewStoreWithClock(fixtures, limits, time.Now, time.Hour)
}

func NewStoreWithClock(fixtures []Fixture, limits Limits, now func() time.Time, ttl time.Duration) (*Store, error) {
	if !validLimits(limits) || len(fixtures) == 0 || uint32(len(fixtures)) > limits.MaxFixtureFiles || now == nil || ttl <= 0 || ttl > 24*time.Hour {
		return nil, ErrFixtureDenied
	}
	store := &Store{limits: limits, fixtures: make(map[string]*repository, len(fixtures)), workspaces: map[string]workspace{}, now: now, ttl: ttl}
	for _, fixture := range fixtures {
		repository, err := buildRepository(fixture, limits)
		if err != nil {
			return nil, err
		}
		key := fixtureKey(repository.alias, repository.revision)
		if _, exists := store.fixtures[key]; exists {
			return nil, ErrFixtureDenied
		}
		store.fixtures[key] = repository
	}
	return store, nil
}

func (store *Store) Close() {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, repository := range store.fixtures {
		for name, item := range repository.files {
			for index := range item.content {
				item.content[index] = 0
			}
			delete(repository.files, name)
		}
	}
	store.fixtures = nil
	store.workspaces = nil
}

type Binding struct {
	RunIdentity  string
	TaskIdentity string
}

func HandlerSpecs(store *Store, binding Binding) ([]capability.HandlerSpec, error) {
	if store == nil || !validIdentity(binding.RunIdentity) || !validIdentity(binding.TaskIdentity) {
		return nil, ErrWorkspaceDenied
	}
	return []capability.HandlerSpec{
		{ToolID: RepoOpenToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(repoOpenInputSchema), OutputSchema: []byte(repoOpenOutputSchema), Handler: &openHandler{store: store, binding: binding}},
		{ToolID: WorkspaceSearchToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema), Handler: &searchHandler{store: store, binding: binding}},
		{ToolID: WorkspaceReadManyToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(readInputSchema), OutputSchema: []byte(readOutputSchema), Handler: &readHandler{store: store, binding: binding}},
		{ToolID: RepoManifestToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(pageInputSchema), OutputSchema: []byte(manifestOutputSchema), Handler: &metadataHandler{store: store, binding: binding, toolID: RepoManifestToolID}},
		{ToolID: WorkspaceListToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(listInputSchema), OutputSchema: []byte(listOutputSchema), Handler: &metadataHandler{store: store, binding: binding, toolID: WorkspaceListToolID}},
		{ToolID: WorkspaceGlobToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(globInputSchema), OutputSchema: []byte(globOutputSchema), Handler: &metadataHandler{store: store, binding: binding, toolID: WorkspaceGlobToolID}},
		{ToolID: WorkspaceStatManyToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(statInputSchema), OutputSchema: []byte(statOutputSchema), Handler: &metadataHandler{store: store, binding: binding, toolID: WorkspaceStatManyToolID}},
	}, nil
}

func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !validIdentity(grantVersion) {
		return nil, nil, ErrWorkspaceDenied
	}
	tools := []toolcatalog.DiscoveredTool{
		{ToolID: RepoOpenToolID, ServerID: "fake-workspace", Name: "repo_open", Description: "Open an approved immutable fake repository fixture as a Host-owned workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(repoOpenInputSchema), OutputSchema: []byte(repoOpenOutputSchema)},
		{ToolID: WorkspaceSearchToolID, ServerID: "fake-workspace", Name: "workspace_search", Description: "Search bounded UTF-8 content in a Host-owned fake workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema)},
		{ToolID: WorkspaceReadManyToolID, ServerID: "fake-workspace", Name: "workspace_read_many", Description: "Read bounded approved paths from a Host-owned fake workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(readInputSchema), OutputSchema: []byte(readOutputSchema)},
		{ToolID: RepoManifestToolID, ServerID: "fake-workspace", Name: "repo_manifest", Description: "Page through immutable file metadata in a Host-owned fake workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(pageInputSchema), OutputSchema: []byte(manifestOutputSchema)},
		{ToolID: WorkspaceListToolID, ServerID: "fake-workspace", Name: "workspace_list", Description: "List bounded paths under an optional prefix in a Host-owned fake workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(listInputSchema), OutputSchema: []byte(listOutputSchema)},
		{ToolID: WorkspaceGlobToolID, ServerID: "fake-workspace", Name: "workspace_glob", Description: "Match a bounded canonical path glob in a Host-owned fake workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(globInputSchema), OutputSchema: []byte(globOutputSchema)},
		{ToolID: WorkspaceStatManyToolID, ServerID: "fake-workspace", Name: "workspace_stat_many", Description: "Read size and digest metadata for bounded workspace paths.", HandlerVersion: HandlerVersion, InputSchema: []byte(statInputSchema), OutputSchema: []byte(statOutputSchema)},
	}
	grants := make(map[string]toolcatalog.Grant, len(tools))
	for _, tool := range tools {
		grants[tool.ToolID] = toolcatalog.Grant{ToolID: tool.ToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls}
	}
	return tools, grants, nil
}

type openHandler struct {
	store   *Store
	binding Binding
}
type searchHandler struct {
	store   *Store
	binding Binding
}
type readHandler struct {
	store   *Store
	binding Binding
}
type metadataHandler struct {
	store   *Store
	binding Binding
	toolID  string
}

type openArguments struct {
	Alias    string `json:"alias"`
	Revision string `json:"revision"`
}
type openResult struct {
	WorkspaceID      string `json:"workspace_id"`
	Alias            string `json:"alias"`
	ResolvedRevision string `json:"resolved_revision"`
	ManifestDigest   string `json:"manifest_digest"`
	FileCount        int    `json:"file_count"`
	TotalBytes       uint64 `json:"total_bytes"`
	ExpiresAt        string `json:"expires_at"`
}

func (handler *openHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != handler.binding.RunIdentity {
		return nil, classified(ErrWorkspaceDenied)
	}
	var arguments openArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, classified(ErrFixtureDenied)
	}
	result, err := handler.store.open(handler.binding, arguments.Alias, arguments.Revision)
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(result)
}

type searchArguments struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	MaxResults  uint32 `json:"max_results"`
}
type searchMatch struct {
	Path    string `json:"path"`
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	Snippet string `json:"snippet"`
}
type searchResult struct {
	Matches   []searchMatch `json:"matches"`
	Truncated bool          `json:"truncated"`
}

func (handler *searchHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != handler.binding.RunIdentity {
		return nil, classified(ErrWorkspaceDenied)
	}
	var arguments searchArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, classified(ErrWorkspaceDenied)
	}
	result, err := handler.store.search(handler.binding, arguments)
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(result)
}

type readArguments struct {
	WorkspaceID string   `json:"workspace_id"`
	Paths       []string `json:"paths"`
}
type readItem struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type readResult struct {
	Items      []readItem `json:"items"`
	TotalBytes uint64     `json:"total_bytes"`
}

func (handler *readHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != handler.binding.RunIdentity {
		return nil, classified(ErrWorkspaceDenied)
	}
	var arguments readArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, classified(ErrWorkspaceDenied)
	}
	result, err := handler.store.readMany(handler.binding, arguments)
	if err != nil {
		return nil, classified(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, classified(ErrWorkspaceDenied)
	}
	if uint64(len(encoded)) > handler.store.limits.MaxReadBytes {
		return nil, classified(ErrQuotaExceeded)
	}
	return encoded, nil
}

type pageArguments struct {
	WorkspaceID string `json:"workspace_id"`
	Cursor      uint32 `json:"cursor"`
	Limit       uint32 `json:"limit"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  uint64 `json:"bytes"`
}

type manifestResult struct {
	Alias          string         `json:"alias"`
	Revision       string         `json:"revision"`
	ManifestDigest string         `json:"manifest_digest"`
	Files          []manifestFile `json:"files"`
	NextCursor     *uint32        `json:"next_cursor"`
}

type listArguments struct {
	WorkspaceID string `json:"workspace_id"`
	Prefix      string `json:"prefix"`
	Cursor      uint32 `json:"cursor"`
	Limit       uint32 `json:"limit"`
}

type listResult struct {
	Paths      []string `json:"paths"`
	NextCursor *uint32  `json:"next_cursor"`
}

type globArguments struct {
	WorkspaceID string `json:"workspace_id"`
	Pattern     string `json:"pattern"`
	MaxResults  uint32 `json:"max_results"`
}

type globResult struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type statArguments struct {
	WorkspaceID string   `json:"workspace_id"`
	Paths       []string `json:"paths"`
}

type statItem struct {
	Path   string `json:"path"`
	Bytes  uint64 `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type statResult struct {
	Items []statItem `json:"items"`
}

func (handler *metadataHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != handler.binding.RunIdentity {
		return nil, classified(ErrWorkspaceDenied)
	}
	var result any
	var err error
	switch handler.toolID {
	case RepoManifestToolID:
		var arguments pageArguments
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			err = ErrWorkspaceDenied
		} else {
			result, err = handler.store.manifest(handler.binding, arguments)
		}
	case WorkspaceListToolID:
		var arguments listArguments
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			err = ErrWorkspaceDenied
		} else {
			result, err = handler.store.list(handler.binding, arguments)
		}
	case WorkspaceGlobToolID:
		var arguments globArguments
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			err = ErrWorkspaceDenied
		} else {
			result, err = handler.store.glob(handler.binding, arguments)
		}
	case WorkspaceStatManyToolID:
		var arguments statArguments
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			err = ErrWorkspaceDenied
		} else {
			result, err = handler.store.statMany(handler.binding, arguments)
		}
	default:
		err = ErrWorkspaceDenied
	}
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(result)
}

func (store *Store) open(binding Binding, alias, revision string) (openResult, error) {
	if !validAlias(alias) || !validRevision(revision) {
		return openResult{}, ErrFixtureDenied
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	repository, exists := store.fixtures[fixtureKey(alias, revision)]
	if !exists {
		return openResult{}, ErrFixtureDenied
	}
	workspaceID := digest("workspace\x00" + binding.RunIdentity + "\x00" + binding.TaskIdentity + "\x00" + repository.alias + "\x00" + repository.revision + "\x00" + repository.manifestDigest)
	now := store.now().UTC()
	if now.IsZero() {
		return openResult{}, ErrWorkspaceDenied
	}
	expiresAt := now.Add(store.ttl)
	for id, value := range store.workspaces {
		if !now.Before(value.expiresAt) {
			delete(store.workspaces, id)
		}
	}
	if _, exists := store.workspaces[workspaceID]; !exists && uint32(len(store.workspaces)) >= store.limits.MaxWorkspaces {
		return openResult{}, ErrQuotaExceeded
	}
	store.workspaces[workspaceID] = workspace{id: workspaceID, runIdentity: binding.RunIdentity, taskIdentity: binding.TaskIdentity, repository: repository, expiresAt: expiresAt}
	return openResult{WorkspaceID: workspaceID, Alias: alias, ResolvedRevision: revision, ManifestDigest: repository.manifestDigest, FileCount: len(repository.files), TotalBytes: repository.totalBytes, ExpiresAt: expiresAt.Format(time.RFC3339Nano)}, nil
}

func (store *Store) getWorkspace(binding Binding, id string) (workspace, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.workspaces[id]
	if !exists || value.runIdentity != binding.RunIdentity || value.taskIdentity != binding.TaskIdentity {
		return workspace{}, ErrWorkspaceDenied
	}
	if !store.now().UTC().Before(value.expiresAt) {
		delete(store.workspaces, id)
		return workspace{}, ErrWorkspaceExpired
	}
	return value, nil
}

func (store *Store) manifest(binding Binding, arguments pageArguments) (manifestResult, error) {
	value, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return manifestResult{}, err
	}
	paths := sortedPaths(value.repository.files)
	start, end, next, err := pageBounds(len(paths), arguments.Cursor, arguments.Limit)
	if err != nil {
		return manifestResult{}, err
	}
	result := manifestResult{Alias: value.repository.alias, Revision: value.repository.revision, ManifestDigest: value.repository.manifestDigest, Files: make([]manifestFile, 0, end-start), NextCursor: next}
	for _, name := range paths[start:end] {
		item := value.repository.files[name]
		result.Files = append(result.Files, manifestFile{Path: name, SHA256: item.digest, Bytes: uint64(len(item.content))})
	}
	return result, nil
}

func (store *Store) list(binding Binding, arguments listArguments) (listResult, error) {
	if !validPrefix(arguments.Prefix) {
		return listResult{}, ErrPathDenied
	}
	value, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return listResult{}, err
	}
	paths := sortedPaths(value.repository.files)
	if arguments.Prefix != "" {
		filtered := paths[:0]
		for _, name := range paths {
			if strings.HasPrefix(name, arguments.Prefix) {
				filtered = append(filtered, name)
			}
		}
		paths = filtered
	}
	start, end, next, err := pageBounds(len(paths), arguments.Cursor, arguments.Limit)
	if err != nil {
		return listResult{}, err
	}
	return listResult{Paths: append([]string(nil), paths[start:end]...), NextCursor: next}, nil
}

func (store *Store) glob(binding Binding, arguments globArguments) (globResult, error) {
	if !validGlob(arguments.Pattern) || arguments.MaxResults == 0 || arguments.MaxResults > 100 {
		return globResult{}, ErrPathDenied
	}
	value, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return globResult{}, err
	}
	result := globResult{Paths: []string{}}
	for _, name := range sortedPaths(value.repository.files) {
		matched, _ := path.Match(arguments.Pattern, name)
		if !matched {
			continue
		}
		if uint32(len(result.Paths)) == arguments.MaxResults {
			result.Truncated = true
			break
		}
		result.Paths = append(result.Paths, name)
	}
	return result, nil
}

func (store *Store) statMany(binding Binding, arguments statArguments) (statResult, error) {
	if len(arguments.Paths) == 0 || len(arguments.Paths) > 100 || uint32(len(arguments.Paths)) > store.limits.MaxReadPaths {
		return statResult{}, ErrQuotaExceeded
	}
	value, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return statResult{}, err
	}
	result := statResult{Items: make([]statItem, 0, len(arguments.Paths))}
	seen := map[string]struct{}{}
	for _, name := range arguments.Paths {
		if !validPath(name) {
			return statResult{}, ErrPathDenied
		}
		if _, duplicate := seen[name]; duplicate {
			return statResult{}, ErrPathDenied
		}
		seen[name] = struct{}{}
		item, exists := value.repository.files[name]
		if !exists {
			return statResult{}, ErrPathDenied
		}
		result.Items = append(result.Items, statItem{Path: name, Bytes: uint64(len(item.content)), SHA256: item.digest})
	}
	return result, nil
}

func pageBounds(length int, cursor, limit uint32) (int, int, *uint32, error) {
	if limit == 0 || limit > 100 || uint64(cursor) > uint64(length) {
		return 0, 0, nil, ErrQuotaExceeded
	}
	start := int(cursor)
	end := start + int(limit)
	if end > length {
		end = length
	}
	var next *uint32
	if end < length {
		value := uint32(end)
		next = &value
	}
	return start, end, next, nil
}

func (store *Store) search(binding Binding, arguments searchArguments) (searchResult, error) {
	if arguments.Query == "" || len(arguments.Query) > 256 || !utf8.ValidString(arguments.Query) || arguments.MaxResults == 0 || arguments.MaxResults > store.limits.MaxSearchResults {
		return searchResult{}, ErrQuotaExceeded
	}
	workspace, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return searchResult{}, err
	}
	paths := sortedPaths(workspace.repository.files)
	result := searchResult{Matches: []searchMatch{}}
	for _, name := range paths {
		lines := strings.Split(string(workspace.repository.files[name].content), "\n")
		for index, line := range lines {
			column := strings.Index(line, arguments.Query)
			if column < 0 {
				continue
			}
			if uint32(len(result.Matches)) == arguments.MaxResults {
				result.Truncated = true
				return result, nil
			}
			snippet := line
			if len(snippet) > int(store.limits.MaxSearchLineBytes) {
				snippet = truncateUTF8(snippet, int(store.limits.MaxSearchLineBytes))
			}
			result.Matches = append(result.Matches, searchMatch{Path: name, Line: uint32(index + 1), Column: uint32(column + 1), Snippet: snippet})
		}
	}
	return result, nil
}

func (store *Store) readMany(binding Binding, arguments readArguments) (readResult, error) {
	if len(arguments.Paths) == 0 || uint32(len(arguments.Paths)) > store.limits.MaxReadPaths {
		return readResult{}, ErrQuotaExceeded
	}
	workspace, err := store.getWorkspace(binding, arguments.WorkspaceID)
	if err != nil {
		return readResult{}, err
	}
	result := readResult{Items: make([]readItem, 0, len(arguments.Paths))}
	seen := map[string]struct{}{}
	for _, name := range arguments.Paths {
		if !validPath(name) {
			return readResult{}, ErrPathDenied
		}
		if _, duplicate := seen[name]; duplicate {
			return readResult{}, ErrPathDenied
		}
		seen[name] = struct{}{}
		item, exists := workspace.repository.files[name]
		if !exists {
			return readResult{}, ErrPathDenied
		}
		result.TotalBytes += uint64(len(item.content))
		if result.TotalBytes > store.limits.MaxReadBytes {
			return readResult{}, ErrQuotaExceeded
		}
		result.Items = append(result.Items, readItem{Path: name, Content: string(item.content), SHA256: item.digest})
	}
	return result, nil
}

func buildRepository(fixture Fixture, limits Limits) (*repository, error) {
	if !validAlias(fixture.Alias) || !validRevision(fixture.Revision) || len(fixture.Files) == 0 || uint32(len(fixture.Files)) > limits.MaxFixtureFiles {
		return nil, ErrFixtureDenied
	}
	repository := &repository{alias: fixture.Alias, revision: fixture.Revision, files: make(map[string]file, len(fixture.Files))}
	for name, content := range fixture.Files {
		if !validPath(name) || len(content) == 0 || uint64(len(content)) > limits.MaxFileBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return nil, ErrFixtureDenied
		}
		repository.totalBytes += uint64(len(content))
		if repository.totalBytes > limits.MaxFixtureBytes {
			return nil, ErrQuotaExceeded
		}
		repository.files[name] = file{content: append([]byte(nil), content...), digest: digestBytes(content)}
	}
	repository.manifestDigest = manifestDigest(repository.files)
	return repository, nil
}

func manifestDigest(files map[string]file) string {
	hash := sha256.New()
	var size [8]byte
	for _, name := range sortedPaths(files) {
		binary.BigEndian.PutUint64(size[:], uint64(len(name)))
		hash.Write(size[:])
		hash.Write([]byte(name))
		binary.BigEndian.PutUint64(size[:], uint64(len(files[name].content)))
		hash.Write(size[:])
		hash.Write([]byte(files[name].digest))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sortedPaths(files map[string]file) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func fixtureKey(alias, revision string) string { return alias + "\x00" + revision }
func digest(value string) string               { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validLimits(l Limits) bool {
	return l.MaxWorkspaces > 0 && l.MaxWorkspaces <= 100000 && l.MaxFixtureFiles > 0 && l.MaxFixtureFiles <= 100000 && l.MaxFixtureBytes > 0 && l.MaxFixtureBytes <= 1<<30 && l.MaxFileBytes > 0 && l.MaxFileBytes <= l.MaxFixtureBytes && l.MaxReadPaths > 0 && l.MaxReadPaths <= 4096 && l.MaxReadBytes > 0 && l.MaxReadBytes <= l.MaxFixtureBytes && l.MaxSearchResults > 0 && l.MaxSearchResults <= 10000 && l.MaxSearchLineBytes > 0 && l.MaxSearchLineBytes <= 4096
}
func validAlias(value string) bool {
	return validIdentity(value) && len(value) <= 128 && !strings.Contains(value, "/")
}
func validIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-/", r)) {
			return false
		}
	}
	return true
}
func validRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func validPath(value string) bool {
	return value != "" && len(value) <= 512 && utf8.ValidString(value) && !strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "../")
}
func validPrefix(value string) bool {
	if value == "" {
		return true
	}
	trimmed := strings.TrimSuffix(value, "/")
	return trimmed != "" && validPath(trimmed)
}
func validGlob(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "" {
			return false
		}
	}
	_, err := path.Match(value, "probe")
	return err == nil
}
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
func classified(err error) error {
	switch {
	case errors.Is(err, ErrPathDenied):
		return capability.NewHandlerFailure("path_denied", "workspace path was denied", err)
	case errors.Is(err, ErrQuotaExceeded):
		return capability.NewHandlerFailure("quota_exceeded", "workspace quota was exceeded", err)
	case errors.Is(err, ErrFixtureDenied):
		return capability.NewHandlerFailure("fixture_denied", "repository fixture was denied", err)
	case errors.Is(err, ErrWorkspaceExpired):
		return capability.NewHandlerFailure("workspace_expired", "workspace lease expired", err)
	default:
		return capability.NewHandlerFailure("workspace_denied", "workspace access was denied", err)
	}
}

const repoOpenInputSchema = `{"type":"object","additionalProperties":false,"required":["alias","revision"],"properties":{"alias":{"type":"string","minLength":1,"maxLength":128},"revision":{"type":"string","pattern":"^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$"}}}`
const repoOpenOutputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","alias","resolved_revision","manifest_digest","file_count","total_bytes","expires_at"],"properties":{"workspace_id":{"type":"string"},"alias":{"type":"string"},"resolved_revision":{"type":"string"},"manifest_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"file_count":{"type":"integer","minimum":1},"total_bytes":{"type":"integer","minimum":1},"expires_at":{"type":"string"}}}`
const searchInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","query","max_results"],"properties":{"workspace_id":{"type":"string"},"query":{"type":"string","minLength":1,"maxLength":256},"max_results":{"type":"integer","minimum":1,"maximum":10000}}}`
const searchOutputSchema = `{"type":"object","additionalProperties":false,"required":["matches","truncated"],"properties":{"matches":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["path","line","column","snippet"],"properties":{"path":{"type":"string"},"line":{"type":"integer","minimum":1},"column":{"type":"integer","minimum":1},"snippet":{"type":"string"}}}},"truncated":{"type":"boolean"}}}`
const readInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","paths"],"properties":{"workspace_id":{"type":"string"},"paths":{"type":"array","minItems":1,"maxItems":4096,"items":{"type":"string","minLength":1,"maxLength":512}}}}`
const readOutputSchema = `{"type":"object","additionalProperties":false,"required":["items","total_bytes"],"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["path","content","sha256"],"properties":{"path":{"type":"string"},"content":{"type":"string"},"sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}}}},"total_bytes":{"type":"integer","minimum":0}}}`
const pageInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","cursor","limit"],"properties":{"workspace_id":{"type":"string"},"cursor":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100}}}`
const nullableCursorSchema = `{"anyOf":[{"type":"integer","minimum":1},{"type":"null"}]}`
const manifestOutputSchema = `{"type":"object","additionalProperties":false,"required":["alias","revision","manifest_digest","files","next_cursor"],"properties":{"alias":{"type":"string"},"revision":{"type":"string"},"manifest_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"files":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["path","sha256","bytes"],"properties":{"path":{"type":"string"},"sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"bytes":{"type":"integer","minimum":1}}}},"next_cursor":` + nullableCursorSchema + `}}`
const listInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","prefix","cursor","limit"],"properties":{"workspace_id":{"type":"string"},"prefix":{"type":"string","maxLength":512},"cursor":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100}}}`
const listOutputSchema = `{"type":"object","additionalProperties":false,"required":["paths","next_cursor"],"properties":{"paths":{"type":"array","maxItems":100,"items":{"type":"string"}},"next_cursor":` + nullableCursorSchema + `}}`
const globInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","pattern","max_results"],"properties":{"workspace_id":{"type":"string"},"pattern":{"type":"string","minLength":1,"maxLength":512},"max_results":{"type":"integer","minimum":1,"maximum":100}}}`
const globOutputSchema = `{"type":"object","additionalProperties":false,"required":["paths","truncated"],"properties":{"paths":{"type":"array","maxItems":100,"items":{"type":"string"}},"truncated":{"type":"boolean"}}}`
const statInputSchema = `{"type":"object","additionalProperties":false,"required":["workspace_id","paths"],"properties":{"workspace_id":{"type":"string"},"paths":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string","minLength":1,"maxLength":512}}}}`
const statOutputSchema = `{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["path","bytes","sha256"],"properties":{"path":{"type":"string"},"bytes":{"type":"integer","minimum":1},"sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}}}}}}`

var _ capability.Handler = (*openHandler)(nil)
var _ capability.Handler = (*searchHandler)(nil)
var _ capability.Handler = (*readHandler)(nil)
var _ capability.Handler = (*metadataHandler)(nil)
