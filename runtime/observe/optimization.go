package observe

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	OptimizationSchemaVersion  = "pysolate.workflow-boundary-observation.v0"
	maxOptimizationReportBytes = 2 << 20
	maxOptimizationRuns        = 256
	maxOptimizationItems       = 4096
)

var ErrInvalidOptimizationReport = errors.New("invalid workflow-boundary optimization report")

var optimizationReasonCodes = map[string]bool{
	"necessarily_reached_read":      true,
	"declared_independent":          true,
	"identical_inflight_request":    true,
	"fresh_exact_retained_result":   true,
	"mechanism_disabled":            true,
	"freshness_mismatch":            true,
	"authority_mismatch":            true,
	"privacy_mismatch":              true,
	"identity_mismatch":             true,
	"not_declared_independent":      true,
	"started_or_ambiguous":          true,
	"consumer_default_off":          true,
	"unverified_analysis":           true,
	"call_site_missing":             true,
	"call_not_necessarily_reached":  true,
	"capability_plan_missing":       true,
	"capability_plan_mismatch":      true,
	"capability_unqualified":        true,
	"observation_binding_missing":   true,
	"canonical_arguments_invalid":   true,
	"resource_argument_missing":     true,
	"frozen_context_invalid":        true,
	"speculation_budget_exhausted":  true,
	"qualified_call_invalid":        true,
	"observation_identity_mismatch": true,
	"observation_not_ready":         true,
	"coalescing_contract_missing":   true,
	"cache_contract_missing":        true,
	"backend_contract_missing":      true,
	"whole_run_shape_invalid":       true,
	"whole_run_not_reusable":        true,
}

var optimizationErrorClasses = map[string]bool{
	"capability_failed":    true,
	"guest_failed":         true,
	"host_failed":          true,
	"cancelled":            true,
	"ambiguous_completion": true,
}

type ObservedRun struct {
	RunID                  string `json:"run_id"`
	WorkloadID             string `json:"workload_id"`
	Treatment              string `json:"treatment"`
	Order                  uint32 `json:"order"`
	StartedNanos           uint64 `json:"started_nanos"`
	EndedNanos             uint64 `json:"ended_nanos"`
	TerminalDisposition    string `json:"terminal_disposition"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
}

type ObservedSpan struct {
	SpanID              string `json:"span_id"`
	ParentSpanID        string `json:"parent_span_id"`
	RunID               string `json:"run_id"`
	Kind                string `json:"kind"`
	Label               string `json:"label"`
	EvidenceClass       string `json:"evidence_class"`
	StartedNanos        uint64 `json:"started_nanos"`
	EndedNanos          uint64 `json:"ended_nanos"`
	PhysicalExecutionID string `json:"physical_execution_id"`
	InputSHA256         string `json:"input_sha256"`
	OutputSHA256        string `json:"output_sha256"`
}

type LogicalRequest struct {
	LogicalRequestID       string `json:"logical_request_id"`
	RunID                  string `json:"run_id"`
	WorkflowID             string `json:"workflow_id"`
	WorkflowNodeID         string `json:"workflow_node_id"`
	OccurrenceID           string `json:"occurrence_id"`
	Surface                string `json:"surface"`
	Capability             string `json:"capability"`
	BoundaryIdentitySHA256 string `json:"boundary_identity_sha256"`
	AuthoritySHA256        string `json:"authority_sha256"`
	FreshnessSHA256        string `json:"freshness_sha256"`
	PrivacySHA256          string `json:"privacy_sha256"`
	QualifiedNanos         uint64 `json:"qualified_nanos"`
	DemandedNanos          uint64 `json:"demanded_nanos"`
	CompletedNanos         uint64 `json:"completed_nanos"`
	PhysicalExecutionID    string `json:"physical_execution_id"`
}

type PhysicalExecution struct {
	PhysicalExecutionID      string   `json:"physical_execution_id"`
	RunID                    string   `json:"run_id"`
	ProducerLogicalRequestID string   `json:"producer_logical_request_id"`
	Surface                  string   `json:"surface"`
	Capability               string   `json:"capability"`
	BoundaryIdentitySHA256   string   `json:"boundary_identity_sha256"`
	AuthoritySHA256          string   `json:"authority_sha256"`
	FreshnessSHA256          string   `json:"freshness_sha256"`
	PrivacySHA256            string   `json:"privacy_sha256"`
	StartedNanos             uint64   `json:"started_nanos"`
	EndedNanos               uint64   `json:"ended_nanos"`
	TerminalDisposition      string   `json:"terminal_disposition"`
	ResultSHA256             string   `json:"result_sha256"`
	ErrorClass               string   `json:"error_class"`
	Consumers                []string `json:"consumers"`
}

type OptimizationDecision struct {
	DecisionID               string   `json:"decision_id"`
	Kind                     string   `json:"kind"`
	Outcome                  string   `json:"outcome"`
	LogicalRequestIDs        []string `json:"logical_request_ids"`
	PhysicalExecutionID      string   `json:"physical_execution_id"`
	ProducerLogicalRequestID string   `json:"producer_logical_request_id"`
	DeclarationSHA256        string   `json:"declaration_sha256"`
	EvidenceClass            string   `json:"evidence_class"`
	Reason                   string   `json:"reason"`
}

type OptimizationReport struct {
	SchemaVersion          string                 `json:"schema_version"`
	StudyID                string                 `json:"study_id"`
	WorkloadManifestSHA256 string                 `json:"workload_manifest_sha256"`
	ShuffleSeed            uint64                 `json:"shuffle_seed"`
	ClockPolicy            string                 `json:"clock_policy"`
	Runs                   []ObservedRun          `json:"runs"`
	Spans                  []ObservedSpan         `json:"spans"`
	LogicalRequests        []LogicalRequest       `json:"logical_requests"`
	PhysicalExecutions     []PhysicalExecution    `json:"physical_executions"`
	Decisions              []OptimizationDecision `json:"decisions"`
	ConsumerAdmitted       bool                   `json:"consumer_admitted"`
	SealSHA256             string                 `json:"seal_sha256"`
}

type verifiedOptimizationState struct{ report OptimizationReport }

type VerifiedOptimizationReport struct{ state *verifiedOptimizationState }

func BuildOptimizationReport(report OptimizationReport) (VerifiedOptimizationReport, error) {
	if report.SealSHA256 != "" || validateOptimizationReport(report, false) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	cloned, err := cloneOptimizationReport(report)
	if err != nil {
		return VerifiedOptimizationReport{}, err
	}
	seal, err := optimizationSeal(cloned)
	if err != nil {
		return VerifiedOptimizationReport{}, err
	}
	cloned.SealSHA256 = seal
	if validateOptimizationReport(cloned, true) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	encoded, err := json.Marshal(cloned)
	if err != nil || len(encoded) > maxOptimizationReportBytes || rejectDuplicateJSON(encoded) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	return VerifiedOptimizationReport{state: &verifiedOptimizationState{report: cloned}}, nil
}

func (verified VerifiedOptimizationReport) Report() (OptimizationReport, error) {
	if verified.state == nil || validateOptimizationReport(verified.state.report, true) != nil {
		return OptimizationReport{}, ErrInvalidOptimizationReport
	}
	return cloneOptimizationReport(verified.state.report)
}

func EncodeOptimizationReport(verified VerifiedOptimizationReport) ([]byte, error) {
	report, err := verified.Report()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maxOptimizationReportBytes {
		return nil, ErrInvalidOptimizationReport
	}
	return encoded, nil
}

func DecodeOptimizationReport(raw []byte) (VerifiedOptimizationReport, error) {
	if len(raw) == 0 || len(raw) > maxOptimizationReportBytes || rejectDuplicateJSON(raw) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report OptimizationReport
	if decoder.Decode(&report) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || validateOptimizationReport(report, true) != nil {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	canonical, err := json.Marshal(report)
	if err != nil || !bytes.Equal(canonical, raw) {
		return VerifiedOptimizationReport{}, ErrInvalidOptimizationReport
	}
	cloned, err := cloneOptimizationReport(report)
	if err != nil {
		return VerifiedOptimizationReport{}, err
	}
	return VerifiedOptimizationReport{state: &verifiedOptimizationState{report: cloned}}, nil
}

func validateOptimizationReport(report OptimizationReport, sealed bool) error {
	if report.SchemaVersion != OptimizationSchemaVersion || !opaqueIdentifier(report.StudyID, "study") ||
		!digest.MatchString(report.WorkloadManifestSHA256) || report.ShuffleSeed == 0 ||
		report.ClockPolicy != "study_relative_monotonic_nanos" || report.ConsumerAdmitted ||
		report.Runs == nil || report.Spans == nil || report.LogicalRequests == nil ||
		report.PhysicalExecutions == nil || report.Decisions == nil ||
		len(report.Runs) == 0 || len(report.Runs) > maxOptimizationRuns ||
		len(report.Spans) > maxOptimizationItems || len(report.LogicalRequests) > maxOptimizationItems ||
		len(report.PhysicalExecutions) > maxOptimizationItems || len(report.Decisions) > maxOptimizationItems {
		return ErrInvalidOptimizationReport
	}
	if sealed {
		expected, err := optimizationSeal(report)
		if err != nil || report.SealSHA256 != expected {
			return ErrInvalidOptimizationReport
		}
	} else if report.SealSHA256 != "" {
		return ErrInvalidOptimizationReport
	}
	runs := make(map[string]ObservedRun, len(report.Runs))
	for index, run := range report.Runs {
		if !validRun(run, uint32(index+1)) || (index > 0 && report.Runs[index-1].RunID >= run.RunID) {
			return ErrInvalidOptimizationReport
		}
		runs[run.RunID] = run
	}
	spans := make(map[string]ObservedSpan, len(report.Spans))
	for index, span := range report.Spans {
		run, ok := runs[span.RunID]
		if !ok || !validSpan(span, run) || (index > 0 && !spanAfter(report.Spans[index-1], span)) {
			return ErrInvalidOptimizationReport
		}
		if _, exists := spans[span.SpanID]; exists {
			return ErrInvalidOptimizationReport
		}
		if span.ParentSpanID != "" {
			parent, exists := spans[span.ParentSpanID]
			if !exists || parent.RunID != span.RunID || parent.StartedNanos > span.StartedNanos {
				return ErrInvalidOptimizationReport
			}
		}
		spans[span.SpanID] = span
	}
	requests := make(map[string]LogicalRequest, len(report.LogicalRequests))
	requestsByPhysical := make(map[string][]string)
	for index, request := range report.LogicalRequests {
		run, ok := runs[request.RunID]
		if !ok || !validLogicalRequest(request, run) || (index > 0 && report.LogicalRequests[index-1].LogicalRequestID >= request.LogicalRequestID) {
			return ErrInvalidOptimizationReport
		}
		requests[request.LogicalRequestID] = request
		requestsByPhysical[request.PhysicalExecutionID] = append(requestsByPhysical[request.PhysicalExecutionID], request.LogicalRequestID)
	}
	physical := make(map[string]PhysicalExecution, len(report.PhysicalExecutions))
	for index, execution := range report.PhysicalExecutions {
		run, ok := runs[execution.RunID]
		producer, producerOK := requests[execution.ProducerLogicalRequestID]
		if !ok || !producerOK || !validPhysicalExecution(execution, run) || producer.RunID != execution.RunID ||
			producer.PhysicalExecutionID != execution.PhysicalExecutionID || producer.BoundaryIdentitySHA256 != execution.BoundaryIdentitySHA256 ||
			producer.Surface != execution.Surface || producer.Capability != execution.Capability ||
			producer.AuthoritySHA256 != execution.AuthoritySHA256 || producer.FreshnessSHA256 != execution.FreshnessSHA256 ||
			producer.PrivacySHA256 != execution.PrivacySHA256 ||
			!slices.Equal(execution.Consumers, requestsByPhysical[execution.PhysicalExecutionID]) ||
			(index > 0 && report.PhysicalExecutions[index-1].PhysicalExecutionID >= execution.PhysicalExecutionID) {
			return ErrInvalidOptimizationReport
		}
		physical[execution.PhysicalExecutionID] = execution
	}
	for _, request := range report.LogicalRequests {
		execution, ok := physical[request.PhysicalExecutionID]
		requestRun := runs[request.RunID]
		producerRun := runs[execution.RunID]
		if !ok || request.BoundaryIdentitySHA256 != execution.BoundaryIdentitySHA256 || request.Surface != execution.Surface ||
			request.Capability != execution.Capability || request.AuthoritySHA256 != execution.AuthoritySHA256 ||
			request.FreshnessSHA256 != execution.FreshnessSHA256 || request.PrivacySHA256 != execution.PrivacySHA256 ||
			requestRun.ArtifactSHA256 != producerRun.ArtifactSHA256 || requestRun.ExecutionProfileSHA256 != producerRun.ExecutionProfileSHA256 ||
			requestRun.CapabilityPlanSHA256 != producerRun.CapabilityPlanSHA256 || execution.EndedNanos > request.CompletedNanos {
			return ErrInvalidOptimizationReport
		}
	}
	physicalSpans := make(map[string]int, len(physical))
	for _, span := range report.Spans {
		if span.PhysicalExecutionID != "" {
			execution, ok := physical[span.PhysicalExecutionID]
			expectedKind := "host." + execution.Surface
			expectedOutput := execution.ResultSHA256
			if execution.TerminalDisposition != "succeeded" {
				expectedOutput = ""
			}
			if !ok || span.EvidenceClass != "measured" || span.Kind != expectedKind || span.RunID != execution.RunID ||
				span.StartedNanos != execution.StartedNanos || span.EndedNanos != execution.EndedNanos ||
				span.InputSHA256 != execution.BoundaryIdentitySHA256 || span.OutputSHA256 != expectedOutput {
				return ErrInvalidOptimizationReport
			}
			physicalSpans[execution.PhysicalExecutionID]++
		}
	}
	for id := range physical {
		if physicalSpans[id] != 1 {
			return ErrInvalidOptimizationReport
		}
	}
	decisionKeys := map[string]bool{}
	sharingEvidence := map[string]uint8{}
	for index, decision := range report.Decisions {
		key := decision.Kind + "\x00" + decision.Outcome + "\x00" + fmt.Sprint(decision.LogicalRequestIDs)
		if decisionKeys[key] || !validDecision(decision, requests, physical) || (index > 0 && report.Decisions[index-1].DecisionID >= decision.DecisionID) {
			return ErrInvalidOptimizationReport
		}
		decisionKeys[key] = true
		if decision.Outcome == "admitted" && (decision.Kind == "coalesced" || decision.Kind == "reused") {
			execution := physical[requests[decision.LogicalRequestIDs[0]].PhysicalExecutionID]
			for _, logicalID := range decision.LogicalRequestIDs {
				if logicalID != execution.ProducerLogicalRequestID {
					sharingEvidence[logicalID]++
				}
			}
		}
	}
	for _, execution := range report.PhysicalExecutions {
		for _, logicalID := range execution.Consumers {
			if logicalID != execution.ProducerLogicalRequestID && sharingEvidence[logicalID] != 1 {
				return ErrInvalidOptimizationReport
			}
		}
	}
	return nil
}

func validRun(run ObservedRun, order uint32) bool {
	return opaqueIdentifier(run.RunID, "run") && opaqueIdentifier(run.WorkloadID, "workload") &&
		(run.Treatment == "baseline" || run.Treatment == "optimized") && run.Order == order &&
		run.StartedNanos < run.EndedNanos &&
		(run.TerminalDisposition == "succeeded" || run.TerminalDisposition == "failed" || run.TerminalDisposition == "cancelled") &&
		digest.MatchString(run.ArtifactSHA256) && digest.MatchString(run.ExecutionProfileSHA256) && digest.MatchString(run.CapabilityPlanSHA256)
}

func validSpan(span ObservedSpan, run ObservedRun) bool {
	labels := map[string]string{
		"model.invocation": "model invocation",
		"model.output":     "model output",
		"guest.wasm":       "WASM execution",
		"host.tool":        "typed tool execution",
		"host.wasi":        "typed WASI execution",
	}
	expected, ok := labels[span.Kind]
	if !ok || span.Label != expected || !opaqueIdentifier(span.SpanID, "span") ||
		(span.ParentSpanID != "" && !opaqueIdentifier(span.ParentSpanID, "span")) ||
		span.StartedNanos < run.StartedNanos || span.EndedNanos > run.EndedNanos || span.StartedNanos > span.EndedNanos ||
		!slices.Contains([]string{"measured", "replayed"}, span.EvidenceClass) ||
		(span.InputSHA256 != "" && !digest.MatchString(span.InputSHA256)) || (span.OutputSHA256 != "" && !digest.MatchString(span.OutputSHA256)) {
		return false
	}
	if span.Kind == "host.tool" || span.Kind == "host.wasi" {
		return opaqueIdentifier(span.PhysicalExecutionID, "physical")
	}
	return span.PhysicalExecutionID == ""
}

func spanAfter(previous, current ObservedSpan) bool {
	return previous.StartedNanos < current.StartedNanos || (previous.StartedNanos == current.StartedNanos && previous.SpanID < current.SpanID)
}

func validLogicalRequest(request LogicalRequest, run ObservedRun) bool {
	return opaqueIdentifier(request.LogicalRequestID, "logical") && opaqueIdentifier(request.WorkflowID, "workflow") &&
		opaqueIdentifier(request.WorkflowNodeID, "node") && opaqueIdentifier(request.OccurrenceID, "occurrence") &&
		validBoundary(request.Surface, request.Capability, request.BoundaryIdentitySHA256) &&
		digest.MatchString(request.AuthoritySHA256) && digest.MatchString(request.FreshnessSHA256) && digest.MatchString(request.PrivacySHA256) &&
		run.StartedNanos <= request.QualifiedNanos && request.QualifiedNanos <= request.DemandedNanos &&
		request.DemandedNanos <= request.CompletedNanos && request.CompletedNanos <= run.EndedNanos &&
		opaqueIdentifier(request.PhysicalExecutionID, "physical")
}

func validPhysicalExecution(execution PhysicalExecution, run ObservedRun) bool {
	if !opaqueIdentifier(execution.PhysicalExecutionID, "physical") || !opaqueIdentifier(execution.ProducerLogicalRequestID, "logical") ||
		!validBoundary(execution.Surface, execution.Capability, execution.BoundaryIdentitySHA256) ||
		!digest.MatchString(execution.AuthoritySHA256) || !digest.MatchString(execution.FreshnessSHA256) || !digest.MatchString(execution.PrivacySHA256) ||
		run.StartedNanos > execution.StartedNanos || execution.StartedNanos >= execution.EndedNanos || execution.EndedNanos > run.EndedNanos ||
		execution.Consumers == nil || len(execution.Consumers) == 0 || !sortedUniqueOpaqueIDs(execution.Consumers, "logical") {
		return false
	}
	switch execution.TerminalDisposition {
	case "succeeded":
		return digest.MatchString(execution.ResultSHA256) && execution.ErrorClass == ""
	case "failed":
		return execution.ResultSHA256 == "" && optimizationErrorClasses[execution.ErrorClass]
	case "cancelled":
		return execution.ResultSHA256 == "" && execution.ErrorClass == "cancelled"
	case "ambiguous":
		return execution.ResultSHA256 == "" && execution.ErrorClass == "ambiguous_completion"
	default:
		return false
	}
}

func validBoundary(surface, capability, identitySHA string) bool {
	return (surface == "tool" || surface == "wasi") && capabilityName.MatchString(capability) && digest.MatchString(identitySHA)
}

func validDecision(decision OptimizationDecision, requests map[string]LogicalRequest, physical map[string]PhysicalExecution) bool {
	if !opaqueIdentifier(decision.DecisionID, "decision") || !optimizationReasonCodes[decision.Reason] ||
		decision.LogicalRequestIDs == nil || len(decision.LogicalRequestIDs) == 0 || !sortedUniqueOpaqueIDs(decision.LogicalRequestIDs, "logical") ||
		decision.EvidenceClass != "host_recorded" ||
		!slices.Contains([]string{"preissued", "declared_parallel", "coalesced", "reused"}, decision.Kind) ||
		(decision.Outcome != "admitted" && decision.Outcome != "rejected") {
		return false
	}
	for _, id := range decision.LogicalRequestIDs {
		if _, ok := requests[id]; !ok {
			return false
		}
	}
	if decision.Outcome == "rejected" {
		return decision.PhysicalExecutionID == "" && decision.ProducerLogicalRequestID == "" && decision.DeclarationSHA256 == ""
	}
	admittedReason := map[string]string{
		"preissued":         "necessarily_reached_read",
		"declared_parallel": "declared_independent",
		"coalesced":         "identical_inflight_request",
		"reused":            "fresh_exact_retained_result",
	}
	if decision.Reason != admittedReason[decision.Kind] {
		return false
	}
	switch decision.Kind {
	case "preissued":
		if len(decision.LogicalRequestIDs) != 1 || decision.DeclarationSHA256 != "" {
			return false
		}
		request := requests[decision.LogicalRequestIDs[0]]
		execution, ok := physical[decision.PhysicalExecutionID]
		return ok && decision.ProducerLogicalRequestID == request.LogicalRequestID && execution.ProducerLogicalRequestID == request.LogicalRequestID &&
			request.PhysicalExecutionID == execution.PhysicalExecutionID && request.QualifiedNanos < execution.StartedNanos && execution.StartedNanos < request.DemandedNanos
	case "reused":
		if len(decision.LogicalRequestIDs) != 1 || decision.DeclarationSHA256 != "" {
			return false
		}
		request := requests[decision.LogicalRequestIDs[0]]
		execution, ok := physical[decision.PhysicalExecutionID]
		return ok && execution.TerminalDisposition == "succeeded" && decision.ProducerLogicalRequestID == execution.ProducerLogicalRequestID && decision.ProducerLogicalRequestID != request.LogicalRequestID &&
			request.PhysicalExecutionID == execution.PhysicalExecutionID && execution.EndedNanos < request.DemandedNanos
	case "coalesced":
		if len(decision.LogicalRequestIDs) < 2 || decision.DeclarationSHA256 != "" {
			return false
		}
		execution, ok := physical[decision.PhysicalExecutionID]
		producer := requests[decision.ProducerLogicalRequestID]
		if !ok || decision.ProducerLogicalRequestID != execution.ProducerLogicalRequestID ||
			!slices.Contains(decision.LogicalRequestIDs, decision.ProducerLogicalRequestID) || producer.DemandedNanos > execution.StartedNanos {
			return false
		}
		for _, id := range decision.LogicalRequestIDs {
			request := requests[id]
			if request.PhysicalExecutionID != execution.PhysicalExecutionID ||
				(id != decision.ProducerLogicalRequestID && (request.DemandedNanos < execution.StartedNanos || request.DemandedNanos >= execution.EndedNanos)) {
				return false
			}
		}
		return true
	case "declared_parallel":
		if len(decision.LogicalRequestIDs) < 2 || !digest.MatchString(decision.DeclarationSHA256) || decision.PhysicalExecutionID != "" || decision.ProducerLogicalRequestID != "" {
			return false
		}
		var latestStart uint64
		earliestEnd := ^uint64(0)
		seenPhysical := map[string]bool{}
		var declarationRun, declarationWorkflow string
		for _, id := range decision.LogicalRequestIDs {
			request := requests[id]
			if declarationRun == "" {
				declarationRun, declarationWorkflow = request.RunID, request.WorkflowID
			} else if request.RunID != declarationRun || request.WorkflowID != declarationWorkflow {
				return false
			}
			execution := physical[request.PhysicalExecutionID]
			if seenPhysical[execution.PhysicalExecutionID] {
				return false
			}
			seenPhysical[execution.PhysicalExecutionID] = true
			latestStart = max(latestStart, execution.StartedNanos)
			earliestEnd = min(earliestEnd, execution.EndedNanos)
		}
		return latestStart < earliestEnd
	default:
		return false
	}
}

func sortedUniqueOpaqueIDs(values []string, prefix string) bool {
	for index, value := range values {
		if !opaqueIdentifier(value, prefix) || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func opaqueIdentifier(value, prefix string) bool {
	expected := prefix + "-"
	if !strings.HasPrefix(value, expected) {
		return false
	}
	suffix := value[len(expected):]
	if len(suffix) < 16 || len(suffix) > 64 {
		return false
	}
	for _, character := range suffix {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func optimizationSeal(report OptimizationReport) (string, error) {
	report.SealSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maxOptimizationReportBytes {
		return "", ErrInvalidOptimizationReport
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

func cloneOptimizationReport(report OptimizationReport) (OptimizationReport, error) {
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maxOptimizationReportBytes {
		return OptimizationReport{}, ErrInvalidOptimizationReport
	}
	var cloned OptimizationReport
	if json.Unmarshal(encoded, &cloned) != nil {
		return OptimizationReport{}, ErrInvalidOptimizationReport
	}
	return cloned, nil
}
