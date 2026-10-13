package versionfs

import (
	"errors"
	"io"
	"io/fs"
	"sort"
	"sync"
	"syscall"
	"time"
)

var _ fs.FS = (*View)(nil)
var _ fs.StatFS = (*View)(nil)
var _ fs.ReadDirFS = (*View)(nil)
var _ fs.ReadFileFS = (*View)(nil)
var _ fs.ReadDirFile = (*fileHandle)(nil)
var _ io.ReaderAt = (*fileHandle)(nil)
var _ io.Seeker = (*fileHandle)(nil)

type fileInfo struct {
	name string
	item *node
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64 {
	if i.item.dir {
		return 0
	}
	return int64(len(i.item.data))
}
func (i fileInfo) Mode() fs.FileMode {
	if i.item.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return i.item.dir }

// Sys exposes the immutable version identity for recording adapters.
func (i fileInfo) Sys() any { return i.item.identity }

type dirEntry struct{ info fileInfo }

func (d dirEntry) Name() string               { return d.info.Name() }
func (d dirEntry) IsDir() bool                { return d.info.IsDir() }
func (d dirEntry) Type() fs.FileMode          { return d.info.Mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.info, nil }

func (v *View) lookup(name string) (*node, string, error) {
	if v == nil || v.state == nil {
		return nil, "", fs.ErrInvalid
	}
	parts, err := validPath(name, int(^uint(0)>>1))
	if err != nil {
		return nil, "", err
	}
	current := v.state.root
	if len(parts) == 0 {
		return current, ".", nil
	}
	for index, part := range parts {
		if !current.dir {
			return nil, "", syscall.ENOTDIR
		}
		current = current.children[part]
		if current == nil {
			return nil, "", fs.ErrNotExist
		}
		if index < len(parts)-1 && !current.dir {
			return nil, "", syscall.ENOTDIR
		}
	}
	return current, parts[len(parts)-1], nil
}

func (v *View) Identity(name string) (Identity, error) {
	item, _, err := v.lookup(name)
	if err != nil {
		return Identity{}, pathError("identity", name, err)
	}
	return item.identity, nil
}
func (v *View) Stat(name string) (fs.FileInfo, error) {
	item, base, err := v.lookup(name)
	if err != nil {
		return nil, pathError("stat", name, err)
	}
	return fileInfo{name: base, item: item}, nil
}
func (v *View) ReadFile(name string) ([]byte, error) {
	item, _, err := v.lookup(name)
	if err != nil {
		return nil, pathError("readfile", name, err)
	}
	if item.dir {
		return nil, pathError("readfile", name, syscall.EISDIR)
	}
	return append([]byte(nil), item.data...), nil
}
func (v *View) ReadDir(name string) ([]fs.DirEntry, error) {
	item, _, err := v.lookup(name)
	if err != nil {
		return nil, pathError("readdir", name, err)
	}
	if !item.dir {
		return nil, pathError("readdir", name, syscall.ENOTDIR)
	}
	names := make([]string, 0, len(item.children))
	for child := range item.children {
		names = append(names, child)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, child := range names {
		entries = append(entries, dirEntry{fileInfo{name: child, item: item.children[child]}})
	}
	return entries, nil
}

func (v *View) Open(name string) (fs.File, error) {
	item, base, err := v.lookup(name)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	if item.dir {
		names := make([]string, 0, len(item.children))
		for child := range item.children {
			names = append(names, child)
		}
		sort.Strings(names)
		entries := make([]fs.DirEntry, 0, len(names))
		for _, child := range names {
			entries = append(entries, dirEntry{fileInfo{name: child, item: item.children[child]}})
		}
		return &fileHandle{name: base, item: item, entries: entries}, nil
	}
	return &fileHandle{name: base, item: item}, nil
}

type fileHandle struct {
	mu          sync.Mutex
	name        string
	item        *node
	offset      int64
	entryOffset int
	closed      bool
	entries     []fs.DirEntry
}

func (f *fileHandle) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fs.ErrClosed
	}
	return fileInfo{name: f.name, item: f.item}, nil
}
func (f *fileHandle) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fs.ErrClosed
	}
	f.closed = true
	return nil
}
func (f *fileHandle) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.item.dir {
		return 0, syscall.EISDIR
	}
	n, err := readAt(f.item.data, p, f.offset)
	f.offset += int64(n)
	return n, err
}
func (f *fileHandle) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.item.dir {
		return 0, syscall.EISDIR
	}
	return readAt(f.item.data, p, off)
}
func readAt(data, p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (f *fileHandle) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.item.dir {
		return 0, syscall.EISDIR
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.offset
	case io.SeekEnd:
		base = int64(len(f.item.data))
	default:
		return 0, errors.New("invalid seek whence")
	}
	if offset > 0 && base > int64(^uint64(0)>>1)-offset {
		return 0, errors.New("invalid seek offset")
	}
	if offset < 0 && offset < -base {
		return 0, errors.New("negative position")
	}
	next := base + offset
	f.offset = next
	return next, nil
}
func (f *fileHandle) ReadDir(count int) ([]fs.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fs.ErrClosed
	}
	if !f.item.dir {
		return nil, syscall.ENOTDIR
	}
	remaining := len(f.entries) - f.entryOffset
	if count <= 0 {
		out := append([]fs.DirEntry(nil), f.entries[f.entryOffset:]...)
		f.entryOffset = len(f.entries)
		return out, nil
	}
	if remaining == 0 {
		return nil, io.EOF
	}
	if count > remaining {
		count = remaining
	}
	out := append([]fs.DirEntry(nil), f.entries[f.entryOffset:f.entryOffset+count]...)
	f.entryOffset += count
	return out, nil
}
