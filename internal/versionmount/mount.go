// Package versionmount binds immutable input views to the wazero FS boundary.
package versionmount

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/fsrecord"
	"github.com/bkmashiro/agent-python-runtime/runtime/versionfs"
	sys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

// FS records adapter calls, including failed lookups and denied modifications.
// Operations rejected by WASI before reaching this adapter are not in this log.
type FS struct {
	sys.UnimplementedFS
	view     *versionfs.View
	base     sys.FS
	sink     fsrecord.Sink
	cancel   func()
	mu       sync.Mutex
	failure  error
	captured map[versionfs.Identity]bool
}

func New(view *versionfs.View, sink fsrecord.Sink, cancel func()) (*FS, error) {
	if view == nil {
		return nil, errors.New("nil file view")
	}
	if _, err := view.Identity("."); err != nil {
		return nil, err
	}
	if sink != nil && sink.ViewRevision() != view.Revision() {
		return nil, errors.New("filesystem recorder belongs to another view")
	}
	m := &FS{view: view, base: &sysfs.ReadFS{FS: &sysfs.AdaptFS{FS: view}}, sink: sink, cancel: cancel}
	if sink != nil {
		m.captured = make(map[versionfs.Identity]bool)
		m.base = &sysfs.ReadFS{FS: &sysfs.AdaptFS{FS: recordedView{m}}}
	}
	return m, nil
}
func (m *FS) Err() error { m.mu.Lock(); defer m.mu.Unlock(); return m.failure }
func (m *FS) failLocked(err error) {
	if m.failure == nil {
		m.failure = err
		if m.cancel != nil {
			m.cancel()
		}
	}
}
func valid(path string) bool {
	return utf8.ValidString(path) && !strings.ContainsAny(path, "\\\x00") && fs.ValidPath(path)
}

// A trailing slash from WASI retains its directory requirement.
func normalized(path string) (string, bool) {
	// wasi-vfs may issue mount-relative names such as "/." while enumerating.
	// This prefix is scoped to this immutable tree, never the host OS root.
	path = strings.TrimLeft(path, "/")
	if path == "" {
		path = "."
	}
	dir := strings.HasSuffix(path, "/")
	if dir {
		path = strings.TrimRight(path, "/")
	}
	return path, dir
}
func detail(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (m *FS) record(op, path string, id versionfs.Identity, offset, count int64, e sys.Errno, d json.RawMessage) sys.Errno {
	if m.sink == nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return sys.EIO
	}
	event := fsrecord.Event{Operation: op, Path: path, Resource: id.ResourceID, Version: id.Version, Offset: offset, Count: count, Detail: d}
	if e != 0 {
		event.Error = e.Error()
	}
	if err := m.sink.Append(event); err != nil {
		m.failLocked(err)
		return sys.EIO
	}
	return e
}
func (m *FS) identity(path string) versionfs.Identity {
	if m.sink == nil {
		return versionfs.Identity{}
	}
	id, _ := m.view.Identity(path)
	return id
}
func (m *FS) capture(path string, id versionfs.Identity) sys.Errno {
	if m.sink == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return sys.EIO
	}
	if m.captured[id] {
		return 0
	}
	if err := m.sink.EnsureContent(id.ResourceID, id.Version, func() ([]byte, error) { return m.view.ReadFile(path) }); err != nil {
		m.failLocked(err)
		return sys.EIO
	}
	m.captured[id] = true
	return 0
}
func (m *FS) OpenFile(path string, flags sys.Oflag, perm fs.FileMode) (sys.File, sys.Errno) {
	if m.Err() != nil {
		return nil, sys.EIO
	}
	name, dir := normalized(path)
	var f sys.File
	var e sys.Errno
	if !valid(name) {
		e = sys.EINVAL
	} else if flags&(sys.O_WRONLY|sys.O_RDWR|sys.O_CREAT|sys.O_TRUNC|sys.O_APPEND|sys.O_EXCL) != 0 {
		e = sys.EROFS
	} else {
		if dir {
			flags |= sys.O_DIRECTORY
		}
		f, e = m.base.OpenFile(name, flags, perm)
	}
	var d json.RawMessage
	if m.sink != nil {
		d = detail(map[string]any{"flags": flags})
	}
	if re := m.record("open", path, m.identity(name), 0, 0, e, d); re != 0 {
		if f != nil {
			_ = f.Close()
		}
		return nil, re
	}
	return f, 0
}
func (m *FS) stat(path, op string) (wazerosys.Stat_t, sys.Errno) {
	if m.Err() != nil {
		return wazerosys.Stat_t{}, sys.EIO
	}
	name, dir := normalized(path)
	var st wazerosys.Stat_t
	var e sys.Errno
	if !valid(name) {
		e = sys.EINVAL
	} else {
		st, e = m.base.Stat(name)
		if e == 0 && dir && st.Mode&fs.ModeDir == 0 {
			e = sys.ENOTDIR
		}
	}
	var d json.RawMessage
	if m.sink != nil && e == 0 {
		d = detail(st)
	}
	e = m.record(op, path, m.identity(name), 0, 0, e, d)
	return st, e
}
func (m *FS) Stat(p string) (wazerosys.Stat_t, sys.Errno)  { return m.stat(p, "stat") }
func (m *FS) Lstat(p string) (wazerosys.Stat_t, sys.Errno) { return m.stat(p, "lstat") }
func (m *FS) Readlink(p string) (string, sys.Errno) {
	name, _ := normalized(p)
	e := sys.EINVAL
	if valid(name) {
		if _, err := m.view.Identity(name); err != nil {
			e = sys.ENOENT
		}
	}
	return "", m.record("readlink", p, m.identity(name), 0, 0, e, nil)
}
func (m *FS) deny(op, p, target string) sys.Errno {
	var d json.RawMessage
	if m.sink != nil && target != "" {
		d = detail(map[string]string{"target": target})
	}
	return m.record(op, p, m.identity(p), 0, 0, sys.EROFS, d)
}
func (m *FS) Mkdir(p string, _ fs.FileMode) sys.Errno { return m.deny("mkdir", p, "") }
func (m *FS) Chmod(p string, _ fs.FileMode) sys.Errno { return m.deny("chmod", p, "") }
func (m *FS) Rename(p, q string) sys.Errno            { return m.deny("rename", p, q) }
func (m *FS) Rmdir(p string) sys.Errno                { return m.deny("rmdir", p, "") }
func (m *FS) Unlink(p string) sys.Errno               { return m.deny("unlink", p, "") }
func (m *FS) Link(p, q string) sys.Errno              { return m.deny("link", p, q) }
func (m *FS) Symlink(p, q string) sys.Errno           { return m.deny("symlink", q, p) }
func (m *FS) Utimens(p string, _, _ int64) sys.Errno  { return m.deny("utimens", p, "") }
