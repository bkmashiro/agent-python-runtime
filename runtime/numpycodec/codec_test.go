package numpycodec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
)

const (
	testDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testBindings() Bindings {
	return Bindings{
		ArtifactSHA256: testDigestA, ExecutionProfileID: "numpy-core", ExecutionProfileSHA256: testDigestA,
		ImportClosureSHA256: testDigestA, SourceSHA256: testDigestA, InputsSHA256: testDigestA,
		PassRegistrationSHA256: testDigestA,
	}
}

func testProducerValue(dtype string, shape []uint64, body []byte) []byte {
	value := ProducerValue{
		SchemaVersion: ProducerValueSchemaVersion, DType: dtype, Shape: shape, Order: "C",
		CContiguous: true, NBytes: uint64(len(body)), BodySHA256: resultblob.BytesDigest(body),
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}
	raw, _ := json.Marshal(value)
	return raw
}

func TestDecodeProducerValueSealsExactTypedDescriptor(t *testing.T) {
	body := make([]byte, 48)
	for index := range body {
		body[index] = byte(index)
	}
	descriptor, decodedBody, err := DecodeProducerValue(testProducerValue("<i8", []uint64{2, 3}, body), testBindings(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Codec != CodecV1 || descriptor.DType != "<i8" || descriptor.Endianness != "little" ||
		descriptor.Order != "C" || descriptor.NBytes != 48 || descriptor.BodySHA256 != resultblob.BytesDigest(body) ||
		descriptor.IdentitySHA256 == "" || !bytes.Equal(decodedBody, body) {
		t.Fatalf("descriptor=%+v body=%x", descriptor, decodedBody)
	}
	raw, err := descriptor.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDescriptor(raw)
	if err != nil || !equalDescriptor(decoded, descriptor) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestProducerRejectsUnsupportedOrInconsistentArrays(t *testing.T) {
	validBody := make([]byte, 48)
	cases := []struct {
		name   string
		mutate func(*ProducerValue)
	}{
		{"object", func(v *ProducerValue) { v.DType = "|O" }},
		{"big endian", func(v *ProducerValue) { v.DType = ">i8" }},
		{"fortran", func(v *ProducerValue) { v.Order = "F" }},
		{"strided", func(v *ProducerValue) { v.CContiguous = false }},
		{"rank", func(v *ProducerValue) { v.Shape = make([]uint64, MaxRank+1) }},
		{"zero dimension", func(v *ProducerValue) { v.Shape = []uint64{2, 0, 3} }},
		{"nbytes", func(v *ProducerValue) { v.NBytes++ }},
		{"hash", func(v *ProducerValue) { v.BodySHA256 = testDigestB }},
		{"noncanonical base64", func(v *ProducerValue) { v.BodyBase64 += "\n" }},
	}
	for _, candidate := range cases {
		t.Run(candidate.name, func(t *testing.T) {
			var value ProducerValue
			if err := json.Unmarshal(testProducerValue("<i8", []uint64{2, 3}, validBody), &value); err != nil {
				t.Fatal(err)
			}
			candidate.mutate(&value)
			raw, _ := json.Marshal(value)
			if _, _, err := DecodeProducerValue(raw, testBindings(), 1024); err == nil {
				t.Fatal("invalid producer accepted")
			}
		})
	}
	overflow := ProducerValue{
		SchemaVersion: ProducerValueSchemaVersion, DType: "<i8", Shape: []uint64{^uint64(0), 2},
		Order: "C", CContiguous: true, NBytes: 1, BodySHA256: resultblob.BytesDigest([]byte{0}), BodyBase64: "AA==",
	}
	raw, _ := json.Marshal(overflow)
	if _, _, err := DecodeProducerValue(raw, testBindings(), ^uint64(0)); err == nil {
		t.Fatal("shape overflow accepted")
	}
}

func TestProducerEnvelopeIsBoundedBeforeJSONAndBase64Decode(t *testing.T) {
	oversized := bytes.Repeat([]byte("{"), 4096+8)
	if _, _, err := DecodeProducerValue(oversized, testBindings(), 1); !errors.Is(err, ErrInvalidProducer) {
		t.Fatalf("oversized raw envelope err=%v", err)
	}
	value := ProducerValue{
		SchemaVersion: ProducerValueSchemaVersion, DType: "|u1", Shape: []uint64{1}, Order: "C", CContiguous: true,
		NBytes: 1, BodySHA256: resultblob.BytesDigest([]byte{0}), BodyBase64: "AAAA",
	}
	raw, _ := json.Marshal(value)
	if _, _, err := DecodeProducerValue(raw, testBindings(), 1); !errors.Is(err, ErrInvalidProducer) {
		t.Fatalf("encoded length mismatch err=%v", err)
	}
}

func TestBindingsAndDescriptorDecodeFailClosed(t *testing.T) {
	body := make([]byte, 8)
	bindings := testBindings()
	bindings.ExecutionProfileID = "base"
	if _, _, err := DecodeProducerValue(testProducerValue("<i8", []uint64{1}, body), bindings, 1024); !errors.Is(err, ErrBinding) {
		t.Fatalf("profile err=%v", err)
	}
	bindings = testBindings()
	bindings.SourceSHA256 = "sha256:bad"
	if _, _, err := DecodeProducerValue(testProducerValue("<i8", []uint64{1}, body), bindings, 1024); !errors.Is(err, ErrBinding) {
		t.Fatalf("digest err=%v", err)
	}
	descriptor, _, _ := DecodeProducerValue(testProducerValue("<i8", []uint64{1}, body), testBindings(), 1024)
	raw, _ := descriptor.CanonicalJSON()
	for _, invalid := range [][]byte{
		append([]byte(" "), raw...),
		bytes.Replace(raw, []byte(`"codec"`), []byte(`"unknown":true,"codec"`), 1),
		bytes.Replace(raw, []byte(testDigestA), []byte(testDigestB), 1),
	} {
		if _, err := DecodeDescriptor(invalid); err == nil {
			t.Fatalf("invalid descriptor accepted: %s", invalid)
		}
	}
}

func TestPublishAndFreshMaterializationRequestUseImmutableLeaseCopies(t *testing.T) {
	body := make([]byte, 48)
	producer := testProducerValue("<i8", []uint64{2, 3}, body)
	store, _ := resultblob.NewStore("run-1", resultblob.Limits{
		MaxEntries: 1, MaxBodyBytes: 1024, MaxRetainedBytes: 2048, MaxMetadataBytes: 4096, MaxLeases: 2,
	})
	descriptor, blobDescriptor, publicationEvidence, err := Publish(context.Background(), store, "run-1", producer, testBindings(), resultblob.NewPublicationGuard(publicationauth.Mint()), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if publicationEvidence.GuestToHostCopyBytes != 48 || publicationEvidence.ProducerEnvelopeBytes != uint64(len(producer)) || publicationEvidence.DecodeSealDurationNS < 0 {
		t.Fatalf("publication evidence=%+v", publicationEvidence)
	}
	consumerSource := "array_value[0, 0] = 99\nresult = {'first': int(array_value[0, 0])}"
	consumerSHA, _ := ConsumerIdentity(descriptor, "array_value", consumerSource)
	first, _ := store.NewLease(blobDescriptor.IdentitySHA256, consumerSHA)
	second, _ := store.NewLease(blobDescriptor.IdentitySHA256, testDigestB)
	firstClaim, _ := store.Claim(first)
	plan, err := BuildMaterializationRequest("consumer-1", "array_value", firstClaim, consumerSource)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ConsumerBindingSHA256 != consumerSHA || plan.LeaseID != first.ID() || plan.HostToGuestCopyBytes != 48 ||
		plan.RequestBytes != uint64(len(plan.Request)) || plan.RequestBuildDurationNS < 0 ||
		!digestPattern.MatchString(plan.ConsumerSourceSHA256) || !digestPattern.MatchString(plan.FinalSourceSHA256) ||
		!digestPattern.MatchString(plan.InputsSHA256) || !digestPattern.MatchString(plan.RequestSHA256) {
		t.Fatalf("materialization plan=%+v", plan)
	}
	if bytes.Contains(plan.Request, []byte("pointer")) || bytes.Contains(plan.Request, []byte("shared_memory")) ||
		!bytes.Contains(plan.Request, []byte(CodecV1)) || !bytes.Contains(plan.Request, []byte(base64.StdEncoding.EncodeToString(body))) ||
		!bytes.Contains(plan.Request, []byte("pysolate.numpy-materialization-result.v1")) {
		t.Fatalf("request=%s", plan.Request)
	}
	firstClaim.Body[0] = 99
	if err := store.Consume(first); err != nil {
		t.Fatal(err)
	}
	secondClaim, _ := store.Claim(second)
	if !bytes.Equal(secondClaim.Body, body) || resultblob.BytesDigest(secondClaim.Body) != descriptor.BodySHA256 {
		t.Fatal("consumer claim mutated Host body")
	}
	metadataDescriptor, err := DecodeDescriptor(secondClaim.Metadata)
	if err != nil || !equalDescriptor(metadataDescriptor, descriptor) {
		t.Fatalf("metadata=%+v err=%v", metadataDescriptor, err)
	}
}

func TestMaterializationRequestRejectsBadOutputAndClaimJoins(t *testing.T) {
	body := make([]byte, 8)
	store, _ := resultblob.NewStore("run-1", resultblob.Limits{
		MaxEntries: 1, MaxBodyBytes: 1024, MaxRetainedBytes: 2048, MaxMetadataBytes: 4096, MaxLeases: 6,
	})
	descriptor, blobDescriptor, _, err := Publish(context.Background(), store, "run-1", testProducerValue("<i8", []uint64{1}, body), testBindings(), resultblob.NewPublicationGuard(publicationauth.Mint()), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"", "result", "bad-name", "__pysolate_ndarray_body_b64"} {
		lease, _ := store.NewLease(blobDescriptor.IdentitySHA256, testDigestA)
		claim, _ := store.Claim(lease)
		if _, err := BuildMaterializationRequest("consumer-1", output, claim, "result = 1"); err == nil {
			t.Fatalf("output %q accepted", output)
		}
	}
	source := "result = int(array_value[0])"
	consumerSHA, _ := ConsumerIdentity(descriptor, "array_value", source)
	lease, _ := store.NewLease(blobDescriptor.IdentitySHA256, consumerSHA)
	claim, _ := store.Claim(lease)
	claim.Descriptor.BodySHA256 = testDigestB
	if _, err := BuildMaterializationRequest("consumer-1", "array_value", claim, source); err == nil {
		t.Fatal("body join mismatch accepted")
	}
	lease, _ = store.NewLease(blobDescriptor.IdentitySHA256, testDigestB)
	claim, _ = store.Claim(lease)
	if _, err := BuildMaterializationRequest("consumer-1", "array_value", claim, source); !errors.Is(err, ErrBinding) {
		t.Fatalf("consumer binding substitution err=%v", err)
	}

	forged := descriptor
	forged.Shape = []uint64{2}
	forged.NBytes = 16
	forged.IdentitySHA256 = digestJSON(descriptorIdentity{
		SchemaVersion: forged.SchemaVersion, Codec: forged.Codec, DType: forged.DType, Shape: forged.Shape,
		Order: forged.Order, Endianness: forged.Endianness, NBytes: forged.NBytes, BodySHA256: forged.BodySHA256, Bindings: forged.Bindings,
	})
	forgedMetadata, _ := forged.CanonicalJSON()
	forgedStore, _ := resultblob.NewStore("run-forged", resultblob.Limits{
		MaxEntries: 1, MaxBodyBytes: 1024, MaxRetainedBytes: 2048, MaxMetadataBytes: 4096, MaxLeases: 1,
	})
	forgedBlob, err := forgedStore.Publish(context.Background(), resultblob.Publication{
		RunID: "run-forged", Codec: CodecV1, Metadata: forgedMetadata, BindingSHA256: forged.IdentitySHA256,
		ExpectedBodySHA256: resultblob.BytesDigest(body),
		Guard:              resultblob.NewPublicationGuard(publicationauth.Mint()),
	}, body)
	if err != nil {
		t.Fatal(err)
	}
	forgedConsumerSHA, _ := ConsumerIdentity(forged, "array_value", source)
	forgedLease, _ := forgedStore.NewLease(forgedBlob.IdentitySHA256, forgedConsumerSHA)
	forgedClaim, _ := forgedStore.Claim(forgedLease)
	if _, err := BuildMaterializationRequest("consumer-1", "array_value", forgedClaim, source); !errors.Is(err, ErrMaterialization) {
		t.Fatalf("descriptor nbytes/body mismatch err=%v", err)
	}
}

func equalDescriptor(left, right Descriptor) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func TestGeneratedMaterializerAlwaysCopiesIntoPrivateCArray(t *testing.T) {
	if !strings.Contains(materializationPrelude, ".copy(order='C')") ||
		!strings.Contains(materializationPrelude, "b64decode") ||
		!strings.Contains(materializationPrelude, "sha256") ||
		strings.Contains(materializationPrelude, "shared_memory") {
		t.Fatalf("prelude=%s", materializationPrelude)
	}
}
