package labstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const StatsSchemaVersion = "pysolate.labstore-stats.v1"

// Stats reports logical regular-file bytes, not allocated filesystem blocks.
// IndexBytes includes privacy classifications and named retention roots.
type StoreStats struct {
	SchemaVersion     string `json:"schema_version"`
	ObjectCount       uint64 `json:"object_count"`
	RootCount         uint64 `json:"root_count"`
	LinkCount         uint64 `json:"link_count"`
	LogicalBodyBytes  uint64 `json:"logical_body_bytes"`
	ObjectFileBytes   uint64 `json:"object_file_bytes"`
	PrivacyIndexBytes uint64 `json:"privacy_index_bytes"`
	RootIndexBytes    uint64 `json:"root_index_bytes"`
	IndexBytes        uint64 `json:"index_bytes"`
	StoredBytes       uint64 `json:"stored_bytes"`
	PrivateObjects    uint64 `json:"private_objects"`
	PortableObjects   uint64 `json:"portable_objects"`
}

func (store *Store) Stats() (StoreStats, error) {
	if store == nil {
		return StoreStats{}, ErrClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return StoreStats{}, ErrClosed
	}
	objects, err := store.listObjectsLocked()
	if err != nil {
		return StoreStats{}, err
	}
	roots, err := store.listRootsLocked(true)
	if err != nil {
		return StoreStats{}, err
	}
	stats := StoreStats{SchemaVersion: StatsSchemaVersion, ObjectCount: uint64(len(objects)), RootCount: uint64(len(roots))}
	for _, object := range objects {
		stats.LinkCount += uint64(len(object.Links))
		stats.LogicalBodyBytes += uint64(len(object.Body))
		if object.Privacy == PrivacyPortable {
			stats.PortableObjects++
		} else {
			stats.PrivateObjects++
		}
		objectInfo, err := store.root.Lstat(objectPath(object.Ref))
		if err != nil || !objectInfo.Mode().IsRegular() || objectInfo.Mode()&os.ModeSymlink != 0 || objectInfo.Size() < 0 {
			return StoreStats{}, fmt.Errorf("%w: invalid object while collecting stats", ErrCorrupt)
		}
		stats.ObjectFileBytes += uint64(objectInfo.Size())
		privacyInfo, err := store.root.Lstat(privacyPath(object.Ref))
		if err == nil {
			if !privacyInfo.Mode().IsRegular() || privacyInfo.Mode()&os.ModeSymlink != 0 || privacyInfo.Size() < 0 {
				return StoreStats{}, fmt.Errorf("%w: invalid privacy index while collecting stats", ErrCorrupt)
			}
			stats.PrivacyIndexBytes += uint64(privacyInfo.Size())
		} else if !errors.Is(err, fs.ErrNotExist) {
			return StoreStats{}, fmt.Errorf("inspect privacy index: %w", err)
		}
	}
	for _, root := range roots {
		info, err := store.root.Lstat(rootPath(root.Name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 {
			return StoreStats{}, fmt.Errorf("%w: invalid root index while collecting stats", ErrCorrupt)
		}
		stats.RootIndexBytes += uint64(info.Size())
	}
	stats.IndexBytes = stats.PrivacyIndexBytes + stats.RootIndexBytes
	stats.StoredBytes = stats.ObjectFileBytes + stats.IndexBytes
	return stats, nil
}
