package labstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const RootSchemaVersion = "pysolate.labstore-root.v1"

var (
	rootNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	hexPairPattern  = regexp.MustCompile(`^[0-9a-f]{2}$`)
	hexTailPattern  = regexp.MustCompile(`^[0-9a-f]{62}\.obj$`)
)

type NamedRoot struct {
	Name   string `json:"name"`
	Target Ref    `json:"target"`
}

type rootRecord struct {
	SchemaVersion string `json:"schema_version"`
	Name          string `json:"name"`
	Target        Ref    `json:"target"`
}

type ReferenceCount struct {
	Ref   Ref    `json:"ref"`
	Count uint64 `json:"count"`
}

type RetentionPlan struct {
	Roots           []NamedRoot      `json:"roots"`
	ReferenceCounts []ReferenceCount `json:"reference_counts"`
	Reachable       []Ref            `json:"reachable"`
	Unreachable     []Ref            `json:"unreachable"`
}

type SweepReport struct {
	Deleted        []Ref  `json:"deleted"`
	ReclaimedBytes uint64 `json:"reclaimed_bytes"`
}

func (store *Store) Pin(name string, target Ref) error {
	if store == nil {
		return ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.availableForWrite(); err != nil {
		return err
	}
	if !validRootName(name) {
		return fmt.Errorf("%w: invalid root name", ErrInvalid)
	}
	if _, err := store.getLocked(target); err != nil {
		return fmt.Errorf("%w: root target is unavailable", ErrInvalid)
	}
	path := rootPath(name)
	info, inspectErr := store.root.Lstat(path)
	exists := inspectErr == nil
	if exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600) {
		return fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	if inspectErr != nil && !errors.Is(inspectErr, fs.ErrNotExist) {
		return fmt.Errorf("inspect root record: %w", inspectErr)
	}
	if !exists {
		roots, err := store.listRootsLocked(false)
		if err != nil {
			return err
		}
		if uint64(len(roots)) >= uint64(store.options.MaxRoots) {
			return fmt.Errorf("%w: root count exceeds configured limit", ErrInvalid)
		}
	}
	record := rootRecord{SchemaVersion: RootSchemaVersion, Name: name, Target: target}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if exists {
		return store.replaceAtomicLocked(path, encoded)
	}
	published, err := store.publishExclusiveLocked(path, encoded)
	if err != nil {
		return err
	}
	if !published {
		return fmt.Errorf("%w: root was concurrently published", ErrCorrupt)
	}
	return nil
}

func (store *Store) Resolve(name string) (Ref, error) {
	if store == nil {
		return Ref{}, ErrClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return Ref{}, ErrClosed
	}
	record, err := store.readRootLocked(name, true)
	if err != nil {
		return Ref{}, err
	}
	return record.Target, nil
}

func (store *Store) Unpin(name string) error {
	if store == nil {
		return ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.availableForWrite(); err != nil {
		return err
	}
	if !validRootName(name) {
		return fmt.Errorf("%w: invalid root name", ErrInvalid)
	}
	path := rootPath(name)
	info, err := store.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	if err := store.root.Remove(path); err != nil {
		return fmt.Errorf("remove root record: %w", err)
	}
	return store.syncDirectoryLocked("roots")
}

func (store *Store) PlanRetention() (RetentionPlan, error) {
	if store == nil {
		return RetentionPlan{}, ErrClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return RetentionPlan{}, ErrClosed
	}
	return store.planRetentionLocked()
}

func (store *Store) planRetentionLocked() (RetentionPlan, error) {
	objects, err := store.listObjectsLocked()
	if err != nil {
		return RetentionPlan{}, err
	}
	roots, err := store.listRootsLocked(true)
	if err != nil {
		return RetentionPlan{}, err
	}
	byRef := make(map[Ref]Object, len(objects))
	counts := make(map[Ref]uint64, len(objects))
	for _, object := range objects {
		byRef[object.Ref] = object
		counts[object.Ref] = 0
	}
	for _, object := range objects {
		for _, link := range object.Links {
			if _, ok := byRef[link]; !ok {
				return RetentionPlan{}, fmt.Errorf("%w: dangling object link", ErrCorrupt)
			}
			counts[link]++
		}
	}
	for _, root := range roots {
		if _, ok := byRef[root.Target]; !ok {
			return RetentionPlan{}, fmt.Errorf("%w: dangling retention root", ErrCorrupt)
		}
		counts[root.Target]++
	}
	reachableSet := make(map[Ref]struct{})
	stack := make([]Ref, 0, len(roots))
	for _, root := range roots {
		stack = append(stack, root.Target)
	}
	for len(stack) != 0 {
		last := len(stack) - 1
		ref := stack[last]
		stack = stack[:last]
		if _, seen := reachableSet[ref]; seen {
			continue
		}
		if uint64(len(reachableSet)) >= uint64(store.options.MaxReachableObjects) {
			return RetentionPlan{}, fmt.Errorf("%w: reachable graph exceeds configured limit", ErrInvalid)
		}
		reachableSet[ref] = struct{}{}
		stack = append(stack, byRef[ref].Links...)
	}
	plan := RetentionPlan{Roots: roots}
	for _, object := range objects {
		plan.ReferenceCounts = append(plan.ReferenceCounts, ReferenceCount{Ref: object.Ref, Count: counts[object.Ref]})
		if _, reachable := reachableSet[object.Ref]; reachable {
			plan.Reachable = append(plan.Reachable, object.Ref)
		} else {
			plan.Unreachable = append(plan.Unreachable, object.Ref)
		}
	}
	return plan, nil
}

func (store *Store) Sweep() (SweepReport, error) {
	if store == nil {
		return SweepReport{}, ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.availableForWrite(); err != nil {
		return SweepReport{}, err
	}
	plan, err := store.planRetentionLocked()
	if err != nil {
		return SweepReport{}, err
	}
	// Validate every exact deletion target first. This prevents a symlink or
	// malformed entry from turning collection into a partial best-effort walk.
	for _, ref := range plan.Unreachable {
		if _, err := store.getLocked(ref); err != nil {
			return SweepReport{}, err
		}
		if err := store.validateOptionalPrivacyPathLocked(ref); err != nil {
			return SweepReport{}, err
		}
	}
	report := SweepReport{Deleted: append([]Ref(nil), plan.Unreachable...)}
	for _, ref := range plan.Unreachable {
		path := objectPath(ref)
		info, err := store.root.Lstat(path)
		if err != nil {
			return SweepReport{}, fmt.Errorf("inspect collected object: %w", err)
		}
		if info.Size() > 0 {
			report.ReclaimedBytes += uint64(info.Size())
		}
		if err := store.root.Remove(path); err != nil {
			return SweepReport{}, fmt.Errorf("remove unreachable object: %w", err)
		}
		if err := store.syncDirectoryLocked(filepath.ToSlash(filepath.Dir(path))); err != nil {
			return SweepReport{}, err
		}
		policyPath := privacyPath(ref)
		if policyInfo, err := store.root.Lstat(policyPath); err == nil {
			if policyInfo.Size() > 0 {
				report.ReclaimedBytes += uint64(policyInfo.Size())
			}
			if err := store.root.Remove(policyPath); err != nil {
				return SweepReport{}, fmt.Errorf("remove unreachable privacy metadata: %w", err)
			}
			if err := store.syncDirectoryLocked(filepath.ToSlash(filepath.Dir(policyPath))); err != nil {
				return SweepReport{}, err
			}
		}
	}
	return report, nil
}

func (store *Store) validateOptionalPrivacyPathLocked(ref Ref) error {
	path := privacyPath(ref)
	info, err := store.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: invalid privacy metadata", ErrCorrupt)
	}
	return nil
}

func (store *Store) readRootLocked(name string, validateTarget bool) (rootRecord, error) {
	if !validRootName(name) {
		return rootRecord{}, fmt.Errorf("%w: invalid root name", ErrInvalid)
	}
	path := rootPath(name)
	info, err := store.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rootRecord{}, ErrNotFound
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 4096 {
		return rootRecord{}, fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	file, err := store.root.Open(path)
	if err != nil {
		return rootRecord{}, fmt.Errorf("%w: open root record", ErrCorrupt)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return rootRecord{}, fmt.Errorf("%w: root changed while opening", ErrCorrupt)
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return rootRecord{}, fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	if _, _, err := canonicalJSON(data); err != nil {
		return rootRecord{}, fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record rootRecord
	if err := decoder.Decode(&record); err != nil || decoder.Decode(&struct{}{}) != io.EOF || record.SchemaVersion != RootSchemaVersion || record.Name != name || record.Target.validate() != nil {
		return rootRecord{}, fmt.Errorf("%w: invalid root record", ErrCorrupt)
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return rootRecord{}, fmt.Errorf("%w: non-canonical root record", ErrCorrupt)
	}
	if validateTarget {
		if _, err := store.getLocked(record.Target); err != nil {
			return rootRecord{}, fmt.Errorf("%w: root target is unavailable", ErrCorrupt)
		}
	}
	return record, nil
}

func (store *Store) listRootsLocked(validateTargets bool) ([]NamedRoot, error) {
	entries, err := store.readDirBoundedLocked("roots", uint64(store.options.MaxRoots)+1)
	if err != nil {
		return nil, err
	}
	if uint64(len(entries)) > uint64(store.options.MaxRoots) {
		return nil, fmt.Errorf("%w: root count exceeds configured limit", ErrInvalid)
	}
	result := make([]NamedRoot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("%w: invalid root directory entry", ErrCorrupt)
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		record, err := store.readRootLocked(name, validateTargets)
		if err != nil {
			return nil, err
		}
		result = append(result, NamedRoot{Name: name, Target: record.Target})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (store *Store) listObjectsLocked() ([]Object, error) {
	objects := make([]Object, 0)
	for _, kind := range allKinds {
		kindPath := filepath.ToSlash(filepath.Join("objects", string(kind)))
		info, err := store.root.Lstat(kindPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("%w: invalid kind directory", ErrCorrupt)
		}
		prefixes, err := store.readDirBoundedLocked(kindPath, 257)
		if err != nil || len(prefixes) > 256 {
			return nil, fmt.Errorf("%w: invalid object prefix directory", ErrCorrupt)
		}
		for _, prefix := range prefixes {
			if !prefix.IsDir() || prefix.Type()&os.ModeSymlink != 0 || !hexPairPattern.MatchString(prefix.Name()) {
				return nil, fmt.Errorf("%w: invalid object prefix", ErrCorrupt)
			}
			prefixPath := filepath.ToSlash(filepath.Join(kindPath, prefix.Name()))
			remaining := uint64(store.options.MaxReachableObjects) - uint64(len(objects))
			entries, err := store.readDirBoundedLocked(prefixPath, remaining+1)
			if err != nil || uint64(len(entries)) > remaining {
				return nil, fmt.Errorf("%w: object count exceeds configured limit", ErrInvalid)
			}
			for _, entry := range entries {
				if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !hexTailPattern.MatchString(entry.Name()) {
					return nil, fmt.Errorf("%w: invalid object directory entry", ErrCorrupt)
				}
				digest := prefix.Name() + strings.TrimSuffix(entry.Name(), ".obj")
				object, err := store.getLocked(Ref{Kind: kind, SHA256: "sha256:" + digest})
				if err != nil {
					return nil, err
				}
				objects = append(objects, object)
			}
		}
	}
	sort.Slice(objects, func(i, j int) bool { return refLess(objects[i].Ref, objects[j].Ref) })
	return objects, nil
}

func (store *Store) readDirBoundedLocked(path string, maximum uint64) ([]fs.DirEntry, error) {
	if maximum == 0 || maximum > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("%w: invalid directory bound", ErrInvalid)
	}
	info, err := store.root.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: invalid store directory", ErrCorrupt)
	}
	directory, err := store.root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open store directory: %w", err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(int(maximum))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read store directory: %w", err)
	}
	return entries, nil
}

func validRootName(name string) bool {
	return rootNamePattern.MatchString(name)
}

func rootPath(name string) string {
	return filepath.ToSlash(filepath.Join("roots", name+".json"))
}

func refLess(left, right Ref) bool {
	if left.Kind == right.Kind {
		return left.SHA256 < right.SHA256
	}
	return left.Kind < right.Kind
}
