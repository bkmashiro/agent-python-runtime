package versionmount

import (
	"encoding/json"
	"io"
	"io/fs"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/versionfs"
	sys "github.com/tetratelabs/wazero/experimental/sys"
)

// Keep handles on the standard io/fs interfaces. wazero's adapter converts their
// errors to WASI errno; no second implementation of its syscall-file ABI is needed.
type recordedView struct{ m *FS }

func (v recordedView) Open(path string) (fs.File, error) {
	if v.m.Err() != nil {
		return nil, sys.EIO
	}
	f, err := v.m.view.Open(path)
	if err != nil {
		return nil, err
	}
	return &file{File: f, m: v.m, path: path, id: v.m.identity(path)}, nil
}

type file struct {
	fs.File
	m       *FS
	path    string
	id      versionfs.Identity
	mu      sync.Mutex
	offset  int64
	entries int64
}

func (f *file) log(op string, offset, count int64, err error, d json.RawMessage) error {
	errno := sys.UnwrapOSError(err)
	if got := f.m.record(op, f.path, f.id, offset, count, errno, d); got != errno {
		return got
	}
	return err
}
func (f *file) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m.Err() != nil {
		return 0, sys.EIO
	}
	start := f.offset
	n, err := f.File.Read(p)
	f.offset += int64(n)
	if (err == nil || err == io.EOF) && len(p) > 0 {
		if e := f.m.capture(f.path, f.id); e != 0 {
			return 0, e
		}
	}
	if e := f.log("read", start, int64(n), err, nil); e != err {
		return 0, e
	}
	return n, err
}
func (f *file) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m.Err() != nil {
		return 0, sys.EIO
	}
	n, err := f.File.(io.ReaderAt).ReadAt(p, off)
	if (err == nil || err == io.EOF) && len(p) > 0 {
		if e := f.m.capture(f.path, f.id); e != 0 {
			return 0, e
		}
	}
	if e := f.log("pread", off, int64(n), err, nil); e != err {
		return 0, e
	}
	return n, err
}
func (f *file) Seek(off int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m.Err() != nil {
		return 0, sys.EIO
	}
	pos, err := f.File.(io.Seeker).Seek(off, whence)
	if err == nil {
		f.offset = pos
		if pos == 0 {
			f.entries = 0
		}
	}
	return pos, f.log("seek", pos, 0, err, detail(map[string]any{"offset": off, "whence": whence}))
}
func (f *file) ReadDir(n int) ([]fs.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m.Err() != nil {
		return nil, sys.EIO
	}
	entries, err := f.File.(fs.ReadDirFile).ReadDir(n)
	start := f.entries
	f.entries += int64(len(entries))
	type entry struct {
		Name string      `json:"name"`
		Type fs.FileMode `json:"type"`
	}
	names := make([]entry, len(entries))
	for i, e := range entries {
		names[i] = entry{e.Name(), e.Type()}
	}
	if e := f.log("readdir", start, int64(len(entries)), err, detail(names)); e != err {
		return nil, e
	}
	return entries, err
}
func (f *file) Stat() (fs.FileInfo, error) {
	if f.m.Err() != nil {
		return nil, sys.EIO
	}
	st, err := f.File.Stat()
	var d json.RawMessage
	if err == nil {
		d = detail(map[string]any{"size": st.Size(), "mode": st.Mode()})
	}
	if e := f.log("fstat", 0, 0, err, d); e != nil {
		return nil, e
	}
	return st, nil
}
func (f *file) Close() error { err := f.File.Close(); return f.log("close", 0, 0, err, nil) }
