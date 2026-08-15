package observe_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func TestBuildOptimizationReportSealsLogicalToPhysicalProvenance(t *testing.T) {
	report := validOptimizationReport()
	verified, err := observe.BuildOptimizationReport(report)
	if err != nil {
		t.Fatal(err)
	}
	first, err := verified.Report()
	if err != nil {
		t.Fatal(err)
	}
	if first.SealSHA256 == "" || first.ConsumerAdmitted {
		t.Fatalf("unexpected sealed report: %+v", first)
	}
	first.Runs[0].RunID = "mutated"
	first.PhysicalExecutions[0].Consumers[0] = "mutated"
	second, err := verified.Report()
	if err != nil {
		t.Fatal(err)
	}
	if second.Runs[0].RunID != "run-0000000000000001" || second.PhysicalExecutions[0].Consumers[0] != "logical-0000000000000001" {
		t.Fatal("verified report aliased caller mutation")
	}
	encoded, err := observe.EncodeOptimizationReport(verified)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := observe.DecodeOptimizationReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedReport, err := decoded.Report()
	if err != nil || decodedReport.SealSHA256 != second.SealSHA256 {
		t.Fatalf("decode err=%v report=%+v", err, decodedReport)
	}
}

func TestOptimizationReportRejectsBrokenProvenanceAndTiming(t *testing.T) {
	cases := map[string]func(*observe.OptimizationReport){
		"unknown physical execution": func(report *observe.OptimizationReport) {
			report.LogicalRequests[0].PhysicalExecutionID = "physical-missing"
		},
		"consumer set mismatch": func(report *observe.OptimizationReport) {
			report.PhysicalExecutions[0].Consumers = []string{"logical-0000000000000001"}
		},
		"producer mismatch": func(report *observe.OptimizationReport) {
			report.PhysicalExecutions[0].ProducerLogicalRequestID = "logical-0000000000000002"
		},
		"reuse before completion": func(report *observe.OptimizationReport) {
			report.LogicalRequests[1].QualifiedNanos = 100
			report.LogicalRequests[1].DemandedNanos = 150
			report.LogicalRequests[1].CompletedNanos = 150
		},
		"preissue starts late": func(report *observe.OptimizationReport) {
			report.PhysicalExecutions[0].StartedNanos = 170
			report.Spans[2].StartedNanos = 170
		},
		"coalesce outside flight": func(report *observe.OptimizationReport) {
			report.LogicalRequests[4].DemandedNanos = 440
			report.LogicalRequests[4].CompletedNanos = 440
		},
		"coalesce exactly at terminal edge": func(report *observe.OptimizationReport) {
			report.LogicalRequests[4].DemandedNanos = 380
			report.LogicalRequests[4].CompletedNanos = 380
		},
		"coalesce producer demands after physical start": func(report *observe.OptimizationReport) {
			report.LogicalRequests[2].DemandedNanos = 350
		},
		"preissue logical completion precedes physical completion": func(report *observe.OptimizationReport) {
			report.LogicalRequests[0].CompletedNanos = 150
		},
		"parallel without overlap": func(report *observe.OptimizationReport) {
			report.PhysicalExecutions[2].StartedNanos = 381
			report.PhysicalExecutions[2].EndedNanos = 430
			report.LogicalRequests[3].CompletedNanos = 430
		},
		"parallel without declaration": func(report *observe.OptimizationReport) {
			report.Decisions[2].DeclarationSHA256 = ""
		},
		"parallel declaration crosses workflow": func(report *observe.OptimizationReport) {
			report.LogicalRequests[3].WorkflowID = "workflow-0000000000000002"
		},
		"rejected decision claims physical authority": func(report *observe.OptimizationReport) {
			report.Decisions[4].PhysicalExecutionID = "physical-0000000000000004"
		},
		"unlinked physical consumer": func(report *observe.OptimizationReport) {
			for index, decision := range report.Decisions {
				if decision.Kind == "reused" && decision.Outcome == "admitted" {
					report.Decisions = append(report.Decisions[:index], report.Decisions[index+1:]...)
					return
				}
			}
		},
		"raw body in metadata": func(report *observe.OptimizationReport) {
			report.Spans[0].Label = "prompt: secret body"
		},
		"free form decision reason": func(report *observe.OptimizationReport) {
			report.Decisions[4].Reason = "api_key_sk_live_SECRET123"
		},
		"reuse crosses capability plan": func(report *observe.OptimizationReport) {
			report.Runs[1].CapabilityPlanSHA256 = digest("other-plan")
		},
		"reuse crosses authority partition": func(report *observe.OptimizationReport) {
			report.LogicalRequests[1].AuthoritySHA256 = digest("other-authority")
		},
		"physical execution lacks measured span": func(report *observe.OptimizationReport) {
			report.Spans = report.Spans[:len(report.Spans)-1]
		},
		"WASI execution is mislabeled as Host tool": func(report *observe.OptimizationReport) {
			report.LogicalRequests[5].Surface = "wasi"
			report.LogicalRequests[5].Capability = "wasi.fd_read"
			report.PhysicalExecutions[3].Surface = "wasi"
			report.PhysicalExecutions[3].Capability = "wasi.fd_read"
		},
		"Host span output disagrees with result": func(report *observe.OptimizationReport) {
			report.Spans[5].OutputSHA256 = digest("wrong-result")
		},
		"non opaque identifier": func(report *observe.OptimizationReport) {
			report.StudyID = "api_key_secret"
		},
		"equal qualification and start": func(report *observe.OptimizationReport) {
			report.LogicalRequests[0].QualifiedNanos = report.PhysicalExecutions[0].StartedNanos
		},
		"equal completion and reuse demand": func(report *observe.OptimizationReport) {
			report.LogicalRequests[1].DemandedNanos = report.PhysicalExecutions[0].EndedNanos
		},
		"consumer admitted": func(report *observe.OptimizationReport) {
			report.ConsumerAdmitted = true
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			report := validOptimizationReport()
			mutate(&report)
			if _, err := observe.BuildOptimizationReport(report); err == nil {
				t.Fatal("invalid report admitted")
			}
		})
	}
}

func TestOptimizationReportRejectsNonCanonicalOrderingAndSealMutation(t *testing.T) {
	report := validOptimizationReport()
	report.LogicalRequests[0], report.LogicalRequests[1] = report.LogicalRequests[1], report.LogicalRequests[0]
	if _, err := observe.BuildOptimizationReport(report); err == nil {
		t.Fatal("unsorted requests admitted")
	}
	verified, err := observe.BuildOptimizationReport(validOptimizationReport())
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := verified.Report()
	sealed.Decisions[0].Reason = "changed"
	encoded, _ := json.Marshal(sealed)
	if _, err := observe.DecodeOptimizationReport(encoded); err == nil {
		t.Fatal("mutated seal admitted")
	}
	if _, err := observe.EncodeOptimizationReport(observe.VerifiedOptimizationReport{}); err == nil {
		t.Fatal("zero verified handle encoded")
	}
}

func TestOptimizationReportDecodeRejectsUnknownDuplicateAndNonCanonicalJSON(t *testing.T) {
	verified, err := observe.BuildOptimizationReport(validOptimizationReport())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := observe.EncodeOptimizationReport(verified)
	unknown := append([]byte(`{"unknown":1,`), encoded[1:]...)
	if !json.Valid(unknown) {
		t.Fatal("unknown-field fixture is malformed")
	}
	if _, err := observe.DecodeOptimizationReport(unknown); err == nil {
		t.Fatal("unknown field admitted")
	}
	duplicate := append([]byte(`{"schema_version":"pysolate.workflow-boundary-observation.v0",`), encoded[1:]...)
	if !json.Valid(duplicate) {
		t.Fatal("duplicate-field fixture is malformed")
	}
	if _, err := observe.DecodeOptimizationReport(duplicate); err == nil {
		t.Fatal("duplicate field admitted")
	}
	pretty := []byte("\n" + string(encoded))
	if _, err := observe.DecodeOptimizationReport(pretty); err == nil {
		t.Fatal("non-canonical whitespace admitted")
	}
}

func TestBuildOptimizationReportRejectsPayloadTooLargeForDecoder(t *testing.T) {
	if _, err := observe.BuildOptimizationReport(syntheticOptimizationReport(4096)); err == nil {
		t.Fatal("report larger than decoder budget admitted")
	}
}

func TestBuildOptimizationReportMatchesDecoderNodeBudget(t *testing.T) {
	report := syntheticOptimizationReport(150)
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) >= 2<<20 {
		t.Fatalf("node-budget fixture must remain below byte limit: bytes=%d err=%v", len(encoded), err)
	}
	if _, err := observe.BuildOptimizationReport(report); err == nil {
		t.Fatal("builder admitted report that canonical decoder would reject")
	}
}

func syntheticOptimizationReport(count int) observe.OptimizationReport {
	d := digest
	report := observe.OptimizationReport{
		SchemaVersion: "pysolate.workflow-boundary-observation.v0", StudyID: "study-0000000000000002",
		WorkloadManifestSHA256: d("manifest"), ShuffleSeed: 1, ClockPolicy: "study_relative_monotonic_nanos",
		Runs:  []observe.ObservedRun{{RunID: "run-0000000000000001", WorkloadID: "workload-0000000000000001", Treatment: "optimized", Order: 1, StartedNanos: 1, EndedNanos: 10000, TerminalDisposition: "succeeded", ArtifactSHA256: d("artifact"), ExecutionProfileSHA256: d("profile"), CapabilityPlanSHA256: d("plan")}},
		Spans: []observe.ObservedSpan{}, LogicalRequests: []observe.LogicalRequest{}, PhysicalExecutions: []observe.PhysicalExecution{}, Decisions: []observe.OptimizationDecision{},
	}
	for index := 0; index < count; index++ {
		logicalID := fmt.Sprintf("logical-%016x", index)
		physicalID := fmt.Sprintf("physical-%016x", index)
		started := uint64(10 + index*2)
		identity := d(physicalID)
		result := d(logicalID)
		report.LogicalRequests = append(report.LogicalRequests, observe.LogicalRequest{LogicalRequestID: logicalID, RunID: "run-0000000000000001", WorkflowID: "workflow-0000000000000002", WorkflowNodeID: "node-0000000000000006", OccurrenceID: fmt.Sprintf("occurrence-%016x", index), Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: identity, AuthoritySHA256: d("authority"), FreshnessSHA256: d("freshness"), PrivacySHA256: d("privacy"), QualifiedNanos: started, DemandedNanos: started, CompletedNanos: started + 1, PhysicalExecutionID: physicalID})
		report.PhysicalExecutions = append(report.PhysicalExecutions, observe.PhysicalExecution{PhysicalExecutionID: physicalID, RunID: "run-0000000000000001", ProducerLogicalRequestID: logicalID, Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: identity, AuthoritySHA256: d("authority"), FreshnessSHA256: d("freshness"), PrivacySHA256: d("privacy"), StartedNanos: started, EndedNanos: started + 1, TerminalDisposition: "succeeded", ResultSHA256: result, Consumers: []string{logicalID}})
		report.Spans = append(report.Spans, observe.ObservedSpan{SpanID: fmt.Sprintf("span-%016x", index), RunID: "run-0000000000000001", Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured", StartedNanos: started, EndedNanos: started + 1, PhysicalExecutionID: physicalID, InputSHA256: identity, OutputSHA256: result})
	}
	return report
}

func validOptimizationReport() observe.OptimizationReport {
	d := digest
	identity := d("boundary")
	authority := d("authority")
	freshness := d("freshness")
	privacy := d("privacy")
	return observe.OptimizationReport{
		SchemaVersion:          "pysolate.workflow-boundary-observation.v0",
		StudyID:                "study-0000000000000001",
		WorkloadManifestSHA256: d("workload-manifest"),
		ShuffleSeed:            20260815,
		ClockPolicy:            "study_relative_monotonic_nanos",
		Runs: []observe.ObservedRun{
			{RunID: "run-0000000000000001", WorkloadID: "workload-0000000000000001", Treatment: "optimized", Order: 1, StartedNanos: 10, EndedNanos: 300, TerminalDisposition: "succeeded", ArtifactSHA256: d("artifact"), ExecutionProfileSHA256: d("profile"), CapabilityPlanSHA256: d("plan")},
			{RunID: "run-0000000000000002", WorkloadID: "workload-0000000000000002", Treatment: "optimized", Order: 2, StartedNanos: 50, EndedNanos: 800, TerminalDisposition: "succeeded", ArtifactSHA256: d("artifact"), ExecutionProfileSHA256: d("profile"), CapabilityPlanSHA256: d("plan")},
		},
		Spans: []observe.ObservedSpan{
			{SpanID: "span-0000000000000001", RunID: "run-0000000000000001", Kind: "model.invocation", Label: "model invocation", EvidenceClass: "replayed", StartedNanos: 20, EndedNanos: 70, InputSHA256: d("prompt-a"), OutputSHA256: d("model-a")},
			{SpanID: "span-0000000000000002", ParentSpanID: "span-0000000000000001", RunID: "run-0000000000000001", Kind: "guest.wasm", Label: "WASM execution", EvidenceClass: "measured", StartedNanos: 80, EndedNanos: 230, InputSHA256: d("source-a"), OutputSHA256: d("result-a")},
			{SpanID: "span-0000000000000003", ParentSpanID: "span-0000000000000002", RunID: "run-0000000000000001", Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured", StartedNanos: 90, EndedNanos: 200, PhysicalExecutionID: "physical-0000000000000001", InputSHA256: identity, OutputSHA256: d("result")},
			{SpanID: "span-0000000000000004", RunID: "run-0000000000000002", Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured", StartedNanos: 336, EndedNanos: 385, PhysicalExecutionID: "physical-0000000000000003", InputSHA256: d("right"), OutputSHA256: d("right-result")},
			{SpanID: "span-0000000000000005", RunID: "run-0000000000000002", Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured", StartedNanos: 345, EndedNanos: 380, PhysicalExecutionID: "physical-0000000000000002", InputSHA256: d("left"), OutputSHA256: d("left-result")},
			{SpanID: "span-0000000000000006", RunID: "run-0000000000000002", Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured", StartedNanos: 625, EndedNanos: 700, PhysicalExecutionID: "physical-0000000000000004", InputSHA256: d("live"), OutputSHA256: d("live-result")},
		},
		LogicalRequests: []observe.LogicalRequest{
			{LogicalRequestID: "logical-0000000000000001", RunID: "run-0000000000000001", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000001", OccurrenceID: "occurrence-0000000000000001", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: identity, AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 80, DemandedNanos: 150, CompletedNanos: 200, PhysicalExecutionID: "physical-0000000000000001"},
			{LogicalRequestID: "logical-0000000000000002", RunID: "run-0000000000000002", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000001", OccurrenceID: "occurrence-0000000000000002", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: identity, AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 450, DemandedNanos: 500, CompletedNanos: 500, PhysicalExecutionID: "physical-0000000000000001"},
			{LogicalRequestID: "logical-0000000000000003", RunID: "run-0000000000000002", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000002", OccurrenceID: "occurrence-0000000000000003", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("left"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 320, DemandedNanos: 340, CompletedNanos: 380, PhysicalExecutionID: "physical-0000000000000002"},
			{LogicalRequestID: "logical-0000000000000004", RunID: "run-0000000000000002", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000003", OccurrenceID: "occurrence-0000000000000004", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("right"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 325, DemandedNanos: 345, CompletedNanos: 385, PhysicalExecutionID: "physical-0000000000000003"},
			{LogicalRequestID: "logical-0000000000000005", RunID: "run-0000000000000002", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000004", OccurrenceID: "occurrence-0000000000000005", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("left"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 330, DemandedNanos: 350, CompletedNanos: 380, PhysicalExecutionID: "physical-0000000000000002"},
			{LogicalRequestID: "logical-0000000000000006", RunID: "run-0000000000000002", WorkflowID: "workflow-0000000000000001", WorkflowNodeID: "node-0000000000000005", OccurrenceID: "occurrence-0000000000000006", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("live"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, QualifiedNanos: 600, DemandedNanos: 620, CompletedNanos: 700, PhysicalExecutionID: "physical-0000000000000004"},
		},
		PhysicalExecutions: []observe.PhysicalExecution{
			{PhysicalExecutionID: "physical-0000000000000001", RunID: "run-0000000000000001", ProducerLogicalRequestID: "logical-0000000000000001", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: identity, AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, StartedNanos: 90, EndedNanos: 200, TerminalDisposition: "succeeded", ResultSHA256: d("result"), Consumers: []string{"logical-0000000000000001", "logical-0000000000000002"}},
			{PhysicalExecutionID: "physical-0000000000000002", RunID: "run-0000000000000002", ProducerLogicalRequestID: "logical-0000000000000003", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("left"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, StartedNanos: 345, EndedNanos: 380, TerminalDisposition: "succeeded", ResultSHA256: d("left-result"), Consumers: []string{"logical-0000000000000003", "logical-0000000000000005"}},
			{PhysicalExecutionID: "physical-0000000000000003", RunID: "run-0000000000000002", ProducerLogicalRequestID: "logical-0000000000000004", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("right"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, StartedNanos: 336, EndedNanos: 385, TerminalDisposition: "succeeded", ResultSHA256: d("right-result"), Consumers: []string{"logical-0000000000000004"}},
			{PhysicalExecutionID: "physical-0000000000000004", RunID: "run-0000000000000002", ProducerLogicalRequestID: "logical-0000000000000006", Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: d("live"), AuthoritySHA256: authority, FreshnessSHA256: freshness, PrivacySHA256: privacy, StartedNanos: 625, EndedNanos: 700, TerminalDisposition: "succeeded", ResultSHA256: d("live-result"), Consumers: []string{"logical-0000000000000006"}},
		},
		Decisions: []observe.OptimizationDecision{
			{DecisionID: "decision-0000000000000001", Kind: "preissued", Outcome: "admitted", LogicalRequestIDs: []string{"logical-0000000000000001"}, PhysicalExecutionID: "physical-0000000000000001", ProducerLogicalRequestID: "logical-0000000000000001", EvidenceClass: "host_recorded", Reason: "necessarily_reached_read"},
			{DecisionID: "decision-0000000000000002", Kind: "reused", Outcome: "admitted", LogicalRequestIDs: []string{"logical-0000000000000002"}, PhysicalExecutionID: "physical-0000000000000001", ProducerLogicalRequestID: "logical-0000000000000001", EvidenceClass: "host_recorded", Reason: "fresh_exact_retained_result"},
			{DecisionID: "decision-0000000000000003", Kind: "declared_parallel", Outcome: "admitted", LogicalRequestIDs: []string{"logical-0000000000000003", "logical-0000000000000004"}, DeclarationSHA256: d("declared-independent"), EvidenceClass: "host_recorded", Reason: "declared_independent"},
			{DecisionID: "decision-0000000000000004", Kind: "coalesced", Outcome: "admitted", LogicalRequestIDs: []string{"logical-0000000000000003", "logical-0000000000000005"}, PhysicalExecutionID: "physical-0000000000000002", ProducerLogicalRequestID: "logical-0000000000000003", EvidenceClass: "host_recorded", Reason: "identical_inflight_request"},
			{DecisionID: "decision-0000000000000005", Kind: "preissued", Outcome: "rejected", LogicalRequestIDs: []string{"logical-0000000000000006"}, EvidenceClass: "host_recorded", Reason: "freshness_mismatch"},
		},
		ConsumerAdmitted: false,
	}
}

func digest(value string) string {
	const hex = "0123456789abcdef"
	result := make([]byte, 64)
	for index := range result {
		result[index] = hex[(index+len(value))%len(hex)]
	}
	return "sha256:" + string(result)
}
