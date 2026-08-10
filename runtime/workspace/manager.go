// Package workspace provides Host-owned, mutable file workspaces which can be
// attached to a sequence of disposable reactor instances. A workspace carries
// ordinary files and directories only; it never carries interpreter or Host
// capability state.
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

const maxWorkspacePathBytes = 4096

var (
	ErrInvalidWorkspace  = errors.New("invalid workspace")
	ErrWorkspaceBusy     = errors.New("workspace is busy")
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceClosed   = errors.New("workspace manager is closed")
)

// Ref is an opaque Host-owned workspace identity. It never contains a path.
type Ref string

// Limits bound the complete workspace tree, excluding its root directory.
type Limits struct {
	MaxFiles     uint32
	MaxBytes     uint64
	MaxFileBytes uint64
	MaxDepth     uint32
}

// DefaultLimits returns the conservative v1 workspace bounds.
func DefaultLimits() Limits {
	return Limits{MaxFiles: 4096, MaxBytes: 256 << 20, MaxFileBytes: 64 << 20, MaxDepth: 32}
}

// DefaultTemporaryLimits returns stricter bounds for per-instance scratch data.
func DefaultTemporaryLimits() Limits {
	return Limits{MaxFiles: 1024, MaxBytes: 64 << 20, MaxFileBytes: 16 << 20, MaxDepth: 16}
}

func (limits Limits) validate() error {
	if limits.MaxFiles == 0 || limits.MaxBytes == 0 || limits.MaxFileBytes == 0 || limits.MaxDepth == 0 || limits.MaxFileBytes > limits.MaxBytes {
		return fmt.Errorf("%w: invalid limits", ErrInvalidWorkspace)
	}
	return nil
}

// InitialFile is a validated value copy provisioned before any guest starts.
type InitialFile struct {
	Path       string
	Data       []byte
	Executable bool
}

type entry struct {
	root   string
	limits Limits
	owner  string
}

// Manager owns workspace roots beneath one private 0700 Host directory.
type Manager struct {
	mu      sync.Mutex
	base    string
	entries map[Ref]*entry
	closed  bool
}

// NewManager binds a private existing base directory. It never adopts
// arbitrary child directories as workspaces.
func NewManager(base string) (*Manager, error) {
	if base == "" || !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return nil, fmt.Errorf("%w: manager base must be a clean absolute path", ErrInvalidWorkspace)
	}
	info, err := os.Lstat(base)
	if err != nil {
		return nil, fmt.Errorf("open workspace manager base: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("%w: manager base must be a private 0700 directory", ErrInvalidWorkspace)
	}
	return &Manager{base: base, entries: make(map[Ref]*entry)}, nil
}

// Create allocates a new workspace and copies validated initial files into it.
func (manager *Manager) Create(files []InitialFile, limits Limits) (Ref, error) {
	if manager == nil {
		return "", ErrWorkspaceClosed
	}
	if err := limits.validate(); err != nil {
		return "", err
	}
	canonical := append([]InitialFile(nil), files...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	seen := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		cleaned, err := cleanGuestPath(canonical[index].Path, limits.MaxDepth, false)
		if err != nil || cleaned == "." || cleaned != canonical[index].Path {
			return "", fmt.Errorf("%w: invalid initial file path", ErrInvalidWorkspace)
		}
		canonical[index].Path = cleaned
		if _, exists := seen[cleaned]; exists {
			return "", fmt.Errorf("%w: duplicate initial file", ErrInvalidWorkspace)
		}
		seen[cleaned] = struct{}{}
		for parent := path.Dir(cleaned); parent != "."; parent = path.Dir(parent) {
			if _, fileParent := seen[parent]; fileParent {
				return "", fmt.Errorf("%w: regular file used as a parent", ErrInvalidWorkspace)
			}
		}
		if uint64(len(canonical[index].Data)) > limits.MaxFileBytes {
			return "", fmt.Errorf("%w: initial file exceeds per-file limit", ErrInvalidWorkspace)
		}
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", ErrWorkspaceClosed
	}
	ref, root, err := manager.allocateRootLocked()
	if err != nil {
		return "", err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(root)
		}
	}()
	for _, file := range canonical {
		full := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return "", fmt.Errorf("materialize initial directories: %w", err)
		}
		mode := fs.FileMode(0o600)
		if file.Executable {
			mode = 0o700
		}
		if err := os.WriteFile(full, file.Data, mode); err != nil {
			return "", fmt.Errorf("materialize initial file: %w", err)
		}
	}
	usage, err := scanOrdinaryTree(root, limits)
	if err != nil {
		return "", err
	}
	_ = usage
	manager.entries[ref] = &entry{root: root, limits: limits}
	failed = false
	return ref, nil
}

func (manager *Manager) allocateRootLocked() (Ref, string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", "", fmt.Errorf("create workspace identity: %w", err)
		}
		ref := Ref("ws-" + hex.EncodeToString(random[:]))
		if _, exists := manager.entries[ref]; exists {
			continue
		}
		root := filepath.Join(manager.base, string(ref))
		if err := os.Mkdir(root, 0o700); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return "", "", fmt.Errorf("create workspace root: %w", err)
		}
		return ref, root, nil
	}
	return "", "", errors.New("create unique workspace identity")
}

// Acquire validates the stored tree and grants one exclusive writer lease.
func (manager *Manager) Acquire(ref Ref, owner string) (*Lease, error) {
	if manager == nil {
		return nil, ErrWorkspaceClosed
	}
	if !validRef(ref) || !validOwner(owner) {
		return nil, fmt.Errorf("%w: invalid lease identity", ErrInvalidWorkspace)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrWorkspaceClosed
	}
	item, exists := manager.entries[ref]
	if !exists {
		return nil, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return nil, ErrWorkspaceBusy
	}
	usage, err := scanOrdinaryTree(item.root, item.limits)
	if err != nil {
		return nil, err
	}
	filesystem, err := newRootedFS(item.root, item.limits, usage)
	if err != nil {
		return nil, err
	}
	item.owner = owner
	return &Lease{manager: manager, ref: ref, owner: owner, filesystem: filesystem, temporaries: make(map[*Temporary]struct{})}, nil
}

func validRef(ref Ref) bool {
	value := string(ref)
	if len(value) != 35 || !strings.HasPrefix(value, "ws-") {
		return false
	}
	_, err := hex.DecodeString(value[3:])
	return err == nil
}

func validOwner(owner string) bool {
	if owner == "" || len(owner) > 128 {
		return false
	}
	for _, character := range owner {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

// Destroy removes one inactive workspace tree.
func (manager *Manager) Destroy(ref Ref) error {
	if manager == nil {
		return ErrWorkspaceClosed
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrWorkspaceClosed
	}
	item, exists := manager.entries[ref]
	if !exists {
		return ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return ErrWorkspaceBusy
	}
	if err := os.RemoveAll(item.root); err != nil {
		return fmt.Errorf("destroy workspace: %w", err)
	}
	delete(manager.entries, ref)
	return nil
}

// Close destroys all inactive workspaces and closes the Manager. It fails
// closed while any lease remains active.
func (manager *Manager) Close() error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil
	}
	for _, item := range manager.entries {
		if item.owner != "" {
			return ErrWorkspaceBusy
		}
	}
	var closeErr error
	for ref, item := range manager.entries {
		if err := os.RemoveAll(item.root); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("destroy workspace %s: %w", ref, err))
			continue
		}
		delete(manager.entries, ref)
	}
	if closeErr == nil {
		manager.closed = true
	}
	return closeErr
}

// Lease is an exclusive, revocable attachment to one workspace root.
type Lease struct {
	mu          sync.Mutex
	manager     *Manager
	ref         Ref
	owner       string
	filesystem  *rootedFS
	temporaries map[*Temporary]struct{}
	released    bool
}

// Ref returns the opaque workspace identity.
func (lease *Lease) Ref() Ref {
	if lease == nil {
		return ""
	}
	return lease.ref
}

// FS returns the rooted WASI adapter. It intentionally exposes no Host path.
func (lease *Lease) FS() experimentalsys.FS {
	if lease == nil {
		return nil
	}
	return lease.filesystem
}

// NewTemporary creates a private scratch filesystem tied to this lease. It has
// no Ref and must be closed before the lease can be released.
func (lease *Lease) NewTemporary() (*Temporary, error) {
	if lease == nil {
		return nil, ErrWorkspaceClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil, ErrWorkspaceClosed
	}
	manager := lease.manager
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil, ErrWorkspaceClosed
	}
	item, exists := manager.entries[lease.ref]
	if !exists || item.owner != lease.owner {
		manager.mu.Unlock()
		return nil, errors.New("workspace lease identity changed")
	}
	root, err := os.MkdirTemp(manager.base, "run-tmp-")
	manager.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("create temporary workspace: %w", err)
	}
	limits := DefaultTemporaryLimits()
	usage := treeUsage{entries: make(map[string]fs.FileMode), sizes: make(map[string]uint64)}
	filesystem, err := newRootedFS(root, limits, usage)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("open temporary workspace: %w", err)
	}
	temporary := &Temporary{lease: lease, root: root, filesystem: filesystem}
	lease.temporaries[temporary] = struct{}{}
	return temporary, nil
}

// Temporary is a per-instance scratch filesystem with no continuation identity.
type Temporary struct {
	mu         sync.Mutex
	lease      *Lease
	root       string
	filesystem *rootedFS
	closed     bool
}

// FS returns the rooted scratch filesystem without exposing its Host path.
func (temporary *Temporary) FS() experimentalsys.FS {
	if temporary == nil {
		return nil
	}
	return temporary.filesystem
}

// Close closes all rooted access and removes the scratch tree.
func (temporary *Temporary) Close() error {
	if temporary == nil {
		return nil
	}
	temporary.mu.Lock()
	defer temporary.mu.Unlock()
	if temporary.closed {
		return nil
	}
	closeErr := temporary.filesystem.close()
	removeErr := os.RemoveAll(temporary.root)
	if err := errors.Join(closeErr, removeErr); err != nil {
		return fmt.Errorf("destroy temporary workspace: %w", err)
	}
	temporary.lease.mu.Lock()
	delete(temporary.lease.temporaries, temporary)
	temporary.lease.mu.Unlock()
	temporary.closed = true
	return nil
}

// Release closes the rooted adapter and makes the workspace acquirable again.
func (lease *Lease) Release() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil
	}
	if len(lease.temporaries) != 0 {
		return ErrWorkspaceBusy
	}
	if err := lease.filesystem.close(); err != nil {
		return fmt.Errorf("close workspace filesystem: %w", err)
	}
	lease.manager.mu.Lock()
	defer lease.manager.mu.Unlock()
	item, exists := lease.manager.entries[lease.ref]
	if !exists || item.owner != lease.owner {
		return errors.New("workspace lease identity changed")
	}
	item.owner = ""
	lease.released = true
	return nil
}

type treeUsage struct {
	entries map[string]fs.FileMode
	sizes   map[string]uint64
	files   uint32
	bytes   uint64
}

func scanOrdinaryTree(root string, limits Limits) (treeUsage, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return treeUsage{}, fmt.Errorf("%w: workspace root is unavailable", ErrInvalidWorkspace)
	}
	rootDevice := wazerosys.NewStat_t(rootInfo).Dev
	usage := treeUsage{entries: make(map[string]fs.FileMode), sizes: make(map[string]uint64)}
	err = filepath.WalkDir(root, func(current string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		cleaned, err := cleanGuestPath(name, limits.MaxDepth, false)
		if err != nil || cleaned != name {
			return fmt.Errorf("%w: non-canonical stored path", ErrInvalidWorkspace)
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		stat := wazerosys.NewStat_t(info)
		if stat.Dev != rootDevice {
			return fmt.Errorf("%w: workspace crosses a filesystem boundary", ErrInvalidWorkspace)
		}
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("%w: workspace contains unsupported file type", ErrInvalidWorkspace)
		}
		if mode.IsRegular() && stat.Nlink != 1 {
			return fmt.Errorf("%w: workspace contains hard-linked file", ErrInvalidWorkspace)
		}
		usage.files++
		if usage.files > limits.MaxFiles {
			return fmt.Errorf("%w: workspace file count exceeds limit", ErrInvalidWorkspace)
		}
		usage.entries[name] = mode.Type()
		if mode.IsRegular() {
			size := uint64(info.Size())
			if size > limits.MaxFileBytes || usage.bytes > limits.MaxBytes-size {
				return fmt.Errorf("%w: workspace bytes exceed limit", ErrInvalidWorkspace)
			}
			usage.sizes[name] = size
			usage.bytes += size
		}
		return nil
	})
	if err != nil {
		return treeUsage{}, err
	}
	return usage, nil
}

func cleanGuestPath(name string, maxDepth uint32, allowRoot bool) (string, error) {
	if name == "" || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 || strings.Contains(name, "\\") || path.IsAbs(name) || len(name) > maxWorkspacePathBytes {
		return "", ErrInvalidWorkspace
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != name {
		return "", ErrInvalidWorkspace
	}
	if cleaned == "." {
		if allowRoot {
			return cleaned, nil
		}
		return "", ErrInvalidWorkspace
	}
	if uint32(strings.Count(cleaned, "/")+1) > maxDepth {
		return "", ErrInvalidWorkspace
	}
	for _, component := range strings.Split(cleaned, "/") {
		if len(component) > 255 || strings.HasPrefix(component, "..") || component == ".git" {
			return "", ErrInvalidWorkspace
		}
	}
	return cleaned, nil
}
