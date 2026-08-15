package workflowbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

const EvidenceSchemaVersion = "pysolate.workflow-benchmark-evidence.v0"

var ErrExperiment = errors.New("workflow benchmark failed")

type WASMFunc func(context.Context, Task) (string, error)

type TaskMetrics struct {
	TaskID                      string `json:"task_id"`
	WorkloadID                  string `json:"workload_id"`
	Class                       string `json:"class"`
	NegativeDimension           string `json:"negative_dimension,omitempty"`
	BaselineDurationNanos       uint64 `json:"baseline_duration_nanos"`
	OptimizedDurationNanos      uint64 `json:"optimized_duration_nanos"`
	BaselinePhysicalExecutions  uint32 `json:"baseline_physical_executions"`
	OptimizedPhysicalExecutions uint32 `json:"optimized_physical_executions"`
	AdmittedDecisions           uint32 `json:"admitted_decisions"`
	RejectedDecisions           uint32 `json:"rejected_decisions"`
	OutputEquivalent            bool   `json:"output_equivalent"`
	EffectsEquivalent           bool   `json:"effects_equivalent"`
	EvidenceComplete            bool   `json:"evidence_complete"`
	ObservationSealSHA256       string `json:"observation_seal_sha256"`
}

type Evidence struct {
	SchemaVersion               string            `json:"schema_version"`
	Manifest                    json.RawMessage   `json:"manifest"`
	Reports                     []json.RawMessage `json:"reports"`
	Tasks                       []TaskMetrics     `json:"tasks"`
	Divergences                 uint32            `json:"divergences"`
	BaselinePhysicalExecutions  uint32            `json:"baseline_physical_executions"`
	OptimizedPhysicalExecutions uint32            `json:"optimized_physical_executions"`
	SealSHA256                  string            `json:"seal_sha256"`
}

type treatmentResult struct {
	run       observe.ObservedRun
	spans     []observe.ObservedSpan
	logical   []observe.LogicalRequest
	physical  []observe.PhysicalExecution
	decisions []observe.OptimizationDecision
	duration  uint64
	output    string
	effects   string
}

type measuredTool struct {
	physical observe.PhysicalExecution
	span     observe.ObservedSpan
}

func ExecutePair(ctx context.Context, manifest Manifest, wasm WASMFunc) (Evidence, error) {
	if manifest.Validate() != nil || wasm == nil {
		return Evidence{}, ErrExperiment
	}
	manifestJSON, err := EncodeManifest(manifest)
	if err != nil {
		return Evidence{}, ErrExperiment
	}
	manifestSHA := digest(string(manifestJSON))
	evidence := Evidence{
		SchemaVersion: EvidenceSchemaVersion, Manifest: append(json.RawMessage(nil), manifestJSON...),
		Reports: []json.RawMessage{}, Tasks: []TaskMetrics{},
	}
	for _, task := range manifest.Tasks {
		origin := time.Now()
		baseline, err := executeTreatment(ctx, manifest, task, "baseline", 1, origin, wasm)
		if err != nil {
			return Evidence{}, err
		}
		optimized, err := executeTreatment(ctx, manifest, task, "optimized", 2, origin, wasm)
		if err != nil {
			return Evidence{}, err
		}
		report := observe.OptimizationReport{
			SchemaVersion: observe.OptimizationSchemaVersion,
			StudyID:       opaqueObservation("study", task.TaskID), WorkloadManifestSHA256: manifestSHA,
			ShuffleSeed: manifest.Seed, ClockPolicy: "study_relative_monotonic_nanos",
			Runs:               []observe.ObservedRun{baseline.run, optimized.run},
			Spans:              append(append([]observe.ObservedSpan{}, baseline.spans...), optimized.spans...),
			LogicalRequests:    append(append([]observe.LogicalRequest{}, baseline.logical...), optimized.logical...),
			PhysicalExecutions: append(append([]observe.PhysicalExecution{}, baseline.physical...), optimized.physical...),
			Decisions:          append([]observe.OptimizationDecision{}, optimized.decisions...), ConsumerAdmitted: false,
		}
		sort.Slice(report.Spans, func(i, j int) bool {
			return report.Spans[i].StartedNanos < report.Spans[j].StartedNanos ||
				(report.Spans[i].StartedNanos == report.Spans[j].StartedNanos && report.Spans[i].SpanID < report.Spans[j].SpanID)
		})
		sort.Slice(report.LogicalRequests, func(i, j int) bool {
			return report.LogicalRequests[i].LogicalRequestID < report.LogicalRequests[j].LogicalRequestID
		})
		sort.Slice(report.PhysicalExecutions, func(i, j int) bool {
			return report.PhysicalExecutions[i].PhysicalExecutionID < report.PhysicalExecutions[j].PhysicalExecutionID
		})
		sort.Slice(report.Decisions, func(i, j int) bool { return report.Decisions[i].DecisionID < report.Decisions[j].DecisionID })
		verified, err := observe.BuildOptimizationReport(report)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: observation %s class=%s: %v", ErrExperiment, task.TaskID, task.Class, err)
		}
		raw, err := observe.EncodeOptimizationReport(verified)
		if err != nil {
			return Evidence{}, ErrExperiment
		}
		sealed, _ := verified.Report()
		metrics := TaskMetrics{
			TaskID: task.TaskID, WorkloadID: task.WorkloadID, Class: task.Class, NegativeDimension: task.NegativeDimension,
			BaselineDurationNanos: baseline.duration, OptimizedDurationNanos: optimized.duration,
			BaselinePhysicalExecutions: uint32(len(baseline.physical)), OptimizedPhysicalExecutions: uint32(len(optimized.physical)),
			OutputEquivalent:  baseline.output == optimized.output,
			EffectsEquivalent: baseline.effects == optimized.effects, EvidenceComplete: true, ObservationSealSHA256: sealed.SealSHA256,
		}
		for _, decision := range optimized.decisions {
			if decision.Outcome == "admitted" {
				metrics.AdmittedDecisions++
			} else {
				metrics.RejectedDecisions++
			}
		}
		if !metrics.OutputEquivalent || !metrics.EffectsEquivalent {
			evidence.Divergences++
		}
		evidence.BaselinePhysicalExecutions += metrics.BaselinePhysicalExecutions
		evidence.OptimizedPhysicalExecutions += metrics.OptimizedPhysicalExecutions
		evidence.Reports = append(evidence.Reports, append(json.RawMessage(nil), raw...))
		evidence.Tasks = append(evidence.Tasks, metrics)
	}
	evidence.SealSHA256 = evidenceSeal(evidence)
	if evidence.Validate() != nil {
		return Evidence{}, ErrExperiment
	}
	return evidence, nil
}

func executeTreatment(ctx context.Context, manifest Manifest, task Task, treatment string, order uint32, origin time.Time, wasm WASMFunc) (treatmentResult, error) {
	result := treatmentResult{spans: []observe.ObservedSpan{}, logical: []observe.LogicalRequest{}, physical: []observe.PhysicalExecution{}, decisions: []observe.OptimizationDecision{}}
	runID := fmt.Sprintf("run-%016x", order)
	workflowID := opaqueObservation("workflow", task.TaskID)
	runStarted := elapsed(origin)
	if runStarted == 0 {
		time.Sleep(time.Nanosecond)
		runStarted = elapsed(origin)
	}
	toolNodes := toolNodes(task)
	if treatment == "optimized" && task.Class == "preissue" {
		node := toolNodes[0]
		logicalID := logicalID(treatment, task, 0)
		qualified := elapsed(origin)
		time.Sleep(100 * time.Microsecond)
		physicalID := physicalID(treatment, task, 0)
		started, completed := startTool(ctx, origin, runID, physicalID, logicalID, node, []string{logicalID})
		<-started
		appendModelSpan(ctx, &result, origin, runID, task)
		demanded := elapsed(origin)
		outcome := <-completed
		if outcome.err != nil {
			return treatmentResult{}, outcome.err
		}
		measurement := outcome.measurement
		result.physical = append(result.physical, measurement.physical)
		result.spans = append(result.spans, measurement.span)
		result.logical = append(result.logical, logicalRequest(task, node, runID, workflowID, logicalID, physicalID, qualified, demanded, measurement.physical.EndedNanos))
		result.decisions = append(result.decisions, admittedDecision(task, "preissued", []string{logicalID}, physicalID, logicalID, "necessarily_reached_read"))
	} else {
		appendModelSpan(ctx, &result, origin, runID, task)
		if err := executeTools(ctx, &result, origin, runID, workflowID, task, treatment, toolNodes); err != nil {
			return treatmentResult{}, err
		}
	}
	appendOutputSpan(ctx, &result, origin, runID, task)
	wasmStarted := elapsed(origin)
	wasmOutput, err := wasm(ctx, task)
	if err != nil || !digestPattern.MatchString(wasmOutput) {
		return treatmentResult{}, fmt.Errorf("%w: WASM fixture output", ErrExperiment)
	}
	wasmEnded := elapsed(origin)
	result.spans = append(result.spans, observe.ObservedSpan{
		SpanID: opaqueObservation("span", treatment+":"+task.TaskID+":wasm"), RunID: runID,
		Kind: "guest.wasm", Label: "WASM execution", EvidenceClass: "measured",
		StartedNanos: wasmStarted, EndedNanos: wasmEnded,
		InputSHA256: manifest.Identity.ArtifactSHA256, OutputSHA256: wasmOutput,
	})
	runEnded := elapsed(origin)
	result.run = observe.ObservedRun{
		RunID: runID, WorkloadID: task.WorkloadID, Treatment: treatment, Order: order,
		StartedNanos: runStarted, EndedNanos: runEnded, TerminalDisposition: "succeeded",
		ArtifactSHA256:         manifest.Identity.ArtifactSHA256,
		ExecutionProfileSHA256: manifest.Identity.ExecutionProfileSHA256,
		CapabilityPlanSHA256:   manifest.Identity.CapabilityPlanSHA256,
	}
	result.duration = runEnded - runStarted
	physicalByID := make(map[string]observe.PhysicalExecution, len(result.physical))
	for _, execution := range result.physical {
		physicalByID[execution.PhysicalExecutionID] = execution
	}
	logicalByNode := make(map[string]observe.LogicalRequest, len(result.logical))
	for _, request := range result.logical {
		logicalByNode[request.WorkflowNodeID] = request
	}
	parts := []byte(task.TaskID)
	for _, node := range task.Nodes {
		if node.Kind != "tool.read" {
			continue
		}
		request, ok := logicalByNode[node.NodeID]
		execution, physicalOK := physicalByID[request.PhysicalExecutionID]
		if !ok || !physicalOK || execution.ResultSHA256 != node.ResultSHA256 {
			return treatmentResult{}, fmt.Errorf("%w: tool result oracle", ErrExperiment)
		}
		parts = append(parts, execution.ResultSHA256...)
	}
	toolOutput := digest(string(parts))
	if toolOutput != task.ExpectedOutputSHA256 {
		return treatmentResult{}, fmt.Errorf("%w: manifest output oracle", ErrExperiment)
	}
	result.output = digest(toolOutput + "\x00" + wasmOutput + "\x00" + digest("model-fixture:"+task.TaskID))
	result.effects = digest("read-only-no-external-effects:" + task.TaskID)
	return result, nil
}

func executeTools(ctx context.Context, result *treatmentResult, origin time.Time, runID, workflowID string, task Task, treatment string, nodes []Node) error {
	if treatment == "optimized" {
		switch task.Class {
		case "declared_parallel":
			logicalIDs := []string{logicalID(treatment, task, 0), logicalID(treatment, task, 1)}
			type pending struct {
				node       Node
				logicalID  string
				physicalID string
				qualified  uint64
				demanded   uint64
				completed  <-chan toolOutcome
			}
			pendingTools := make([]pending, 0, 2)
			for index, node := range nodes {
				qualified := elapsed(origin)
				demanded := elapsed(origin)
				physical := physicalID(treatment, task, index)
				_, completed := startTool(ctx, origin, runID, physical, logicalIDs[index], node, []string{logicalIDs[index]})
				pendingTools = append(pendingTools, pending{node: node, logicalID: logicalIDs[index], physicalID: physical, qualified: qualified, demanded: demanded, completed: completed})
			}
			for _, pendingTool := range pendingTools {
				outcome := <-pendingTool.completed
				if outcome.err != nil {
					return outcome.err
				}
				measurement := outcome.measurement
				result.physical = append(result.physical, measurement.physical)
				result.spans = append(result.spans, measurement.span)
				result.logical = append(result.logical, logicalRequest(task, pendingTool.node, runID, workflowID, pendingTool.logicalID, pendingTool.physicalID, pendingTool.qualified, pendingTool.demanded, measurement.physical.EndedNanos))
			}
			sort.Strings(logicalIDs)
			result.decisions = append(result.decisions, admittedDecision(task, "declared_parallel", logicalIDs, "", "", "declared_independent"))
			return nil
		case "coalesced":
			producer := logicalID(treatment, task, 0)
			follower := logicalID(treatment, task, 1)
			physical := physicalID(treatment, task, 0)
			qualifiedProducer := elapsed(origin)
			demandedProducer := elapsed(origin)
			consumers := []string{producer, follower}
			sort.Strings(consumers)
			started, completed := startTool(ctx, origin, runID, physical, producer, nodes[0], consumers)
			<-started
			time.Sleep(100 * time.Microsecond)
			qualifiedFollower := elapsed(origin)
			demandedFollower := elapsed(origin)
			outcome := <-completed
			if outcome.err != nil {
				return outcome.err
			}
			measurement := outcome.measurement
			result.physical = append(result.physical, measurement.physical)
			result.spans = append(result.spans, measurement.span)
			result.logical = append(result.logical,
				logicalRequest(task, nodes[0], runID, workflowID, producer, physical, qualifiedProducer, demandedProducer, measurement.physical.EndedNanos),
				logicalRequest(task, nodes[1], runID, workflowID, follower, physical, qualifiedFollower, demandedFollower, measurement.physical.EndedNanos),
			)
			result.decisions = append(result.decisions, admittedDecision(task, "coalesced", consumers, physical, producer, "identical_inflight_request"))
			return nil
		case "retained_reuse":
			producer := logicalID(treatment, task, 0)
			consumer := logicalID(treatment, task, 1)
			physical := physicalID(treatment, task, 0)
			qualified := elapsed(origin)
			demanded := elapsed(origin)
			measurement, err := runTool(ctx, origin, runID, physical, producer, nodes[0], []string{consumer, producer})
			if err != nil {
				return err
			}
			result.physical = append(result.physical, measurement.physical)
			result.spans = append(result.spans, measurement.span)
			result.logical = append(result.logical, logicalRequest(task, nodes[0], runID, workflowID, producer, physical, qualified, demanded, measurement.physical.EndedNanos))
			time.Sleep(100 * time.Microsecond)
			reuseDemand := elapsed(origin)
			result.logical = append(result.logical, logicalRequest(task, nodes[1], runID, workflowID, consumer, physical, reuseDemand, reuseDemand, reuseDemand))
			result.decisions = append(result.decisions, admittedDecision(task, "reused", []string{consumer}, physical, producer, "fresh_exact_retained_result"))
			return nil
		}
	}
	for index, node := range nodes {
		logical := logicalID(treatment, task, index)
		physical := physicalID(treatment, task, index)
		qualified := elapsed(origin)
		demanded := elapsed(origin)
		measurement, err := runTool(ctx, origin, runID, physical, logical, node, []string{logical})
		if err != nil {
			return err
		}
		result.physical = append(result.physical, measurement.physical)
		result.spans = append(result.spans, measurement.span)
		result.logical = append(result.logical, logicalRequest(task, node, runID, workflowID, logical, physical, qualified, demanded, measurement.physical.EndedNanos))
	}
	if treatment == "optimized" && task.Class == "near_match" {
		reason := "identity_mismatch"
		switch task.NegativeDimension {
		case "freshness":
			reason = "freshness_mismatch"
		case "privacy":
			reason = "privacy_mismatch"
		case "authority":
			reason = "authority_mismatch"
		}
		result.decisions = append(result.decisions, rejectedDecision(task, logicalID(treatment, task, 1), reason))
	}
	return nil
}

func appendModelSpan(ctx context.Context, result *treatmentResult, origin time.Time, runID string, task Task) {
	node := task.Nodes[0]
	started := elapsed(origin)
	sleepContext(ctx, time.Duration(node.FixtureDurationMillis)*time.Millisecond)
	ended := elapsed(origin)
	result.spans = append(result.spans, observe.ObservedSpan{
		SpanID: opaqueObservation("span", runID+":model"), RunID: runID,
		Kind: "model.invocation", Label: "model invocation", EvidenceClass: "replayed",
		StartedNanos: started, EndedNanos: ended, InputSHA256: digest(task.TaskID + ":model-input"), OutputSHA256: digest(task.TaskID + ":model-output"),
	})
}

func appendOutputSpan(ctx context.Context, result *treatmentResult, origin time.Time, runID string, task Task) {
	var node Node
	for _, candidate := range task.Nodes {
		if candidate.Kind == "wait" {
			node = candidate
			break
		}
	}
	started := elapsed(origin)
	sleepContext(ctx, time.Duration(node.FixtureDurationMillis)*time.Millisecond)
	ended := elapsed(origin)
	result.spans = append(result.spans, observe.ObservedSpan{
		SpanID: opaqueObservation("span", runID+":output"), RunID: runID,
		Kind: "model.output", Label: "model output", EvidenceClass: "replayed",
		StartedNanos: started, EndedNanos: ended, InputSHA256: digest(task.TaskID + ":output-input"), OutputSHA256: digest(task.TaskID + ":output-output"),
	})
}

type toolOutcome struct {
	measurement measuredTool
	err         error
}

func startTool(ctx context.Context, origin time.Time, runID, physicalID, producer string, node Node, consumers []string) (<-chan struct{}, <-chan toolOutcome) {
	startedSignal := make(chan struct{})
	completed := make(chan toolOutcome, 1)
	go func() {
		started := elapsed(origin)
		close(startedSignal)
		err := sleepContext(ctx, time.Duration(node.FixtureDurationMillis)*time.Millisecond)
		ended := elapsed(origin)
		if err != nil {
			completed <- toolOutcome{err: err}
			return
		}
		physical := observe.PhysicalExecution{
			PhysicalExecutionID: physicalID, RunID: runID, ProducerLogicalRequestID: producer,
			Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: node.BoundaryIdentitySHA256,
			AuthoritySHA256: node.AuthoritySHA256, FreshnessSHA256: node.FreshnessSHA256, PrivacySHA256: node.PrivacySHA256,
			StartedNanos: started, EndedNanos: ended, TerminalDisposition: "succeeded", ResultSHA256: node.ResultSHA256,
			Consumers: append([]string{}, consumers...),
		}
		sort.Strings(physical.Consumers)
		span := observe.ObservedSpan{
			SpanID: opaqueObservation("span", physicalID), RunID: runID,
			Kind: "host.tool", Label: "typed tool execution", EvidenceClass: "measured",
			StartedNanos: started, EndedNanos: ended, PhysicalExecutionID: physicalID,
			InputSHA256: node.BoundaryIdentitySHA256, OutputSHA256: node.ResultSHA256,
		}
		completed <- toolOutcome{measurement: measuredTool{physical: physical, span: span}}
	}()
	return startedSignal, completed
}

func runTool(ctx context.Context, origin time.Time, runID, physicalID, producer string, node Node, consumers []string) (measuredTool, error) {
	_, completed := startTool(ctx, origin, runID, physicalID, producer, node, consumers)
	outcome := <-completed
	return outcome.measurement, outcome.err
}

func logicalRequest(task Task, node Node, runID, workflowID, logicalID, physicalID string, qualified, demanded, completed uint64) observe.LogicalRequest {
	return observe.LogicalRequest{
		LogicalRequestID: logicalID, RunID: runID, WorkflowID: workflowID,
		WorkflowNodeID: node.NodeID, OccurrenceID: node.OccurrenceID,
		Surface: "tool", Capability: "fixture.read", BoundaryIdentitySHA256: node.BoundaryIdentitySHA256,
		AuthoritySHA256: node.AuthoritySHA256, FreshnessSHA256: node.FreshnessSHA256, PrivacySHA256: node.PrivacySHA256,
		QualifiedNanos: qualified, DemandedNanos: demanded, CompletedNanos: completed, PhysicalExecutionID: physicalID,
	}
}

func admittedDecision(task Task, kind string, logical []string, physical, producer, reason string) observe.OptimizationDecision {
	decision := observe.OptimizationDecision{
		DecisionID: opaqueObservation("decision", task.TaskID+":"+kind), Kind: kind, Outcome: "admitted", Reason: reason,
		LogicalRequestIDs: append([]string{}, logical...), PhysicalExecutionID: physical, ProducerLogicalRequestID: producer,
		EvidenceClass: "host_recorded",
	}
	sort.Strings(decision.LogicalRequestIDs)
	if kind == "declared_parallel" {
		decision.DeclarationSHA256 = digest(task.TaskID + ":declared-independent")
	}
	return decision
}

func rejectedDecision(task Task, logicalID, reason string) observe.OptimizationDecision {
	return observe.OptimizationDecision{
		DecisionID: opaqueObservation("decision", task.TaskID+":rejected"), Kind: "reused", Outcome: "rejected", Reason: reason,
		LogicalRequestIDs: []string{logicalID}, EvidenceClass: "host_recorded",
	}
}

func toolNodes(task Task) []Node {
	result := []Node{}
	for _, node := range task.Nodes {
		if node.Kind == "tool.read" {
			result = append(result, node)
		}
	}
	return result
}

func logicalID(treatment string, task Task, index int) string {
	return opaqueObservation("logical", fmt.Sprintf("%s:%s:%d", treatment, task.TaskID, index))
}

func physicalID(treatment string, task Task, index int) string {
	return opaqueObservation("physical", fmt.Sprintf("%s:%s:%d", treatment, task.TaskID, index))
}

func opaqueObservation(prefix, value string) string {
	sum := sha256.Sum256([]byte("pysolate.workflow-benchmark-observation.v0\x00" + prefix + "\x00" + value))
	return fmt.Sprintf("%s-%x", prefix, sum[:8])
}

func elapsed(origin time.Time) uint64 {
	value := time.Since(origin).Nanoseconds()
	if value <= 0 {
		return 1
	}
	return uint64(value)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != EvidenceSchemaVersion || evidence.Manifest == nil || evidence.Reports == nil || evidence.Tasks == nil ||
		len(evidence.Reports) != len(evidence.Tasks) || len(evidence.Tasks) != 14 || evidence.Divergences != 0 ||
		!digestPattern.MatchString(evidence.SealSHA256) || evidence.SealSHA256 != evidenceSeal(evidence) {
		return ErrExperiment
	}
	manifest, err := DecodeManifest(evidence.Manifest)
	if err != nil || len(manifest.Tasks) != len(evidence.Tasks) {
		return ErrExperiment
	}
	var baseline, optimized uint32
	for index, metrics := range evidence.Tasks {
		if metrics.TaskID != manifest.Tasks[index].TaskID || metrics.WorkloadID != manifest.Tasks[index].WorkloadID ||
			metrics.Class != manifest.Tasks[index].Class || metrics.NegativeDimension != manifest.Tasks[index].NegativeDimension ||
			!metrics.OutputEquivalent || !metrics.EffectsEquivalent || !metrics.EvidenceComplete ||
			metrics.BaselineDurationNanos == 0 || metrics.OptimizedDurationNanos == 0 || !digestPattern.MatchString(metrics.ObservationSealSHA256) {
			return ErrExperiment
		}
		verified, err := observe.DecodeOptimizationReport(evidence.Reports[index])
		if err != nil {
			return ErrExperiment
		}
		report, err := verified.Report()
		if err != nil || report.SealSHA256 != metrics.ObservationSealSHA256 || report.ShuffleSeed != manifest.Seed ||
			len(report.Runs) != 2 || report.Runs[0].Treatment != "baseline" || report.Runs[1].Treatment != "optimized" {
			return ErrExperiment
		}
		admitted, rejected := uint32(0), uint32(0)
		for _, decision := range report.Decisions {
			if decision.Outcome == "admitted" {
				admitted++
			} else {
				rejected++
			}
		}
		if admitted != metrics.AdmittedDecisions || rejected != metrics.RejectedDecisions ||
			uint32(countPhysical(report, "baseline")) != metrics.BaselinePhysicalExecutions ||
			uint32(countPhysical(report, "optimized")) != metrics.OptimizedPhysicalExecutions {
			return ErrExperiment
		}
		baseline += metrics.BaselinePhysicalExecutions
		optimized += metrics.OptimizedPhysicalExecutions
	}
	if baseline != evidence.BaselinePhysicalExecutions || optimized != evidence.OptimizedPhysicalExecutions || optimized > baseline {
		return ErrExperiment
	}
	return nil
}

func countPhysical(report observe.OptimizationReport, treatment string) int {
	runs := map[string]string{}
	for _, run := range report.Runs {
		runs[run.RunID] = run.Treatment
	}
	count := 0
	for _, physical := range report.PhysicalExecutions {
		if runs[physical.RunID] == treatment {
			count++
		}
	}
	return count
}

func EncodeEvidence(evidence Evidence) ([]byte, error) {
	if evidence.Validate() != nil {
		return nil, ErrExperiment
	}
	encoded, err := json.Marshal(evidence)
	if err != nil || len(encoded) > 8<<20 {
		return nil, ErrExperiment
	}
	return encoded, nil
}

func DecodeEvidence(raw []byte) (Evidence, error) {
	if len(raw) == 0 || len(raw) > 8<<20 || rejectDuplicateJSON(raw) != nil {
		return Evidence{}, ErrExperiment
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if decoder.Decode(&evidence) != nil || evidence.Validate() != nil {
		return Evidence{}, ErrExperiment
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Evidence{}, ErrExperiment
	}
	canonical, err := EncodeEvidence(evidence)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Evidence{}, ErrExperiment
	}
	return evidence, nil
}

func evidenceSeal(evidence Evidence) string {
	evidence.SealSHA256 = ""
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	return digest(string(encoded))
}
