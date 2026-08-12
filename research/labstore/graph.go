package labstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	WorkspaceTreeSchemaVersion = "pysolate.labstore-workspace-tree.v1"
	BranchSchemaVersion        = "pysolate.labstore-branch-relation.v1"
)

type WorkspaceEntry struct {
	Path       string `json:"path"`
	Executable bool   `json:"executable"`
	Size       uint64 `json:"size"`
	Content    Ref    `json:"content"`
}

type WorkspaceTree struct {
	SchemaVersion string           `json:"schema_version"`
	EntryCount    uint32           `json:"entry_count"`
	TotalBytes    uint64           `json:"total_bytes"`
	Entries       []WorkspaceEntry `json:"entries"`
}

func (store *Store) PutWorkspaceTree(entries []WorkspaceEntry, options PutOptions) (Ref, bool, error) {
	if store == nil {
		return Ref{}, false, ErrClosed
	}
	if len(options.Links) != 0 || uint64(len(entries)) > uint64(store.options.MaxTreeEntries) {
		return Ref{}, false, fmt.Errorf("%w: invalid workspace tree options or entry count", ErrInvalid)
	}
	canonical := append([]WorkspaceEntry(nil), entries...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	linksByRef := make(map[Ref]struct{}, len(canonical))
	var total uint64
	for index := range canonical {
		entry := &canonical[index]
		if err := validateWorkspacePath(entry.Path); err != nil || (index > 0 && canonical[index-1].Path == entry.Path) || entry.Content.Kind != KindFile {
			return Ref{}, false, fmt.Errorf("%w: invalid workspace tree entry", ErrInvalid)
		}
		content, err := store.Get(entry.Content)
		if err != nil {
			return Ref{}, false, fmt.Errorf("%w: workspace content is unavailable", ErrInvalid)
		}
		size := uint64(len(content.Body))
		if entry.Size != 0 && entry.Size != size {
			return Ref{}, false, fmt.Errorf("%w: workspace entry size mismatch", ErrInvalid)
		}
		entry.Size = size
		if total > ^uint64(0)-size {
			return Ref{}, false, fmt.Errorf("%w: workspace byte count overflow", ErrInvalid)
		}
		total += size
		linksByRef[entry.Content] = struct{}{}
	}
	links := make([]Ref, 0, len(linksByRef))
	for link := range linksByRef {
		links = append(links, link)
	}
	links, err := normalizeLinks(links, store.options.MaxLinks)
	if err != nil {
		return Ref{}, false, err
	}
	tree := WorkspaceTree{
		SchemaVersion: WorkspaceTreeSchemaVersion,
		EntryCount:    uint32(len(canonical)),
		TotalBytes:    total,
		Entries:       canonical,
	}
	body, err := json.Marshal(tree)
	if err != nil {
		return Ref{}, false, fmt.Errorf("encode workspace tree: %w", err)
	}
	options.Links = links
	return store.PutJSON(KindWorkspaceTree, body, options)
}

func (store *Store) GetWorkspaceTree(ref Ref) (WorkspaceTree, error) {
	if ref.Kind != KindWorkspaceTree {
		return WorkspaceTree{}, fmt.Errorf("%w: reference is not a workspace tree", ErrInvalid)
	}
	object, err := store.Get(ref)
	if err != nil {
		return WorkspaceTree{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(object.Body))
	decoder.DisallowUnknownFields()
	var tree WorkspaceTree
	if err := decoder.Decode(&tree); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return WorkspaceTree{}, fmt.Errorf("%w: invalid workspace tree body", ErrCorrupt)
	}
	encodedTree, err := json.Marshal(tree)
	if err != nil {
		return WorkspaceTree{}, fmt.Errorf("%w: invalid workspace tree body", ErrCorrupt)
	}
	_, canonicalBody, err := canonicalJSON(encodedTree)
	if err != nil || !bytes.Equal(canonicalBody, object.Body) || tree.SchemaVersion != WorkspaceTreeSchemaVersion || tree.EntryCount != uint32(len(tree.Entries)) || uint64(len(tree.Entries)) > uint64(store.options.MaxTreeEntries) {
		return WorkspaceTree{}, fmt.Errorf("%w: invalid workspace tree body", ErrCorrupt)
	}
	linksByRef := make(map[Ref]struct{}, len(tree.Entries))
	var total uint64
	var previous string
	for _, entry := range tree.Entries {
		if err := validateWorkspacePath(entry.Path); err != nil || (previous != "" && entry.Path <= previous) || entry.Content.Kind != KindFile {
			return WorkspaceTree{}, fmt.Errorf("%w: invalid workspace tree entry", ErrCorrupt)
		}
		previous = entry.Path
		content, err := store.Get(entry.Content)
		if err != nil || uint64(len(content.Body)) != entry.Size || total > ^uint64(0)-entry.Size {
			return WorkspaceTree{}, fmt.Errorf("%w: invalid workspace tree content", ErrCorrupt)
		}
		total += entry.Size
		linksByRef[entry.Content] = struct{}{}
	}
	if total != tree.TotalBytes {
		return WorkspaceTree{}, fmt.Errorf("%w: workspace tree byte count mismatch", ErrCorrupt)
	}
	links := make([]Ref, 0, len(linksByRef))
	for link := range linksByRef {
		links = append(links, link)
	}
	links, err = normalizeLinks(links, store.options.MaxLinks)
	if err != nil || !equalRefs(links, object.Links) {
		return WorkspaceTree{}, fmt.Errorf("%w: workspace tree links mismatch", ErrCorrupt)
	}
	tree.Entries = append([]WorkspaceEntry(nil), tree.Entries...)
	return tree, nil
}

func validateWorkspacePath(value string) error {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." {
		return fmt.Errorf("%w: workspace path is not canonical", ErrInvalid)
	}
	segments := strings.Split(value, "/")
	if len(segments) > 64 {
		return fmt.Errorf("%w: workspace path exceeds depth", ErrInvalid)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: workspace path contains traversal", ErrInvalid)
		}
	}
	return nil
}

type Branch struct {
	ParentRun        Ref    `json:"parent_run"`
	ChildExecution   Ref    `json:"child_execution"`
	ForkOperation    uint32 `json:"fork_operation"`
	Prefix           Ref    `json:"prefix"`
	InitialWorkspace Ref    `json:"initial_workspace"`
	Manifest         Ref    `json:"manifest"`
}

type branchDocument struct {
	SchemaVersion string `json:"schema_version"`
	Branch
}

func (store *Store) PutBranch(branch Branch, options PutOptions) (Ref, bool, error) {
	if len(options.Links) != 0 {
		return Ref{}, false, fmt.Errorf("%w: branch links are derived", ErrInvalid)
	}
	if branch.ParentRun.Kind != KindRun || branch.ChildExecution.Kind != KindExecution || branch.Prefix.Kind != KindToolPayload || branch.InitialWorkspace.Kind != KindWorkspaceTree || branch.Manifest.Kind != KindSemanticDocument {
		return Ref{}, false, fmt.Errorf("%w: branch reference kinds do not match the contract", ErrInvalid)
	}
	document := branchDocument{SchemaVersion: BranchSchemaVersion, Branch: branch}
	body, err := json.Marshal(document)
	if err != nil {
		return Ref{}, false, fmt.Errorf("encode branch relation: %w", err)
	}
	options.Links = []Ref{branch.ParentRun, branch.ChildExecution, branch.Prefix, branch.InitialWorkspace, branch.Manifest}
	return store.PutJSON(KindBranch, body, options)
}

func (store *Store) GetBranch(ref Ref) (Branch, error) {
	if ref.Kind != KindBranch {
		return Branch{}, fmt.Errorf("%w: reference is not a branch relation", ErrInvalid)
	}
	object, err := store.Get(ref)
	if err != nil {
		return Branch{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(object.Body))
	decoder.DisallowUnknownFields()
	var document branchDocument
	if err := decoder.Decode(&document); err != nil || decoder.Decode(&struct{}{}) != io.EOF || document.SchemaVersion != BranchSchemaVersion {
		return Branch{}, fmt.Errorf("%w: invalid branch relation", ErrCorrupt)
	}
	encodedDocument, err := json.Marshal(document)
	if err != nil {
		return Branch{}, fmt.Errorf("%w: non-canonical branch relation", ErrCorrupt)
	}
	_, canonical, err := canonicalJSON(encodedDocument)
	if err != nil || !bytes.Equal(canonical, object.Body) {
		return Branch{}, fmt.Errorf("%w: non-canonical branch relation", ErrCorrupt)
	}
	branch := document.Branch
	if branch.ParentRun.Kind != KindRun || branch.ChildExecution.Kind != KindExecution || branch.Prefix.Kind != KindToolPayload || branch.InitialWorkspace.Kind != KindWorkspaceTree || branch.Manifest.Kind != KindSemanticDocument {
		return Branch{}, fmt.Errorf("%w: invalid branch reference kinds", ErrCorrupt)
	}
	links, err := normalizeLinks([]Ref{branch.ParentRun, branch.ChildExecution, branch.Prefix, branch.InitialWorkspace, branch.Manifest}, store.options.MaxLinks)
	if err != nil || !equalRefs(links, object.Links) {
		return Branch{}, fmt.Errorf("%w: branch links mismatch", ErrCorrupt)
	}
	for _, link := range links {
		if _, err := store.Get(link); err != nil {
			return Branch{}, fmt.Errorf("%w: branch target is unavailable", ErrCorrupt)
		}
	}
	return branch, nil
}
