package labstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxJSONNodes = 100_000

func canonicalJSON(raw []byte) (any, []byte, error) {
	if !utf8.Valid(raw) {
		return nil, nil, fmt.Errorf("%w: JSON is not valid UTF-8", ErrInvalid)
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid JSON", ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: JSON contains trailing data", ErrInvalid)
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encode canonical JSON", ErrInvalid)
	}
	return document, canonical, nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: JSON contains trailing data", ErrInvalid)
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxJSONNodes {
		return fmt.Errorf("%w: JSON is too complex", ErrInvalid)
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrInvalid)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			key, ok := keyToken.(string)
			if keyErr != nil || !ok {
				return fmt.Errorf("%w: invalid JSON object", ErrInvalid)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%w: JSON contains duplicate keys", ErrInvalid)
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim('}') {
			return fmt.Errorf("%w: invalid JSON object", ErrInvalid)
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim(']') {
			return fmt.Errorf("%w: invalid JSON array", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: invalid JSON", ErrInvalid)
	}
	return nil
}
