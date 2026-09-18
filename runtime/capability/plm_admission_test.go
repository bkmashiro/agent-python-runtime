package capability_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestPLMTypedCallUsesProgrammaticAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, callID, code string
		invalidSource      bool
	}{
		{name: "wrong-parent", callID: "unbound-call", code: "programmatic_call_identity_mismatch"},
		{name: "source-bound", callID: "parent:program:1"},
		{name: "invalid-source", callID: "parent:program:1", code: "source_binding_invalid", invalidSource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := plmOwnerAdapter()
			plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
			binding := receipt.SourceBinding{
				SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound,
				DocumentID: "sha256:" + strings.Repeat("1", 64), SourceSHA256: "sha256:" + strings.Repeat("2", 64),
				OccurrenceID: "sha256:" + strings.Repeat("3", 64), Capability: "workspace.read_text", DynamicOccurrence: 1,
				StartLine: 2, StartColumn: 9, EndLine: 2, EndColumn: 38,
			}
			if tc.invalidSource {
				binding = receipt.SourceBinding{}
			}
			resolver, recorded := newRecordingSourceResolver(t, binding)
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "typed-admission", Plan: plan, ProgrammaticParentCallID: "parent", SourceResolver: resolver})
			if err != nil {
				t.Fatal(err)
			}
			table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := broker.Finalize(false); err != nil {
					t.Error(err)
				}
			})
			certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
			adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
			raw := []byte(fmt.Sprintf(`{"call_id":%q,"capability":"workspace.read_text","arguments":{"path":"a.txt"}}`, tc.callID))
			if err := table.PrepareOrReuse(context.Background(), "typed-slot", raw, contract, certificate); err != nil {
				t.Fatal(err)
			}
			response, err := table.LinearizeAndMaterialize(context.Background(), "typed-slot", plmVerticalLogical(certificate, "a.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.code != "" {
				if !containsCode(response, tc.code) {
					t.Fatalf("response=%s", response)
				}
				if tc.code == "programmatic_call_identity_mismatch" && (broker.Calls() != 0 || len(recorded.requests) != 0) {
					t.Fatal("rejected identity consumed logical admission")
				}
				return
			}
			if !containsResult(response, "ready") {
				t.Fatalf("response=%s", response)
			}
			receipts := broker.Receipts()
			if len(receipts) != 1 || receipts[0].ParentCallID != "parent" || receipts[0].Source == nil || *receipts[0].Source != binding || len(recorded.requests) != 1 {
				t.Fatalf("lost parent/source binding: receipts=%+v resolver=%+v", receipts, recorded.requests)
			}
		})
	}
}

func TestBrokerAllowsOnlyOneStagedOwner(t *testing.T) {
	for _, tableFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(tableFirst), func(t *testing.T) {
			plan, _ := plmVerticalPlan(t, capability.TemporalImmutable, plmOwnerAdapter())
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "one-owner", Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			if tableFirst {
				if _, err = capability.NewSplitPhaseTable(broker, ownerLimits()); err != nil {
					t.Fatal(err)
				}
				err = broker.AttachStagedClaimer(&stagedClaimer{})
			} else {
				if err = broker.AttachStagedClaimer(&stagedClaimer{}); err != nil {
					t.Fatal(err)
				}
				_, err = capability.NewSplitPhaseTable(broker, ownerLimits())
			}
			if !errors.Is(err, capability.ErrInvalidBroker) {
				t.Fatalf("second owner attached: %v", err)
			}
			if err = broker.Finalize(false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
