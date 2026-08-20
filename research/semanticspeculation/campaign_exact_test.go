package semanticspeculation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPhase3CampaignPlanIdentityDoesNotDependOnHandlerInstance(t *testing.T) {
	first, err := NewPhase3CampaignPlan(capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"first"}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPhase3CampaignPlan(capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"second"}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity() == "" || first.Identity() != second.Identity() || Phase3PrivacyIdentity == "" {
		t.Fatalf("first=%s second=%s privacy=%s", first.Identity(), second.Identity(), Phase3PrivacyIdentity)
	}
}

func TestCampaignOpaqueRunIDDoesNotExposeCaseOrTreatment(t *testing.T) {
	value := campaignOpaqueRunID("external_read_valid_suffix", 1, "semantic_pre_dispatch")
	if len(value) != len("phase3-")+24 || value == campaignOpaqueRunID("external_read_valid_suffix", 2, "semantic_pre_dispatch") {
		t.Fatalf("opaque_run_id=%q", value)
	}
}
