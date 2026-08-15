export type WorkflowNode = {
  node_id: string;
  occurrence_id: string;
  kind: 'model.invocation' | 'tool.read' | 'wait' | 'wasm.compute';
  fixture_duration_millis: number;
  boundary_identity_sha256?: string;
  authority_sha256?: string;
  freshness_sha256?: string;
  privacy_sha256?: string;
  resource_sha256?: string;
  result_sha256?: string;
};

export type WorkflowTask = {
  task_id: string;
  workload_id: string;
  class: 'preissue' | 'declared_parallel' | 'coalesced' | 'retained_reuse' | 'near_match' | 'ordinary';
  negative_dimension?: string;
  expected_rejection_reason?: string;
  submission_order: number;
  expected_output_sha256: string;
  nodes: WorkflowNode[];
};

export type WorkflowRun = {
  run_id: string;
  workload_id: string;
  treatment: 'baseline' | 'optimized';
  order: number;
  started_nanos: number;
  ended_nanos: number;
  terminal_disposition: string;
  artifact_sha256: string;
  execution_profile_sha256: string;
  capability_plan_sha256: string;
};

export type WorkflowSpan = {
  span_id: string;
  run_id: string;
  kind: 'model.invocation' | 'model.output' | 'guest.wasm' | 'host.tool' | 'host.wasi';
  label: string;
  evidence_class: 'measured' | 'replayed';
  started_nanos: number;
  ended_nanos: number;
  physical_execution_id?: string;
  input_sha256?: string;
  output_sha256?: string;
};

export type WorkflowLogicalRequest = {
  logical_request_id: string;
  run_id: string;
  workflow_id: string;
  workflow_node_id: string;
  occurrence_id: string;
  surface: 'tool' | 'wasi';
  capability: string;
  boundary_identity_sha256: string;
  authority_sha256: string;
  freshness_sha256: string;
  privacy_sha256: string;
  qualified_nanos: number;
  demanded_nanos: number;
  completed_nanos: number;
  physical_execution_id: string;
};

export type WorkflowPhysicalExecution = {
  physical_execution_id: string;
  run_id: string;
  producer_logical_request_id: string;
  surface: 'tool' | 'wasi';
  capability: string;
  boundary_identity_sha256: string;
  authority_sha256: string;
  freshness_sha256: string;
  privacy_sha256: string;
  started_nanos: number;
  ended_nanos: number;
  terminal_disposition: string;
  result_sha256: string;
  error_class: string;
  consumers: string[];
};

export type WorkflowDecision = {
  decision_id: string;
  kind: 'preissued' | 'declared_parallel' | 'coalesced' | 'reused';
  outcome: 'admitted' | 'rejected';
  reason: string;
  logical_request_ids: string[];
  physical_execution_id?: string;
  producer_logical_request_id?: string;
  evidence_class: 'host_recorded';
  declaration_sha256?: string;
};

export type WorkflowReport = {
  schema_version: 'pysolate.workflow-boundary-observation.v0';
  study_id: string;
  workload_manifest_sha256: string;
  shuffle_seed: number;
  clock_policy: 'study_relative_monotonic_nanos';
  runs: WorkflowRun[];
  spans: WorkflowSpan[];
  logical_requests: WorkflowLogicalRequest[];
  physical_executions: WorkflowPhysicalExecution[];
  decisions: WorkflowDecision[];
  consumer_admitted: false;
  seal_sha256: string;
};

export type WorkflowTaskMetrics = {
  task_id: string;
  workload_id: string;
  class: WorkflowTask['class'];
  negative_dimension?: string;
  baseline_duration_nanos: number;
  optimized_duration_nanos: number;
  baseline_physical_executions: number;
  optimized_physical_executions: number;
  admitted_decisions: number;
  rejected_decisions: number;
  output_equivalent: boolean;
  effects_equivalent: boolean;
  evidence_complete: boolean;
  observation_seal_sha256: string;
};

export type WorkflowEvidence = {
  schema_version: 'pysolate.workflow-benchmark-evidence.v0';
  manifest: {
    schema_version: 'pysolate.workflow-benchmark-manifest.v0';
    seed: number;
    runtime_identity: {
      source_commit: string;
      artifact_sha256: string;
      execution_profile_sha256: string;
      capability_plan_sha256: string;
      harness_sha256: string;
    };
    tasks: WorkflowTask[];
    seal_sha256: string;
  };
  reports: WorkflowReport[];
  tasks: WorkflowTaskMetrics[];
  divergences: number;
  baseline_physical_executions: number;
  optimized_physical_executions: number;
  seal_sha256: string;
};

const digestRE = /^sha256:[0-9a-f]{64}$/;
const commitRE = /^[0-9a-f]{40}$/;
const opaqueRE = /^(task|workload|run|span|logical|physical|decision|study|workflow|node|occurrence)-[0-9a-f]{16,64}$/;
const forbiddenKeyRE = /^(prompt|model_output|chain_of_thought|private_body|result_body|raw_body|python_source|credential|workspace_path)$/;
const reasonCodes = new Set(['necessarily_reached_read', 'declared_independent', 'identical_inflight_request', 'fresh_exact_retained_result', 'mechanism_disabled', 'freshness_mismatch', 'authority_mismatch', 'privacy_mismatch', 'identity_mismatch', 'effect_not_read_only', 'not_necessarily_reached', 'declaration_missing', 'expired', 'budget_exhausted']);
const spanLabels: Record<string, string> = { 'model.invocation': 'model invocation', 'model.output': 'model output', 'guest.wasm': 'WASM execution', 'host.tool': 'typed tool execution', 'host.wasi': 'typed WASI execution' };

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

function exactKeys(value: unknown, allowed: readonly string[], message: string) {
  assert(typeof value === 'object' && value !== null && !Array.isArray(value), message);
  const permitted = new Set(allowed);
  assert(Object.keys(value).every((key) => permitted.has(key)), message);
}

function scanPortable(value: unknown): void {
  if (Array.isArray(value)) {
    value.forEach(scanPortable);
    return;
  }
  if (!value || typeof value !== 'object') return;
  for (const [key, nested] of Object.entries(value)) {
    assert(!forbiddenKeyRE.test(key), `private evidence key rejected: ${key}`);
    scanPortable(nested);
  }
}

async function sha256(value: string): Promise<string> {
  const bytes = new TextEncoder().encode(value);
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return `sha256:${Array.from(new Uint8Array(digest)).map((byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

export async function validateWorkflowEvidence(value: WorkflowEvidence): Promise<WorkflowEvidence> {
  exactKeys(value, ['schema_version', 'manifest', 'reports', 'tasks', 'divergences', 'baseline_physical_executions', 'optimized_physical_executions', 'seal_sha256'], 'unknown evidence field');
  exactKeys(value.manifest, ['schema_version', 'seed', 'runtime_identity', 'tasks', 'seal_sha256'], 'unknown manifest field');
  exactKeys(value.manifest.runtime_identity, ['source_commit', 'artifact_sha256', 'execution_profile_sha256', 'capability_plan_sha256', 'harness_sha256'], 'unknown runtime identity field');
  scanPortable(value);
  assert(value.schema_version === 'pysolate.workflow-benchmark-evidence.v0', 'invalid workflow evidence schema');
  assert(value.manifest?.schema_version === 'pysolate.workflow-benchmark-manifest.v0', 'invalid manifest schema');
  assert(Number.isSafeInteger(value.manifest.seed) && value.manifest.seed > 0, 'invalid seed');
  assert(value.manifest.tasks.length === 14 && value.tasks.length === 14 && value.reports.length === 14, 'incomplete paired evidence');
  assert(value.divergences === 0, 'observable divergence present');
  assert(value.optimized_physical_executions <= value.baseline_physical_executions, 'invalid aggregate physical count');
  assert(digestRE.test(value.seal_sha256) && digestRE.test(value.manifest.seal_sha256), 'invalid evidence seal');
  const runtimeIdentity = value.manifest.runtime_identity;
  assert(commitRE.test(runtimeIdentity.source_commit) && digestRE.test(runtimeIdentity.artifact_sha256) && digestRE.test(runtimeIdentity.execution_profile_sha256) && digestRE.test(runtimeIdentity.capability_plan_sha256) && digestRE.test(runtimeIdentity.harness_sha256), 'invalid runtime identity');
  const manifestForSeal = structuredClone(value.manifest);
  manifestForSeal.seal_sha256 = '';
  assert(await sha256(JSON.stringify(manifestForSeal)) === value.manifest.seal_sha256, 'manifest seal mismatch');
  const manifestSHA = await sha256(JSON.stringify(value.manifest));

  const physicalTotal = { baseline: 0, optimized: 0 };
  const taskClasses = new Set(['preissue', 'declared_parallel', 'coalesced', 'retained_reuse', 'near_match', 'ordinary']);
  const negativeDimensions = new Set(['arguments', 'freshness', 'resource', 'privacy', 'authority', 'source', 'artifact', 'workflow']);
  for (const [index, task] of value.manifest.tasks.entries()) {
    const metrics = value.tasks[index];
    const report = value.reports[index];
    exactKeys(task, ['task_id', 'workload_id', 'submission_order', 'class', 'negative_dimension', 'expected_rejection_reason', 'nodes', 'expected_output_sha256'], 'unknown task field');
    exactKeys(metrics, ['task_id', 'workload_id', 'class', 'negative_dimension', 'baseline_duration_nanos', 'optimized_duration_nanos', 'baseline_physical_executions', 'optimized_physical_executions', 'admitted_decisions', 'rejected_decisions', 'output_equivalent', 'effects_equivalent', 'evidence_complete', 'observation_seal_sha256'], 'unknown task metric field');
    exactKeys(report, ['schema_version', 'study_id', 'workload_manifest_sha256', 'shuffle_seed', 'clock_policy', 'runs', 'spans', 'logical_requests', 'physical_executions', 'decisions', 'consumer_admitted', 'seal_sha256'], 'unknown report field');
    const reportForSeal = structuredClone(report);
    reportForSeal.seal_sha256 = '';
    assert(await sha256(JSON.stringify(reportForSeal)) === report.seal_sha256, 'report seal mismatch');
    assert(task.submission_order === index + 1 && opaqueRE.test(task.task_id) && opaqueRE.test(task.workload_id), 'invalid shuffled task');
    assert(taskClasses.has(task.class) && digestRE.test(task.expected_output_sha256) && (task.class === 'near_match' ? negativeDimensions.has(task.negative_dimension ?? '') : task.negative_dimension === undefined), 'invalid prepared task');
    task.nodes.forEach((node) => {
      exactKeys(node, ['node_id', 'occurrence_id', 'kind', 'fixture_duration_millis', 'boundary_identity_sha256', 'result_sha256', 'authority_sha256', 'freshness_sha256', 'privacy_sha256', 'resource_sha256'], 'unknown node field');
      const identities = [node.boundary_identity_sha256, node.authority_sha256, node.freshness_sha256, node.privacy_sha256, node.resource_sha256, node.result_sha256];
      assert(['model.invocation', 'tool.read', 'wait', 'wasm.compute'].includes(node.kind) && opaqueRE.test(node.node_id) && opaqueRE.test(node.occurrence_id) && Number.isSafeInteger(node.fixture_duration_millis) && node.fixture_duration_millis >= 1 && node.fixture_duration_millis <= 100, 'invalid prepared node');
      assert(node.kind === 'tool.read' ? identities.every((identity) => typeof identity === 'string' && digestRE.test(identity)) : identities.every((identity) => identity === undefined), 'invalid node boundary identity');
    });
    assert(metrics.task_id === task.task_id && metrics.workload_id === task.workload_id && metrics.class === task.class, 'task metric drift');
    assert(metrics.output_equivalent && metrics.effects_equivalent && metrics.evidence_complete, 'incomplete task evidence');
    assert(report.schema_version === 'pysolate.workflow-boundary-observation.v0' && report.shuffle_seed === value.manifest.seed && report.workload_manifest_sha256 === manifestSHA && opaqueRE.test(report.study_id), 'report generation drift');
    assert(report.consumer_admitted === false && report.runs.length === 2, 'Lab report acquired authority');
    assert(report.runs[0].treatment === 'baseline' && report.runs[1].treatment === 'optimized', 'invalid treatment pair');
    report.runs.forEach((run, runIndex) => {
      exactKeys(run, ['run_id', 'workload_id', 'treatment', 'order', 'started_nanos', 'ended_nanos', 'terminal_disposition', 'artifact_sha256', 'execution_profile_sha256', 'capability_plan_sha256'], 'unknown run field');
      assert(opaqueRE.test(run.run_id) && opaqueRE.test(run.workload_id) && run.order === runIndex + 1 && run.terminal_disposition === 'succeeded' && digestRE.test(run.artifact_sha256) && digestRE.test(run.execution_profile_sha256) && digestRE.test(run.capability_plan_sha256), 'invalid run identity');
      assert(Number.isSafeInteger(run.started_nanos) && Number.isSafeInteger(run.ended_nanos) && run.started_nanos < run.ended_nanos, 'invalid run interval');
    });
    report.spans.forEach((span) => {
      exactKeys(span, ['span_id', 'run_id', 'parent_span_id', 'kind', 'label', 'evidence_class', 'started_nanos', 'ended_nanos', 'physical_execution_id', 'input_sha256', 'output_sha256'], 'unknown span field');
      const hostSpan = span.kind === 'host.tool' || span.kind === 'host.wasi';
      assert(opaqueRE.test(span.span_id) && opaqueRE.test(span.run_id) && spanLabels[span.kind] === span.label && ['measured', 'replayed'].includes(span.evidence_class), 'invalid span');
      assert((span.input_sha256 === undefined || digestRE.test(span.input_sha256)) && (span.output_sha256 === undefined || digestRE.test(span.output_sha256)) && hostSpan === Boolean(span.physical_execution_id), 'invalid span identity');
      assert(hostSpan ? span.evidence_class === 'measured' : (span.kind === 'model.invocation' || span.kind === 'model.output') ? span.evidence_class === 'replayed' : span.evidence_class === 'measured', 'invalid span evidence class');
    });
    const logical = new Map(report.logical_requests.map((request) => [request.logical_request_id, request]));
    const physical = new Map(report.physical_executions.map((execution) => [execution.physical_execution_id, execution]));
    assert(logical.size === report.logical_requests.length && physical.size === report.physical_executions.length, 'duplicate provenance identifier');
    const reverseConsumers = new Map<string, string[]>();
    for (const request of logical.values()) {
      exactKeys(request, ['logical_request_id', 'run_id', 'workflow_id', 'workflow_node_id', 'occurrence_id', 'surface', 'capability', 'boundary_identity_sha256', 'authority_sha256', 'freshness_sha256', 'privacy_sha256', 'qualified_nanos', 'demanded_nanos', 'completed_nanos', 'physical_execution_id'], 'unknown logical request field');
      assert(opaqueRE.test(request.logical_request_id) && opaqueRE.test(request.run_id) && opaqueRE.test(request.workflow_id) && opaqueRE.test(request.workflow_node_id) && opaqueRE.test(request.occurrence_id) && physical.has(request.physical_execution_id), 'orphan logical request');
      assert(request.surface === 'tool' && request.capability === 'fixture.read' && digestRE.test(request.boundary_identity_sha256) && digestRE.test(request.authority_sha256) && digestRE.test(request.freshness_sha256) && digestRE.test(request.privacy_sha256), 'invalid logical boundary');
      assert(physical.get(request.physical_execution_id)?.run_id === request.run_id, 'cross-treatment logical mapping');
      const execution = physical.get(request.physical_execution_id)!;
      assert(request.boundary_identity_sha256 === execution.boundary_identity_sha256 && request.authority_sha256 === execution.authority_sha256 && request.freshness_sha256 === execution.freshness_sha256 && request.privacy_sha256 === execution.privacy_sha256 && request.completed_nanos >= execution.ended_nanos, 'logical and physical identity drift');
      reverseConsumers.set(request.physical_execution_id, [...(reverseConsumers.get(request.physical_execution_id) ?? []), request.logical_request_id]);
    }
    for (const execution of physical.values()) {
      exactKeys(execution, ['physical_execution_id', 'run_id', 'producer_logical_request_id', 'surface', 'capability', 'boundary_identity_sha256', 'authority_sha256', 'freshness_sha256', 'privacy_sha256', 'started_nanos', 'ended_nanos', 'terminal_disposition', 'result_sha256', 'error_class', 'consumers'], 'unknown physical execution field');
      assert(opaqueRE.test(execution.physical_execution_id) && logical.has(execution.producer_logical_request_id), 'orphan physical execution');
      assert(opaqueRE.test(execution.run_id) && execution.surface === 'tool' && execution.capability === 'fixture.read' && execution.terminal_disposition === 'succeeded' && execution.error_class === '' && digestRE.test(execution.boundary_identity_sha256) && digestRE.test(execution.authority_sha256) && digestRE.test(execution.freshness_sha256) && digestRE.test(execution.privacy_sha256) && digestRE.test(execution.result_sha256), 'invalid physical boundary');
      assert(Number.isSafeInteger(execution.started_nanos) && Number.isSafeInteger(execution.ended_nanos) && execution.started_nanos < execution.ended_nanos, 'invalid physical interval');
      assert(execution.consumers.every((consumer) => logical.has(consumer)), 'orphan execution consumer');
      const expectedConsumers = (reverseConsumers.get(execution.physical_execution_id) ?? []).sort();
      assert(JSON.stringify([...execution.consumers].sort()) === JSON.stringify(expectedConsumers), 'physical consumer reverse-index mismatch');
      assert(logical.get(execution.producer_logical_request_id)?.physical_execution_id === execution.physical_execution_id, 'invalid physical producer');
      const treatment = report.runs.find((run) => run.run_id === execution.run_id)?.treatment;
      assert(Boolean(treatment), 'execution references unknown run');
      physicalTotal[treatment as 'baseline' | 'optimized'] += 1;
    }
    assert(report.decisions.every((decision) => {
      exactKeys(decision, ['decision_id', 'kind', 'outcome', 'logical_request_ids', 'physical_execution_id', 'producer_logical_request_id', 'declaration_sha256', 'evidence_class', 'reason'], 'unknown decision field');
      return opaqueRE.test(decision.decision_id) && ['preissued', 'declared_parallel', 'coalesced', 'reused'].includes(decision.kind) && ['admitted', 'rejected'].includes(decision.outcome) && reasonCodes.has(decision.reason) && decision.evidence_class === 'host_recorded' && decision.logical_request_ids.every((id) => logical.has(id)) && (decision.declaration_sha256 === undefined || decision.declaration_sha256 === '' || digestRE.test(decision.declaration_sha256));
    }), 'invalid optimization decision');
    const sharingEvidence = new Map<string, number>();
    for (const decision of report.decisions) {
      if (decision.outcome === 'rejected') {
        assert(!decision.physical_execution_id && !decision.producer_logical_request_id, 'rejected decision acquired authority');
      } else if (decision.kind === 'preissued') {
        const request = logical.get(decision.logical_request_ids[0])!;
        const execution = physical.get(decision.physical_execution_id ?? '')!;
        assert(decision.logical_request_ids.length === 1 && execution?.producer_logical_request_id === request.logical_request_id && request.qualified_nanos < execution.started_nanos && execution.started_nanos < request.demanded_nanos, 'invalid preissue timing');
      } else if (decision.kind === 'reused') {
        const request = logical.get(decision.logical_request_ids[0])!;
        const execution = physical.get(decision.physical_execution_id ?? '')!;
        assert(decision.logical_request_ids.length === 1 && execution?.ended_nanos < request.demanded_nanos && execution.producer_logical_request_id !== request.logical_request_id, 'invalid reuse timing');
      } else if (decision.kind === 'coalesced') {
        const execution = physical.get(decision.physical_execution_id ?? '')!;
        assert(Boolean(execution) && decision.logical_request_ids.length >= 2 && decision.logical_request_ids.includes(decision.producer_logical_request_id ?? ''), 'invalid coalescing provenance');
        for (const requestID of decision.logical_request_ids) {
          const request = logical.get(requestID)!;
          assert(request.physical_execution_id === execution.physical_execution_id && (requestID === decision.producer_logical_request_id || (request.demanded_nanos >= execution.started_nanos && request.demanded_nanos < execution.ended_nanos)), 'invalid coalescing timing');
        }
      }
      if (decision.outcome === 'admitted' && (decision.kind === 'coalesced' || decision.kind === 'reused')) {
        for (const requestID of decision.logical_request_ids) {
          if (requestID !== decision.producer_logical_request_id) sharingEvidence.set(requestID, (sharingEvidence.get(requestID) ?? 0) + 1);
        }
      }
      if (decision.kind === 'declared_parallel' && decision.outcome === 'admitted') {
        const requests = decision.logical_request_ids.map((id) => logical.get(id)!);
        assert(requests.every((request) => request.run_id === requests[0].run_id && request.workflow_id === requests[0].workflow_id), 'cross-workflow parallel declaration');
        const executions = requests.map((request) => physical.get(request.physical_execution_id)!);
        assert(new Set(executions.map((execution) => execution.physical_execution_id)).size === executions.length && Math.max(...executions.map((execution) => execution.started_nanos)) < Math.min(...executions.map((execution) => execution.ended_nanos)), 'invalid parallel overlap');
      }
    }
    for (const execution of physical.values()) {
      for (const consumer of execution.consumers) {
        if (consumer !== execution.producer_logical_request_id) assert(sharingEvidence.get(consumer) === 1, 'unlinked sharing consumer');
      }
    }
    assert(digestRE.test(report.seal_sha256) && report.seal_sha256 === metrics.observation_seal_sha256, 'report seal drift');
  }
  assert(physicalTotal.baseline === value.baseline_physical_executions && physicalTotal.optimized === value.optimized_physical_executions, 'aggregate count drift');

  const copy = structuredClone(value);
  copy.seal_sha256 = '';
  assert(await sha256(JSON.stringify(copy)) === value.seal_sha256, 'workflow evidence seal mismatch');
  return value;
}
