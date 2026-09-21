package workspace

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
)

// Revision identifies a canonical metadata-and-content snapshot. The digest
// never contains or depends on the private Host backing path.
type Revision string

type FileState struct {
	Path       string `json:"path"`
	Size       uint64 `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type Snapshot struct {
	Revision  Revision    `json:"revision"`
	FileCount uint32      `json:"file_count"`
	Bytes     uint64      `json:"bytes"`
	Files     []FileState `json:"files"`
}

type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

type Change struct {
	Path   string     `json:"path"`
	Kind   ChangeKind `json:"kind"`
	Before *FileState `json:"before,omitempty"`
	After  *FileState `json:"after,omitempty"`
}

type ChangeSummary struct {
	Before   Revision `json:"before"`
	After    Revision `json:"after"`
	Added    uint32   `json:"added"`
	Modified uint32   `json:"modified"`
	Deleted  uint32   `json:"deleted"`
	Changes  []Change `json:"changes"`
}

// Snapshot returns a stable, content-addressed file manifest. It is available
// only between Guest runs, while the lease has exclusive ownership.
func (lease *Lease) Snapshot() (Snapshot, error) {
	if lease == nil {
		return Snapshot{}, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return Snapshot{}, ErrWorkspaceClosed
	}
	if lease.running {
		return Snapshot{}, ErrWorkspaceBusy
	}
	return lease.snapshotLocked()
}

func (lease *Lease) snapshotLocked() (Snapshot, error) {
	usage, err := scanOrdinaryTree(lease.root, lease.filesystem.limits)
	if err != nil {
		return Snapshot{}, err
	}
	paths := make([]string, 0, len(usage.sizes))
	for name := range usage.sizes {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	root, err := os.OpenRoot(lease.root)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()

	files := make([]FileState, 0, len(paths))
	for _, name := range paths {
		file, err := root.Open(name)
		if err != nil {
			return Snapshot{}, fmt.Errorf("snapshot workspace file %s: %w", name, err)
		}
		before, err := file.Stat()
		if err != nil || !before.Mode().IsRegular() || before.Size() < 0 || uint64(before.Size()) != usage.sizes[name] {
			_ = file.Close()
			return Snapshot{}, fmt.Errorf("snapshot workspace file changed before read: %s", name)
		}
		hash := sha256.New()
		copied, copyErr := io.Copy(hash, file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		linkedAfter, linkErr := root.Lstat(name)
		if copyErr != nil || statErr != nil || closeErr != nil || linkErr != nil || copied < 0 || uint64(copied) != usage.sizes[name] ||
			!sameSourceObject(before, after) || !before.ModTime().Equal(after.ModTime()) ||
			!sameSourceObject(before, linkedAfter) || !before.ModTime().Equal(linkedAfter.ModTime()) {
			return Snapshot{}, fmt.Errorf("snapshot workspace file changed during read: %s", name)
		}
		files = append(files, FileState{
			Path:       name,
			Size:       usage.sizes[name],
			SHA256:     hex.EncodeToString(hash.Sum(nil)),
			Executable: usage.entries[name].Perm()&0o111 != 0,
		})
	}
	return Snapshot{
		Revision:  snapshotRevision(files),
		FileCount: uint32(len(files)),
		Bytes:     usage.bytes,
		Files:     files,
	}, nil
}

func snapshotRevision(files []FileState) Revision {
	hash := sha256.New()
	_, _ = hash.Write([]byte("pysolate-workspace-snapshot-v1\x00"))
	var encoded [8]byte
	for _, file := range files {
		binary.BigEndian.PutUint64(encoded[:], uint64(len(file.Path)))
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write([]byte(file.Path))
		binary.BigEndian.PutUint64(encoded[:], file.Size)
		_, _ = hash.Write(encoded[:])
		if file.Executable {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
		_, _ = hash.Write([]byte(file.SHA256))
	}
	return Revision("sha256:" + hex.EncodeToString(hash.Sum(nil)))
}

// Diff compares two snapshots without reading file contents. Changes are
// sorted by path so API responses and review output are deterministic.
func Diff(before, after Snapshot) ChangeSummary {
	oldFiles := make(map[string]FileState, len(before.Files))
	newFiles := make(map[string]FileState, len(after.Files))
	paths := make(map[string]struct{}, len(before.Files)+len(after.Files))
	for _, file := range before.Files {
		oldFiles[file.Path] = file
		paths[file.Path] = struct{}{}
	}
	for _, file := range after.Files {
		newFiles[file.Path] = file
		paths[file.Path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	summary := ChangeSummary{Before: before.Revision, After: after.Revision, Changes: make([]Change, 0)}
	for _, name := range ordered {
		oldFile, hadOld := oldFiles[name]
		newFile, hasNew := newFiles[name]
		switch {
		case !hadOld:
			value := newFile
			summary.Added++
			summary.Changes = append(summary.Changes, Change{Path: name, Kind: ChangeAdded, After: &value})
		case !hasNew:
			value := oldFile
			summary.Deleted++
			summary.Changes = append(summary.Changes, Change{Path: name, Kind: ChangeDeleted, Before: &value})
		case oldFile != newFile:
			oldValue, newValue := oldFile, newFile
			summary.Modified++
			summary.Changes = append(summary.Changes, Change{Path: name, Kind: ChangeModified, Before: &oldValue, After: &newValue})
		}
	}
	return summary
}
