package labstore

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const contentIdentityDomain = "pysolate.labstore.content.v1\x00"

type diskHeader struct {
	SchemaVersion string `json:"schema_version"`
	Kind          Kind   `json:"kind"`
	SHA256        string `json:"sha256"`
	BodyBytes     uint64 `json:"body_bytes"`
	Links         []Ref  `json:"links"`
}

type privacyRecord struct {
	SchemaVersion string  `json:"schema_version"`
	Privacy       Privacy `json:"privacy"`
}

type Store struct {
	mu      sync.RWMutex
	root    *os.Root
	path    string
	options Options
	closed  bool
}

func Open(path string, options Options) (*Store, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("%w: store path must be absolute and canonical", ErrInvalid)
	}
	options = options.normalized()
	if err := prepareStoreRoot(path, options.ReadOnly); err != nil {
		return nil, err
	}
	rooted, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open labstore root: %w", err)
	}
	store := &Store{root: rooted, path: path, options: options}
	if err := store.prepareLayout(options.ReadOnly); err != nil {
		_ = rooted.Close()
		return nil, err
	}
	return store, nil
}

func prepareStoreRoot(path string, readOnly bool) error {
	if err := rejectSymlinkComponents(path, true); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%w: store root must be a real directory", ErrInvalid)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect labstore root: %w", err)
	}
	if readOnly {
		return fmt.Errorf("%w: read-only store does not exist", ErrNotFound)
	}
	parentInfo, parentErr := os.Lstat(filepath.Dir(path))
	if parentErr != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("%w: store parent must be an existing real directory", ErrInvalid)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return fmt.Errorf("create labstore root: %w", err)
	}
	return nil
}

func rejectSymlinkComponents(path string, allowMissingLeaf bool) error {
	current := path
	first := true
	for {
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) && allowMissingLeaf && first {
			first = false
			current = filepath.Dir(current)
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: inspect store path component", ErrInvalid)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: store path crosses a symbolic link", ErrInvalid)
		}
		if !first && !info.IsDir() {
			return fmt.Errorf("%w: store ancestor is not a directory", ErrInvalid)
		}
		// Continue across Host-private ancestors so a symlink cannot be hidden
		// below the selected private boundary. Stop at the first public system
		// ancestor; common temporary paths may themselves use an OS-managed
		// alias such as /var -> /private/var.
		if !first && info.Mode().Perm()&0o077 != 0 {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
		first = false
	}
}

func (store *Store) prepareLayout(readOnly bool) error {
	for _, name := range []string{"objects", "metadata", "metadata/privacy", "roots"} {
		info, err := store.root.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
				return fmt.Errorf("%w: %s is not a real directory", ErrInvalid, name)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) || readOnly {
			return fmt.Errorf("%w: missing or invalid store layout %s", ErrInvalid, name)
		}
		if err := store.root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create labstore directory %s: %w", name, err)
		}
	}
	return nil
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	return store.root.Close()
}

func (store *Store) Put(kind Kind, body []byte, options PutOptions) (Ref, bool, error) {
	if store == nil {
		return Ref{}, false, ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.availableForWrite(); err != nil {
		return Ref{}, false, err
	}
	if !validKind(kind) || !validPrivacy(options.Privacy) {
		return Ref{}, false, fmt.Errorf("%w: invalid object kind or privacy", ErrInvalid)
	}
	if options.Credentials != CredentialsAbsent {
		return Ref{}, false, ErrCredentials
	}
	if uint64(len(body)) > store.options.MaxObjectBytes {
		return Ref{}, false, fmt.Errorf("%w: body exceeds configured limit", ErrInvalid)
	}
	if structuredKind(kind) {
		document, canonical, err := canonicalJSON(body)
		if err != nil || !bytes.Equal(canonical, body) {
			return Ref{}, false, fmt.Errorf("%w: structured object body must be canonical JSON", ErrInvalid)
		}
		if containsCredentialField(document) {
			return Ref{}, false, ErrCredentials
		}
	}
	links, err := normalizeLinks(options.Links, store.options.MaxLinks)
	if err != nil {
		return Ref{}, false, err
	}
	for _, link := range links {
		linkedObject, err := store.getLocked(link)
		if err != nil {
			return Ref{}, false, fmt.Errorf("%w: referenced object %s is unavailable", ErrInvalid, link)
		}
		if options.Privacy == PrivacyPortable && linkedObject.Privacy != PrivacyPortable {
			return Ref{}, false, ErrPrivate
		}
	}
	ref, err := contentRef(kind, links, body)
	if err != nil {
		return Ref{}, false, err
	}
	if existing, getErr := store.getLocked(ref); getErr == nil {
		if !bytes.Equal(existing.Body, body) || !equalRefs(existing.Links, links) {
			return Ref{}, false, fmt.Errorf("%w: identity collision", ErrCorrupt)
		}
		if err := store.writeExistingPrivacyLocked(ref, options.Privacy); err != nil {
			return Ref{}, false, err
		}
		return ref, false, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return Ref{}, false, getErr
	}
	encoded, err := encodeObject(ref, links, body)
	if err != nil {
		return Ref{}, false, err
	}
	objectPath := objectPath(ref)
	if err := store.ensureObjectParentLocked(ref, false); err != nil {
		return Ref{}, false, err
	}
	published, err := store.publishExclusiveLocked(objectPath, encoded)
	if err != nil {
		return Ref{}, false, err
	}
	if !published {
		existing, getErr := store.getLocked(ref)
		if getErr != nil || !bytes.Equal(existing.Body, body) || !equalRefs(existing.Links, links) {
			return Ref{}, false, fmt.Errorf("%w: concurrent object publication mismatch", ErrCorrupt)
		}
		if err := store.writeExistingPrivacyLocked(ref, options.Privacy); err != nil {
			return Ref{}, false, err
		}
		return ref, false, nil
	}
	if err := store.writePrivacyLocked(ref, options.Privacy); err != nil {
		return Ref{}, false, err
	}
	return ref, published, nil
}

func (store *Store) writeExistingPrivacyLocked(ref Ref, requested Privacy) error {
	var err error
	for attempt := 0; attempt < 16; attempt++ {
		_, err = store.root.Lstat(privacyPath(ref))
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
		// Object and mutable classification are separate files. Another Store
		// handle may have won object publication and still be fsyncing its
		// sidecar. Bound the convergence wait rather than treating that narrow
		// window as persistent missing metadata.
		time.Sleep(time.Millisecond)
	}
	if errors.Is(err, fs.ErrNotExist) {
		// Missing classification is interpreted as private. A portable
		// deduplicating Put must not turn that fail-safe state into exportable
		// metadata; a private Put may repair the missing sidecar in place.
		if requested == PrivacyPortable {
			return ErrPrivate
		}
		return store.writePrivacyLocked(ref, PrivacyPrivate)
	}
	if err != nil {
		return fmt.Errorf("%w: inspect privacy metadata", ErrCorrupt)
	}
	return store.writePrivacyLocked(ref, requested)
}

// PutJSON canonicalizes a normalized semantic body before publication. It is
// intentionally unavailable for opaque prompt, provider, code, and file blobs.
func (store *Store) PutJSON(kind Kind, raw []byte, options PutOptions) (Ref, bool, error) {
	if !structuredKind(kind) {
		return Ref{}, false, fmt.Errorf("%w: kind is not a structured JSON object", ErrInvalid)
	}
	document, canonical, err := canonicalJSON(raw)
	if err != nil {
		return Ref{}, false, err
	}
	if containsCredentialField(document) {
		return Ref{}, false, ErrCredentials
	}
	return store.Put(kind, canonical, options)
}

func (store *Store) Get(ref Ref) (Object, error) {
	if store == nil {
		return Object{}, ErrClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return Object{}, ErrClosed
	}
	return store.getLocked(ref)
}

// GetPortable is the explicit export boundary. Ordinary Get remains available
// to the protected local Lab regardless of classification.
func (store *Store) GetPortable(ref Ref) (Object, error) {
	if store == nil {
		return Object{}, ErrClosed
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return Object{}, ErrClosed
	}
	object, err := store.getLocked(ref)
	if err != nil {
		return Object{}, err
	}
	visited := make(map[Ref]struct{})
	if err := store.requirePortableLocked(ref, visited); err != nil {
		return Object{}, err
	}
	return object, nil
}

func (store *Store) requirePortableLocked(ref Ref, visited map[Ref]struct{}) error {
	if _, ok := visited[ref]; ok {
		return nil
	}
	if uint64(len(visited)) >= uint64(store.options.MaxReachableObjects) {
		return fmt.Errorf("%w: portable graph exceeds configured bound", ErrInvalid)
	}
	visited[ref] = struct{}{}
	object, err := store.getLocked(ref)
	if err != nil {
		return err
	}
	if object.Privacy != PrivacyPortable {
		return ErrPrivate
	}
	for _, link := range object.Links {
		if err := store.requirePortableLocked(link, visited); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) getLocked(ref Ref) (Object, error) {
	if err := ref.validate(); err != nil {
		return Object{}, err
	}
	if err := store.ensureObjectParentLocked(ref, true); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Object{}, ErrNotFound
		}
		return Object{}, err
	}
	path := objectPath(ref)
	info, err := store.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("inspect labstore object: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return Object{}, fmt.Errorf("%w: object is not a protected regular file", ErrCorrupt)
	}
	maximumFileBytes := int64(len(objectMagic)) + 4 + int64(store.options.MaxHeaderBytes) + int64(store.options.MaxObjectBytes)
	if info.Size() < int64(len(objectMagic)+4) || info.Size() > maximumFileBytes {
		return Object{}, fmt.Errorf("%w: object file size is outside bounds", ErrCorrupt)
	}
	file, err := store.root.Open(path)
	if err != nil {
		return Object{}, fmt.Errorf("open labstore object: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return Object{}, fmt.Errorf("%w: object changed while opening", ErrCorrupt)
	}
	refOnDisk, links, body, err := decodeObject(file, store.options)
	if err != nil {
		return Object{}, err
	}
	if refOnDisk != ref {
		return Object{}, fmt.Errorf("%w: object reference mismatch", ErrCorrupt)
	}
	privacy, err := store.readPrivacyLocked(ref)
	if err != nil {
		return Object{}, err
	}
	return Object{Ref: ref, Kind: ref.Kind, Privacy: privacy, Links: append([]Ref(nil), links...), Body: append([]byte(nil), body...)}, nil
}

func (store *Store) availableForWrite() error {
	if store.closed {
		return ErrClosed
	}
	if store.options.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

func normalizeLinks(input []Ref, maximum uint32) ([]Ref, error) {
	if uint64(len(input)) > uint64(maximum) {
		return nil, fmt.Errorf("%w: too many object links", ErrInvalid)
	}
	links := append([]Ref(nil), input...)
	for _, link := range links {
		if err := link.validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Kind == links[j].Kind {
			return links[i].SHA256 < links[j].SHA256
		}
		return links[i].Kind < links[j].Kind
	})
	for index := 1; index < len(links); index++ {
		if links[index] == links[index-1] {
			return nil, fmt.Errorf("%w: duplicate object link", ErrInvalid)
		}
	}
	return links, nil
}

func contentRef(kind Kind, links []Ref, body []byte) (Ref, error) {
	encodedLinks, err := json.Marshal(links)
	if err != nil {
		return Ref{}, fmt.Errorf("encode content links: %w", err)
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, contentIdentityDomain)
	_, _ = io.WriteString(hash, string(kind))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encodedLinks)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(body)
	return Ref{Kind: kind, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}

func encodeObject(ref Ref, links []Ref, body []byte) ([]byte, error) {
	header := diskHeader{SchemaVersion: ObjectSchemaVersion, Kind: ref.Kind, SHA256: ref.SHA256, BodyBytes: uint64(len(body)), Links: links}
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("encode labstore object header: %w", err)
	}
	var output bytes.Buffer
	_, _ = output.WriteString(objectMagic)
	if err := binary.Write(&output, binary.BigEndian, uint32(len(encodedHeader))); err != nil {
		return nil, err
	}
	_, _ = output.Write(encodedHeader)
	_, _ = output.Write(body)
	return output.Bytes(), nil
}

func decodeObject(reader io.Reader, options Options) (Ref, []Ref, []byte, error) {
	magic := make([]byte, len(objectMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != objectMagic {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object magic", ErrCorrupt)
	}
	var headerBytes uint32
	if err := binary.Read(reader, binary.BigEndian, &headerBytes); err != nil || headerBytes == 0 || headerBytes > options.MaxHeaderBytes {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object header length", ErrCorrupt)
	}
	encodedHeader := make([]byte, headerBytes)
	if _, err := io.ReadFull(reader, encodedHeader); err != nil {
		return Ref{}, nil, nil, fmt.Errorf("%w: truncated object header", ErrCorrupt)
	}
	if _, _, err := canonicalJSON(encodedHeader); err != nil {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object header: %v", ErrCorrupt, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encodedHeader))
	decoder.DisallowUnknownFields()
	var header diskHeader
	if err := decoder.Decode(&header); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object header", ErrCorrupt)
	}
	canonicalHeader, err := json.Marshal(header)
	if err != nil || !bytes.Equal(canonicalHeader, encodedHeader) {
		return Ref{}, nil, nil, fmt.Errorf("%w: non-canonical object header", ErrCorrupt)
	}
	ref := Ref{Kind: header.Kind, SHA256: header.SHA256}
	if header.SchemaVersion != ObjectSchemaVersion || ref.validate() != nil || header.BodyBytes > options.MaxObjectBytes {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object header values", ErrCorrupt)
	}
	links, err := normalizeLinks(header.Links, options.MaxLinks)
	if err != nil || !equalRefs(links, header.Links) {
		return Ref{}, nil, nil, fmt.Errorf("%w: invalid object links", ErrCorrupt)
	}
	body := make([]byte, header.BodyBytes)
	if _, err := io.ReadFull(reader, body); err != nil {
		return Ref{}, nil, nil, fmt.Errorf("%w: truncated object body", ErrCorrupt)
	}
	var trailing [1]byte
	if count, err := reader.Read(trailing[:]); count != 0 || !errors.Is(err, io.EOF) {
		return Ref{}, nil, nil, fmt.Errorf("%w: trailing object bytes", ErrCorrupt)
	}
	calculated, err := contentRef(header.Kind, links, body)
	if err != nil || calculated != ref {
		return Ref{}, nil, nil, fmt.Errorf("%w: object digest mismatch", ErrCorrupt)
	}
	return ref, links, body, nil
}

func (store *Store) ensureObjectParentLocked(ref Ref, existingOnly bool) error {
	kindDir := filepath.ToSlash(filepath.Join("objects", string(ref.Kind)))
	prefixDir := filepath.ToSlash(filepath.Join(kindDir, digestHex(ref)[:2]))
	for _, name := range []string{kindDir, prefixDir} {
		info, err := store.root.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
				return fmt.Errorf("%w: object directory is not a real directory", ErrCorrupt)
			}
			continue
		}
		if existingOnly || !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := store.root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create object directory: %w", err)
		}
	}
	return nil
}

func (store *Store) ensurePrivacyParentLocked(ref Ref, existingOnly bool) error {
	kindDir := filepath.ToSlash(filepath.Join("metadata/privacy", string(ref.Kind)))
	prefixDir := filepath.ToSlash(filepath.Join(kindDir, digestHex(ref)[:2]))
	for _, name := range []string{kindDir, prefixDir} {
		info, err := store.root.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
				return fmt.Errorf("%w: privacy directory is not a real directory", ErrCorrupt)
			}
			continue
		}
		if existingOnly || !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := store.root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create privacy directory: %w", err)
		}
	}
	return nil
}

func objectPath(ref Ref) string {
	hexDigest := digestHex(ref)
	return filepath.ToSlash(filepath.Join("objects", string(ref.Kind), hexDigest[:2], hexDigest[2:]+".obj"))
}

func privacyPath(ref Ref) string {
	hexDigest := digestHex(ref)
	return filepath.ToSlash(filepath.Join("metadata/privacy", string(ref.Kind), hexDigest[:2], hexDigest[2:]+".json"))
}

func digestHex(ref Ref) string {
	return strings.TrimPrefix(ref.SHA256, "sha256:")
}

func (store *Store) writePrivacyLocked(ref Ref, requested Privacy) error {
	if err := store.ensurePrivacyParentLocked(ref, false); err != nil {
		return err
	}
	path := privacyPath(ref)
	record := privacyRecord{SchemaVersion: PolicySchemaVersion, Privacy: requested}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		info, inspectErr := store.root.Lstat(path)
		if errors.Is(inspectErr, fs.ErrNotExist) {
			published, err := store.publishExclusiveLocked(path, encoded)
			if err != nil {
				return err
			}
			if published {
				return nil
			}
			// A different Store handle won the exclusive publication. Re-read
			// its classification so a private request can still tighten it.
			continue
		}
		if inspectErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return fmt.Errorf("%w: invalid privacy metadata", ErrCorrupt)
		}
		current, err := store.readPrivacyLocked(ref)
		if err != nil {
			return err
		}
		if current == PrivacyPrivate || current == requested {
			return nil
		}
		if current == PrivacyPortable && requested == PrivacyPrivate {
			return store.replaceAtomicLocked(path, encoded)
		}
	}
	return fmt.Errorf("%w: privacy metadata publication did not converge", ErrCorrupt)
}

func structuredKind(kind Kind) bool {
	switch kind {
	case KindToolPayload, KindSemanticDocument, KindMetadataEvent, KindRun,
		KindExecution, KindBranch, KindWorkspaceTree:
		return true
	default:
		return false
	}
}

func containsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			switch normalized {
			case "authorization", "password", "secret", "token", "api_key", "apikey",
				"credential", "credentials", "access_token", "refresh_token":
				return true
			}
			if containsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func (store *Store) readPrivacyLocked(ref Ref) (Privacy, error) {
	if err := store.ensurePrivacyParentLocked(ref, true); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PrivacyPrivate, nil
		}
		return "", err
	}
	path := privacyPath(ref)
	info, err := store.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Missing mutable metadata fails safely to the non-exportable class.
		return PrivacyPrivate, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 1024 {
		return "", fmt.Errorf("%w: invalid privacy metadata", ErrCorrupt)
	}
	file, err := store.root.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: open privacy metadata", ErrCorrupt)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil || len(data) > 1024 {
		return "", fmt.Errorf("%w: read privacy metadata", ErrCorrupt)
	}
	if _, _, err := canonicalJSON(data); err != nil {
		return "", fmt.Errorf("%w: non-canonical privacy metadata", ErrCorrupt)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record privacyRecord
	if err := decoder.Decode(&record); err != nil || decoder.Decode(&struct{}{}) != io.EOF || record.SchemaVersion != PolicySchemaVersion || !validPrivacy(record.Privacy) {
		return "", fmt.Errorf("%w: invalid privacy metadata", ErrCorrupt)
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return "", fmt.Errorf("%w: non-canonical privacy metadata", ErrCorrupt)
	}
	return record.Privacy, nil
}

func (store *Store) publishExclusiveLocked(destination string, data []byte) (bool, error) {
	directory := filepath.ToSlash(filepath.Dir(destination))
	stage, file, err := store.createStageLocked(directory)
	if err != nil {
		return false, err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = store.root.Remove(stage)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return false, fmt.Errorf("protect labstore stage: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return false, fmt.Errorf("write labstore stage: %w", err)
	}
	if err := file.Sync(); err != nil {
		return false, fmt.Errorf("sync labstore stage: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close labstore stage: %w", err)
	}
	if err := store.root.Link(stage, destination); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("publish labstore object: %w", err)
	}
	if err := store.syncDirectoryLocked(directory); err != nil {
		_ = store.root.Remove(destination)
		return false, fmt.Errorf("sync labstore publication: %w", err)
	}
	if err := store.root.Remove(stage); err != nil {
		return false, fmt.Errorf("remove labstore stage: %w", err)
	}
	cleanup = false
	if err := store.syncDirectoryLocked(directory); err != nil {
		return false, fmt.Errorf("sync labstore directory: %w", err)
	}
	return true, nil
}

func (store *Store) replaceAtomicLocked(destination string, data []byte) error {
	directory := filepath.ToSlash(filepath.Dir(destination))
	stage, file, err := store.createStageLocked(directory)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = store.root.Remove(stage)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := store.root.Rename(stage, destination); err != nil {
		return fmt.Errorf("replace labstore metadata: %w", err)
	}
	cleanup = false
	return store.syncDirectoryLocked(directory)
}

func (store *Store) createStageLocked(directory string) (string, *os.File, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var nonce [16]byte
		if _, err := cryptorand.Read(nonce[:]); err != nil {
			return "", nil, fmt.Errorf("create labstore stage name: %w", err)
		}
		stage := filepath.ToSlash(filepath.Join(directory, ".stage-"+hex.EncodeToString(nonce[:])))
		file, err := store.root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("stage labstore file: %w", err)
		}
		return stage, file, nil
	}
	return "", nil, fmt.Errorf("stage labstore file: %w", fs.ErrExist)
}

func (store *Store) syncDirectoryLocked(path string) error {
	directory, err := store.root.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func equalRefs(left, right []Ref) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
