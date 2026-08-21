package prepareddataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestCanonicalFixtureHasFrozenIdentity(t *testing.T) {
	fixture := CanonicalFixture()
	if got, want := len(fixture), CanonicalFileBytes; got != want {
		t.Fatalf("fixture size = %d, want %d", got, want)
	}
	fileDigest := sha256.Sum256(fixture)
	if got, want := "sha256:"+hex.EncodeToString(fileDigest[:]), CanonicalFileSHA256; got != want {
		t.Fatalf("fixture digest = %s, want %s", got, want)
	}
	bodyDigest := sha256.Sum256(fixture[CanonicalBodyOffset:])
	if got, want := "sha256:"+hex.EncodeToString(bodyDigest[:]), CanonicalBodySHA256; got != want {
		t.Fatalf("body digest = %s, want %s", got, want)
	}
	if !bytes.Equal(fixture, CanonicalFixture()) {
		t.Fatal("fixture generation is not deterministic")
	}
}

func TestCanonicalFixtureRoundTripsTypedMetadataAndBody(t *testing.T) {
	decoded, err := Decode(CanonicalFixture())
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Metadata.DType != DTypeInt64LE || decoded.Metadata.Shape != (Shape{1024, 1024}) ||
		decoded.Metadata.Order != OrderC || decoded.Metadata.FortranOrder ||
		decoded.Metadata.HeaderBytes != CanonicalHeaderBytes || decoded.Metadata.BodyBytes != CanonicalBodyBytes ||
		decoded.Metadata.FileBytes != CanonicalFileBytes || decoded.Metadata.Sum != CanonicalSum ||
		decoded.Metadata.First != 0 || decoded.Metadata.Last != 1_048_575 {
		t.Fatalf("metadata = %+v", decoded.Metadata)
	}
	if len(decoded.Body) != CanonicalBodyBytes {
		t.Fatalf("body size = %d, want %d", len(decoded.Body), CanonicalBodyBytes)
	}
	if !bytes.Equal(decoded.Body, CanonicalFixture()[CanonicalBodyOffset:]) {
		t.Fatal("decoded body differs from canonical body")
	}
	decoded.Body[0] ^= 1
	if CanonicalFixture()[CanonicalBodyOffset] != 0 {
		t.Fatal("decoded body aliases the fixture")
	}
}

func TestDecodeRejectsMalformedNPYBeforePublication(t *testing.T) {
	base := CanonicalFixture()
	cases := []struct {
		name string
		edit func([]byte)
		want error
	}{
		{"magic", func(data []byte) { data[0] = 0 }, ErrInvalidMagic},
		{"version", func(data []byte) { data[6] = 2 }, ErrInvalidVersion},
		{"header length", func(data []byte) { data[8] = 0 }, ErrInvalidHeader},
		{"dtype", func(data []byte) { replaceHeader(data, "'<i8'", "'<f8'") }, ErrUnsupportedDType},
		{"shape", func(data []byte) { replaceHeader(data, "(1024, 1024)", "(1024, 1023)") }, ErrUnsupportedShape},
		{"fortran order", func(data []byte) { replaceHeader(data, "fortran_order': False", "fortran_order': True ") }, ErrUnsupportedFortranOrder},
		{"order", func(data []byte) { data[CanonicalBodyOffset-2] = 'x' }, ErrUnsupportedOrder},
		{"body length", func(data []byte) {}, ErrInvalidBodyLength},
		{"body digest", func(data []byte) { data[CanonicalBodyOffset] ^= 1 }, ErrBodyDigestMismatch},
		{"trailing bytes", func(data []byte) { data = append(data, 0) }, ErrTrailingBytes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), base...)
			if tc.name == "body length" {
				data = data[:len(data)-1]
			} else if tc.name == "trailing bytes" {
				data = append(data, 0)
			} else {
				tc.edit(data)
			}
			decoded, err := Decode(data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decode() error = %v, want %v", err, tc.want)
			}
			if len(decoded.Body) != 0 {
				t.Fatalf("rejected %s published %d body bytes", tc.name, len(decoded.Body))
			}
		})
	}
}

func TestDecodeReaderIsBoundedAndRejectsTrailingInput(t *testing.T) {
	fixture := CanonicalFixture()
	decoded, err := DecodeReader(bytes.NewReader(fixture))
	if err != nil || len(decoded.Body) != CanonicalBodyBytes {
		t.Fatalf("DecodeReader() = body %d, error %v", len(decoded.Body), err)
	}
	_, err = DecodeReader(io.MultiReader(bytes.NewReader(fixture), bytes.NewReader([]byte{1})))
	if !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing reader error = %v", err)
	}
}

func replaceHeader(data []byte, old, new string) {
	oldBytes := []byte(old)
	newBytes := []byte(new)
	start := bytes.Index(data[CanonicalHeaderOffset:CanonicalBodyOffset], oldBytes)
	if start < 0 || len(oldBytes) != len(newBytes) {
		panic("test header replacement is not fixed-width")
	}
	copy(data[CanonicalHeaderOffset+start:], newBytes)
}
