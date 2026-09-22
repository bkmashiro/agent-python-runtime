package pysolate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	wasmDataSection   = 11
	wasmMemorySection = 5
	wasmImportSection = 2
	wasmStartSection  = 8
	wasmPageSizeBytes = uint64(65536)
)

type cowDataSegment struct {
	index  int
	offset uint64
	data   []byte
}

type cowDataImage struct {
	shell      []byte
	memorySize uint64
	active     []cowDataSegment
}

// transformCOWDataImage makes a replacement-only shell. It deliberately parses
// and rewrites only the Data section; all other section bytes are copied as-is.
func transformCOWDataImage(wasm []byte) (cowDataImage, error) {
	var image cowDataImage
	if len(wasm) < 8 || string(wasm[:4]) != "\x00asm" || binary.LittleEndian.Uint32(wasm[4:8]) != 1 {
		return image, errors.New("COW data image requires a wasm MVP module")
	}
	var memoryCount, importedMemoryCount uint32
	var memoryMin, memoryMax uint32
	var haveMemory bool
	var dataSeen, activeSeen bool
	var code []byte
	var dataModes []bool
	out := make([]byte, 0, len(wasm))
	out = append(out, wasm[:8]...)
	for pos := 8; pos < len(wasm); {
		sectionStart := pos
		id := wasm[pos]
		pos++
		lengthStart := pos
		length, next, err := readULEB(wasm, pos, 32)
		if err != nil {
			return image, fmt.Errorf("section length: %w", err)
		}
		pos = next
		payloadStart := pos
		payloadEnd := payloadStart + int(length)
		if payloadEnd < payloadStart || payloadEnd > len(wasm) {
			return image, errors.New("truncated wasm section")
		}
		payload := wasm[payloadStart:payloadEnd]
		switch id {
		case wasmImportSection:
			count, cursor, err := readULEB(payload, 0, 32)
			if err != nil {
				return image, fmt.Errorf("imports: %w", err)
			}
			for i := uint64(0); i < count; i++ {
				var e error
				cursor, e = skipName(payload, cursor)
				if e != nil {
					return image, fmt.Errorf("import module: %w", e)
				}
				cursor, e = skipName(payload, cursor)
				if e != nil {
					return image, fmt.Errorf("import name: %w", e)
				}
				if cursor >= len(payload) {
					return image, errors.New("truncated import kind")
				}
				kind := payload[cursor]
				cursor++
				switch kind {
				case 0: // function type index
					_, cursor, e = readULEB(payload, cursor, 32)
				case 1: // table type
					if cursor >= len(payload) {
						e = errors.New("truncated table type")
					} else {
						cursor++
						cursor, e = skipLimits(payload, cursor)
					}
				case 2: // memory type
					importedMemoryCount++
					cursor, e = skipLimits(payload, cursor)
				case 3: // global type
					if cursor+2 > len(payload) {
						e = errors.New("truncated global type")
					} else {
						cursor += 2
					}
				default:
					e = fmt.Errorf("unsupported import kind %d", kind)
				}
				if e != nil {
					return image, fmt.Errorf("import %d: %w", i, e)
				}
			}
		case wasmMemorySection:
			count, cursor, err := readULEB(payload, 0, 32)
			if err != nil {
				return image, fmt.Errorf("memories: %w", err)
			}
			memoryCount = uint32(count)
			if count == 1 {
				flags, c, e := readULEB(payload, cursor, 32)
				if e != nil {
					return image, fmt.Errorf("memory limits: %w", e)
				}
				min, c2, e := readULEB(payload, c, 32)
				if e != nil {
					return image, fmt.Errorf("memory minimum: %w", e)
				}
				max := min
				if flags != 1 {
					return image, fmt.Errorf("unsupported memory limits flags 0x%x", flags)
				}
				max, _, e = readULEB(payload, c2, 32)
				if e != nil {
					return image, fmt.Errorf("memory maximum: %w", e)
				}
				memoryMin, memoryMax, haveMemory = uint32(min), uint32(max), true
			}
		case 10:
			if code != nil {
				return image, errors.New("multiple Code sections")
			}
			code = payload
		case wasmStartSection:
			return image, errors.New("COW data image does not support a start function")
		case wasmDataSection:
			if dataSeen {
				return image, errors.New("multiple Data sections")
			}
			dataSeen = true
			rewritten, segments, err := rewriteDataSection(payload, &activeSeen, memoryMin, haveMemory)
			if err != nil {
				return image, err
			}
			image.active = segments
			count, _, _ := readULEB(payload, 0, 32)
			dataModes = make([]bool, int(count))
			for _, segment := range segments {
				dataModes[segment.index] = true
			}
			// The section id and length are part of the changed Data section;
			// segment entries and all expressions remain byte-for-byte intact.
			out = append(out, id)
			out = appendULEB(out, uint32(len(rewritten)))
			out = append(out, rewritten...)
			pos = payloadEnd
			_ = sectionStart
			continue
		}
		out = append(out, wasm[sectionStart:lengthStart]...)
		out = append(out, wasm[lengthStart:payloadEnd]...)
		pos = payloadEnd
	}
	if importedMemoryCount != 0 {
		return image, errors.New("COW data image rejects imported memory")
	}
	if memoryCount != 1 || !haveMemory {
		return image, errors.New("COW data image requires one fixed local memory")
	}
	if memoryMin == 0 || memoryMax < memoryMin {
		return image, errors.New("COW data image requires a nonzero fixed memory minimum")
	}
	if !dataSeen || !activeSeen {
		return image, errors.New("COW data image requires an active Data segment")
	}
	if uint64(memoryMin) > (^uint64(0))/wasmPageSizeBytes {
		return image, errors.New("memory size overflows")
	}
	if code != nil {
		if err := validateDataImageCode(code, dataModes); err != nil {
			return image, err
		}
	}
	image.memorySize = uint64(memoryMin) * wasmPageSizeBytes
	image.shell = bytes.Clone(out)
	return image, nil
}

func rewriteDataSection(payload []byte, activeSeen *bool, memoryMin uint32, haveMemory bool) ([]byte, []cowDataSegment, error) {
	count, cursor, err := readULEB(payload, 0, 32)
	if err != nil {
		return nil, nil, fmt.Errorf("data count: %w", err)
	}
	if count > uint64(len(payload)-cursor) {
		return nil, nil, errors.New("data count exceeds section size")
	}
	out := append([]byte(nil), payload[:cursor]...)
	active := make([]cowDataSegment, 0, count)
	for index := uint64(0); index < count; index++ {
		entryStart := cursor
		flags, next, err := readULEB(payload, cursor, 32)
		if err != nil {
			return nil, nil, fmt.Errorf("data segment %d flags: %w", index, err)
		}
		cursor = next
		isActive := flags == 0 || flags == 2
		if flags != 0 && flags != 1 && flags != 2 {
			return nil, nil, fmt.Errorf("data segment %d has unsupported flags %d", index, flags)
		}
		if isActive {
			*activeSeen = true
			if flags == 2 {
				memoryIndex, n, e := readULEB(payload, cursor, 32)
				if e != nil {
					return nil, nil, fmt.Errorf("data segment %d memory index: %w", index, e)
				}
				if memoryIndex != 0 {
					return nil, nil, fmt.Errorf("data segment %d targets memory %d", index, memoryIndex)
				}
				cursor = n
			}
			offset, n, e := readI32Const(payload, cursor)
			if e != nil {
				return nil, nil, fmt.Errorf("data segment %d offset: %w", index, e)
			}
			cursor = n
			if offset < 0 {
				return nil, nil, fmt.Errorf("data segment %d has negative offset", index)
			}
			if !haveMemory {
				return nil, nil, errors.New("active data appears before memory validation")
			}
			lengthStart := cursor
			length, n, e := readULEB(payload, cursor, 32)
			if e != nil {
				return nil, nil, fmt.Errorf("data segment %d payload length: %w", index, e)
			}
			cursor = n
			end := cursor + int(length)
			if end < cursor || end > len(payload) {
				return nil, nil, fmt.Errorf("data segment %d payload truncated", index)
			}
			if uint64(offset)+uint64(length) > uint64(memoryMin)*wasmPageSizeBytes {
				return nil, nil, fmt.Errorf("data segment %d is out of bounds", index)
			}
			active = append(active, cowDataSegment{index: int(index), offset: uint64(offset), data: payload[cursor:end]})
			// Keep flags, memory index, and offset expression exactly; only
			// replace the active payload vector with zero length.
			out = append(out, payload[entryStart:lengthStart]...)
			out = appendULEB(out, 0)
			cursor = end
			continue
		}
		// Passive segments are copied in their entirety, including payload.
		length, n, e := readULEB(payload, cursor, 32)
		if e != nil {
			return nil, nil, fmt.Errorf("data segment %d payload length: %w", index, e)
		}
		cursor = n
		end := cursor + int(length)
		if end < cursor || end > len(payload) {
			return nil, nil, fmt.Errorf("data segment %d payload truncated", index)
		}
		out = append(out, payload[entryStart:end]...)
		cursor = end
	}
	if cursor != len(payload) {
		return nil, nil, errors.New("trailing bytes in Data section")
	}
	return out, active, nil
}

func readI32Const(buf []byte, pos int) (int32, int, error) {
	if pos >= len(buf) || buf[pos] != 0x41 {
		return 0, pos, errors.New("offset is not i32.const")
	}
	value, next, err := readSLEB(buf, pos+1, 32)
	if err != nil {
		return 0, pos, err
	}
	if next >= len(buf) || buf[next] != 0x0b {
		return 0, pos, errors.New("offset expression is not terminated")
	}
	return int32(value), next + 1, nil
}

func skipName(buf []byte, pos int) (int, error) {
	length, next, err := readULEB(buf, pos, 32)
	if err != nil {
		return pos, err
	}
	end := next + int(length)
	if end < next || end > len(buf) {
		return pos, errors.New("truncated name")
	}
	return end, nil
}

func skipLimits(buf []byte, pos int) (int, error) {
	flags, next, err := readULEB(buf, pos, 32)
	if err != nil {
		return pos, err
	}
	_, next, err = readULEB(buf, next, 32)
	if err != nil {
		return pos, err
	}
	if flags&1 != 0 {
		_, next, err = readULEB(buf, next, 32)
	}
	return next, err
}

func readULEB(buf []byte, pos int, bits uint) (uint64, int, error) {
	var value uint64
	for i := uint(0); i < (bits+6)/7; i++ {
		if pos >= len(buf) {
			return 0, pos, errors.New("truncated unsigned leb128")
		}
		b := buf[pos]
		pos++
		if i == 9 && b > 1 {
			return 0, pos, errors.New("unsigned leb128 overflow")
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if bits < 64 && value >= (uint64(1)<<bits) {
				return 0, pos, errors.New("unsigned leb128 value overflow")
			}
			return value, pos, nil
		}
	}
	return 0, pos, errors.New("unsigned leb128 too long")
}

func readSLEB(buf []byte, pos int, bits uint) (int64, int, error) {
	var value int64
	var shift uint
	var b byte
	for i := 0; i < int((bits+6)/7); i++ {
		if pos >= len(buf) {
			return 0, pos, errors.New("truncated signed leb128")
		}
		b = buf[pos]
		pos++
		if bits == 64 && i == 9 && b != 0 && b != 0x7f {
			return 0, pos, errors.New("signed leb128 overflow")
		}
		value |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			if shift < 64 && b&0x40 != 0 {
				value |= ^int64(0) << shift
			}
			if bits < 64 {
				min, max := -(int64(1) << (bits - 1)), (int64(1)<<(bits-1))-1
				if value < min || value > max {
					return 0, pos, errors.New("signed leb128 value overflow")
				}
			}
			return value, pos, nil
		}
		shift = uint(i+1) * 7
	}
	return 0, pos, errors.New("signed leb128 too long")
}

func appendULEB(dst []byte, value uint32) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}
