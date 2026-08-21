package semantic

import (
	"errors"
	"reflect"
	"testing"
)

func TestPassRegistrationAcceptsOnlyClosedConsumerCombinations(t *testing.T) {
	analyzer := AnalyzerIdentity()
	config := legalityDigest("pass-config")
	cases := []struct {
		name     PassName
		version  string
		consumer PassConsumer
		bindings []PassBinding
	}{
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, PassConsumerOverlayOnly, SemanticPreDispatchBindings()},
		{PassPreparedPureRegion, PreparedPureRegionPassVersion, PassConsumerExecutionPatch, PreparedPureRegionBindings()},
	}
	for _, candidate := range cases {
		registration, err := NewPassRegistration(candidate.name, candidate.version, analyzer, config, candidate.consumer, candidate.bindings)
		if err != nil || PassName(registration.Name()) != candidate.name || registration.Version() != candidate.version ||
			registration.AnalyzerSHA256() != analyzer || registration.ConfigSHA256() != config || registration.Consumer() != candidate.consumer ||
			!reflect.DeepEqual(registration.RequiredBindings(), candidate.bindings) || registration.IdentitySHA256() == "" {
			t.Fatalf("registration=%+v err=%v", registration, err)
		}
		bindings := registration.RequiredBindings()
		bindings[0] = "forged"
		if reflect.DeepEqual(registration.RequiredBindings(), bindings) {
			t.Fatal("required bindings were mutable")
		}
	}
}

func TestPassRegistrationRejectsUnknownDriftAndConsumerConfusion(t *testing.T) {
	analyzer := AnalyzerIdentity()
	config := legalityDigest("pass-config")
	cases := []struct {
		name     PassName
		version  string
		analyzer string
		config   string
		consumer PassConsumer
		bindings []PassBinding
	}{
		{"unknown", "v1", analyzer, config, PassConsumerOverlayOnly, SemanticPreDispatchBindings()},
		{PassSemanticPreDispatch, "wrong", analyzer, config, PassConsumerOverlayOnly, SemanticPreDispatchBindings()},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, "sha256:bad", config, PassConsumerOverlayOnly, SemanticPreDispatchBindings()},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, analyzer, "sha256:bad", PassConsumerOverlayOnly, SemanticPreDispatchBindings()},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, analyzer, config, PassConsumerExecutionPatch, SemanticPreDispatchBindings()},
		{PassPreparedPureRegion, PreparedPureRegionPassVersion, analyzer, config, PassConsumerOverlayOnly, PreparedPureRegionBindings()},
		{PassPreparedPureRegion, PreparedPureRegionPassVersion, analyzer, config, PassConsumerExecutionPatch, SemanticPreDispatchBindings()},
	}
	for _, candidate := range cases {
		if _, err := NewPassRegistration(candidate.name, candidate.version, candidate.analyzer, candidate.config, candidate.consumer, candidate.bindings); !errors.Is(err, ErrInvalidPassRegistration) {
			t.Fatalf("accepted %+v: %v", candidate, err)
		}
	}
}

func TestPassRegistryRejectsDuplicateRegistration(t *testing.T) {
	registration, err := NewPassRegistration(PassSemanticPreDispatch, SemanticPreDispatchPassVersion, AnalyzerIdentity(), legalityDigest("pass-config"), PassConsumerOverlayOnly, SemanticPreDispatchBindings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPassRegistry(registration, registration); !errors.Is(err, ErrDuplicatePassRegistration) {
		t.Fatalf("duplicate error=%v", err)
	}
	registry, err := NewPassRegistry(registration)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.Lookup(PassSemanticPreDispatch)
	if !ok || resolved.IdentitySHA256() != registration.IdentitySHA256() {
		t.Fatalf("resolved=%+v ok=%v", resolved, ok)
	}
	if _, ok := registry.Lookup("unknown"); ok {
		t.Fatal("unknown registration resolved")
	}
}
