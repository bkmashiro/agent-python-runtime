package pysolate

import "fmt"

// validateDataImageCode walks instructions, never scans raw bytes for opcodes:
// constants and immediates can contain the same bytes as memory.init. The
// engine validates full typing later; unsupported encodings fail closed here.
func validateDataImageCode(code []byte, active []bool) error {
	count, p, err := readULEB(code, 0, 32)
	if err != nil {
		return err
	}
	for fn := uint64(0); fn < count; fn++ {
		size, next, err := readULEB(code, p, 32)
		if err != nil {
			return err
		}
		end := next + int(size)
		if end < next || end > len(code) {
			return fmt.Errorf("truncated code body")
		}
		body := code[next:end]
		locals, q, err := readULEB(body, 0, 32)
		if err != nil {
			return err
		}
		for i := uint64(0); i < locals; i++ {
			_, q, err = readULEB(body, q, 32)
			if err != nil || q >= len(body) {
				return fmt.Errorf("truncated locals")
			}
			// Only the single-byte value types supported by this narrow path.
			switch body[q] {
			case 0x7f, 0x7e, 0x7d, 0x7c, 0x7b, 0x70, 0x6f:
			default:
				return fmt.Errorf("unsupported local type")
			}
			q++
		}
		unsigned := func() (uint64, error) { var v uint64; v, q, err = readULEB(body, q, 32); return v, err }
		skip := func(n int) error {
			if n > len(body)-q {
				return fmt.Errorf("truncated instruction")
			}
			q += n
			return nil
		}
		for q < len(body) {
			op := body[q]
			q++
			switch {
			case op == 0x00 || op == 0x01 || op == 0x05 || op == 0x0b || op == 0x0f || op == 0x1a || op == 0x1b || op == 0xd1:
			case op >= 0x45 && op <= 0xc4:
			case op == 0x02 || op == 0x03 || op == 0x04 || op == 0xd0:
				_, q, err = readSLEB(body, q, 33)
			case op == 0x0c || op == 0x0d || op == 0x10 || (op >= 0x20 && op <= 0x26) || op == 0xd2 || op == 0x3f || op == 0x40:
				_, err = unsigned()
			case op == 0x0e:
				var n uint64
				n, err = unsigned()
				if err == nil && n > uint64(len(body)-q) {
					return fmt.Errorf("truncated branch table")
				}
				for i := uint64(0); err == nil && i <= n; i++ {
					_, err = unsigned()
				}
			case op == 0x11 || (op >= 0x28 && op <= 0x3e):
				_, err = unsigned()
				if err == nil {
					_, err = unsigned()
				}
			case op == 0x1c:
				var n uint64
				n, err = unsigned()
				if err == nil {
					if n > uint64(len(body)-q) {
						return fmt.Errorf("truncated typed select")
					}
					err = skip(int(n))
				}
			case op == 0x41:
				_, q, err = readSLEB(body, q, 32)
			case op == 0x42:
				_, q, err = readSLEB(body, q, 64)
			case op == 0x43:
				err = skip(4)
			case op == 0x44:
				err = skip(8)
			case op == 0xfc:
				var sub uint64
				sub, err = unsigned()
				if err != nil {
					break
				}
				switch {
				case sub <= 7:
				case sub == 8 || sub == 9:
					var index uint64
					index, err = unsigned()
					if err != nil {
						break
					}
					if index >= uint64(len(active)) {
						return fmt.Errorf("data instruction index out of range")
					}
					if active[index] {
						return fmt.Errorf("COW data image rejects instruction %d referencing active data segment %d", sub, index)
					}
					if sub == 8 {
						_, err = unsigned()
					}
				case sub == 10 || sub == 12 || sub == 14:
					_, err = unsigned()
					if err == nil {
						_, err = unsigned()
					}
				case sub == 11 || sub == 13 || (sub >= 15 && sub <= 17):
					_, err = unsigned()
				default:
					return fmt.Errorf("unsupported misc instruction %d", sub)
				}
			default:
				return fmt.Errorf("COW data image does not support opcode 0x%x", op)
			}
			if err != nil {
				return fmt.Errorf("function %d instruction: %w", fn, err)
			}
		}
		p = end
	}
	if p != len(code) {
		return fmt.Errorf("trailing code bytes")
	}
	return nil
}
