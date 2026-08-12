package labview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxDocumentBytes = 16 << 20

func Encode(kind Kind, value any) ([]byte, string, error) {
	if err := validateDocument(kind, value); err != nil {
		return nil, "", ErrInvalid
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > MaxDocumentBytes {
		return nil, "", ErrInvalid
	}
	h := sha256.Sum256(raw)
	return raw, fmt.Sprintf("sha256:%x", h[:]), nil
}

func Decode(kind Kind, raw []byte) (any, string, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes {
		return nil, "", ErrInvalid
	}
	var value any
	switch kind {
	case KindIndex:
		value = &Index{}
	case KindStudySummary:
		value = &StudySummary{}
	case KindRunDetail:
		value = &RunDetail{}
	case KindTimelinePage:
		value = &TimelinePage{}
	case KindBranchDAG:
		value = &BranchDAG{}
	case KindWorkspaceDiff:
		value = &WorkspaceDiff{}
	case KindRunComparison:
		value = &RunComparison{}
	case KindObjectRef:
		value = &ObjectRef{}
	case KindProblem:
		value = &Problem{}
	default:
		return nil, "", ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return nil, "", ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, "", ErrInvalid
	}
	encoded, identity, err := Encode(kind, value)
	if err != nil || !bytes.Equal(encoded, raw) {
		return nil, "", ErrInvalid
	}
	return value, identity, nil
}

func validateDocument(kind Kind, value any) error {
	switch kind {
	case KindIndex:
		v, ok := deref[Index](value)
		if !ok {
			return ErrInvalid
		}
		return validateIndex(v)
	case KindStudySummary:
		v, ok := deref[StudySummary](value)
		if !ok {
			return ErrInvalid
		}
		return validateStudy(v)
	case KindRunDetail:
		v, ok := deref[RunDetail](value)
		if !ok {
			return ErrInvalid
		}
		return validateRun(v)
	case KindTimelinePage:
		v, ok := deref[TimelinePage](value)
		if !ok {
			return ErrInvalid
		}
		return validateTimeline(v)
	case KindBranchDAG:
		v, ok := deref[BranchDAG](value)
		if !ok {
			return ErrInvalid
		}
		return validateDAG(v)
	case KindWorkspaceDiff:
		v, ok := deref[WorkspaceDiff](value)
		if !ok {
			return ErrInvalid
		}
		return validateWorkspace(v)
	case KindRunComparison:
		v, ok := deref[RunComparison](value)
		if !ok {
			return ErrInvalid
		}
		return validateComparison(v)
	case KindObjectRef:
		v, ok := deref[ObjectRef](value)
		if !ok {
			return ErrInvalid
		}
		return validateObjectRef(v)
	case KindProblem:
		v, ok := deref[Problem](value)
		if !ok {
			return ErrInvalid
		}
		return validateProblem(v)
	default:
		return ErrInvalid
	}
}

func deref[T any](value any) (T, bool) {
	if v, ok := value.(T); ok {
		return v, true
	}
	if v, ok := value.(*T); ok && v != nil {
		return *v, true
	}
	var zero T
	return zero, false
}
