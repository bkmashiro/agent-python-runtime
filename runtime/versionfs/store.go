// Package versionfs provides Host-published immutable filesystem Views.
// Quotas apply to each current tree; retained older Views keep their own data alive.
package versionfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"
)

type Limits struct {
	MaxFiles     int
	MaxBytes     int64
	MaxFileBytes int64
	MaxDepth     int
}

func DefaultLimits() Limits {
	return Limits{MaxFiles: 4096, MaxBytes: 256 << 20, MaxFileBytes: 64 << 20, MaxDepth: 32}
}

type Identity struct {
	ResourceID string
	Version    uint64
}

type node struct {
	identity Identity
	dir      bool
	children map[string]*node
	data     []byte
}

type treeState struct {
	root       *node
	generation uint64
	files      int
	bytes      int64
}

type Store struct {
	mu      sync.Mutex
	limits  Limits
	storeID string
	state   *treeState
}

type View struct {
	storeID string
	state   *treeState
}

func New(limits Limits) (*Store, error) {
	if limits.MaxFiles <= 0 || limits.MaxBytes < 0 || limits.MaxFileBytes < 0 || limits.MaxFileBytes > limits.MaxBytes || limits.MaxDepth <= 0 {
		return nil, errors.New("versionfs: invalid limits")
	}
	id, err := randomID("store-")
	if err != nil {
		return nil, fmt.Errorf("versionfs: create store identity: %w", err)
	}
	rootID := id + "-root"
	return &Store{limits: limits, storeID: id, state: &treeState{root: &node{identity: Identity{ResourceID: rootID, Version: 1}, dir: true, children: make(map[string]*node)}}}, nil
}

func randomID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

func (s *Store) View() *View {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &View{storeID: s.storeID, state: s.state}
}

func (v *View) Revision() string {
	if v == nil || v.state == nil {
		return ""
	}
	return fmt.Sprintf("%s-%016x", v.storeID, v.state.generation)
}

func (s *Store) WriteFile(name string, data []byte) error {
	return s.mutate(name, func(parent *node, base string, old *node) (*node, int, int64, error) {
		if old != nil && old.dir {
			return nil, 0, 0, pathError("write", name, fs.ErrInvalid)
		}
		if old == nil && parent.children[base] != nil {
			return nil, 0, 0, pathError("write", name, fs.ErrExist)
		}
		if old == nil && s.state.files >= s.limits.MaxFiles {
			return nil, 0, 0, errors.New("versionfs: file/directory quota exceeded")
		}
		size := int64(len(data))
		if size > s.limits.MaxFileBytes {
			return nil, 0, 0, errors.New("versionfs: per-file byte quota exceeded")
		}
		oldSize := int64(0)
		if old != nil {
			oldSize = int64(len(old.data))
		}
		if size > oldSize && size-oldSize > s.limits.MaxBytes-s.state.bytes {
			return nil, 0, 0, errors.New("versionfs: tree byte quota exceeded")
		}
		identity := Identity{}
		if old != nil {
			if old.identity.Version == ^uint64(0) {
				return nil, 0, 0, errors.New("versionfs: resource version exhausted")
			}
			identity = Identity{ResourceID: old.identity.ResourceID, Version: old.identity.Version + 1}
		} else {
			id := fmt.Sprintf("%s-%016x", s.storeID, s.state.generation+1)
			identity = Identity{ResourceID: id, Version: 1}
		}
		owned := append([]byte(nil), data...)
		return &node{identity: identity, data: owned}, boolInt(old == nil), size - oldSize, nil
	})
}

func (s *Store) Mkdir(name string) error {
	return s.mutate(name, func(parent *node, base string, old *node) (*node, int, int64, error) {
		if old != nil {
			return nil, 0, 0, pathError("mkdir", name, fs.ErrExist)
		}
		if s.state.files >= s.limits.MaxFiles {
			return nil, 0, 0, errors.New("versionfs: file/directory quota exceeded")
		}
		id := fmt.Sprintf("%s-%016x", s.storeID, s.state.generation+1)
		return &node{identity: Identity{ResourceID: id, Version: 1}, dir: true, children: make(map[string]*node)}, 1, 0, nil
	})
}

func (s *Store) Remove(name string) error {
	return s.mutate(name, func(parent *node, base string, old *node) (*node, int, int64, error) {
		if old == nil {
			return nil, 0, 0, pathError("remove", name, fs.ErrNotExist)
		}
		if old.dir && len(old.children) != 0 {
			return nil, 0, 0, pathError("remove", name, fs.ErrInvalid)
		}
		bytes := int64(0)
		if !old.dir {
			bytes = int64(len(old.data))
		}
		return nil, -1, -bytes, nil
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) mutate(name string, change func(*node, string, *node) (*node, int, int64, error)) error {
	if s == nil {
		return errors.New("versionfs: nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	components, err := validPath(name, s.limits.MaxDepth)
	if err != nil {
		return pathError("mutate", name, err)
	}
	if len(components) == 0 {
		return pathError("mutate", name, fs.ErrInvalid)
	}
	parents := make([]*node, 0, len(components))
	current := s.state.root
	for _, component := range components[:len(components)-1] {
		if !current.dir {
			return pathError("mutate", name, syscall.ENOTDIR)
		}
		parents = append(parents, current)
		current = current.children[component]
		if current == nil {
			return pathError("mutate", name, fs.ErrNotExist)
		}
	}
	if !current.dir {
		return pathError("mutate", name, syscall.ENOTDIR)
	}
	base := components[len(components)-1]
	old := current.children[base]
	if s.state.generation == ^uint64(0) {
		return errors.New("versionfs: generation exhausted")
	}
	replacement, countDelta, byteDelta, err := change(current, base, old)
	if err != nil {
		return err
	}
	updated := cloneDir(current)
	if updated.identity.Version == ^uint64(0) {
		return errors.New("versionfs: directory version exhausted")
	}
	if replacement == nil {
		delete(updated.children, base)
	} else {
		updated.children[base] = replacement
	}
	for _, dir := range parents {
		if dir.identity.Version == ^uint64(0) {
			return errors.New("versionfs: directory version exhausted")
		}
	}
	// Directory observations include entry metadata, not only names. A changed
	// descendant can change DirEntry.Info, so version all copied ancestors.
	updated.identity.Version++
	for i := len(parents) - 1; i >= 0; i-- {
		copyDir := cloneDir(parents[i])
		copyDir.children[components[i]] = updated
		copyDir.identity.Version++
		updated = copyDir
	}
	s.state = &treeState{root: updated, generation: s.state.generation + 1, files: s.state.files + countDelta, bytes: s.state.bytes + byteDelta}
	return nil
}

func cloneDir(old *node) *node {
	children := make(map[string]*node, len(old.children)+1)
	for name, child := range old.children {
		children[name] = child
	}
	copyNode := *old
	copyNode.children = children
	return &copyNode
}

func validPath(name string, maxDepth int) ([]string, error) {
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	if name == "." {
		return nil, nil
	}
	parts := strings.Split(name, "/")
	if len(parts) > maxDepth {
		return nil, fs.ErrInvalid
	}
	return parts, nil
}

func pathError(op, name string, err error) error { return &fs.PathError{Op: op, Path: name, Err: err} }
