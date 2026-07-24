package effect

import (
	"fmt"
	"sync"
)

type ConfigApplyReceipt struct {
	ApplyID       string
	ResourceID    string
	BeforeVersion uint64
	PostVersion   uint64
	BeforeDigest  string
	PostDigest    string
	UndoToken     string
}

type ConfigRollbackReceipt struct {
	RollbackID     string
	ApplyID        string
	ResourceID     string
	Version        uint64
	RestoredDigest string
}

type ConfigEvent struct {
	Kind         string
	EffectID     string
	ResourceID   string
	BeforeDigest string
	PostDigest   string
}

type configValue struct {
	Version uint64
	Value   string
}

type configUndo struct {
	Receipt ConfigApplyReceipt
	Before  string
}

type ConfigStore struct {
	mu           sync.Mutex
	values       map[string]configValue
	applies      map[string]ConfigApplyReceipt
	applyKeys    map[string]string
	undos        map[string]configUndo
	rollbacks    map[string]ConfigRollbackReceipt
	rollbackKeys map[string]string
	history      []ConfigEvent
}

func NewConfigStore(initial map[string]string) *ConfigStore {
	store := &ConfigStore{values: map[string]configValue{}, applies: map[string]ConfigApplyReceipt{}, applyKeys: map[string]string{}, undos: map[string]configUndo{}, rollbacks: map[string]ConfigRollbackReceipt{}, rollbackKeys: map[string]string{}}
	for id, value := range initial {
		store.values[id] = configValue{Version: 1, Value: value}
	}
	return store
}

func (store *ConfigStore) Apply(applyID, resourceID string, expectedVersion uint64, value string) (ConfigApplyReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := Digest(resourceID + "\x00" + fmt.Sprint(expectedVersion) + "\x00" + value)
	if prior, ok := store.applies[applyID]; ok {
		if store.applyKeys[applyID] != key {
			return ConfigApplyReceipt{}, ErrConflict
		}
		return prior, nil
	}
	current, ok := store.values[resourceID]
	if !ok {
		return ConfigApplyReceipt{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return ConfigApplyReceipt{}, ErrVersionConflict
	}
	receipt := ConfigApplyReceipt{ApplyID: applyID, ResourceID: resourceID, BeforeVersion: current.Version, PostVersion: current.Version + 1, BeforeDigest: Digest(current.Value), PostDigest: Digest(value), UndoToken: "undo_" + Digest(applyID + "\x00" + resourceID)[7:]}
	store.values[resourceID] = configValue{Version: receipt.PostVersion, Value: value}
	store.applies[applyID], store.applyKeys[applyID] = receipt, key
	store.undos[receipt.UndoToken] = configUndo{Receipt: receipt, Before: current.Value}
	store.history = append(store.history, ConfigEvent{Kind: "apply", EffectID: applyID, ResourceID: resourceID, BeforeDigest: receipt.BeforeDigest, PostDigest: receipt.PostDigest})
	return receipt, nil
}

func (store *ConfigStore) Rollback(rollbackID, undoToken string) (ConfigRollbackReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if prior, ok := store.rollbacks[rollbackID]; ok {
		if store.rollbackKeys[rollbackID] != undoToken {
			return ConfigRollbackReceipt{}, ErrConflict
		}
		return prior, nil
	}
	undo, ok := store.undos[undoToken]
	if !ok {
		return ConfigRollbackReceipt{}, ErrNotFound
	}
	current := store.values[undo.Receipt.ResourceID]
	if current.Version != undo.Receipt.PostVersion || Digest(current.Value) != undo.Receipt.PostDigest {
		return ConfigRollbackReceipt{}, ErrVersionConflict
	}
	current.Version++
	current.Value = undo.Before
	store.values[undo.Receipt.ResourceID] = current
	receipt := ConfigRollbackReceipt{RollbackID: rollbackID, ApplyID: undo.Receipt.ApplyID, ResourceID: undo.Receipt.ResourceID, Version: current.Version, RestoredDigest: Digest(current.Value)}
	store.rollbacks[rollbackID], store.rollbackKeys[rollbackID] = receipt, undoToken
	store.history = append(store.history, ConfigEvent{Kind: "rollback", EffectID: rollbackID, ResourceID: receipt.ResourceID, BeforeDigest: undo.Receipt.PostDigest, PostDigest: receipt.RestoredDigest})
	return receipt, nil
}

func (store *ConfigStore) History() []ConfigEvent {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ConfigEvent(nil), store.history...)
}

func (store *ConfigStore) Value(resourceID string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[resourceID].Value
}
