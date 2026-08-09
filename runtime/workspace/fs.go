package workspace

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

type rootedFS struct {
	experimentalsys.UnimplementedFS

	mu       sync.Mutex
	root     *os.Root
	delegate experimentalsys.FS
	rootDev  uint64
	limits   Limits
	usage    treeUsage
	open     map[string]uint32
	closed   bool
}

func newRootedFS(rootPath string, limits Limits, usage treeUsage) (*rootedFS, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	rootInfo, err := root.Lstat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &rootedFS{
		root: root, delegate: experimentalsysfs.DirFS(rootPath), rootDev: wazerosys.NewStat_t(rootInfo).Dev, limits: limits, usage: usage,
		open: make(map[string]uint32),
	}, nil
}

func (filesystem *rootedFS) close() error {
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if filesystem.closed {
		return nil
	}
	filesystem.closed = true
	return filesystem.root.Close()
}

func (filesystem *rootedFS) clean(name string, allowRoot bool) (string, experimentalsys.Errno) {
	cleaned, err := cleanGuestPath(name, filesystem.limits.MaxDepth, allowRoot)
	if err != nil {
		return "", experimentalsys.EPERM
	}
	return cleaned, 0
}

func (filesystem *rootedFS) validateComponents(name string, allowMissingFinal bool) experimentalsys.Errno {
	if name == "." {
		return 0
	}
	components := strings.Split(name, "/")
	for index := range components {
		current := strings.Join(components[:index+1], "/")
		info, err := filesystem.root.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) && allowMissingFinal && index == len(components)-1 {
				return 0
			}
			return experimentalsys.UnwrapOSError(err)
		}
		mode := info.Mode()
		stat := wazerosys.NewStat_t(info)
		if stat.Dev != filesystem.rootDev || (mode.IsRegular() && stat.Nlink != 1) || mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return experimentalsys.EPERM
		}
		if index < len(components)-1 && !mode.IsDir() {
			return experimentalsys.ENOTDIR
		}
	}
	return 0
}

func (filesystem *rootedFS) OpenFile(name string, flag experimentalsys.Oflag, perm fs.FileMode) (experimentalsys.File, experimentalsys.Errno) {
	cleaned, errno := filesystem.clean(name, true)
	if errno != 0 {
		return nil, errno
	}
	creating := flag&experimentalsys.O_CREAT != 0
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if filesystem.closed {
		return nil, experimentalsys.EBADF
	}
	if errno = filesystem.validateComponents(cleaned, creating); errno != 0 {
		return nil, errno
	}
	_, exists := filesystem.usage.entries[cleaned]
	if cleaned == "." {
		exists = true
	} else {
		_, diskErr := filesystem.root.Lstat(cleaned)
		diskExists := diskErr == nil
		if diskErr != nil && !os.IsNotExist(diskErr) {
			return nil, experimentalsys.UnwrapOSError(diskErr)
		}
		if diskExists != exists {
			return nil, experimentalsys.EPERM
		}
	}
	if creating && !exists && filesystem.usage.files >= filesystem.limits.MaxFiles {
		return nil, experimentalsys.EACCES
	}
	if !exists && creating {
		parent := path.Dir(cleaned)
		if parent != "." {
			mode, parentExists := filesystem.usage.entries[parent]
			if !parentExists || !mode.IsDir() {
				return nil, experimentalsys.ENOENT
			}
		}
	}
	canonicalPerm := fs.FileMode(0o600)
	if perm&0o111 != 0 {
		canonicalPerm = 0o700
	}
	opened, errno := filesystem.delegate.OpenFile(cleaned, flag|experimentalsys.O_NOFOLLOW, canonicalPerm)
	if errno != 0 {
		return nil, errno
	}
	if !exists {
		filesystem.usage.entries[cleaned] = 0
		filesystem.usage.sizes[cleaned] = 0
		filesystem.usage.files++
	}
	if flag&experimentalsys.O_TRUNC != 0 {
		old := filesystem.usage.sizes[cleaned]
		filesystem.usage.bytes -= old
		filesystem.usage.sizes[cleaned] = 0
	}
	filesystem.open[cleaned]++
	return &quotaFile{File: opened, delegate: opened, filesystem: filesystem, name: cleaned, appendMode: flag&experimentalsys.O_APPEND != 0}, 0
}

func (filesystem *rootedFS) Mkdir(name string, perm fs.FileMode) experimentalsys.Errno {
	cleaned, errno := filesystem.clean(name, false)
	if errno != 0 {
		return errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if filesystem.closed {
		return experimentalsys.EBADF
	}
	if errno = filesystem.validateComponents(cleaned, true); errno != 0 {
		return errno
	}
	if _, exists := filesystem.usage.entries[cleaned]; exists {
		return experimentalsys.EEXIST
	}
	if filesystem.usage.files >= filesystem.limits.MaxFiles {
		return experimentalsys.EACCES
	}
	parent := path.Dir(cleaned)
	if parent != "." {
		mode, exists := filesystem.usage.entries[parent]
		if !exists || !mode.IsDir() {
			return experimentalsys.ENOENT
		}
	}
	if errno = filesystem.delegate.Mkdir(cleaned, 0o700); errno != 0 {
		return errno
	}
	filesystem.usage.entries[cleaned] = fs.ModeDir
	filesystem.usage.files++
	return 0
}

func (filesystem *rootedFS) Stat(name string) (wazerosys.Stat_t, experimentalsys.Errno) {
	cleaned, errno := filesystem.clean(name, true)
	if errno != 0 {
		return wazerosys.Stat_t{}, errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if filesystem.closed {
		return wazerosys.Stat_t{}, experimentalsys.EBADF
	}
	if errno = filesystem.validateComponents(cleaned, false); errno != 0 {
		return wazerosys.Stat_t{}, errno
	}
	value, errno := filesystem.delegate.Stat(cleaned)
	return maskStat(value), errno
}

func (filesystem *rootedFS) Lstat(name string) (wazerosys.Stat_t, experimentalsys.Errno) {
	return filesystem.Stat(name)
}

func maskStat(value wazerosys.Stat_t) wazerosys.Stat_t {
	value.Dev = 0
	value.Ino = 0
	value.Nlink = 1
	value.Mode &= fs.ModeDir | fs.ModePerm
	return value
}

func (filesystem *rootedFS) Chmod(name string, perm fs.FileMode) experimentalsys.Errno {
	cleaned, errno := filesystem.clean(name, false)
	if errno != 0 {
		return errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if errno = filesystem.validateComponents(cleaned, false); errno != 0 {
		return errno
	}
	mode, exists := filesystem.usage.entries[cleaned]
	if !exists {
		return experimentalsys.ENOENT
	}
	canonical := fs.FileMode(0o600)
	if mode.IsDir() || perm&0o111 != 0 {
		canonical = 0o700
	}
	return filesystem.delegate.Chmod(cleaned, canonical)
}

func (filesystem *rootedFS) Rename(from, to string) experimentalsys.Errno {
	cleanFrom, errno := filesystem.clean(from, false)
	if errno != 0 {
		return errno
	}
	cleanTo, errno := filesystem.clean(to, false)
	if errno != 0 {
		return errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if errno = filesystem.validateComponents(cleanFrom, false); errno != 0 {
		return errno
	}
	if errno = filesystem.validateComponents(cleanTo, true); errno != 0 {
		return errno
	}
	if _, exists := filesystem.usage.entries[cleanTo]; exists {
		return experimentalsys.EEXIST
	}
	if filesystem.hasOpenAtOrBelow(cleanFrom) || filesystem.hasOpenAtOrBelow(cleanTo) {
		return experimentalsys.EACCES
	}
	parent := path.Dir(cleanTo)
	if parent != "." {
		mode, exists := filesystem.usage.entries[parent]
		if !exists || !mode.IsDir() {
			return experimentalsys.ENOENT
		}
	}
	updates := make(map[string]string)
	for existing := range filesystem.usage.entries {
		if existing == cleanFrom || strings.HasPrefix(existing, cleanFrom+"/") {
			suffix := strings.TrimPrefix(existing, cleanFrom)
			replacement := cleanTo + suffix
			if _, err := cleanGuestPath(replacement, filesystem.limits.MaxDepth, false); err != nil {
				return experimentalsys.EPERM
			}
			updates[existing] = replacement
		}
	}
	if len(updates) == 0 {
		return experimentalsys.ENOENT
	}
	if errno = filesystem.delegate.Rename(cleanFrom, cleanTo); errno != 0 {
		return errno
	}
	for oldName, newName := range updates {
		filesystem.usage.entries[newName] = filesystem.usage.entries[oldName]
		delete(filesystem.usage.entries, oldName)
		if size, exists := filesystem.usage.sizes[oldName]; exists {
			filesystem.usage.sizes[newName] = size
			delete(filesystem.usage.sizes, oldName)
		}
	}
	return 0
}

func (filesystem *rootedFS) Unlink(name string) experimentalsys.Errno {
	cleaned, errno := filesystem.clean(name, false)
	if errno != 0 {
		return errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if errno = filesystem.validateComponents(cleaned, false); errno != 0 {
		return errno
	}
	mode, exists := filesystem.usage.entries[cleaned]
	if !exists {
		return experimentalsys.ENOENT
	}
	if mode.IsDir() {
		return experimentalsys.EISDIR
	}
	if filesystem.open[cleaned] != 0 {
		return experimentalsys.EACCES
	}
	if errno = filesystem.delegate.Unlink(cleaned); errno != 0 {
		return errno
	}
	filesystem.usage.files--
	filesystem.usage.bytes -= filesystem.usage.sizes[cleaned]
	delete(filesystem.usage.entries, cleaned)
	delete(filesystem.usage.sizes, cleaned)
	return 0
}

func (filesystem *rootedFS) Rmdir(name string) experimentalsys.Errno {
	cleaned, errno := filesystem.clean(name, false)
	if errno != 0 {
		return errno
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if errno = filesystem.validateComponents(cleaned, false); errno != 0 {
		return errno
	}
	mode, exists := filesystem.usage.entries[cleaned]
	if !exists {
		return experimentalsys.ENOENT
	}
	if !mode.IsDir() {
		return experimentalsys.ENOTDIR
	}
	if filesystem.hasOpenAtOrBelow(cleaned) {
		return experimentalsys.EACCES
	}
	if errno = filesystem.delegate.Rmdir(cleaned); errno != 0 {
		return errno
	}
	filesystem.usage.files--
	delete(filesystem.usage.entries, cleaned)
	return 0
}

func (filesystem *rootedFS) hasOpenAtOrBelow(name string) bool {
	for openName, count := range filesystem.open {
		if count > 0 && (openName == name || strings.HasPrefix(openName, name+"/")) {
			return true
		}
	}
	return false
}

func (filesystem *rootedFS) Link(_, _ string) experimentalsys.Errno {
	return experimentalsys.EPERM
}

func (filesystem *rootedFS) Symlink(_, _ string) experimentalsys.Errno {
	return experimentalsys.EPERM
}

func (filesystem *rootedFS) Readlink(string) (string, experimentalsys.Errno) {
	return "", experimentalsys.EPERM
}

func (filesystem *rootedFS) Utimens(string, int64, int64) experimentalsys.Errno {
	return experimentalsys.ENOSYS
}

type quotaFile struct {
	experimentalsys.File

	delegate   experimentalsys.File
	filesystem *rootedFS
	name       string
	appendMode bool
	closed     bool
}

func (file *quotaFile) Close() experimentalsys.Errno {
	file.filesystem.mu.Lock()
	defer file.filesystem.mu.Unlock()
	if file.closed {
		return 0
	}
	errno := file.delegate.Close()
	if count := file.filesystem.open[file.name]; count <= 1 {
		delete(file.filesystem.open, file.name)
	} else {
		file.filesystem.open[file.name] = count - 1
	}
	file.closed = true
	return errno
}

func (file *quotaFile) Read(buffer []byte) (int, experimentalsys.Errno) {
	return file.delegate.Read(buffer)
}

func (file *quotaFile) Pread(buffer []byte, offset int64) (int, experimentalsys.Errno) {
	return file.delegate.Pread(buffer, offset)
}

func (file *quotaFile) Write(buffer []byte) (int, experimentalsys.Errno) {
	filesystem := file.filesystem
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if file.closed || filesystem.closed {
		return 0, experimentalsys.EBADF
	}
	current := filesystem.usage.sizes[file.name]
	var offset int64
	var errno experimentalsys.Errno
	if file.appendMode {
		offset = int64(current)
	} else if offset, errno = file.delegate.Seek(0, io.SeekCurrent); errno != 0 {
		return 0, errno
	}
	if offset < 0 || uint64(offset) > current {
		return 0, experimentalsys.EACCES
	}
	if !filesystem.reserveSize(file.name, uint64(offset)+uint64(len(buffer))) {
		return 0, experimentalsys.EACCES
	}
	n, errno := file.delegate.Write(buffer)
	filesystem.reconcileSize(file.name, current)
	return n, errno
}

func (file *quotaFile) Pwrite(buffer []byte, offset int64) (int, experimentalsys.Errno) {
	filesystem := file.filesystem
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if file.closed || filesystem.closed {
		return 0, experimentalsys.EBADF
	}
	current := filesystem.usage.sizes[file.name]
	if offset < 0 || uint64(offset) > current || !filesystem.reserveSize(file.name, uint64(offset)+uint64(len(buffer))) {
		return 0, experimentalsys.EACCES
	}
	n, errno := file.delegate.Pwrite(buffer, offset)
	filesystem.reconcileSize(file.name, current)
	return n, errno
}

func (file *quotaFile) Truncate(size int64) experimentalsys.Errno {
	filesystem := file.filesystem
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if file.closed || filesystem.closed {
		return experimentalsys.EBADF
	}
	current := filesystem.usage.sizes[file.name]
	if size < 0 || uint64(size) > current {
		return experimentalsys.EACCES
	}
	if errno := file.delegate.Truncate(size); errno != 0 {
		return errno
	}
	filesystem.usage.bytes -= current - uint64(size)
	filesystem.usage.sizes[file.name] = uint64(size)
	return 0
}

func (filesystem *rootedFS) reserveSize(name string, size uint64) bool {
	current := filesystem.usage.sizes[name]
	if size <= current {
		return true
	}
	if size > filesystem.limits.MaxFileBytes {
		return false
	}
	delta := size - current
	if delta > filesystem.limits.MaxBytes-filesystem.usage.bytes {
		return false
	}
	filesystem.usage.sizes[name] = size
	filesystem.usage.bytes += delta
	return true
}

func (filesystem *rootedFS) reconcileSize(name string, fallback uint64) {
	value, errno := filesystem.delegate.Stat(name)
	actual := fallback
	if errno == 0 && value.Size >= 0 {
		actual = uint64(value.Size)
	}
	reserved := filesystem.usage.sizes[name]
	if actual < reserved {
		filesystem.usage.bytes -= reserved - actual
	} else if actual > reserved {
		filesystem.usage.bytes += actual - reserved
	}
	filesystem.usage.sizes[name] = actual
}

func (file *quotaFile) Stat() (wazerosys.Stat_t, experimentalsys.Errno) {
	value, errno := file.delegate.Stat()
	return maskStat(value), errno
}

func (file *quotaFile) IsDir() (bool, experimentalsys.Errno) {
	return file.delegate.IsDir()
}

func (file *quotaFile) Readdir(count int) ([]experimentalsys.Dirent, experimentalsys.Errno) {
	filesystem := file.filesystem
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if file.closed || filesystem.closed {
		return nil, experimentalsys.EBADF
	}
	entries, errno := file.delegate.Readdir(count)
	if errno != 0 {
		return nil, errno
	}
	for index := range entries {
		entry := &entries[index]
		if entry.Name == "." || entry.Name == ".." {
			entry.Ino = 0
			continue
		}
		child := entry.Name
		if file.name != "." {
			child = path.Join(file.name, entry.Name)
		}
		tracked, exists := filesystem.usage.entries[child]
		if !exists || (tracked.IsDir() != entry.Type.IsDir()) || (entry.Type != 0 && !entry.Type.IsDir()) {
			return nil, experimentalsys.EPERM
		}
		entry.Ino = 0
	}
	return entries, 0
}

func (file *quotaFile) Sync() experimentalsys.Errno     { return file.delegate.Sync() }
func (file *quotaFile) Datasync() experimentalsys.Errno { return file.delegate.Datasync() }
func (file *quotaFile) SetAppend(enabled bool) experimentalsys.Errno {
	file.appendMode = enabled
	return file.delegate.SetAppend(enabled)
}
func (file *quotaFile) IsAppend() bool                                { return file.appendMode }
func (file *quotaFile) Dev() (uint64, experimentalsys.Errno)          { return 0, 0 }
func (file *quotaFile) Ino() (wazerosys.Inode, experimentalsys.Errno) { return 0, 0 }
func (file *quotaFile) Utimens(int64, int64) experimentalsys.Errno    { return experimentalsys.ENOSYS }
