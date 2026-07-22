package engine

import (
	"encoding/binary"
	"testing"
)

func framed(payload []byte) []byte {
	buffer := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(buffer[:4], uint32(len(payload)))
	copy(buffer[4:], payload)
	return buffer
}

func TestDecodeLengthPrefixedResponse(t *testing.T) {
	payload, err := decodeLengthPrefixedResponse(framed([]byte(`{"status":"ok"}`)), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"status":"ok"}` {
		t.Fatalf("unexpected payload: %s", payload)
	}
}

func TestDecodeLengthPrefixedResponseRejectsInvalidFrames(t *testing.T) {
	for name, frame := range map[string][]byte{
		"short header": {1, 2, 3},
		"truncated":    {5, 0, 0, 0, 1},
		"oversized":    framed(make([]byte, 5)),
	} {
		t.Run(name, func(t *testing.T) {
			max := uint32(4)
			if name != "oversized" {
				max = 1024
			}
			if _, err := decodeLengthPrefixedResponse(frame, max); err == nil {
				t.Fatal("expected frame rejection")
			}
		})
	}
}
