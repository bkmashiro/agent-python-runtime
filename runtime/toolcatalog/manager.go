package toolcatalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

var (
	ErrDiscoveryFailed = errors.New("tool catalog discovery failed")
	ErrInvalidManager  = errors.New("invalid tool catalog manager")
)

type Discovery interface {
	Discover(context.Context) ([]DiscoveredTool, error)
}

type CatalogPolicy struct {
	Allowlist map[string]bool
	Grants    map[string]Grant
}

type CatalogDiff struct {
	PreviousDigest   string
	CurrentDigest    string
	PreviousRevision uint64
	CurrentRevision  uint64
	Added            []string
	Removed          []string
	Changed          []string
	Pinned           bool
}

type Manager struct {
	mu         sync.Mutex
	source     Discovery
	policy     CatalogPolicy
	current    Snapshot
	revision   uint64
	pinCurrent bool
}

func NewManager(source Discovery, policy CatalogPolicy) (*Manager, error) {
	if source == nil {
		return nil, ErrInvalidManager
	}
	return &Manager{source: source, policy: clonePolicy(policy)}, nil
}

func (manager *Manager) SetPolicy(policy CatalogPolicy) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.policy = clonePolicy(policy)
}

func (manager *Manager) PinCurrent(enabled bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pinCurrent = enabled
}

func (manager *Manager) Refresh(ctx context.Context) (Snapshot, CatalogDiff, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	discovered, err := manager.source.Discover(ctx)
	if err != nil {
		if manager.pinCurrent && manager.current.digest != "" && snapshotAllowed(manager.current, manager.policy) {
			return manager.current, CatalogDiff{PreviousDigest: manager.current.digest, CurrentDigest: manager.current.digest, PreviousRevision: manager.current.revision, CurrentRevision: manager.current.revision, Pinned: true}, nil
		}
		return Snapshot{}, CatalogDiff{}, fmt.Errorf("%w: %v", ErrDiscoveryFailed, err)
	}
	selected := make([]DiscoveredTool, 0, len(discovered))
	grants := make(map[string]Grant)
	for _, tool := range discovered {
		if !manager.policy.Allowlist[tool.ToolID] {
			continue
		}
		grant, exists := manager.policy.Grants[tool.ToolID]
		if !exists {
			continue
		}
		selected = append(selected, tool)
		grants[tool.ToolID] = grant
	}
	next, err := BuildSnapshot(selected, grants, BuildOptions{Revision: manager.revision + 1})
	if err != nil {
		return Snapshot{}, CatalogDiff{}, fmt.Errorf("%w: %v", ErrDiscoveryFailed, err)
	}
	diff := diffSnapshots(manager.current, next)
	manager.current = next
	manager.revision = next.revision
	return next, diff, nil
}

func clonePolicy(policy CatalogPolicy) CatalogPolicy {
	result := CatalogPolicy{Allowlist: make(map[string]bool, len(policy.Allowlist)), Grants: make(map[string]Grant, len(policy.Grants))}
	for id, allowed := range policy.Allowlist {
		result.Allowlist[id] = allowed
	}
	for id, grant := range policy.Grants {
		result.Grants[id] = grant
	}
	return result
}

func snapshotAllowed(snapshot Snapshot, policy CatalogPolicy) bool {
	for _, tool := range snapshot.tools {
		grant, exists := policy.Grants[tool.ToolID]
		if !policy.Allowlist[tool.ToolID] || !exists || grant.ToolID != tool.ToolID || grant.EffectClass != tool.EffectClass || grant.Policy != tool.Policy || grant.GrantVersion != tool.GrantVersion || grant.MaxCalls != tool.MaxCalls {
			return false
		}
	}
	return true
}

func diffSnapshots(previous, current Snapshot) CatalogDiff {
	diff := CatalogDiff{PreviousDigest: previous.digest, CurrentDigest: current.digest, PreviousRevision: previous.revision, CurrentRevision: current.revision}
	before := make(map[string]Tool, len(previous.tools))
	after := make(map[string]Tool, len(current.tools))
	for _, tool := range previous.tools {
		before[tool.ToolID] = tool
	}
	for _, tool := range current.tools {
		after[tool.ToolID] = tool
	}
	for id, tool := range after {
		prior, exists := before[id]
		if !exists {
			diff.Added = append(diff.Added, id)
		} else if !reflect.DeepEqual(prior, tool) {
			diff.Changed = append(diff.Changed, id)
		}
	}
	for id := range before {
		if _, exists := after[id]; !exists {
			diff.Removed = append(diff.Removed, id)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Changed)
	return diff
}
