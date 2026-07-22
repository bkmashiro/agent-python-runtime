package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
)

func decodeLengthPrefixedResponse(frame []byte, maxPayload uint32) ([]byte, error) {
	if len(frame) < 4 {
		return nil, errors.New("response frame is shorter than the length prefix")
	}
	length := binary.LittleEndian.Uint32(frame[:4])
	if length > maxPayload {
		return nil, fmt.Errorf("response payload length %d exceeds limit %d", length, maxPayload)
	}
	if uint64(length)+4 > uint64(len(frame)) {
		return nil, errors.New("response frame is truncated")
	}
	payload := make([]byte, length)
	copy(payload, frame[4:4+length])
	return payload, nil
}
