package wazero_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestFactoryRejectsInvalidMechanismsBeforeArtifactParsing(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{Streaming: true}
	_, err := (wazeroengine.Factory{}).New(context.Background(), []byte("not wasm"), config)
	if !errors.Is(err, runtimeconfig.ErrInvalidMechanismSet) {
		t.Fatalf("factory error = %v", err)
	}
}

func TestRunStreamRequiresSelectedMechanism(t *testing.T) {
	runner, err := (wazeroengine.Factory{}).New(context.Background(), []byte{0, 97, 115, 109, 1, 0, 0, 0}, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	prepares := make(chan string)
	close(prepares)
	streamRunner, ok := runner.(interface {
		RunStream(context.Context, []byte, <-chan string) ([]byte, error)
	})
	if !ok {
		t.Fatal("runner lacks stream seam")
	}
	if _, err := streamRunner.RunStream(context.Background(), []byte(`{}`), prepares); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
		t.Fatalf("RunStream() error = %v", err)
	}
}

func TestFactoryIsFreshOnly(t *testing.T) {
	runner, err := (wazeroengine.Factory{}).New(context.Background(), []byte{0, 97, 115, 109, 1, 0, 0, 0}, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	if properties := runner.Properties(); properties.Backend != "wazero" || properties.ExecutionProfileID != "" {
		t.Fatalf("unexpected properties: %#v", properties)
	}
}

func TestDeterministicProfileRejectsArtifactSubstitutionBeforeCompile(t *testing.T) {
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	actual := sha256.Sum256(wasm)
	expectedArtifact := "sha256:" + strings.Repeat("a", 64)
	if expectedArtifact == fmt.Sprintf("sha256:%x", actual[:]) {
		t.Fatal("test artifact unexpectedly matches substitution identity")
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(expectedArtifact, "artifact-substitution")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: expectedArtifact, ManifestSHA256: "sha256:" + strings.Repeat("b", 64),
		ImportRoots: []string{"sys"}, QualifiedImportRoots: []string{"sys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	config.DeterministicVerification = &deterministic
	if _, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config); err == nil {
		t.Fatal("substituted artifact admitted")
	}
}

func TestExecutionProfileRejectsArtifactSubstitutionWithoutDeterministicProfile(t *testing.T) {
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: "sha256:" + strings.Repeat("a", 64),
		ManifestSHA256: "sha256:" + strings.Repeat("b", 64), ImportRoots: []string{"sys"},
		QualifiedImportRoots: []string{"sys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	if _, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config); !errors.Is(err, runtimeconfig.ErrExecutionProfileArtifactMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestLegacyGuestFailsSourceValidationBeforeBroker(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	brokerCalled := false
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		brokerCalled = true
		return nil, nil
	}}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	_, err = runner.Run(context.Background(), []byte(`{"run_id":"r","code":"result = 1","inputs":{}}`), "")
	if err == nil || brokerCalled {
		t.Fatalf("legacy guest did not fail closed: err=%v broker_called=%v", err, brokerCalled)
	}
}
