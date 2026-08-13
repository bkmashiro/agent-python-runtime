package streaming_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

func TestSourceSealBindsOnlyAnAdmittedSuiteObservation(t *testing.T) {
	identity := observationIdentity()
	identity.SourceSHA256 = ""
	record, err := streaming.NewStagedObservation(identity, []byte(`{"value":1}`))
	if err != nil {
		t.Fatal(err)
	}
	seal := streaming.SourceSeal{
		Digest: observationIdentity().SourceSHA256,
		Suites: []streaming.SuiteRecord{{Range: identity.SuiteRange, Digest: identity.SuiteSHA256}},
	}
	if err := seal.BindObservation(record); err != nil {
		t.Fatal(err)
	}
	mismatch := observationIdentity()
	mismatch.SourceSHA256 = ""
	mismatch.SuiteSHA256 = digest('8')
	other, err := streaming.NewStagedObservation(mismatch, []byte(`{"value":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := seal.BindObservation(other); !errors.Is(err, streaming.ErrStagedObservationMismatch) {
		t.Fatalf("BindObservation() error = %v", err)
	}
}

func TestStagedObservationSameArgumentsDifferentOccurrenceDoesNotMatch(t *testing.T) {
	provisional := observationIdentity()
	provisional.SourceSHA256 = ""
	record, err := streaming.NewStagedObservation(provisional, []byte(`{"value":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := record.BindSource(observationIdentity().SourceSHA256); err != nil {
		t.Fatal(err)
	}
	other := observationIdentity()
	other.DynamicOccurrence = 2
	if _, err := record.Consume(other); !errors.Is(err, streaming.ErrStagedObservationMismatch) {
		t.Fatalf("Consume() error = %v", err)
	}
}

func TestStagedObservationBindsFinalSourceAndConsumesOnce(t *testing.T) {
	provisional := observationIdentity()
	provisional.SourceSHA256 = ""
	record, err := streaming.NewStagedObservation(provisional, []byte(`{"value":1}`))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := record.BindSource(digest('f'))
	if err != nil {
		t.Fatal(err)
	}
	query := observationIdentity()
	result, err := sealed.Consume(query)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"value":1}` {
		t.Fatalf("result = %s", result)
	}
	result[0] = 'x'
	if _, err := sealed.Consume(query); !errors.Is(err, streaming.ErrStagedObservationConsumed) {
		t.Fatalf("second consume error = %v", err)
	}
}

func TestStagedObservationRejectsEveryIdentityMismatch(t *testing.T) {
	base := observationIdentity()
	provisional := base
	provisional.SourceSHA256 = ""
	fields := []struct {
		name   string
		mutate func(*streaming.ObservationIdentity)
	}{
		{"stream epoch", func(v *streaming.ObservationIdentity) { v.StreamEpoch = "stream-2" }},
		{"workflow epoch", func(v *streaming.ObservationIdentity) { v.WorkflowEpoch = "workflow-2" }},
		{"source", func(v *streaming.ObservationIdentity) { v.SourceSHA256 = digest('0') }},
		{"suite start", func(v *streaming.ObservationIdentity) { v.SuiteRange.Start++ }},
		{"suite end", func(v *streaming.ObservationIdentity) { v.SuiteRange.End++ }},
		{"suite digest", func(v *streaming.ObservationIdentity) { v.SuiteSHA256 = digest('1') }},
		{"dynamic occurrence", func(v *streaming.ObservationIdentity) { v.DynamicOccurrence++ }},
		{"arguments", func(v *streaming.ObservationIdentity) { v.ArgumentsSHA256 = digest('2') }},
		{"capability", func(v *streaming.ObservationIdentity) { v.Capability = "fixture.other" }},
		{"spec", func(v *streaming.ObservationIdentity) { v.SpecSHA256 = digest('3') }},
		{"handler", func(v *streaming.ObservationIdentity) { v.HandlerIdentity = "handler-v2" }},
		{"plan", func(v *streaming.ObservationIdentity) { v.PlanSHA256 = digest('4') }},
		{"grant policy", func(v *streaming.ObservationIdentity) { v.GrantPolicySHA256 = digest('5') }},
		{"freshness", func(v *streaming.ObservationIdentity) { v.FreshnessEpoch = "fresh-2" }},
		{"expiry", func(v *streaming.ObservationIdentity) { v.ExpiryEpoch = "expiry-2" }},
		{"privacy", func(v *streaming.ObservationIdentity) { v.PrivacyPartition = "project-b" }},
		{"parent lineage", func(v *streaming.ObservationIdentity) { v.ParentLineageSHA256 = digest('6') }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			record, err := streaming.NewStagedObservation(provisional, []byte(`{"value":1}`))
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := record.BindSource(base.SourceSHA256)
			if err != nil {
				t.Fatal(err)
			}
			query := base
			field.mutate(&query)
			if _, err := sealed.Consume(query); !errors.Is(err, streaming.ErrStagedObservationMismatch) {
				t.Fatalf("Consume() error = %v", err)
			}
		})
	}
}

func TestStagedObservationTerminalDispositionPreventsConsume(t *testing.T) {
	for _, disposition := range []streaming.ObservationDisposition{
		streaming.ObservationFailed,
		streaming.ObservationTimedOut,
		streaming.ObservationCancelled,
		streaming.ObservationLate,
		streaming.ObservationOrphaned,
		streaming.ObservationFallback,
	} {
		t.Run(string(disposition), func(t *testing.T) {
			identity := observationIdentity()
			identity.SourceSHA256 = ""
			record, err := streaming.NewStagedObservation(identity, []byte(`{"value":1}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := record.Terminate(disposition); err != nil {
				t.Fatal(err)
			}
			if _, err := record.Consume(observationIdentity()); !errors.Is(err, streaming.ErrStagedObservationTerminal) {
				t.Fatalf("Consume() error = %v", err)
			}
		})
	}
}

func TestObservationIdentityContainsOnlyBoundedIdentityFields(t *testing.T) {
	identity := observationIdentity()
	if err := identity.Validate(true); err != nil {
		t.Fatal(err)
	}
	typeOf := reflect.TypeOf(identity)
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.ToLower(typeOf.Field(index).Name)
		for _, forbidden := range []string{"body", "result", "path", "endpoint", "credential", "token"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("identity exposes forbidden field %q", typeOf.Field(index).Name)
			}
		}
	}
}

func observationIdentity() streaming.ObservationIdentity {
	return streaming.ObservationIdentity{
		SchemaVersion:       streaming.ObservationIdentitySchemaVersion,
		StreamEpoch:         "stream-1",
		WorkflowEpoch:       "workflow-1",
		SourceSHA256:        digest('f'),
		SuiteRange:          streaming.ByteRange{Start: 10, End: 20},
		SuiteSHA256:         digest('a'),
		DynamicOccurrence:   1,
		ArgumentsSHA256:     digest('b'),
		Capability:          "fixture.read",
		SpecSHA256:          digest('c'),
		HandlerIdentity:     "fixture-handler-v1",
		PlanSHA256:          digest('d'),
		GrantPolicySHA256:   digest('e'),
		FreshnessEpoch:      "fresh-1",
		ExpiryEpoch:         "expiry-1",
		PrivacyPartition:    "project-a",
		ParentLineageSHA256: digest('9'),
	}
}

func digest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }
