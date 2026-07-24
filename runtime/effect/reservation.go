package effect

import (
	"fmt"
	"sync"
)

type ReservationReceipt struct {
	ReceiptID     string
	Kind          string
	ReservationID string
	RelatedID     string
	EffectDigest  string
	Status        string
	ErrorCode     string
}

type reservation struct {
	ID     string
	Active bool
}

type ReservationStore struct {
	mu               sync.Mutex
	reservations     map[string]reservation
	reserveByCall    map[string]ReservationReceipt
	reserveKeys      map[string]string
	compensations    map[string]ReservationReceipt
	compensationKeys map[string]string
	history          []ReservationReceipt
}

func NewReservationStore() *ReservationStore {
	return &ReservationStore{reservations: map[string]reservation{}, reserveByCall: map[string]ReservationReceipt{}, reserveKeys: map[string]string{}, compensations: map[string]ReservationReceipt{}, compensationKeys: map[string]string{}}
}

func (store *ReservationStore) Reserve(callID, sku string, quantity uint32) (ReservationReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if quantity == 0 {
		return ReservationReceipt{}, ErrConflict
	}
	key := Digest(sku + "\x00" + fmt.Sprint(quantity))
	if prior, ok := store.reserveByCall[callID]; ok {
		if store.reserveKeys[callID] != key {
			return ReservationReceipt{}, ErrConflict
		}
		return prior, nil
	}
	reservationID := "reservation_" + Digest(callID)[7:]
	receipt := ReservationReceipt{ReceiptID: "receipt_" + Digest("reserve\x00" + callID)[7:], Kind: "reservation", ReservationID: reservationID, EffectDigest: key, Status: "succeeded"}
	store.reservations[reservationID] = reservation{ID: reservationID, Active: true}
	store.reserveByCall[callID], store.reserveKeys[callID] = receipt, key
	store.history = append(store.history, receipt)
	return receipt, nil
}

func (store *ReservationStore) Compensate(compensationID, reservationID string) (ReservationReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if prior, ok := store.compensations[compensationID]; ok {
		if store.compensationKeys[compensationID] != reservationID {
			return ReservationReceipt{}, ErrConflict
		}
		return prior, nil
	}
	value, ok := store.reservations[reservationID]
	if !ok {
		return ReservationReceipt{}, ErrNotFound
	}
	if !value.Active {
		return ReservationReceipt{}, ErrConflict
	}
	value.Active = false
	store.reservations[reservationID] = value
	receipt := ReservationReceipt{ReceiptID: "receipt_" + Digest("compensate\x00" + compensationID)[7:], Kind: "compensation", ReservationID: reservationID, RelatedID: compensationID, EffectDigest: Digest(reservationID + "\x00released"), Status: "succeeded"}
	store.compensations[compensationID], store.compensationKeys[compensationID] = receipt, reservationID
	store.history = append(store.history, receipt)
	return receipt, nil
}

func (store *ReservationStore) RecordCompensationFailure(compensationID, reservationID, errorCode string) (ReservationReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !boundedCode(errorCode) {
		return ReservationReceipt{}, ErrConflict
	}
	if prior, ok := store.compensations[compensationID]; ok {
		if store.compensationKeys[compensationID] != reservationID {
			return ReservationReceipt{}, ErrConflict
		}
		return prior, nil
	}
	value, ok := store.reservations[reservationID]
	if !ok || !value.Active {
		return ReservationReceipt{}, ErrNotFound
	}
	receipt := ReservationReceipt{ReceiptID: "receipt_" + Digest("compensate-failed\x00" + compensationID)[7:], Kind: "compensation", ReservationID: reservationID, RelatedID: compensationID, EffectDigest: Digest(reservationID + "\x00compensation-failed"), Status: "failed", ErrorCode: errorCode}
	store.compensations[compensationID], store.compensationKeys[compensationID] = receipt, reservationID
	store.history = append(store.history, receipt)
	return receipt, nil
}

func boundedCode(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func (store *ReservationStore) Active(reservationID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reservations[reservationID].Active
}
func (store *ReservationStore) History() []ReservationReceipt {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ReservationReceipt(nil), store.history...)
}
