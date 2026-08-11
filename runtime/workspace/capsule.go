package workspace

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// CapsuleMagic identifies the single-file, deterministic workspace storage
	// format. It is not a live filesystem image.
	CapsuleMagic         = "PYSOLATE-WORKSPACE-CAPSULE-V1\n"
	CapsuleSchemaVersion = "pysolate.workspace-capsule.v1"
	maxCapsuleManifest   = 16 << 20
)

// CapsuleInfo is portable workspace identity and bounded storage metadata. A
// local Ref is deliberately excluded because importing always creates a new
// Host-local identity.
type CapsuleInfo struct {
	SchemaVersion   string
	WorkspaceSHA256 string
	TreeSHA256      string
	EntryCount      uint32
	TotalBytes      uint64
	Limits          Limits
}

type capsuleLimits struct {
	MaxFiles     uint32 `json:"max_files"`
	MaxBytes     uint64 `json:"max_bytes"`
	MaxFileBytes uint64 `json:"max_file_bytes"`
	MaxDepth     uint32 `json:"max_depth"`
}

type capsuleEntry struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Executable bool   `json:"executable,omitempty"`
	Size       uint64 `json:"size,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

type capsuleManifest struct {
	SchemaVersion   string         `json:"schema_version"`
	WorkspaceSHA256 string         `json:"workspace_sha256"`
	TreeSHA256      string         `json:"tree_sha256"`
	EntryCount      uint32         `json:"entry_count"`
	TotalBytes      uint64         `json:"total_bytes"`
	Limits          capsuleLimits  `json:"limits"`
	Entries         []capsuleEntry `json:"entries"`
}

type capsuleIdentity struct {
	SchemaVersion string         `json:"schema_version"`
	TreeSHA256    string         `json:"tree_sha256"`
	EntryCount    uint32         `json:"entry_count"`
	TotalBytes    uint64         `json:"total_bytes"`
	Limits        capsuleLimits  `json:"limits"`
	Entries       []capsuleEntry `json:"entries"`
}

func limitsForCapsule(limits Limits) capsuleLimits {
	return capsuleLimits{
		MaxFiles: limits.MaxFiles, MaxBytes: limits.MaxBytes,
		MaxFileBytes: limits.MaxFileBytes, MaxDepth: limits.MaxDepth,
	}
}

func (limits capsuleLimits) runtimeLimits() Limits {
	return Limits{
		MaxFiles: limits.MaxFiles, MaxBytes: limits.MaxBytes,
		MaxFileBytes: limits.MaxFileBytes, MaxDepth: limits.MaxDepth,
	}
}

// ExportCapsule serializes one inactive workspace into a deterministic,
// self-contained stream. Host paths, local Refs, leases, interpreter state,
// capabilities and per-run temporary files are not workspace contents and are
// intentionally absent. If serialization fails, the supplied writer may
// contain a prefix; atomic file publication is a caller responsibility.
func (manager *Manager) ExportCapsule(ref Ref, writer io.Writer) (CapsuleInfo, error) {
	if manager == nil {
		return CapsuleInfo{}, ErrWorkspaceClosed
	}
	if writer == nil || !validRef(ref) {
		return CapsuleInfo{}, fmt.Errorf("%w: invalid capsule export", ErrInvalidWorkspace)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return CapsuleInfo{}, ErrWorkspaceClosed
	}
	item, exists := manager.entries[ref]
	if !exists {
		return CapsuleInfo{}, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return CapsuleInfo{}, ErrWorkspaceBusy
	}
	rooted, err := os.OpenRoot(item.root)
	if err != nil {
		return CapsuleInfo{}, fmt.Errorf("open workspace capsule root: %w", err)
	}
	defer rooted.Close()
	entries, totalBytes, err := collectCapsuleEntries(rooted, item.root, item.limits)
	if err != nil {
		return CapsuleInfo{}, err
	}
	manifest := capsuleManifest{
		SchemaVersion: CapsuleSchemaVersion,
		EntryCount:    uint32(len(entries)),
		TotalBytes:    totalBytes,
		Limits:        limitsForCapsule(item.limits),
		Entries:       entries,
	}
	manifest.TreeSHA256, err = capsuleTreeDigest(entries)
	if err != nil {
		return CapsuleInfo{}, err
	}
	manifest.WorkspaceSHA256, err = capsuleWorkspaceDigest(manifest)
	if err != nil {
		return CapsuleInfo{}, err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return CapsuleInfo{}, fmt.Errorf("encode workspace capsule manifest: %w", err)
	}
	if len(encoded) > maxCapsuleManifest {
		return CapsuleInfo{}, fmt.Errorf("%w: capsule manifest exceeds limit", ErrInvalidWorkspace)
	}
	buffered := bufio.NewWriter(writer)
	if _, err := io.WriteString(buffered, CapsuleMagic); err != nil {
		return CapsuleInfo{}, fmt.Errorf("write workspace capsule magic: %w", err)
	}
	if err := binary.Write(buffered, binary.BigEndian, uint64(len(encoded))); err != nil {
		return CapsuleInfo{}, fmt.Errorf("write workspace capsule manifest length: %w", err)
	}
	if _, err := buffered.Write(encoded); err != nil {
		return CapsuleInfo{}, fmt.Errorf("write workspace capsule manifest: %w", err)
	}
	for _, capsuleEntry := range entries {
		if capsuleEntry.Kind != "file" {
			continue
		}
		if err := streamCapsuleFile(rooted, capsuleEntry, buffered); err != nil {
			return CapsuleInfo{}, err
		}
	}
	if err := buffered.Flush(); err != nil {
		return CapsuleInfo{}, fmt.Errorf("flush workspace capsule: %w", err)
	}
	return manifest.info(), nil
}

func collectCapsuleEntries(rooted *os.Root, root string, limits Limits) ([]capsuleEntry, uint64, error) {
	usage, err := scanOrdinaryTree(root, limits)
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(usage.entries))
	for name := range usage.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]capsuleEntry, 0, len(names))
	var totalBytes uint64
	for _, name := range names {
		info, err := rooted.Lstat(filepath.FromSlash(name))
		if err != nil {
			return nil, 0, fmt.Errorf("inspect workspace capsule entry: %w", err)
		}
		if info.IsDir() {
			entries = append(entries, capsuleEntry{Path: name, Kind: "directory"})
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, 0, fmt.Errorf("%w: capsule contains unsupported file type", ErrInvalidWorkspace)
		}
		digest, size, err := digestCapsuleFile(rooted, name, uint64(info.Size()))
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, capsuleEntry{
			Path: name, Kind: "file", Executable: info.Mode().Perm()&0o100 != 0,
			Size: size, SHA256: digest,
		})
		totalBytes += size
	}
	if uint32(len(entries)) != usage.files || totalBytes != usage.bytes {
		return nil, 0, fmt.Errorf("%w: workspace changed during capsule export", ErrInvalidWorkspace)
	}
	return entries, totalBytes, nil
}

func digestCapsuleFile(rooted *os.Root, name string, expectedSize uint64) (string, uint64, error) {
	file, err := rooted.Open(filepath.FromSlash(name))
	if err != nil {
		return "", 0, fmt.Errorf("open workspace capsule file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("read workspace capsule file: %w", err)
	}
	if count < 0 || uint64(count) != expectedSize {
		return "", 0, fmt.Errorf("%w: workspace changed during capsule export", ErrInvalidWorkspace)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), uint64(count), nil
}

func streamCapsuleFile(rooted *os.Root, entry capsuleEntry, writer io.Writer) error {
	file, err := rooted.Open(filepath.FromSlash(entry.Path))
	if err != nil {
		return fmt.Errorf("open workspace capsule payload: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(writer, hash), file)
	if err != nil {
		return fmt.Errorf("write workspace capsule payload: %w", err)
	}
	if count < 0 || uint64(count) != entry.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("%w: workspace changed during capsule export", ErrInvalidWorkspace)
	}
	return nil
}

// ImportCapsule verifies and materializes a complete capsule beneath the
// Manager's private root. The capsule's stored limits must fit inside the
// Host-provided ceiling; the capsule can never enlarge Host authority.
func (manager *Manager) ImportCapsule(reader io.Reader, ceiling Limits) (Ref, CapsuleInfo, error) {
	if manager == nil {
		return "", CapsuleInfo{}, ErrWorkspaceClosed
	}
	if reader == nil {
		return "", CapsuleInfo{}, fmt.Errorf("%w: invalid capsule import", ErrInvalidWorkspace)
	}
	if err := ceiling.validate(); err != nil {
		return "", CapsuleInfo{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", CapsuleInfo{}, ErrWorkspaceClosed
	}

	manifest, err := readCapsuleManifest(reader, ceiling)
	if err != nil {
		return "", CapsuleInfo{}, err
	}
	limits := manifest.Limits.runtimeLimits()
	ref, root, err := manager.allocateRootLocked()
	if err != nil {
		return "", CapsuleInfo{}, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(root)
		}
	}()
	for _, entry := range manifest.Entries {
		full := filepath.Join(root, filepath.FromSlash(entry.Path))
		if entry.Kind == "directory" {
			if err := os.Mkdir(full, 0o700); err != nil {
				return "", CapsuleInfo{}, fmt.Errorf("materialize capsule directory: %w", err)
			}
			continue
		}
		mode := fs.FileMode(0o600)
		if entry.Executable {
			mode = 0o700
		}
		file, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return "", CapsuleInfo{}, fmt.Errorf("materialize capsule file: %w", err)
		}
		hash := sha256.New()
		count, copyErr := io.CopyN(io.MultiWriter(file, hash), reader, int64(entry.Size))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || count < 0 || uint64(count) != entry.Size {
			return "", CapsuleInfo{}, fmt.Errorf("%w: truncated capsule payload", ErrInvalidWorkspace)
		}
		if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return "", CapsuleInfo{}, fmt.Errorf("%w: capsule payload digest mismatch", ErrInvalidWorkspace)
		}
	}
	var trailing [1]byte
	if count, err := io.ReadFull(reader, trailing[:]); err == nil || count != 0 || !errors.Is(err, io.EOF) {
		return "", CapsuleInfo{}, fmt.Errorf("%w: trailing capsule payload", ErrInvalidWorkspace)
	}
	usage, err := scanOrdinaryTree(root, limits)
	if err != nil {
		return "", CapsuleInfo{}, err
	}
	if usage.files != manifest.EntryCount || usage.bytes != manifest.TotalBytes {
		return "", CapsuleInfo{}, fmt.Errorf("%w: materialized capsule does not match manifest", ErrInvalidWorkspace)
	}
	manager.entries[ref] = &entry{root: root, limits: limits}
	failed = false
	return ref, manifest.info(), nil
}

func readCapsuleManifest(reader io.Reader, ceiling Limits) (capsuleManifest, error) {
	magic := make([]byte, len(CapsuleMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != CapsuleMagic {
		return capsuleManifest{}, fmt.Errorf("%w: invalid capsule magic", ErrInvalidWorkspace)
	}
	var manifestLength uint64
	if err := binary.Read(reader, binary.BigEndian, &manifestLength); err != nil || manifestLength == 0 || manifestLength > maxCapsuleManifest {
		return capsuleManifest{}, fmt.Errorf("%w: invalid capsule manifest length", ErrInvalidWorkspace)
	}
	encoded := make([]byte, int(manifestLength))
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return capsuleManifest{}, fmt.Errorf("%w: truncated capsule manifest", ErrInvalidWorkspace)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var manifest capsuleManifest
	if err := decoder.Decode(&manifest); err != nil {
		return capsuleManifest{}, fmt.Errorf("%w: invalid capsule manifest", ErrInvalidWorkspace)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return capsuleManifest{}, fmt.Errorf("%w: invalid capsule manifest suffix", ErrInvalidWorkspace)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return capsuleManifest{}, fmt.Errorf("%w: capsule manifest is not canonical", ErrInvalidWorkspace)
	}
	if err := validateCapsuleManifest(manifest, ceiling); err != nil {
		return capsuleManifest{}, err
	}
	return manifest, nil
}

func validateCapsuleManifest(manifest capsuleManifest, ceiling Limits) error {
	if manifest.SchemaVersion != CapsuleSchemaVersion || manifest.EntryCount != uint32(len(manifest.Entries)) {
		return fmt.Errorf("%w: invalid capsule manifest identity", ErrInvalidWorkspace)
	}
	limits := manifest.Limits.runtimeLimits()
	if err := limits.validate(); err != nil || !limitsWithin(limits, ceiling) {
		return fmt.Errorf("%w: capsule limits exceed Host ceiling", ErrInvalidWorkspace)
	}
	if manifest.EntryCount > limits.MaxFiles || manifest.EntryCount > ceiling.MaxFiles || manifest.TotalBytes > limits.MaxBytes || manifest.TotalBytes > ceiling.MaxBytes {
		return fmt.Errorf("%w: capsule usage exceeds limits", ErrInvalidWorkspace)
	}
	seen := make(map[string]string, len(manifest.Entries))
	var prior string
	var totalBytes uint64
	for _, entry := range manifest.Entries {
		cleaned, err := cleanGuestPath(entry.Path, limits.MaxDepth, false)
		if err != nil || cleaned != entry.Path || (prior != "" && entry.Path <= prior) {
			return fmt.Errorf("%w: capsule paths are not canonical and sorted", ErrInvalidWorkspace)
		}
		prior = entry.Path
		parent := path.Dir(entry.Path)
		if parent != "." && seen[parent] != "directory" {
			return fmt.Errorf("%w: capsule parent directory is missing", ErrInvalidWorkspace)
		}
		switch entry.Kind {
		case "directory":
			if entry.Executable || entry.Size != 0 || entry.SHA256 != "" {
				return fmt.Errorf("%w: invalid capsule directory", ErrInvalidWorkspace)
			}
		case "file":
			if entry.Size > math.MaxInt64 || entry.Size > limits.MaxFileBytes || entry.Size > ceiling.MaxFileBytes || !validSHA256(entry.SHA256) || entry.Size > manifest.TotalBytes || totalBytes > manifest.TotalBytes-entry.Size {
				return fmt.Errorf("%w: invalid capsule file", ErrInvalidWorkspace)
			}
			totalBytes += entry.Size
		default:
			return fmt.Errorf("%w: unknown capsule entry kind", ErrInvalidWorkspace)
		}
		seen[entry.Path] = entry.Kind
	}
	if totalBytes != manifest.TotalBytes {
		return fmt.Errorf("%w: capsule byte count mismatch", ErrInvalidWorkspace)
	}
	workspaceDigest, err := capsuleWorkspaceDigest(manifest)
	if err != nil || workspaceDigest != manifest.WorkspaceSHA256 {
		return fmt.Errorf("%w: capsule workspace digest mismatch", ErrInvalidWorkspace)
	}
	digest, err := capsuleTreeDigest(manifest.Entries)
	if err != nil || digest != manifest.TreeSHA256 {
		return fmt.Errorf("%w: capsule tree digest mismatch", ErrInvalidWorkspace)
	}
	return nil
}

func limitsWithin(value, ceiling Limits) bool {
	return value.MaxFiles <= ceiling.MaxFiles && value.MaxBytes <= ceiling.MaxBytes && value.MaxFileBytes <= ceiling.MaxFileBytes && value.MaxDepth <= ceiling.MaxDepth
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func capsuleTreeDigest(entries []capsuleEntry) (string, error) {
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("encode capsule tree identity: %w", err)
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "pysolate.workspace-tree.v1\x00")
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func capsuleWorkspaceDigest(manifest capsuleManifest) (string, error) {
	identity := capsuleIdentity{
		SchemaVersion: manifest.SchemaVersion, TreeSHA256: manifest.TreeSHA256,
		EntryCount: manifest.EntryCount, TotalBytes: manifest.TotalBytes,
		Limits: manifest.Limits, Entries: manifest.Entries,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode capsule workspace identity: %w", err)
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "pysolate.workspace.v1\x00")
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (manifest capsuleManifest) info() CapsuleInfo {
	return CapsuleInfo{
		SchemaVersion:   manifest.SchemaVersion,
		WorkspaceSHA256: manifest.WorkspaceSHA256,
		TreeSHA256:      manifest.TreeSHA256,
		EntryCount:      manifest.EntryCount,
		TotalBytes:      manifest.TotalBytes,
		Limits:          manifest.Limits.runtimeLimits(),
	}
}
