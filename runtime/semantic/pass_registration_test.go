package semantic

import (
	"errors"
	"testing"
)

func TestPassRegistrationAcceptsOnlyClosedConsumerCombinations(t *testing.T) {
	analyzer := AnalyzerIdentity()
	config := legalityDigest("pass-config")
	cases := []struct {
		name     PassName
		version  string
		consumer PassConsumer
	}{
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, PassConsumerOverlayOnly},
		{PassPreparedPureRegion, PreparedPureRegionPassVersion, PassConsumerExecutionPatch},
	}
	for _, candidate := range cases {
		registration, err := NewPassRegistration(candidate.name, candidate.version, analyzer, config, candidate.consumer)
		if err != nil || PassName(registration.Name()) != candidate.name || registration.Version() != candidate.version ||
			registration.AnalyzerSHA256() != analyzer || registration.ConfigSHA256() != config || registration.Consumer() != candidate.consumer ||
			registration.IdentitySHA256() == "" {
			t.Fatalf("registration=%+v err=%v", registration, err)
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
	}{
		{"unknown", "v1", analyzer, config, PassConsumerOverlayOnly},
		{PassSemanticPreDispatch, "wrong", analyzer, config, PassConsumerOverlayOnly},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, "sha256:bad", config, PassConsumerOverlayOnly},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, analyzer, "sha256:bad", PassConsumerOverlayOnly},
		{PassSemanticPreDispatch, SemanticPreDispatchPassVersion, analyzer, config, PassConsumerExecutionPatch},
		{PassPreparedPureRegion, PreparedPureRegionPassVersion, analyzer, config, PassConsumerOverlayOnly},
	}
	for _, candidate := range cases {
		if _, err := NewPassRegistration(candidate.name, candidate.version, candidate.analyzer, candidate.config, candidate.consumer); !errors.Is(err, ErrInvalidPassRegistration) {
			t.Fatalf("accepted %+v: %v", candidate, err)
		}
	}
}

func TestPassRegistryRejectsDuplicateRegistration(t *testing.T) {
	registration, err := NewPassRegistration(PassSemanticPreDispatch, SemanticPreDispatchPassVersion, AnalyzerIdentity(), legalityDigest("pass-config"), PassConsumerOverlayOnly)
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
