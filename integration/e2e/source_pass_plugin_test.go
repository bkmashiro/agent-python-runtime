package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

func TestRealGuestStaticPassPluginTransformsAndExecutesOriginalRequest(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	allowedImports := []string{"json"}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('7'),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := unifiedPassCatalog(t)
	registry, err = registry.Enable(sourcepatch.PureScalarCSEName)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.ExecutionProfile = &profile
	runner, err := (wazeroengine.Factory{Passes: registry}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	selectedPass, ok := registry.Lookup(sourcepatch.PureScalarCSEName)
	if !ok {
		t.Fatal("catalog lost CSE pass")
	}
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 6, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())

	source := "seed = 7\nleft = seed * seed + 3\nright = seed * seed + 3\nresult = [left, right]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "source-pass-plugin-e2e", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := registry.Execute(context.Background(), sourcepatch.PureScalarCSEName, session, engine, request)
	if err != nil || !execution.Applied || execution.Patch.ReplacementCount != 1 {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	patch := execution.Patch
	derived := execution.Payload

	negativeRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "source-pass-plugin-negative", Code: "left = abs(7)\nright = abs(7)\nresult = right\n", Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	negative, err := registry.Execute(context.Background(), sourcepatch.PureScalarCSEName, session, engine, negativeRequest)
	if err != nil || negative.Applied || negative.Patch.Status != "not_applicable" {
		t.Fatalf("negative execution=%+v err=%v", negative, err)
	}
	negativeResult, err := decodeSuccessfulGuestResult(negative.Payload)
	if err != nil || string(negativeResult) != `7` {
		t.Fatalf("negative result=%s err=%v", negativeResult, err)
	}
	selfReferenceRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "source-pass-plugin-self-reference", Code: "a = 1\na = a + 1\nb = a + 1\nresult = [a, b]\n", Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	selfReference, err := registry.Execute(context.Background(), sourcepatch.PureScalarCSEName, session, engine, selfReferenceRequest)
	if err != nil || selfReference.Applied || selfReference.Patch.Status != "not_applicable" {
		t.Fatalf("self-reference execution=%+v err=%v", selfReference, err)
	}
	selfReferenceResult, err := decodeSuccessfulGuestResult(selfReference.Payload)
	if err != nil || string(selfReferenceResult) != `[2,3]` {
		t.Fatalf("self-reference result=%s err=%v", selfReferenceResult, err)
	}
	unknownCallRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID:  "source-pass-plugin-unknown-call",
		Code:   "def mutate():\n    global seed\n    seed = 2\nseed = 1\nx = mutate()\nseed = seed * seed\nb = seed * seed\nresult = [seed, b]\n",
		Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	unknownCall, err := registry.Execute(context.Background(), sourcepatch.PureScalarCSEName, session, engine, unknownCallRequest)
	if err != nil || unknownCall.Applied || unknownCall.Patch.Status != "not_applicable" {
		t.Fatalf("unknown-call execution=%+v err=%v", unknownCall, err)
	}
	unknownCallResult, err := decodeSuccessfulGuestResult(unknownCall.Payload)
	if err != nil || string(unknownCallResult) != `[4,16]` {
		t.Fatalf("unknown-call result=%s err=%v", unknownCallResult, err)
	}
	identityRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID:  "source-pass-plugin-identity",
		Code:   "a = 1000\nleft = a * a + 0\nright = a * a + 0\nresult = left is right\n",
		Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Execute(context.Background(), sourcepatch.PureScalarCSEName, session, engine, identityRequest)
	if err != nil || identity.Applied || identity.Patch.Status != "not_applicable" {
		t.Fatalf("identity execution=%+v err=%v", identity, err)
	}
	identityResult, err := decodeSuccessfulGuestResult(identity.Payload)
	if err != nil || string(identityResult) != `false` {
		t.Fatalf("identity result=%s err=%v", identityResult, err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil {
		t.Fatal(err)
	}
	derivedResult, err := decodeSuccessfulGuestResult(derived)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baselineResult, derivedResult) || string(derivedResult) != `[52,52]` {
		t.Fatalf("baseline=%s derived=%s", baselineResult, derivedResult)
	}
	type sourceEnvelope struct {
		SourceContract struct {
			ModelSourceSHA256  string `json:"model_source_sha256"`
			EffectiveASTSHA256 string `json:"effective_ast_sha256"`
		} `json:"source_contract"`
	}
	var baselineEnvelope, derivedEnvelope sourceEnvelope
	if err := json.Unmarshal(baseline, &baselineEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(derived, &derivedEnvelope); err != nil ||
		baselineEnvelope.SourceContract.ModelSourceSHA256 != patch.OriginalSourceSHA256 ||
		derivedEnvelope.SourceContract.ModelSourceSHA256 != patch.OriginalSourceSHA256 ||
		baselineEnvelope.SourceContract.EffectiveASTSHA256 == derivedEnvelope.SourceContract.EffectiveASTSHA256 {
		t.Fatalf("baseline contract=%+v derived contract=%+v err=%v", baselineEnvelope.SourceContract, derivedEnvelope.SourceContract, err)
	}

	expression := strings.TrimSuffix(strings.Repeat("seed * seed - 48 + ", 200), " + ")
	benchmarkSource := "seed = 7\nleft = " + expression + "\nright = " + expression + "\nresult = left == right\n"
	benchmarkPatch, err := registry.Transform(context.Background(), sourcepatch.PureScalarCSEName, session, benchmarkSource)
	if err != nil || !benchmarkPatch.Applied() {
		t.Fatalf("benchmark patch=%+v err=%v", benchmarkPatch, err)
	}
	benchmarkRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "source-pass-plugin-benchmark", Code: benchmarkSource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	baselineNanos := make([]int64, 3)
	treatmentNanos := make([]int64, 3)
	for index := range baselineNanos {
		started := time.Now()
		baseline, err = runner.Run(context.Background(), benchmarkRequest, "")
		baselineNanos[index] = time.Since(started).Nanoseconds()
		if err != nil {
			t.Fatal(err)
		}
		started = time.Now()
		derived, err = engine.RunSourcePatchDerived(context.Background(), benchmarkRequest, benchmarkPatch, selectedPass.Registration())
		treatmentNanos[index] = time.Since(started).Nanoseconds()
		if err != nil {
			t.Fatal(err)
		}
		baselineResult, err = decodeSuccessfulGuestResult(baseline)
		if err != nil {
			t.Fatal(err)
		}
		derivedResult, err = decodeSuccessfulGuestResult(derived)
		if err != nil || !reflect.DeepEqual(baselineResult, derivedResult) {
			t.Fatalf("benchmark baseline=%s derived=%s err=%v", baselineResult, derivedResult, err)
		}
	}
	sort.Slice(baselineNanos, func(i, j int) bool { return baselineNanos[i] < baselineNanos[j] })
	sort.Slice(treatmentNanos, func(i, j int) bool { return treatmentNanos[i] < treatmentNanos[j] })
	t.Logf("synthetic pure_scalar_cse nanos: baseline=%v treatment=%v medians=%d/%d", baselineNanos, treatmentNanos, baselineNanos[1], treatmentNanos[1])
}

func TestRealGuestPureScalarFoldPaperPass(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	allowedImports := []string{"json"}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('8'),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := unifiedPassCatalog(t)
	registry, err = registry.Enable(sourcepatch.PureScalarFoldName)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.ExecutionProfile = &profile
	runner, err := (wazeroengine.Factory{Passes: registry}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	selectedPass, ok := registry.Lookup(sourcepatch.PureScalarFoldName)
	if !ok {
		t.Fatal("catalog lost scalar-fold pass")
	}
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 4, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())

	source := "seed = 7\nfolded = seed * seed + 3\nresult = folded\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "pure-scalar-fold-e2e", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := registry.Execute(context.Background(), sourcepatch.PureScalarFoldName, session, engine, request)
	if err != nil || !execution.Applied || execution.Patch.ReplacementCount != 1 {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil {
		t.Fatal(err)
	}
	derivedResult, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(derivedResult) != `52` || !reflect.DeepEqual(baselineResult, derivedResult) {
		t.Fatalf("baseline=%s derived=%s err=%v", baselineResult, derivedResult, err)
	}

	negativeSource := "def mutate():\n    global seed\n    seed = 2\nseed = 1\nmutate()\nfolded = seed + 1\nresult = folded\n"
	negativeRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "pure-scalar-fold-negative", Code: negativeSource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	negative, err := registry.Execute(context.Background(), sourcepatch.PureScalarFoldName, session, engine, negativeRequest)
	if err != nil || negative.Applied || negative.Patch.Status != "not_applicable" {
		t.Fatalf("negative execution=%+v err=%v", negative, err)
	}
	negativeResult, err := decodeSuccessfulGuestResult(negative.Payload)
	if err != nil || string(negativeResult) != `3` {
		t.Fatalf("negative result=%s err=%v", negativeResult, err)
	}
	identitySource := "a = 1000\nliteral = 1000000\nfolded = a * a + 0\nresult = folded is literal\n"
	identityRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "pure-scalar-fold-identity", Code: identitySource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Execute(context.Background(), sourcepatch.PureScalarFoldName, session, engine, identityRequest)
	if err != nil || identity.Applied || identity.Patch.Status != "not_applicable" {
		t.Fatalf("identity execution=%+v err=%v", identity, err)
	}
	identityResult, err := decodeSuccessfulGuestResult(identity.Payload)
	if err != nil || string(identityResult) != `false` {
		t.Fatalf("identity result=%s err=%v", identityResult, err)
	}

	expression := strings.TrimSuffix(strings.Repeat("1 + ", 200), " + ")
	benchmarkSource := "folded = " + expression + "\nresult = folded\n"
	benchmarkPatch, err := registry.Transform(context.Background(), sourcepatch.PureScalarFoldName, session, benchmarkSource)
	if err != nil || !benchmarkPatch.Applied() {
		t.Fatalf("benchmark patch=%+v err=%v", benchmarkPatch, err)
	}
	benchmarkRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "pure-scalar-fold-benchmark", Code: benchmarkSource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	baselineNanos := make([]int64, 3)
	treatmentNanos := make([]int64, 3)
	for index := range baselineNanos {
		started := time.Now()
		baseline, err = runner.Run(context.Background(), benchmarkRequest, "")
		baselineNanos[index] = time.Since(started).Nanoseconds()
		if err != nil {
			t.Fatal(err)
		}
		started = time.Now()
		derived, runErr := engine.RunSourcePatchDerived(context.Background(), benchmarkRequest, benchmarkPatch, selectedPass.Registration())
		treatmentNanos[index] = time.Since(started).Nanoseconds()
		if runErr != nil {
			t.Fatal(runErr)
		}
		baselineResult, err = decodeSuccessfulGuestResult(baseline)
		if err != nil {
			t.Fatal(err)
		}
		derivedResult, err = decodeSuccessfulGuestResult(derived)
		if err != nil || !reflect.DeepEqual(baselineResult, derivedResult) {
			t.Fatalf("benchmark baseline=%s derived=%s err=%v", baselineResult, derivedResult, err)
		}
	}
	sort.Slice(baselineNanos, func(i, j int) bool { return baselineNanos[i] < baselineNanos[j] })
	sort.Slice(treatmentNanos, func(i, j int) bool { return treatmentNanos[i] < treatmentNanos[j] })
	t.Logf("synthetic pure_scalar_fold nanos: baseline=%v treatment=%v medians=%d/%d", baselineNanos, treatmentNanos, baselineNanos[1], treatmentNanos[1])
}
