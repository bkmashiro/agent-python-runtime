package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	wazerosys "github.com/tetratelabs/wazero/sys"
)

const RootSchemaVersion = "pysolate.workspace-root.v1"

var (
	ErrWorkspaceImmutable = errors.New("workspace root is immutable")
	ErrWorkspaceConflict  = errors.New("workspace expected base conflict")
)

// Root is a portable immutable workspace lineage record. Ref is a Host-local
// materialization handle and is deliberately excluded from IdentityDocument.
type Root struct {
	SchemaVersion        string
	IdentitySHA256       string
	WorkspaceSHA256      string
	ParentIdentitySHA256 string
	Depth                uint32
	ChangedEntries       uint32
	ChangedBytes         uint64
	ref                  Ref
}

func (root Root) Ref() Ref { return root.ref }

// PortableIdentity returns the digest-only lineage identity and depth for a
// mutable base or sealed root.
func (manager *Manager) PortableIdentity(ref Ref) (string, uint32, error) {
	if manager == nil || !validRef(ref) {
		return "", 0, ErrInvalidWorkspace
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", 0, ErrWorkspaceClosed
	}
	item, ok := manager.entries[ref]
	if !ok {
		return "", 0, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return "", 0, ErrWorkspaceBusy
	}
	snapshot, err := snapshotWorkspaceRoot(item.root, item.limits)
	if err != nil {
		return "", 0, err
	}
	return rootParentIdentity(item, snapshot.Info.WorkspaceSHA256)
}

// BindImportedRoot attaches a verified portable lineage record to a capsule
// materialization, replacing any source-manager local Ref.
func (manager *Manager) BindImportedRoot(ref Ref, portable Root) (Root, error) {
	if manager == nil || !validRef(ref) || portable.SchemaVersion != RootSchemaVersion ||
		!validCapsuleDigest(portable.IdentitySHA256) || !validCapsuleDigest(portable.WorkspaceSHA256) ||
		(portable.Depth > 0 && !validCapsuleDigest(portable.ParentIdentitySHA256)) {
		return Root{}, ErrInvalidWorkspace
	}
	document, err := portable.IdentityDocument()
	if err != nil {
		return Root{}, err
	}
	digest := sha256.Sum256(document)
	if "sha256:"+hex.EncodeToString(digest[:]) != portable.IdentitySHA256 {
		return Root{}, ErrWorkspaceConflict
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return Root{}, ErrWorkspaceClosed
	}
	item, ok := manager.entries[ref]
	if !ok {
		return Root{}, ErrWorkspaceNotFound
	}
	if item.owner != "" || item.immutable || item.rootRecord != nil {
		return Root{}, ErrWorkspaceBusy
	}
	snapshot, err := snapshotWorkspaceRoot(item.root, item.limits)
	if err != nil {
		return Root{}, err
	}
	if snapshot.Info.WorkspaceSHA256 != portable.WorkspaceSHA256 {
		return Root{}, ErrWorkspaceConflict
	}
	portable.ref = ref
	stored := portable
	item.rootRecord = &stored
	item.immutable = true
	return portable, nil
}

// SelectRoot explicitly chooses one sealed child of the expected portable parent.
// It does not merge or mutate any root.
func (manager *Manager) SelectRoot(expectedParentWorkspaceSHA256 string, candidates []Root, selectedIdentitySHA256 string) (Root, error) {
	if manager == nil || !validCapsuleDigest(expectedParentWorkspaceSHA256) || !validCapsuleDigest(selectedIdentitySHA256) || len(candidates) == 0 {
		return Root{}, ErrInvalidWorkspace
	}
	parentIdentity, _, err := rootParentIdentity(&entry{}, expectedParentWorkspaceSHA256)
	if err != nil {
		return Root{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return Root{}, ErrWorkspaceClosed
	}
	seen := make(map[string]struct{}, len(candidates))
	var selected Root
	for _, candidate := range candidates {
		if candidate.SchemaVersion != RootSchemaVersion || candidate.ParentIdentitySHA256 != parentIdentity || !validCapsuleDigest(candidate.IdentitySHA256) || !validRef(candidate.ref) {
			return Root{}, ErrWorkspaceConflict
		}
		if _, duplicate := seen[candidate.IdentitySHA256]; duplicate {
			return Root{}, ErrInvalidWorkspace
		}
		seen[candidate.IdentitySHA256] = struct{}{}
		item, ok := manager.entries[candidate.ref]
		if !ok || !item.immutable || item.rootRecord == nil || item.rootRecord.IdentitySHA256 != candidate.IdentitySHA256 {
			return Root{}, ErrWorkspaceConflict
		}
		if candidate.IdentitySHA256 == selectedIdentitySHA256 {
			selected = candidate
		}
	}
	if selected.IdentitySHA256 == "" {
		return Root{}, ErrWorkspaceConflict
	}
	return selected, nil
}

func (root Root) IdentityDocument() ([]byte, error) {
	document := rootIdentityDocument{
		SchemaVersion: root.SchemaVersion, WorkspaceSHA256: root.WorkspaceSHA256,
		ParentIdentitySHA256: root.ParentIdentitySHA256, Depth: root.Depth,
		ChangedEntries: root.ChangedEntries, ChangedBytes: root.ChangedBytes,
	}
	return json.Marshal(document)
}

type rootIdentityDocument struct {
	SchemaVersion        string `json:"schema_version"`
	WorkspaceSHA256      string `json:"workspace_sha256"`
	ParentIdentitySHA256 string `json:"parent_identity_sha256,omitempty"`
	Depth                uint32 `json:"depth"`
	ChangedEntries       uint32 `json:"changed_entries"`
	ChangedBytes         uint64 `json:"changed_bytes"`
}

// Branch is a private mutable child whose terminal Seal creates an immutable
// portable root record. It carries no capability, Guest, or temporary state.
type Branch struct {
	manager        *Manager
	base           Ref
	ref            Ref
	expectedBase   string
	parentIdentity string
	parentDepth    uint32
	baseSnapshot   Snapshot
	terminal       bool
}

func (branch *Branch) Ref() Ref {
	if branch == nil {
		return ""
	}
	return branch.ref
}

// Discard destroys an unsealed private branch.
func (branch *Branch) Discard() error {
	if branch == nil || branch.manager == nil {
		return ErrInvalidWorkspace
	}
	branch.manager.mu.Lock()
	defer branch.manager.mu.Unlock()
	if branch.terminal {
		return ErrAttemptTerminal
	}
	item, ok := branch.manager.entries[branch.ref]
	if !ok {
		return ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return ErrWorkspaceBusy
	}
	if err := os.RemoveAll(item.root); err != nil {
		return fmt.Errorf("discard workspace branch: %w", err)
	}
	delete(branch.manager.entries, branch.ref)
	branch.terminal = true
	return nil
}

func (manager *Manager) ForkBranch(base Ref, expectedBaseSHA256 string) (*Branch, error) {
	if manager == nil || !validRef(base) || !validCapsuleDigest(expectedBaseSHA256) {
		return nil, ErrInvalidWorkspace
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrWorkspaceClosed
	}
	item, ok := manager.entries[base]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return nil, ErrWorkspaceBusy
	}
	baseSnapshot, err := snapshotWorkspaceRoot(item.root, item.limits)
	if err != nil {
		return nil, err
	}
	if baseSnapshot.Info.WorkspaceSHA256 != expectedBaseSHA256 {
		return nil, ErrWorkspaceConflict
	}
	parentIdentity, parentDepth, err := rootParentIdentity(item, baseSnapshot.Info.WorkspaceSHA256)
	if err != nil {
		return nil, err
	}
	source, err := os.OpenRoot(item.root)
	if err != nil {
		return nil, fmt.Errorf("open branch base: %w", err)
	}
	defer source.Close()
	rootInfo, err := os.Lstat(item.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidWorkspace
	}
	ref, destination, err := manager.allocateRootLocked()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(destination)
		}
	}()
	usage := &ingressUsage{}
	if err := copySourceDirectory(source, ".", destination, wazerosys.NewStat_t(rootInfo).Dev, item.limits, usage, nil); err != nil {
		return nil, err
	}
	if _, err := scanOrdinaryTree(destination, item.limits); err != nil {
		return nil, err
	}
	manager.entries[ref] = &entry{root: destination, limits: item.limits}
	failed = false
	return &Branch{
		manager: manager, base: base, ref: ref, expectedBase: expectedBaseSHA256,
		parentIdentity: parentIdentity, parentDepth: parentDepth, baseSnapshot: baseSnapshot,
	}, nil
}

func rootParentIdentity(item *entry, workspaceSHA256 string) (string, uint32, error) {
	if item.rootRecord != nil {
		return item.rootRecord.IdentitySHA256, item.rootRecord.Depth, nil
	}
	document, err := json.Marshal(rootIdentityDocument{
		SchemaVersion: RootSchemaVersion, WorkspaceSHA256: workspaceSHA256,
	})
	if err != nil {
		return "", 0, err
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:]), 0, nil
}

func (branch *Branch) Seal(expectedBaseSHA256 string) (Root, error) {
	if branch == nil || branch.manager == nil || !validCapsuleDigest(expectedBaseSHA256) {
		return Root{}, ErrInvalidWorkspace
	}
	branch.manager.mu.Lock()
	defer branch.manager.mu.Unlock()
	if branch.terminal {
		return Root{}, ErrAttemptTerminal
	}
	if expectedBaseSHA256 != branch.expectedBase {
		return Root{}, ErrWorkspaceConflict
	}
	base, ok := branch.manager.entries[branch.base]
	if !ok {
		return Root{}, ErrWorkspaceNotFound
	}
	child, ok := branch.manager.entries[branch.ref]
	if !ok {
		return Root{}, ErrWorkspaceNotFound
	}
	if base.owner != "" || child.owner != "" {
		return Root{}, ErrWorkspaceBusy
	}
	currentBase, err := snapshotWorkspaceRoot(base.root, base.limits)
	if err != nil {
		return Root{}, err
	}
	if currentBase.Info.WorkspaceSHA256 != expectedBaseSHA256 {
		return Root{}, ErrWorkspaceConflict
	}
	childSnapshot, err := snapshotWorkspaceRoot(child.root, child.limits)
	if err != nil {
		return Root{}, err
	}
	changedEntries, changedBytes := compareSnapshots(branch.baseSnapshot, childSnapshot)
	root := Root{
		SchemaVersion: RootSchemaVersion, WorkspaceSHA256: childSnapshot.Info.WorkspaceSHA256,
		ParentIdentitySHA256: branch.parentIdentity, Depth: branch.parentDepth + 1,
		ChangedEntries: changedEntries, ChangedBytes: changedBytes, ref: branch.ref,
	}
	document, err := root.IdentityDocument()
	if err != nil {
		return Root{}, err
	}
	digest := sha256.Sum256(document)
	root.IdentitySHA256 = "sha256:" + hex.EncodeToString(digest[:])
	child.immutable = true
	stored := root
	child.rootRecord = &stored
	branch.terminal = true
	return root, nil
}

func compareSnapshots(base, child Snapshot) (uint32, uint64) {
	baseEntries := make(map[string]SnapshotEntry, len(base.Entries))
	childEntries := make(map[string]SnapshotEntry, len(child.Entries))
	for _, entry := range base.Entries {
		baseEntries[entry.Path] = entry
	}
	for _, entry := range child.Entries {
		childEntries[entry.Path] = entry
	}
	var changed uint32
	var bytes uint64
	for name, entry := range childEntries {
		previous, exists := baseEntries[name]
		if !exists || previous.Kind != entry.Kind || previous.Executable != entry.Executable || previous.SHA256 != entry.SHA256 {
			changed++
			if entry.Kind == "file" {
				bytes += entry.Size
			}
		}
	}
	for name := range baseEntries {
		if _, exists := childEntries[name]; !exists {
			changed++
		}
	}
	return changed, bytes
}

func validCapsuleDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:7] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}
