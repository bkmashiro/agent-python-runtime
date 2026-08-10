package runtime

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeRunRequestAcceptsBoundedTypedRequirements(t *testing.T) {
	request, err := DecodeRunRequest([]byte(`{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["browser_runtime","posix"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Requirements) != 2 || request.Requirements[0] != RequiredFeatureBrowserRuntime || request.Requirements[1] != RequiredFeaturePOSIX {
		t.Fatalf("requirements=%v", request.Requirements)
	}

	for name, raw := range map[string]string{
		"unknown":           `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["gpu"]}`,
		"ambiguous browser": `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["browser"]}`,
		"semantic web search is not a runtime requirement": `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["web_search"]}`,
		"semantic web fetch is not a runtime requirement":  `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["web_fetch"]}`,
		"duplicate":     `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["browser_runtime","browser_runtime"]}`,
		"null":          `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":null}`,
		"duplicate key": `{"run_id":"run-1","run_id":"run-2","code":"result=1","inputs":{}}`,
		"too many":      `{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["browser_runtime","daemon","dynamic_package_install","native_extension","native_threads","posix","shell","subprocess","browser_runtime"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRunRequest([]byte(raw)); err == nil {
				t.Fatal("invalid requirements accepted")
			}
		})
	}
}

func TestAdmitRunRequirementsReturnsTypedUnsupportedError(t *testing.T) {
	request := RunRequest{Requirements: []RequiredFeature{RequiredFeaturePOSIX, RequiredFeatureBrowserRuntime}}
	err := AdmitRunRequirements(request)
	var unsupported *UnsupportedRunError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error=%v", err)
	}
	if unsupported.Code != OutcomeRuntimeUnsupported || len(unsupported.RequiredFeatures) != 2 || unsupported.RequiredFeatures[0] != RequiredFeatureBrowserRuntime || unsupported.RequiredFeatures[1] != RequiredFeaturePOSIX {
		t.Fatalf("unsupported=%+v", unsupported)
	}
	if err := AdmitRunRequirements(RunRequest{}); err != nil {
		t.Fatalf("empty requirements rejected: %v", err)
	}
}

func TestNewUnsupportedOutcomeIsHostAuthoredAndRequestBound(t *testing.T) {
	raw := []byte(`{"run_id":"run-1","code":"result=1","inputs":{},"requirements":["posix"]}`)
	request, err := DecodeRunRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	admissionErr := AdmitRunRequirements(request)
	outcome, err := NewUnsupportedOutcome(raw, admissionErr)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.SchemaVersion != 1 || outcome.Kind != OutcomeRuntimeUnsupported || !outcome.EscalationRequired || outcome.EscalationReason != EscalationReasonRequiredFeatures ||
		outcome.WorkspaceDisposition != WorkspaceNotStarted || outcome.EffectDisposition != EffectsNotStarted || len(outcome.RequiredFeatures) != 1 || outcome.RequiredFeatures[0] != RequiredFeaturePOSIX ||
		outcome.Evidence.RequestSHA256 != "sha256:584a3cbcde841a08da15ad156496dd01a0d7ca1180ebac0cd01d6c76d586bb8c" {
		t.Fatalf("outcome=%+v", outcome)
	}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecutionOutcome(encoded)
	if err != nil || decoded.Evidence.RequestSHA256 != outcome.Evidence.RequestSHA256 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestUnsupportedOutcomeRejectsOrdinaryFailuresAndMalformedEvidence(t *testing.T) {
	if _, err := NewUnsupportedOutcome([]byte(`{}`), errors.New("python exception: posix required")); err == nil {
		t.Fatal("ordinary error was upgraded")
	}
	for name, raw := range map[string]string{
		"unknown field": `{"schema_version":1,"kind":"runtime_unsupported","escalation_required":true,"escalation_reason":"required_features_unsupported","required_features":["posix"],"workspace_disposition":"not_started","effect_disposition":"not_started","evidence":{"request_sha256":"sha256:584a3cbcde841a08da15ad156496dd01a0d7ca1180ebac0cd01d6c76d586bb8c"},"extra":true}`,
		"bad digest":    `{"schema_version":1,"kind":"runtime_unsupported","escalation_required":true,"escalation_reason":"required_features_unsupported","required_features":["posix"],"workspace_disposition":"not_started","effect_disposition":"not_started","evidence":{"request_sha256":"sha256:nope"}}`,
		"unsafe state":  `{"schema_version":1,"kind":"runtime_unsupported","escalation_required":true,"escalation_reason":"required_features_unsupported","required_features":["posix"],"workspace_disposition":"retained","effect_disposition":"not_started","evidence":{"request_sha256":"sha256:584a3cbcde841a08da15ad156496dd01a0d7ca1180ebac0cd01d6c76d586bb8c"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeExecutionOutcome([]byte(raw)); err == nil {
				t.Fatal("invalid outcome accepted")
			}
		})
	}
}
