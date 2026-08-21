// Package prepareddataset contains the deliberately small, research-only Phase 3
// prepared-data seam. It accepts exactly one canonical NumPy v1 fixture; it is
// not a general NumPy or object serialization package.
package prepareddataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// CanonicalHeaderOffset is the offset of the v1 header dictionary after the
	// six-byte magic, two-byte version, and two-byte header-length field.
	CanonicalHeaderOffset = 10
	// CanonicalHeaderBytes includes the complete NPY prelude and header
	// dictionary, and is also the body offset.
	CanonicalHeaderBytes  = 128
	CanonicalHeaderLength = CanonicalHeaderBytes - CanonicalHeaderOffset
	CanonicalBodyOffset   = CanonicalHeaderBytes
	CanonicalBodyBytes    = 8 * 1024 * 1024
	CanonicalFileBytes    = CanonicalBodyOffset + CanonicalBodyBytes
	CanonicalSum          = int64(549755289600)

	CanonicalFileSHA256 = "sha256:390bd7abd4f26dfdf3440f060f77bd02526e97e0a46838337f56cb8ef978af7e"
	CanonicalBodySHA256 = "sha256:a78cee677876b925402c15818acd3fc020a47754d9d1c26688914ea09070f8d0"
)

const (
	DTypeInt64LE DType = "<i8"
	OrderC       Order = "C"
)

const canonicalHeaderText = "{'descr': '<i8', 'fortran_order': False, 'shape': (1024, 1024), }"

var (
	ErrInvalidMagic            = errors.New("invalid npy magic")
	ErrInvalidVersion          = errors.New("invalid npy version")
	ErrInvalidHeader           = errors.New("invalid npy header")
	ErrUnsupportedDType        = errors.New("unsupported npy dtype")
	ErrUnsupportedShape        = errors.New("unsupported npy shape")
	ErrUnsupportedFortranOrder = errors.New("unsupported npy fortran order")
	ErrUnsupportedOrder        = errors.New("unsupported npy order")
	ErrInvalidBodyLength       = errors.New("invalid npy body length")
	ErrTrailingBytes           = errors.New("trailing npy bytes")
	ErrDecodeBounds            = errors.New("npy decode exceeds bounds")
	ErrBodyDigestMismatch      = errors.New("npy body digest mismatch")
	ErrFileDigestMismatch      = errors.New("npy file digest mismatch")
)

// DType and Order are deliberately typed so a decoded descriptor cannot be
// confused with an arbitrary string from another serialization format.
type DType string
type Order string

// Shape is the only shape admitted by the Phase 3 fixture.
type Shape [2]uint64

// Metadata describes the validated, typed body. HeaderBytes is the complete
// prelude-plus-header size, matching the body offset in an NPY file.
type Metadata struct {
	VersionMajor uint8
	VersionMinor uint8
	DType        DType
	Shape        Shape
	FortranOrder bool
	Order        Order
	HeaderBytes  uint64
	BodyBytes    uint64
	FileBytes    uint64
	BodySHA256   string
	FileSHA256   string
	First        int64
	Last         int64
	Sum          int64
}

// DecodedArray is returned only after the complete file, header, body length,
// body digest, and file digest have all passed validation. Body is an owned
// copy and is always little-endian <i8 bytes for this codec.
type DecodedArray struct {
	Metadata Metadata
	Body     []byte
}

// Array is a short compatibility name for the typed decoder result.
type Array = DecodedArray

// DecodeLimits are hard upper bounds applied before publication. Larger limits
// do not widen the codec's fixed canonical shape; they only leave room for the
// fixed fixture's exact sizes.
type DecodeLimits struct {
	MaxFileBytes   int
	MaxHeaderBytes int
	MaxBodyBytes   int
}

func DefaultDecodeLimits() DecodeLimits {
	return DecodeLimits{
		MaxFileBytes: CanonicalFileBytes, MaxHeaderBytes: CanonicalHeaderBytes, MaxBodyBytes: CanonicalBodyBytes,
	}
}

// CanonicalFixture generates the exact deterministic v1 fixture without NumPy.
// Each call returns fresh storage so callers cannot mutate a later decode.
func CanonicalFixture() []byte {
	fixture := make([]byte, CanonicalFileBytes)
	copy(fixture, []byte("\x93NUMPY"))
	fixture[6], fixture[7] = 1, 0
	binary.LittleEndian.PutUint16(fixture[8:10], CanonicalHeaderLength)
	copy(fixture[CanonicalHeaderOffset:CanonicalBodyOffset], canonicalHeader())
	for index := 0; index < CanonicalBodyBytes/8; index++ {
		binary.LittleEndian.PutUint64(fixture[CanonicalBodyOffset+index*8:], uint64(index))
	}
	return fixture
}

// GenerateFixture is the explicit generator spelling used by harnesses.
func GenerateFixture() []byte { return CanonicalFixture() }

// WriteCanonicalFixture writes one complete fixture and reports short writes.
func WriteCanonicalFixture(writer io.Writer) error {
	if writer == nil {
		return errors.New("nil fixture writer")
	}
	fixture := CanonicalFixture()
	written, err := writer.Write(fixture)
	if err != nil {
		return fmt.Errorf("write canonical fixture: %w", err)
	}
	if written != len(fixture) {
		return io.ErrShortWrite
	}
	return nil
}

func canonicalHeader() []byte {
	header := make([]byte, CanonicalHeaderLength)
	copy(header, canonicalHeaderText)
	for index := len(canonicalHeaderText); index < len(header)-1; index++ {
		header[index] = ' '
	}
	header[len(header)-1] = '\n'
	return header
}

// Decode validates an in-memory NPY file under the fixed Phase 3 bounds.
func Decode(data []byte) (DecodedArray, error) {
	return DecodeWithLimits(data, DefaultDecodeLimits())
}

// DecodeBytes makes the byte-oriented API explicit for callers that prefer it.
func DecodeBytes(data []byte) (DecodedArray, error) { return Decode(data) }

// DecodeReader is bounded to one canonical file plus one byte, which is enough
// to distinguish a valid file from a file with trailing data without reading an
// unbounded stream into memory.
func DecodeReader(reader io.Reader) (DecodedArray, error) {
	if reader == nil {
		return DecodedArray{}, fmt.Errorf("%w: nil reader", ErrDecodeBounds)
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(CanonicalFileBytes)+1))
	if err != nil {
		return DecodedArray{}, fmt.Errorf("read npy: %w", err)
	}
	return Decode(data)
}

// DecodeWithLimits performs all checks before returning an owned body. The
// returned zero value contains no body on every failure path.
func DecodeWithLimits(data []byte, limits DecodeLimits) (DecodedArray, error) {
	if limits.MaxFileBytes == 0 {
		limits.MaxFileBytes = CanonicalFileBytes
	}
	if limits.MaxHeaderBytes == 0 {
		limits.MaxHeaderBytes = CanonicalHeaderBytes
	}
	if limits.MaxBodyBytes == 0 {
		limits.MaxBodyBytes = CanonicalBodyBytes
	}
	if limits.MaxFileBytes < CanonicalFileBytes || limits.MaxHeaderBytes < CanonicalHeaderBytes || limits.MaxBodyBytes < CanonicalBodyBytes {
		return DecodedArray{}, ErrDecodeBounds
	}
	if len(data) > limits.MaxFileBytes {
		if len(data) > CanonicalFileBytes {
			return DecodedArray{}, ErrTrailingBytes
		}
		return DecodedArray{}, ErrDecodeBounds
	}
	if len(data) < CanonicalHeaderOffset+2 {
		return DecodedArray{}, ErrInvalidHeader
	}
	if !bytes.Equal(data[:6], []byte("\x93NUMPY")) {
		return DecodedArray{}, ErrInvalidMagic
	}
	if data[6] != 1 || data[7] != 0 {
		return DecodedArray{}, ErrInvalidVersion
	}

	headerLength := int(binary.LittleEndian.Uint16(data[8:10]))
	if headerLength != CanonicalHeaderLength || headerLength > limits.MaxHeaderBytes-CanonicalHeaderOffset {
		return DecodedArray{}, ErrInvalidHeader
	}
	bodyOffset := CanonicalHeaderOffset + headerLength
	if bodyOffset != CanonicalBodyOffset || bodyOffset > len(data) {
		return DecodedArray{}, ErrInvalidHeader
	}
	if err := validateHeader(data[CanonicalHeaderOffset:bodyOffset]); err != nil {
		return DecodedArray{}, err
	}
	if len(data) < CanonicalFileBytes {
		return DecodedArray{}, ErrInvalidBodyLength
	}
	if len(data) > CanonicalFileBytes {
		return DecodedArray{}, ErrTrailingBytes
	}
	body := data[CanonicalBodyOffset:]
	if len(body) != CanonicalBodyBytes || len(body) > limits.MaxBodyBytes {
		return DecodedArray{}, ErrInvalidBodyLength
	}

	bodyDigest := digest(body)
	if bodyDigest != CanonicalBodySHA256 {
		return DecodedArray{}, fmt.Errorf("%w: got %s", ErrBodyDigestMismatch, bodyDigest)
	}
	fileDigest := digest(data)
	if fileDigest != CanonicalFileSHA256 {
		return DecodedArray{}, fmt.Errorf("%w: got %s", ErrFileDigestMismatch, fileDigest)
	}

	// Copy only after every check succeeds. A rejected decode therefore cannot
	// expose a partial body to the staged lifecycle.
	ownedBody := append([]byte(nil), body...)
	return DecodedArray{
		Metadata: Metadata{
			VersionMajor: 1, VersionMinor: 0, DType: DTypeInt64LE,
			Shape: Shape{1024, 1024}, FortranOrder: false, Order: OrderC,
			HeaderBytes: CanonicalHeaderBytes, BodyBytes: CanonicalBodyBytes,
			FileBytes: CanonicalFileBytes, BodySHA256: bodyDigest,
			FileSHA256: fileDigest, First: 0, Last: 1_048_575, Sum: CanonicalSum,
		},
		Body: ownedBody,
	}, nil
}

func validateHeader(header []byte) error {
	if len(header) != CanonicalHeaderLength {
		return ErrInvalidHeader
	}
	headerText := string(header)
	if !strings.Contains(headerText, "'descr': '<i8'") {
		return ErrUnsupportedDType
	}
	if !strings.Contains(headerText, "'shape': (1024, 1024)") {
		return ErrUnsupportedShape
	}
	if !strings.Contains(headerText, "'fortran_order': False") {
		return ErrUnsupportedFortranOrder
	}
	if !bytes.Equal(header, canonicalHeader()) {
		return ErrUnsupportedOrder
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
