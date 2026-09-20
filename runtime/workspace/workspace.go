// Package workspace owns bounded private filesystems that can be mounted into
// disposable Pysolate Guests. Workspace roots never expose their Host paths.
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

type Ref string

type Limits struct {
	MaxFiles     uint32
	MaxBytes     uint64
	MaxFileBytes uint64
	MaxDepth     uint32
}

func DefaultLimits() Limits {
	return Limits{MaxFiles: 4096, MaxBytes: 256 << 20, MaxFileBytes: 64 << 20, MaxDepth: 32}
}

func (limits Limits) validate() error {
	if limits.MaxFiles == 0 || limits.MaxBytes == 0 || limits.MaxFileBytes == 0 || limits.MaxDepth == 0 || limits.MaxFileBytes > limits.MaxBytes {
		return fmt.Errorf("%w: invalid limits", ErrInvalidWorkspace)
	}
	return nil
}

type InitialFile struct {
	Path       string `json:"path"`
	Data       []byte `json:"data"`
	Executable bool   `json:"executable,omitempty"`
}

type File struct {
	Path       string `json:"path"`
	Data       []byte `json:"data"`
	Executable bool   `json:"executable,omitempty"`
}

type treeUsage struct {
	entries map[string]fs.FileMode
	sizes   map[string]uint64
	files   uint32
	bytes   uint64
}

type entry struct {
	root   string
	limits Limits
	owner  string
}

type Manager struct {
	mu      sync.Mutex
	base    string
	entries map[Ref]*entry
	closed  bool
}

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

func (manager *Manager) Create(files []InitialFile, limits Limits) (Ref, error) {
	if manager == nil {
		return "", ErrWorkspaceClosed
	}
	if err := limits.validate(); err != nil {
		return "", err
	}
	canonical := append([]InitialFile(nil), files...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	seenFiles := make(map[string]bool, len(canonical))
	for index := range canonical {
		cleaned, err := cleanGuestPath(canonical[index].Path, limits.MaxDepth, false)
		if err != nil || cleaned != canonical[index].Path {
			return "", fmt.Errorf("%w: invalid initial file path", ErrInvalidWorkspace)
		}
		canonical[index].Path = cleaned
		if seenFiles[cleaned] {
			return "", fmt.Errorf("%w: duplicate initial file", ErrInvalidWorkspace)
		}
		for parent := path.Dir(cleaned); parent != "."; parent = path.Dir(parent) {
			if seenFiles[parent] {
				return "", fmt.Errorf("%w: regular file used as parent", ErrInvalidWorkspace)
			}
		}
		seenFiles[cleaned] = true
		if uint64(len(canonical[index].Data)) > limits.MaxFileBytes {
			return "", fmt.Errorf("%w: initial file exceeds per-file limit", ErrInvalidWorkspace)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", ErrWorkspaceClosed
	}
	ref, root, err := manager.allocateLocked()
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
			return "", fmt.Errorf("materialize workspace directories: %w", err)
		}
		mode := fs.FileMode(0o600)
		if file.Executable {
			mode = 0o700
		}
		if err := os.WriteFile(full, file.Data, mode); err != nil {
			return "", fmt.Errorf("materialize workspace file: %w", err)
		}
	}
	if _, err := scanOrdinaryTree(root, limits); err != nil {
		return "", err
	}
	manager.entries[ref] = &entry{root: root, limits: limits}
	failed = false
	return ref, nil
}

func (manager *Manager) allocateLocked() (Ref, string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", "", err
		}
		ref := Ref("ws-" + hex.EncodeToString(value[:]))
		root := filepath.Join(manager.base, string(ref))
		if err := os.Mkdir(root, 0o700); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return "", "", err
		}
		return ref, root, nil
	}
	return "", "", errors.New("allocate workspace identity")
}

func (manager *Manager) Acquire(ref Ref, owner string) (*Lease, error) {
	if manager == nil {
		return nil, ErrWorkspaceClosed
	}
	if !validRef(ref) || owner == "" || len(owner) > 128 {
		return nil, fmt.Errorf("%w: invalid lease identity", ErrInvalidWorkspace)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrWorkspaceClosed
	}
	item := manager.entries[ref]
	if item == nil {
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
	return &Lease{manager: manager, ref: ref, owner: owner, root: item.root, filesystem: filesystem}, nil
}

func validRef(ref Ref) bool {
	value := string(ref)
	if len(value) != 35 || !strings.HasPrefix(value, "ws-") {
		return false
	}
	_, err := hex.DecodeString(value[3:])
	return err == nil
}

// Destroy removes one unleased workspace. Callers must release any Lease first.
func (manager *Manager) Destroy(ref Ref) error {
	if manager == nil {
		return ErrWorkspaceClosed
	}
	if !validRef(ref) {
		return fmt.Errorf("%w: invalid workspace reference", ErrInvalidWorkspace)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrWorkspaceClosed
	}
	item := manager.entries[ref]
	if item == nil {
		return ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return ErrWorkspaceBusy
	}
	if err := os.RemoveAll(item.root); err != nil {
		return err
	}
	delete(manager.entries, ref)
	return nil
}

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
	var result error
	for ref, item := range manager.entries {
		if err := os.RemoveAll(item.root); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(manager.entries, ref)
		}
	}
	if result == nil {
		manager.closed = true
	}
	return result
}

type Lease struct {
	mu         sync.Mutex
	manager    *Manager
	ref        Ref
	owner      string
	root       string
	filesystem *rootedFS
	running    bool
	released   bool
}

// BeginRun grants the mounted filesystem to one Guest at a time. EndRun must
// be called after the module instance has closed.
func (lease *Lease) BeginRun() (experimentalsys.FS, uint64, error) {
	if lease == nil {
		return nil, 0, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil, 0, ErrWorkspaceClosed
	}
	if lease.running {
		return nil, 0, ErrWorkspaceBusy
	}
	lease.running = true
	return lease.filesystem, lease.filesystem.usage.bytes, nil
}

func (lease *Lease) EndRun() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	lease.running = false
	lease.mu.Unlock()
}

func (lease *Lease) Files() ([]File, error) {
	if lease == nil {
		return nil, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil, ErrWorkspaceClosed
	}
	if lease.running {
		return nil, ErrWorkspaceBusy
	}
	usage, err := scanOrdinaryTree(lease.root, lease.filesystem.limits)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(usage.sizes))
	for name := range usage.sizes {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	root, err := os.OpenRoot(lease.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, name := range paths {
		data, err := root.ReadFile(name)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: name, Data: data, Executable: usage.entries[name].Perm()&0o111 != 0})
	}
	return files, nil
}

// ReadFile returns one regular file without materializing the rest of the
// workspace. maxBytes bounds the response allocation.
func (lease *Lease) ReadFile(name string, maxBytes uint64) (File, error) {
	if lease == nil || maxBytes == 0 {
		return File{}, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return File{}, ErrWorkspaceClosed
	}
	if lease.running {
		return File{}, ErrWorkspaceBusy
	}
	cleaned, err := cleanGuestPath(name, lease.filesystem.limits.MaxDepth, false)
	if err != nil || cleaned != name {
		return File{}, fmt.Errorf("%w: invalid file path", ErrInvalidWorkspace)
	}
	usage, err := scanOrdinaryTree(lease.root, lease.filesystem.limits)
	if err != nil {
		return File{}, err
	}
	size, ok := usage.sizes[name]
	if !ok {
		return File{}, fs.ErrNotExist
	}
	if size > maxBytes {
		return File{}, fmt.Errorf("%w: file exceeds read limit", ErrInvalidWorkspace)
	}
	root, err := os.OpenRoot(lease.root)
	if err != nil {
		return File{}, err
	}
	defer root.Close()
	data, err := root.ReadFile(name)
	if err != nil {
		return File{}, err
	}
	return File{Path: name, Data: data, Executable: usage.entries[name].Perm()&0o111 != 0}, nil
}

func (lease *Lease) Release() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	if lease.released {
		lease.mu.Unlock()
		return nil
	}
	if lease.running {
		lease.mu.Unlock()
		return ErrWorkspaceBusy
	}
	lease.released = true
	lease.mu.Unlock()
	closeErr := lease.filesystem.close()
	lease.manager.mu.Lock()
	if item := lease.manager.entries[lease.ref]; item != nil && item.owner == lease.owner {
		item.owner = ""
	}
	lease.manager.mu.Unlock()
	return closeErr
}

func cleanGuestPath(name string, maxDepth uint32, allowRoot bool) (string, error) {
	if name == "" || len(name) > maxWorkspacePathBytes || !utf8.ValidString(name) || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", ErrInvalidWorkspace
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		if allowRoot && name == "." {
			return cleaned, nil
		}
		return "", ErrInvalidWorkspace
	}
	if cleaned != name || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidWorkspace
	}
	if uint32(strings.Count(cleaned, "/")+1) > maxDepth {
		return "", ErrInvalidWorkspace
	}
	for _, component := range strings.Split(cleaned, "/") {
		if component == ".git" {
			return "", ErrInvalidWorkspace
		}
	}
	return cleaned, nil
}

func scanOrdinaryTree(rootPath string, limits Limits) (treeUsage, error) {
	usage := treeUsage{entries: make(map[string]fs.FileMode), sizes: make(map[string]uint64)}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return usage, err
	}
	rootDev := wazerosys.NewStat_t(rootInfo).Dev
	err = filepath.WalkDir(rootPath, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == rootPath {
			return nil
		}
		relative, err := filepath.Rel(rootPath, full)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if _, err = cleanGuestPath(name, limits.MaxDepth, false); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat := wazerosys.NewStat_t(info)
		mode := info.Mode()
		if stat.Dev != rootDev || mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) || (mode.IsRegular() && stat.Nlink != 1) {
			return fmt.Errorf("%w: unsupported filesystem entry %s", ErrInvalidWorkspace, name)
		}
		usage.files++
		if usage.files > limits.MaxFiles {
			return fmt.Errorf("%w: file count exceeds limit", ErrInvalidWorkspace)
		}
		usage.entries[name] = mode
		if mode.IsRegular() {
			size := uint64(info.Size())
			if size > limits.MaxFileBytes || size > limits.MaxBytes-usage.bytes {
				return fmt.Errorf("%w: workspace bytes exceed limit", ErrInvalidWorkspace)
			}
			usage.sizes[name] = size
			usage.bytes += size
		}
		return nil
	})
	return usage, err
}
