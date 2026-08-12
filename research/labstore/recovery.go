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
	"sort"
	"strings"
)

const RecoverySchemaVersion = "pysolate.labstore-recovery.v1"

type RecoveryReport struct {
	SchemaVersion        string `json:"schema_version"`
	OrphanStages         uint64 `json:"orphan_stages"`
	OrphanStageBytes     uint64 `json:"orphan_stage_bytes"`
	OrphanPrivacyRecords uint64 `json:"orphan_privacy_records"`
	OrphanPrivacyBytes   uint64 `json:"orphan_privacy_bytes"`
	RemovedStages        uint64 `json:"removed_stages"`
	ReclaimedBytes       uint64 `json:"reclaimed_bytes"`
}

type recoveryCandidate struct {
	path string
	size uint64
}

// AuditOffline inspects recoverable publication leftovers while holding
// exclusive lifecycle ownership. It fails with ErrBusy if any Store handle is
// open and never mutates the store.
func AuditOffline(path string, options Options) (RecoveryReport, error) {
	return recoverOffline(path, options, false)
}

// RepairOffline removes only fully validated publication stages. Validation of
// the complete candidate set precedes every removal, so malformed entries fail
// closed without a partial repair. Objectless privacy records remain durable so
// recovery cannot downgrade a private classification.
func RepairOffline(path string, options Options) (RecoveryReport, error) {
	return recoverOffline(path, options, true)
}

func recoverOffline(path string, options Options, repair bool) (RecoveryReport, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return RecoveryReport{}, fmt.Errorf("%w: store path must be absolute and canonical", ErrInvalid)
	}
	if err := prepareStoreRoot(path, true); err != nil {
		return RecoveryReport{}, err
	}
	ownership, err := acquireAdvisoryLock(path, true)
	if err != nil {
		return RecoveryReport{}, err
	}
	defer releaseAdvisoryLock(ownership)
	rooted, err := os.OpenRoot(path)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("open labstore root: %w", err)
	}
	defer rooted.Close()
	store := &Store{root: rooted, path: path, options: options.normalized()}
	if err := store.prepareLayout(true); err != nil {
		return RecoveryReport{}, err
	}
	report, candidates, err := store.auditRecoveryLocked()
	if err != nil || !repair {
		return report, err
	}
	directories := make(map[string]struct{})
	for _, candidate := range candidates {
		if err := store.root.Remove(candidate.path); err != nil {
			return RecoveryReport{}, fmt.Errorf("remove validated recovery candidate: %w", err)
		}
		directories[filepath.ToSlash(filepath.Dir(candidate.path))] = struct{}{}
		report.ReclaimedBytes += candidate.size
		report.RemovedStages++
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Strings(ordered)
	for _, directory := range ordered {
		if err := store.syncDirectoryLocked(directory); err != nil {
			return RecoveryReport{}, err
		}
	}
	return report, nil
}

func (store *Store) auditRecoveryLocked() (RecoveryReport, []recoveryCandidate, error) {
	report := RecoveryReport{SchemaVersion: RecoverySchemaVersion}
	var candidates []recoveryCandidate
	var checked uint64
	err := filepath.WalkDir(store.path, func(hostPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(store.path, hostPath)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		checked++
		if checked > uint64(store.options.MaxReachableObjects)*8 {
			return fmt.Errorf("%w: recovery scan exceeds configured bound", ErrInvalid)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: recovery encountered symbolic link", ErrCorrupt)
		}
		if entry.IsDir() {
			if info.Mode().Perm()&0o077 != 0 {
				return fmt.Errorf("%w: recovery encountered unprotected directory", ErrCorrupt)
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 0 {
			return fmt.Errorf("%w: recovery encountered invalid file", ErrCorrupt)
		}
		size := uint64(info.Size())
		if strings.HasPrefix(entry.Name(), ".stage-") {
			if err := store.validateRecoveryStageLocked(rel, size); err != nil {
				return err
			}
			if report.OrphanStages == ^uint64(0) || report.OrphanStageBytes > ^uint64(0)-size {
				return fmt.Errorf("%w: recovery stage accounting overflow", ErrInvalid)
			}
			report.OrphanStages++
			report.OrphanStageBytes += size
			candidates = append(candidates, recoveryCandidate{path: rel, size: size})
			return nil
		}
		if strings.HasPrefix(rel, "metadata/privacy/") {
			ref, err := refFromPrivacyPath(rel)
			if err != nil {
				return err
			}
			if _, err := store.readPrivacyLocked(ref); err != nil {
				return err
			}
			if _, err := store.root.Lstat(objectPath(ref)); errors.Is(err, fs.ErrNotExist) {
				if report.OrphanPrivacyRecords == ^uint64(0) || report.OrphanPrivacyBytes > ^uint64(0)-size {
					return fmt.Errorf("%w: recovery privacy accounting overflow", ErrInvalid)
				}
				report.OrphanPrivacyRecords++
				report.OrphanPrivacyBytes += size
			} else if err != nil {
				return fmt.Errorf("%w: inspect recovery object", ErrCorrupt)
			}
		}
		return nil
	})
	if err != nil {
		return RecoveryReport{}, nil, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return report, candidates, nil
}

func (store *Store) validateRecoveryStageLocked(path string, size uint64) error {
	if size == 0 || size > store.options.MaxObjectBytes+uint64(store.options.MaxHeaderBytes)+4096 {
		return fmt.Errorf("%w: invalid recovery stage size", ErrCorrupt)
	}
	file, err := store.root.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open recovery stage", ErrCorrupt)
	}
	defer file.Close()
	switch {
	case strings.HasPrefix(path, "objects/"):
		ref, _, _, err := decodeObject(file, store.options)
		parts := strings.Split(path, "/")
		if err != nil || len(parts) != 4 || string(ref.Kind) != parts[1] || digestHex(ref)[:2] != parts[2] {
			return fmt.Errorf("%w: invalid object recovery stage", ErrCorrupt)
		}
		return nil
	case strings.HasPrefix(path, "metadata/privacy/"):
		data, err := io.ReadAll(io.LimitReader(file, 1025))
		if err != nil || len(data) > 1024 {
			return fmt.Errorf("%w: invalid privacy recovery stage", ErrCorrupt)
		}
		var record privacyRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF || record.SchemaVersion != PolicySchemaVersion || !validPrivacy(record.Privacy) {
			return fmt.Errorf("%w: invalid privacy recovery stage", ErrCorrupt)
		}
		canonical, _ := json.Marshal(record)
		if !bytes.Equal(canonical, data) {
			return fmt.Errorf("%w: non-canonical privacy recovery stage", ErrCorrupt)
		}
		return nil
	case filepath.ToSlash(filepath.Dir(path)) == "roots":
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		if err != nil || len(data) > 4096 {
			return fmt.Errorf("%w: invalid root recovery stage", ErrCorrupt)
		}
		var record rootRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil || decoder.Decode(&struct{}{}) != io.EOF || record.SchemaVersion != RootSchemaVersion || !validRootName(record.Name) || record.Target.validate() != nil {
			return fmt.Errorf("%w: invalid root recovery stage", ErrCorrupt)
		}
		canonical, _ := json.Marshal(record)
		if !bytes.Equal(canonical, data) {
			return fmt.Errorf("%w: non-canonical root recovery stage", ErrCorrupt)
		}
		return nil
	default:
		return fmt.Errorf("%w: recovery stage outside publication directories", ErrCorrupt)
	}
}

func refFromPrivacyPath(path string) (Ref, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "metadata" || parts[1] != "privacy" || !validKind(Kind(parts[2])) || !hexPairPattern.MatchString(parts[3]) || !strings.HasSuffix(parts[4], ".json") {
		return Ref{}, fmt.Errorf("%w: malformed privacy path", ErrCorrupt)
	}
	tail := strings.TrimSuffix(parts[4], ".json")
	if len(tail) != 62 || !regexpHex(tail) {
		return Ref{}, fmt.Errorf("%w: malformed privacy path", ErrCorrupt)
	}
	ref := Ref{Kind: Kind(parts[2]), SHA256: "sha256:" + parts[3] + tail}
	if err := ref.validate(); err != nil {
		return Ref{}, fmt.Errorf("%w: malformed privacy path", ErrCorrupt)
	}
	return ref, nil
}

func regexpHex(value string) bool {
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
