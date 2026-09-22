package pysolate

import "testing"

func TestDataImageCodeAdmission(t *testing.T) {
	cases := []struct {
		name         string
		instructions []byte
		reject       bool
	}{
		{"active init", []byte{0xfc, 8, 0, 0}, true},
		{"active drop", []byte{0xfc, 9, 0}, true},
		{"passive init", []byte{0xfc, 8, 1, 0}, false},
		{"passive drop", []byte{0xfc, 9, 1}, false},
		{"index out of range", []byte{0xfc, 9, 2}, true},
		{"bytes inside float are not instructions", []byte{0x43, 0xfc, 8, 0, 0}, false},
		{"truncated immediate", []byte{0x44, 0}, true},
		{"unsupported SIMD", []byte{0xfd, 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := append([]byte{0}, tc.instructions...)
			if !tc.reject {
				body = append(body, 0x0b)
			}
			code := append([]byte{1}, appendULEB(nil, uint32(len(body)))...)
			code = append(code, body...)
			err := validateDataImageCode(code, []bool{true, false})
			if (err != nil) != tc.reject {
				t.Fatalf("err=%v reject=%v", err, tc.reject)
			}
		})
	}
	// Check that the transformation actually invokes the guard.
	code := []byte{1, 5, 0, 0xfc, 9, 0, 0x0b}
	_, err := transformCOWDataImage(wasmModule(section(wasmMemorySection, []byte{1, 1, 1, 1}), section(10, code), section(wasmDataSection, []byte{1, 0, 0x41, 0, 0x0b, 1, 7})))
	if err == nil {
		t.Fatal("transformation bypassed code guard")
	}
}

func TestDataImageLEBWidth(t *testing.T) {
	if _, _, err := readULEB([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0}, 0, 32); err == nil {
		t.Fatal("overlong u32 accepted")
	}
	if _, _, err := readSLEB([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0}, 0, 32); err == nil {
		t.Fatal("overlong i32 accepted")
	}
	if _, _, err := readSLEB([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1}, 0, 64); err == nil {
		t.Fatal("overflowing i64 accepted")
	}
}

func BenchmarkCOWDataImagePreparation(b *testing.B) {
	wasm, err := readGuestArtifact()
	if err != nil {
		b.Fatal(err)
	}
	image, err := transformCOWDataImage(wasm)
	if err != nil {
		b.Fatal(err)
	}
	var code []byte
	for p := 8; p < len(wasm); {
		id := wasm[p]
		n, next, e := readULEB(wasm, p+1, 32)
		if e != nil {
			b.Fatal(e)
		}
		if id == 10 {
			code = wasm[next : next+int(n)]
		}
		p = next + int(n)
	}
	n, _, _ := readULEB(dataSectionPayload(wasm), 0, 32)
	active := make([]bool, int(n))
	for _, s := range image.active {
		active[s.index] = true
	}
	b.Run("instruction-check", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := validateDataImageCode(code, active); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full-transform", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := transformCOWDataImage(wasm); err != nil {
				b.Fatal(err)
			}
		}
	})
}
