package wazero

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	wabin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

const (
	wasmPageSize      = 64 * 1024
	wasmDataSectionID = 11
)

type cowSnapshotSegment struct {
	offset uint32
	data   []byte
}

type cowSnapshotShellPlan struct {
	shell    []byte
	seedSize uint64
	segments []cowSnapshotSegment
}

func buildCOWSnapshotShell(full []byte) (*cowSnapshotShellPlan, error) {
	module, err := wabin.DecodeModule(full, wabinwasm.CoreFeaturesV2)
	if err != nil {
		return nil, fmt.Errorf("decode full COW module: %w", err)
	}
	if module.MemorySection == nil || module.MemorySection.Min == 0 ||
		!module.MemorySection.IsMaxEncoded || module.MemorySection.Min != module.MemorySection.Max {
		return nil, errors.New("COW snapshot shell requires one fixed local memory")
	}
	if module.StartSection != nil {
		return nil, errors.New("COW snapshot shell does not support a wasm start section")
	}
	if len(full) < 8 || !bytes.Equal(full[:8], []byte{'\x00', 'a', 's', 'm', '\x01', '\x00', '\x00', '\x00'}) {
		return nil, errors.New("COW snapshot shell has invalid wasm header")
	}
	plan := &cowSnapshotShellPlan{seedSize: uint64(module.MemorySection.Min) * wasmPageSize}
	plan.shell = append(plan.shell, full[:8]...)
	dataSections := 0
	for cursor := 8; cursor < len(full); {
		sectionStart := cursor
		sectionID := full[cursor]
		cursor++
		sectionSize, next, err := decodeWasmUint32(full, cursor)
		if err != nil {
			return nil, fmt.Errorf("decode wasm section %d size: %w", sectionID, err)
		}
		payloadStart := next
		payloadEnd := uint64(payloadStart) + uint64(sectionSize)
		if payloadEnd > uint64(len(full)) {
			return nil, fmt.Errorf("wasm section %d exceeds artifact", sectionID)
		}
		cursor = int(payloadEnd)
		if sectionID != wasmDataSectionID {
			plan.shell = append(plan.shell, full[sectionStart:cursor]...)
			continue
		}
		dataSections++
		if dataSections != 1 {
			return nil, errors.New("COW snapshot shell has multiple data sections")
		}
		payload, err := plan.transformDataSection(full[payloadStart:cursor])
		if err != nil {
			return nil, err
		}
		plan.shell = append(plan.shell, sectionID)
		plan.shell = appendWasmUint32(plan.shell, uint32(len(payload)))
		plan.shell = append(plan.shell, payload...)
	}
	if dataSections != 1 || len(plan.segments) == 0 {
		return nil, errors.New("COW snapshot shell requires non-empty active data segments")
	}
	return plan, nil
}

func (plan *cowSnapshotShellPlan) transformDataSection(payload []byte) ([]byte, error) {
	count, cursor, err := decodeWasmUint32(payload, 0)
	if err != nil {
		return nil, fmt.Errorf("decode data segment count: %w", err)
	}
	out := append([]byte(nil), payload[:cursor]...)
	for index := uint32(0); index < count; index++ {
		segmentStart := cursor
		prefix, next, err := decodeWasmUint32(payload, cursor)
		if err != nil {
			return nil, fmt.Errorf("decode data segment %d prefix: %w", index, err)
		}
		cursor = next
		active := prefix == 0 || prefix == 2
		switch prefix {
		case 0:
		case 1:
		case 2:
			memoryIndex, afterIndex, err := decodeWasmUint32(payload, cursor)
			if err != nil || memoryIndex != 0 {
				return nil, fmt.Errorf("active data segment %d has invalid memory index", index)
			}
			cursor = afterIndex
		default:
			return nil, fmt.Errorf("data segment %d has unsupported prefix %d", index, prefix)
		}
		var offset int32
		if active {
			if cursor >= len(payload) || payload[cursor] != byte(wabinwasm.OpcodeI32Const) {
				return nil, fmt.Errorf("active data segment %d has non-constant offset", index)
			}
			cursor++
			immediateStart := cursor
			for cursor < len(payload) && payload[cursor]&0x80 != 0 {
				cursor++
			}
			if cursor >= len(payload) {
				return nil, fmt.Errorf("active data segment %d has truncated offset", index)
			}
			cursor++
			var decodeErr error
			offset, _, decodeErr = leb128.DecodeInt32(bytes.NewReader(payload[immediateStart:cursor]))
			if decodeErr != nil || offset < 0 || cursor >= len(payload) || payload[cursor] != byte(wabinwasm.OpcodeEnd) {
				return nil, fmt.Errorf("active data segment %d has invalid offset", index)
			}
			cursor++
		}
		lengthStart := cursor
		dataLength, afterLength, err := decodeWasmUint32(payload, cursor)
		if err != nil {
			return nil, fmt.Errorf("decode data segment %d length: %w", index, err)
		}
		dataEnd := uint64(afterLength) + uint64(dataLength)
		if dataEnd > uint64(len(payload)) {
			return nil, fmt.Errorf("data segment %d exceeds data section", index)
		}
		cursor = int(dataEnd)
		if !active {
			out = append(out, payload[segmentStart:cursor]...)
			continue
		}
		end := uint64(offset) + uint64(dataLength)
		if end > plan.seedSize || end > math.MaxUint32+1 {
			return nil, fmt.Errorf("active data segment %d exceeds fixed memory", index)
		}
		if dataLength > 0 {
			data := append([]byte(nil), payload[afterLength:cursor]...)
			plan.segments = append(plan.segments, cowSnapshotSegment{offset: uint32(offset), data: data})
		}
		out = append(out, payload[segmentStart:lengthStart]...)
		out = append(out, 0)
	}
	if cursor != len(payload) {
		return nil, errors.New("data section has trailing bytes")
	}
	return out, nil
}

func decodeWasmUint32(data []byte, offset int) (uint32, int, error) {
	if offset < 0 || offset >= len(data) {
		return 0, offset, errors.New("truncated uint32")
	}
	value, bytesRead, err := leb128.DecodeUint32(bytes.NewReader(data[offset:]))
	if err != nil || bytesRead > uint64(len(data)-offset) {
		return 0, offset, errors.New("invalid uint32")
	}
	return value, offset + int(bytesRead), nil
}

func appendWasmUint32(dst []byte, value uint32) []byte {
	var scratch [binary.MaxVarintLen32]byte
	length := binary.PutUvarint(scratch[:], uint64(value))
	return append(dst, scratch[:length]...)
}

func (plan *cowSnapshotShellPlan) materializeSeed() ([]byte, error) {
	if plan == nil || plan.seedSize == 0 || plan.seedSize > uint64(int(^uint(0)>>1)) {
		return nil, errors.New("COW snapshot seed size is invalid")
	}
	seed := make([]byte, int(plan.seedSize))
	for _, segment := range plan.segments {
		copy(seed[segment.offset:], segment.data)
	}
	return seed, nil
}
