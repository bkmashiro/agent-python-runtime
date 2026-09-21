package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	wazerosys "github.com/tetratelabs/wazero/sys"
)

const ChangeSetSchemaVersion = 1

var (
	ErrInvalidChangeSet = errors.New("invalid workspace change set")
	ErrExportTooLarge   = errors.New("workspace change export exceeds limit")
)

// ExportedChange carries contents only for added and modified regular files.
// Deleted files retain their baseline metadata but no data payload.
type ExportedChange struct {
	Path   string     `json:"path"`
	Kind   ChangeKind `json:"kind"`
	Before *FileState `json:"before,omitempty"`
	After  *FileState `json:"after,omitempty"`
	Data   []byte     `json:"data,omitempty"`
}

// ChangeSet is a bounded, reviewable handoff from a private workspace. It does
// not name a Host destination and applying it is deliberately a separate act.
type ChangeSet struct {
	SchemaVersion int              `json:"schema_version"`
	Before        Revision         `json:"before"`
	After         Revision         `json:"after"`
	Bytes         uint64           `json:"bytes"`
	Changes       []ExportedChange `json:"changes"`
}

// ExportChanges compares a trusted baseline snapshot with the current private
// workspace and materializes only added and modified file contents. maxBytes is
// an additional caller-selected cap below the workspace's own limits.
func (lease *Lease) ExportChanges(before Snapshot, maxBytes uint64) (ChangeSet, error) {
	if lease == nil || maxBytes == 0 {
		return ChangeSet{}, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return ChangeSet{}, ErrWorkspaceClosed
	}
	if lease.running {
		return ChangeSet{}, ErrWorkspaceBusy
	}
	limits := lease.filesystem.limits
	if err := validateSnapshot(before, limits); err != nil {
		return ChangeSet{}, err
	}
	after, err := lease.snapshotLocked()
	if err != nil {
		return ChangeSet{}, err
	}
	summary := Diff(before, after)
	bundle := ChangeSet{
		SchemaVersion: ChangeSetSchemaVersion,
		Before:        before.Revision,
		After:         after.Revision,
		Changes:       make([]ExportedChange, 0, len(summary.Changes)),
	}
	root, err := os.OpenRoot(lease.root)
	if err != nil {
		return ChangeSet{}, err
	}
	defer root.Close()
	for _, change := range summary.Changes {
		exported := ExportedChange{Path: change.Path, Kind: change.Kind, Before: change.Before, After: change.After}
		if change.Kind != ChangeDeleted {
			if change.After == nil || change.After.Size > maxBytes-bundle.Bytes {
				return ChangeSet{}, ErrExportTooLarge
			}
			data, err := root.ReadFile(change.Path)
			if err != nil {
				return ChangeSet{}, fmt.Errorf("export workspace file %s: %w", change.Path, err)
			}
			if !matchesFileState(data, *change.After) {
				return ChangeSet{}, fmt.Errorf("export workspace file changed after snapshot: %s", change.Path)
			}
			exported.Data = data
			bundle.Bytes += uint64(len(data))
		}
		bundle.Changes = append(bundle.Changes, exported)
	}
	if err := bundle.Validate(limits, maxBytes); err != nil {
		return ChangeSet{}, err
	}
	return bundle, nil
}

// Validate rejects malformed or tampered change bundles before Host-side use.
func (bundle ChangeSet) Validate(limits Limits, maxBytes uint64) error {
	if err := limits.validate(); err != nil {
		return err
	}
	if bundle.SchemaVersion != ChangeSetSchemaVersion || !validRevision(bundle.Before) || !validRevision(bundle.After) || maxBytes == 0 {
		return ErrInvalidChangeSet
	}
	if len(bundle.Changes) > int(limits.MaxFiles) {
		return ErrInvalidChangeSet
	}
	var total uint64
	previous := ""
	for index, change := range bundle.Changes {
		cleaned, err := cleanGuestPath(change.Path, limits.MaxDepth, false)
		if err != nil || cleaned != change.Path || (index > 0 && change.Path <= previous) {
			return ErrInvalidChangeSet
		}
		previous = change.Path
		if err := validateExportedChange(change, limits); err != nil {
			return err
		}
		if uint64(len(change.Data)) > maxBytes-total {
			return ErrExportTooLarge
		}
		total += uint64(len(change.Data))
	}
	if total != bundle.Bytes {
		return ErrInvalidChangeSet
	}
	return nil
}

func validateExportedChange(change ExportedChange, limits Limits) error {
	validState := func(state *FileState) bool {
		if state == nil || state.Path != change.Path || state.Size > limits.MaxFileBytes || len(state.SHA256) != sha256.Size*2 {
			return false
		}
		_, err := hex.DecodeString(state.SHA256)
		return err == nil
	}
	switch change.Kind {
	case ChangeAdded:
		if change.Before != nil || !validState(change.After) || uint64(len(change.Data)) != change.After.Size || !matchesFileState(change.Data, *change.After) {
			return ErrInvalidChangeSet
		}
	case ChangeModified:
		if !validState(change.Before) || !validState(change.After) || uint64(len(change.Data)) != change.After.Size || !matchesFileState(change.Data, *change.After) {
			return ErrInvalidChangeSet
		}
	case ChangeDeleted:
		if !validState(change.Before) || change.After != nil || len(change.Data) != 0 {
			return ErrInvalidChangeSet
		}
	default:
		return ErrInvalidChangeSet
	}
	return nil
}

func validateSnapshot(snapshot Snapshot, limits Limits) error {
	if !validRevision(snapshot.Revision) || len(snapshot.Files) > int(limits.MaxFiles) {
		return ErrInvalidChangeSet
	}
	var bytes uint64
	previous := ""
	for index, file := range snapshot.Files {
		cleaned, err := cleanGuestPath(file.Path, limits.MaxDepth, false)
		if err != nil || cleaned != file.Path || (index > 0 && file.Path <= previous) || file.Size > limits.MaxFileBytes || len(file.SHA256) != sha256.Size*2 {
			return ErrInvalidChangeSet
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil || file.Size > limits.MaxBytes-bytes {
			return ErrInvalidChangeSet
		}
		bytes += file.Size
		previous = file.Path
	}
	if uint32(len(snapshot.Files)) != snapshot.FileCount || bytes != snapshot.Bytes || snapshotRevision(snapshot.Files) != snapshot.Revision {
		return ErrInvalidChangeSet
	}
	return nil
}

func validRevision(revision Revision) bool {
	value := string(revision)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func matchesFileState(data []byte, state FileState) bool {
	if uint64(len(data)) != state.Size {
		return false
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]) == state.SHA256
}

type ConflictKind string

const (
	ConflictAlreadyExists ConflictKind = "already_exists"
	ConflictMissing       ConflictKind = "missing"
	ConflictChanged       ConflictKind = "changed"
	ConflictUnsupported   ConflictKind = "unsupported"
)

type Conflict struct {
	Path string       `json:"path"`
	Kind ConflictKind `json:"kind"`
}

type ConflictReport struct {
	Clean     bool       `json:"clean"`
	Conflicts []Conflict `json:"conflicts"`
}

// CheckDirectoryConflicts checks only paths touched by a change set. Unrelated
// Host edits do not conflict. It never writes to the target directory.
func CheckDirectoryConflicts(target string, bundle ChangeSet, limits Limits) (ConflictReport, error) {
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return ConflictReport{}, fmt.Errorf("%w: target must be a clean absolute directory", ErrInvalidWorkspace)
	}
	if err := bundle.Validate(limits, limits.MaxBytes); err != nil {
		return ConflictReport{}, err
	}
	rootInfo, err := os.Lstat(target)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ConflictReport{}, fmt.Errorf("%w: target root is unavailable or unsupported", ErrInvalidWorkspace)
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return ConflictReport{}, fmt.Errorf("open conflict target: %w", err)
	}
	defer root.Close()
	openedRootInfo, err := root.Lstat(".")
	if err != nil || !sameSourceObject(rootInfo, openedRootInfo) {
		return ConflictReport{}, fmt.Errorf("%w: target root changed before conflict check", ErrInvalidWorkspace)
	}
	rootDevice := wazerosys.NewStat_t(rootInfo).Dev
	report := ConflictReport{Clean: true, Conflicts: make([]Conflict, 0)}
	for _, change := range bundle.Changes {
		kind, conflict, err := checkTargetPath(root, change, rootDevice)
		if err != nil {
			return ConflictReport{}, err
		}
		if conflict {
			report.Clean = false
			report.Conflicts = append(report.Conflicts, Conflict{Path: change.Path, Kind: kind})
		}
	}
	finalRootInfo, err := os.Lstat(target)
	if err != nil || !sameSourceObject(rootInfo, finalRootInfo) || !rootInfo.ModTime().Equal(finalRootInfo.ModTime()) {
		return ConflictReport{}, fmt.Errorf("%w: target root changed during conflict check", ErrInvalidWorkspace)
	}
	return report, nil
}

func checkTargetPath(root *os.Root, change ExportedChange, rootDevice uint64) (ConflictKind, bool, error) {
	if safe, err := targetParentsAreSafe(root, change.Path, rootDevice); err != nil {
		return "", false, err
	} else if !safe {
		return ConflictUnsupported, true, nil
	}
	info, err := root.Lstat(change.Path)
	if errors.Is(err, fs.ErrNotExist) {
		if change.Kind == ChangeAdded {
			return "", false, nil
		}
		return ConflictMissing, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect conflict target %s: %w", change.Path, err)
	}
	if change.Kind == ChangeAdded {
		return ConflictAlreadyExists, true, nil
	}
	stat := wazerosys.NewStat_t(info)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || stat.Nlink != 1 || stat.Dev != rootDevice {
		return ConflictUnsupported, true, nil
	}
	actual, stable, err := readStableTargetFile(root, change.Path, info)
	if err != nil {
		return "", false, err
	}
	if !stable || change.Before == nil || actual != *change.Before {
		return ConflictChanged, true, nil
	}
	return "", false, nil
}

func targetParentsAreSafe(root *os.Root, name string, rootDevice uint64) (bool, error) {
	components := strings.Split(path.Dir(name), "/")
	if len(components) == 1 && components[0] == "." {
		return true, nil
	}
	current := ""
	for _, component := range components {
		if current == "" {
			current = component
		} else {
			current = path.Join(current, component)
		}
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect conflict target parent %s: %w", current, err)
		}
		stat := wazerosys.NewStat_t(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Dev != rootDevice {
			return false, nil
		}
	}
	return true, nil
}

func readStableTargetFile(root *os.Root, name string, before fs.FileInfo) (FileState, bool, error) {
	file, err := root.Open(name)
	if err != nil {
		return FileState{}, false, fmt.Errorf("open conflict target %s: %w", name, err)
	}
	hash := sha256.New()
	copied, copyErr := io.Copy(hash, file)
	after, statErr := file.Stat()
	closeErr := file.Close()
	linkedAfter, linkErr := root.Lstat(name)
	if copyErr != nil || statErr != nil || closeErr != nil || linkErr != nil {
		return FileState{}, false, fmt.Errorf("read conflict target %s", name)
	}
	stable := copied >= 0 && sameSourceObject(before, after) && before.ModTime().Equal(after.ModTime()) && sameSourceObject(before, linkedAfter) && before.ModTime().Equal(linkedAfter.ModTime())
	state := FileState{
		Path:       name,
		Size:       uint64(copied),
		SHA256:     hex.EncodeToString(hash.Sum(nil)),
		Executable: before.Mode().Perm()&0o111 != 0,
	}
	return state, stable, nil
}
