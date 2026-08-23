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
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
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
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)

	pass, err := sourcepatch.NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := passplugin.New(pass)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(sourcepatch.PureScalarCSEName)
	if err != nil {
		t.Fatal(err)
	}
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 3, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())

	source := "seed = 7\nleft = seed * seed + 3\nright = seed * seed + 3\nresult = [left, right]\n"
	patch, err := registry.Transform(context.Background(), sourcepatch.PureScalarCSEName, session, source)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Status != "applied" || patch.ReplacementCount != 1 {
		t.Fatalf("patch=%+v", patch)
	}
	notApplicable, err := registry.Transform(
		context.Background(), sourcepatch.PureScalarCSEName, session,
		"left = abs(7)\nright = abs(7)\nresult = right\n",
	)
	if err != nil || notApplicable.Applied() || notApplicable.Status != "not_applicable" {
		t.Fatalf("negative patch=%+v err=%v", notApplicable, err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "source-pass-plugin-e2e", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	derived, err := engine.RunSourcePatchDerived(context.Background(), request, patch, pass.Registration())
	if err != nil {
		t.Fatal(err)
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
	var derivedEnvelope struct {
		SourceContract struct {
			ModelSourceSHA256  string `json:"model_source_sha256"`
			EffectiveASTSHA256 string `json:"effective_ast_sha256"`
		} `json:"source_contract"`
	}
	if err := json.Unmarshal(derived, &derivedEnvelope); err != nil ||
		derivedEnvelope.SourceContract.ModelSourceSHA256 != patch.OriginalSourceSHA256 ||
		derivedEnvelope.SourceContract.EffectiveASTSHA256 != patch.DerivedASTSHA256 {
		t.Fatalf("derived source contract=%+v err=%v", derivedEnvelope.SourceContract, err)
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
		derived, err = engine.RunSourcePatchDerived(context.Background(), benchmarkRequest, benchmarkPatch, pass.Registration())
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
