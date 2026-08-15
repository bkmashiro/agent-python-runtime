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
  const manifestSHA = await sha256(JSON.stringify(value.manifest));

  const physicalTotal = { baseline: 0, optimized: 0 };
  value.manifest.tasks.forEach((task, index) => {
    const metrics = value.tasks[index];
    const report = value.reports[index];
    assert(task.submission_order === index + 1 && opaqueRE.test(task.task_id) && opaqueRE.test(task.workload_id), 'invalid shuffled task');
    assert(digestRE.test(task.expected_output_sha256) && ((task.class === 'near_match') === Boolean(task.negative_dimension)), 'invalid prepared task');
    task.nodes.forEach((node) => {
      const identities = [node.boundary_identity_sha256, node.authority_sha256, node.freshness_sha256, node.privacy_sha256, node.resource_sha256, node.result_sha256];
      assert(opaqueRE.test(node.node_id) && opaqueRE.test(node.occurrence_id) && Number.isSafeInteger(node.fixture_duration_millis) && node.fixture_duration_millis >= 1 && node.fixture_duration_millis <= 100, 'invalid prepared node');
      assert(node.kind === 'tool.read' ? identities.every((identity) => typeof identity === 'string' && digestRE.test(identity)) : identities.every((identity) => identity === undefined), 'invalid node boundary identity');
    });
    assert(metrics.task_id === task.task_id && metrics.workload_id === task.workload_id && metrics.class === task.class, 'task metric drift');
    assert(metrics.output_equivalent && metrics.effects_equivalent && metrics.evidence_complete, 'incomplete task evidence');
    assert(report.schema_version === 'pysolate.workflow-boundary-observation.v0' && report.shuffle_seed === value.manifest.seed && report.workload_manifest_sha256 === manifestSHA && opaqueRE.test(report.study_id), 'report generation drift');
    assert(report.consumer_admitted === false && report.runs.length === 2, 'Lab report acquired authority');
    assert(report.runs[0].treatment === 'baseline' && report.runs[1].treatment === 'optimized', 'invalid treatment pair');
    report.runs.forEach((run, runIndex) => {
      assert(opaqueRE.test(run.run_id) && opaqueRE.test(run.workload_id) && run.order === runIndex + 1 && run.terminal_disposition === 'succeeded' && digestRE.test(run.artifact_sha256) && digestRE.test(run.execution_profile_sha256) && digestRE.test(run.capability_plan_sha256), 'invalid run identity');
    });
    report.spans.forEach((span) => {
      const hostSpan = span.kind === 'host.tool' || span.kind === 'host.wasi';
      assert(opaqueRE.test(span.span_id) && opaqueRE.test(span.run_id) && spanLabels[span.kind] === span.label && ['measured', 'replayed'].includes(span.evidence_class), 'invalid span');
      assert((span.input_sha256 === undefined || digestRE.test(span.input_sha256)) && (span.output_sha256 === undefined || digestRE.test(span.output_sha256)) && hostSpan === Boolean(span.physical_execution_id), 'invalid span identity');
    });
    const logical = new Map(report.logical_requests.map((request) => [request.logical_request_id, request]));
    const physical = new Map(report.physical_executions.map((execution) => [execution.physical_execution_id, execution]));
    for (const request of logical.values()) {
      assert(opaqueRE.test(request.logical_request_id) && opaqueRE.test(request.run_id) && opaqueRE.test(request.workflow_id) && opaqueRE.test(request.workflow_node_id) && opaqueRE.test(request.occurrence_id) && physical.has(request.physical_execution_id), 'orphan logical request');
      assert(request.surface === 'tool' && request.capability === 'fixture.read' && digestRE.test(request.boundary_identity_sha256) && digestRE.test(request.authority_sha256) && digestRE.test(request.freshness_sha256) && digestRE.test(request.privacy_sha256), 'invalid logical boundary');
    }
    for (const execution of physical.values()) {
      assert(opaqueRE.test(execution.physical_execution_id) && logical.has(execution.producer_logical_request_id), 'orphan physical execution');
      assert(opaqueRE.test(execution.run_id) && execution.surface === 'tool' && execution.capability === 'fixture.read' && execution.terminal_disposition === 'succeeded' && execution.error_class === '' && digestRE.test(execution.boundary_identity_sha256) && digestRE.test(execution.authority_sha256) && digestRE.test(execution.freshness_sha256) && digestRE.test(execution.privacy_sha256) && digestRE.test(execution.result_sha256), 'invalid physical boundary');
      assert(execution.consumers.every((consumer) => logical.has(consumer)), 'orphan execution consumer');
      const treatment = report.runs.find((run) => run.run_id === execution.run_id)?.treatment;
      assert(Boolean(treatment), 'execution references unknown run');
      physicalTotal[treatment as 'baseline' | 'optimized'] += 1;
    }
    assert(report.decisions.every((decision) => opaqueRE.test(decision.decision_id) && ['preissued', 'declared_parallel', 'coalesced', 'reused'].includes(decision.kind) && ['admitted', 'rejected'].includes(decision.outcome) && reasonCodes.has(decision.reason) && decision.evidence_class === 'host_recorded' && decision.logical_request_ids.every((id) => logical.has(id)) && (decision.declaration_sha256 === undefined || decision.declaration_sha256 === '' || digestRE.test(decision.declaration_sha256))), 'invalid optimization decision');
    assert(digestRE.test(report.seal_sha256) && report.seal_sha256 === metrics.observation_seal_sha256, 'report seal drift');
  });
  assert(physicalTotal.baseline === value.baseline_physical_executions && physicalTotal.optimized === value.optimized_physical_executions, 'aggregate count drift');

  const copy = structuredClone(value);
  copy.seal_sha256 = '';
  assert(await sha256(JSON.stringify(copy)) === value.seal_sha256, 'workflow evidence seal mismatch');
  return value;
}
