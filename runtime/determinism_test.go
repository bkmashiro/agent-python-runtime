package runtime_test

import (
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestDeterministicVerificationProfileIsArtifactBoundAndDomainSeparated(t *testing.T) {
	artifact := "sha256:" + strings.Repeat("a", 64)
	profile, err := runtimeconfig.NewDeterministicVerificationProfile(artifact, "repeatable-study-1")
	if err != nil {
		t.Fatal(err)
	}
	if profile.SchemaVersion() != runtimeconfig.DeterministicVerificationSchemaVersion ||
		profile.Status() != runtimeconfig.DeterministicVerificationExperimentalPartial ||
		profile.ArtifactSHA256() != artifact || !strings.HasPrefix(profile.Identity(), "sha256:") {
		t.Fatalf("profile=%+v", profile)
	}
	same, err := runtimeconfig.NewDeterministicVerificationProfile(artifact, "repeatable-study-1")
	if err != nil || same.Identity() != profile.Identity() {
		t.Fatalf("same=%+v err=%v", same, err)
	}
	differentSeed, err := runtimeconfig.NewDeterministicVerificationProfile(artifact, "repeatable-study-2")
	if err != nil || differentSeed.Identity() == profile.Identity() {
		t.Fatalf("different=%+v err=%v", differentSeed, err)
	}
	seed := profile.RandomSeed()
	seed[0] ^= 0xff
	if string(seed) == string(profile.RandomSeed()) {
		t.Fatal("random seed aliases profile state")
	}
}

func TestDeterministicVerificationProfileRejectsInvalidOrUnboundConfiguration(t *testing.T) {
	artifact := "sha256:" + strings.Repeat("a", 64)
	for _, testCase := range []struct {
		artifact string
		seed     string
	}{
		{artifact: "", seed: "study"},
		{artifact: artifact, seed: ""},
		{artifact: artifact, seed: strings.Repeat("x", 129)},
		{artifact: artifact, seed: "bad seed"},
	} {
		if _, err := runtimeconfig.NewDeterministicVerificationProfile(testCase.artifact, testCase.seed); err == nil {
			t.Fatalf("accepted artifact=%q seed=%q", testCase.artifact, testCase.seed)
		}
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(artifact, "study")
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.DeterministicVerification = &deterministic
	if err := config.Validate(); !errors.Is(err, runtimeconfig.ErrDeterministicVerificationAdmission) {
		t.Fatalf("unbound config err=%v", err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"datetime"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifact, ManifestSHA256: "sha256:" + strings.Repeat("b", 64),
		ImportRoots: []string{"datetime"}, QualifiedImportRoots: []string{"datetime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config.ExecutionProfile = &bound
	if err := config.Validate(); err != nil {
		t.Fatalf("bound deterministic config: %v", err)
	}
}

func TestDeterministicVerificationDeniesUnsupportedSourceClasses(t *testing.T) {
	for _, code := range []string{
		"import locale\nresult = 1",
		"import threading\nresult = 1",
		"import multiprocessing\nresult = 1",
	} {
		request := runtimeconfig.RunRequest{RunID: "det-admission", Code: code, Inputs: []byte(`{}`)}
		if err := runtimeconfig.AdmitDeterministicVerification(request); !errors.Is(err, runtimeconfig.ErrDeterministicVerificationAdmission) {
			t.Fatalf("code=%q err=%v", code, err)
		}
	}
	request := runtimeconfig.RunRequest{RunID: "det-admission", Code: "import datetime\nresult = datetime.date(2020, 1, 1).isoformat()", Inputs: []byte(`{}`)}
	if err := runtimeconfig.AdmitDeterministicVerification(request); err != nil {
		t.Fatalf("qualified deterministic source rejected: %v", err)
	}
}
