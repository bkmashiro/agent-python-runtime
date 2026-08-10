package wazero

import (
	"errors"
	"io/fs"
	"sync"
	"sync/atomic"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

// workspaceGate keeps a mounted workspace unavailable while a module is being
// initialized or prepared. This prevents workspace-derived state from entering
// a prepared heap or COW baseline. A single-use module activates its gate once,
// immediately after exclusive checkout for a Run.
type workspaceGate struct {
	experimentalsys.UnimplementedFS
	target atomic.Pointer[workspaceTarget]
	active atomic.Bool
}

type workspaceTarget struct {
	filesystem experimentalsys.FS
}

func newWorkspaceGate(target experimentalsys.FS) *workspaceGate {
	gate := &workspaceGate{}
	if target != nil {
		gate.target.Store(&workspaceTarget{filesystem: target})
	}
	return gate
}

func (gate *workspaceGate) activate() error {
	if gate == nil || gate.filesystem() == nil {
		return errors.New("workspace gate is unavailable")
	}
	if !gate.active.CompareAndSwap(false, true) {
		return errors.New("workspace gate was already activated")
	}
	return nil
}

func (gate *workspaceGate) attachAndActivate(filesystem experimentalsys.FS) error {
	if gate == nil || filesystem == nil {
		return errors.New("workspace gate target is unavailable")
	}
	if !gate.target.CompareAndSwap(nil, &workspaceTarget{filesystem: filesystem}) {
		return errors.New("workspace gate target was already attached")
	}
	return gate.activate()
}

func (gate *workspaceGate) filesystem() experimentalsys.FS {
	if gate == nil {
		return nil
	}
	target := gate.target.Load()
	if target == nil {
		return nil
	}
	return target.filesystem
}

func (gate *workspaceGate) allowed() bool {
	return gate != nil && gate.filesystem() != nil && gate.active.Load()
}

func (gate *workspaceGate) OpenFile(path string, flag experimentalsys.Oflag, perm fs.FileMode) (experimentalsys.File, experimentalsys.Errno) {
	if !gate.allowed() {
		if (path == "" || path == "." || path == "/") && workspaceRootReadOnly(flag) {
			return &workspacePreopenFile{gate: gate, flag: flag, perm: perm}, 0
		}
		return nil, experimentalsys.EACCES
	}
	return gate.filesystem().OpenFile(path, flag, perm)
}

func workspaceRootReadOnly(flag experimentalsys.Oflag) bool {
	access := flag & 3
	return access == experimentalsys.O_RDONLY && flag&(experimentalsys.O_APPEND|experimentalsys.O_CREAT|experimentalsys.O_TRUNC) == 0
}
func (gate *workspaceGate) Lstat(path string) (wazerosys.Stat_t, experimentalsys.Errno) {
	if !gate.allowed() {
		return wazerosys.Stat_t{}, experimentalsys.EACCES
	}
	return gate.filesystem().Lstat(path)
}
func (gate *workspaceGate) Stat(path string) (wazerosys.Stat_t, experimentalsys.Errno) {
	if !gate.allowed() {
		return wazerosys.Stat_t{}, experimentalsys.EACCES
	}
	return gate.filesystem().Stat(path)
}
func (gate *workspaceGate) Mkdir(path string, perm fs.FileMode) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Mkdir(path, perm)
}
func (gate *workspaceGate) Chmod(path string, perm fs.FileMode) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Chmod(path, perm)
}
func (gate *workspaceGate) Rename(from, to string) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Rename(from, to)
}
func (gate *workspaceGate) Rmdir(path string) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Rmdir(path)
}
func (gate *workspaceGate) Unlink(path string) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Unlink(path)
}
func (gate *workspaceGate) Link(oldName, newName string) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Link(oldName, newName)
}
func (gate *workspaceGate) Symlink(oldName, link string) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Symlink(oldName, link)
}
func (gate *workspaceGate) Readlink(path string) (string, experimentalsys.Errno) {
	if !gate.allowed() {
		return "", experimentalsys.EACCES
	}
	return gate.filesystem().Readlink(path)
}
func (gate *workspaceGate) Utimens(path string, atim, mtim int64) experimentalsys.Errno {
	if !gate.allowed() {
		return experimentalsys.EACCES
	}
	return gate.filesystem().Utimens(path, atim, mtim)
}

// workspacePreopenFile is a virtual directory before activation. It opens the
// real rooted directory lazily after activation, so prepared modules contain no
// live workspace descriptor.
type workspacePreopenFile struct {
	experimentalsys.UnimplementedFile
	gate *workspaceGate
	flag experimentalsys.Oflag
	perm fs.FileMode

	mutex  sync.Mutex
	file   experimentalsys.File
	closed bool
}

func (file *workspacePreopenFile) delegate() (experimentalsys.File, experimentalsys.Errno) {
	if file == nil || file.gate == nil || !file.gate.allowed() {
		return nil, experimentalsys.EACCES
	}
	file.mutex.Lock()
	defer file.mutex.Unlock()
	if file.closed {
		return nil, experimentalsys.EBADF
	}
	if file.file == nil {
		opened, errno := file.gate.filesystem().OpenFile(".", file.flag, file.perm)
		if errno != 0 {
			return nil, errno
		}
		file.file = opened
	}
	return file.file, 0
}

func (file *workspacePreopenFile) Close() experimentalsys.Errno {
	file.mutex.Lock()
	defer file.mutex.Unlock()
	if file.closed {
		return experimentalsys.EBADF
	}
	file.closed = true
	if file.file != nil {
		return file.file.Close()
	}
	return 0
}
func (file *workspacePreopenFile) IsDir() (bool, experimentalsys.Errno) {
	if file.gate != nil && !file.gate.allowed() {
		return true, 0
	}
	delegate, errno := file.delegate()
	if errno != 0 {
		return false, errno
	}
	return delegate.IsDir()
}
func (file *workspacePreopenFile) Stat() (wazerosys.Stat_t, experimentalsys.Errno) {
	if file.gate != nil && !file.gate.allowed() {
		return wazerosys.Stat_t{Mode: fs.ModeDir | 0o755, Nlink: 1}, 0
	}
	delegate, errno := file.delegate()
	if errno != 0 {
		return wazerosys.Stat_t{}, errno
	}
	return delegate.Stat()
}
func (*workspacePreopenFile) Dev() (uint64, experimentalsys.Errno)          { return 0, 0 }
func (*workspacePreopenFile) Ino() (wazerosys.Inode, experimentalsys.Errno) { return 0, 0 }
func (file *workspacePreopenFile) Readdir(count int) ([]experimentalsys.Dirent, experimentalsys.Errno) {
	delegate, errno := file.delegate()
	if errno != 0 {
		return nil, errno
	}
	return delegate.Readdir(count)
}
func (file *workspacePreopenFile) Read(buffer []byte) (int, experimentalsys.Errno) {
	delegate, errno := file.delegate()
	if errno != 0 {
		return 0, errno
	}
	return delegate.Read(buffer)
}
func (file *workspacePreopenFile) Pread(buffer []byte, offset int64) (int, experimentalsys.Errno) {
	delegate, errno := file.delegate()
	if errno != 0 {
		return 0, errno
	}
	return delegate.Pread(buffer, offset)
}
func (file *workspacePreopenFile) Write(buffer []byte) (int, experimentalsys.Errno) {
	delegate, errno := file.delegate()
	if errno != 0 {
		return 0, errno
	}
	return delegate.Write(buffer)
}
func (file *workspacePreopenFile) Pwrite(buffer []byte, offset int64) (int, experimentalsys.Errno) {
	delegate, errno := file.delegate()
	if errno != 0 {
		return 0, errno
	}
	return delegate.Pwrite(buffer, offset)
}
func (file *workspacePreopenFile) Truncate(size int64) experimentalsys.Errno {
	delegate, errno := file.delegate()
	if errno != 0 {
		return errno
	}
	return delegate.Truncate(size)
}
func (file *workspacePreopenFile) Sync() experimentalsys.Errno {
	delegate, errno := file.delegate()
	if errno != 0 {
		return errno
	}
	return delegate.Sync()
}
func (file *workspacePreopenFile) Datasync() experimentalsys.Errno {
	delegate, errno := file.delegate()
	if errno != 0 {
		return errno
	}
	return delegate.Datasync()
}
func (file *workspacePreopenFile) IsAppend() bool {
	delegate, errno := file.delegate()
	return errno == 0 && delegate.IsAppend()
}
func (file *workspacePreopenFile) SetAppend(enabled bool) experimentalsys.Errno {
	delegate, errno := file.delegate()
	if errno != 0 {
		return errno
	}
	return delegate.SetAppend(enabled)
}
func (file *workspacePreopenFile) Utimens(atim, mtim int64) experimentalsys.Errno {
	delegate, errno := file.delegate()
	if errno != 0 {
		return errno
	}
	return delegate.Utimens(atim, mtim)
}
