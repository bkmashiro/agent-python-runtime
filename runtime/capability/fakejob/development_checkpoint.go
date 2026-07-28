package fakejob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
)

const (
	developmentProviderComponent   = "job_provider"
	developmentControllerComponent = "cancel_controller"
)

func SaveDevelopmentCheckpoint(ctx context.Context, store *devsnapshot.Store, id string, controller *CancelController) (devsnapshot.Snapshot, error) {
	if store == nil || controller == nil || controller.adapter == nil || controller.adapter.config.Provider == nil {
		return devsnapshot.Snapshot{}, ErrJobDenied
	}
	provider := controller.adapter.config.Provider
	controller.mu.Lock()
	provider.mu.Lock()
	providerSnapshot, providerErr := provider.exportSnapshotLocked()
	controllerSnapshot, controllerErr := controller.snapshotLocked()
	provider.mu.Unlock()
	controller.mu.Unlock()
	if providerErr != nil || controllerErr != nil {
		return devsnapshot.Snapshot{}, errors.Join(providerErr, controllerErr)
	}
	providerJSON, err := json.Marshal(providerSnapshot)
	if err != nil {
		return devsnapshot.Snapshot{}, err
	}
	controllerJSON, err := json.Marshal(controllerSnapshot)
	if err != nil {
		return devsnapshot.Snapshot{}, err
	}
	return store.Put(ctx, id, map[string]json.RawMessage{developmentProviderComponent: providerJSON, developmentControllerComponent: controllerJSON})
}

func LoadDevelopmentCheckpoint(ctx context.Context, store *devsnapshot.Store, id string) (ProviderSnapshot, CancelControllerSnapshot, error) {
	if store == nil {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
	}
	saved, err := store.Get(ctx, id)
	if err != nil {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, err
	}
	if len(saved.Components) != 2 {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
	}
	providerBytes, providerExists := saved.Components[developmentProviderComponent]
	controllerBytes, controllerExists := saved.Components[developmentControllerComponent]
	if !providerExists || !controllerExists {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
	}
	var providerSnapshot ProviderSnapshot
	if err := decodeCheckpoint(providerBytes, &providerSnapshot); err != nil || validateProviderSnapshot(providerSnapshot, true) != nil {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
	}
	var controllerSnapshot CancelControllerSnapshot
	if err := decodeCheckpoint(controllerBytes, &controllerSnapshot); err != nil || controllerSnapshot.SchemaVersion != cancelControllerSnapshotVersion || !validIdentity(controllerSnapshot.TransactionID) || len(controllerSnapshot.Stages) > maxJobs {
		return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
	}
	for _, stage := range controllerSnapshot.Stages {
		if validateStageSnapshot(stage) != nil {
			return ProviderSnapshot{}, CancelControllerSnapshot{}, ErrJobDenied
		}
	}
	return providerSnapshot, controllerSnapshot, nil
}

func decodeCheckpoint(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrJobDenied
		}
		return err
	}
	return nil
}
