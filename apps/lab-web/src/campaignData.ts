export type CampaignTreatment = 'baseline' | 'qualified';

export interface MetricSummary {
  median: number;
  min: number;
  max: number;
}

export interface CampaignRun {
  repetition: number;
  treatment: CampaignTreatment;
  physical_executions: number;
  wall_ms: number;
  process_cpu_ms: number;
}

export interface CampaignEvent {
  sequence: number;
  program_id: string;
  type: string;
  at_ns: number;
  reason?: string;
  physical_execution_id?: string;
}

export interface CampaignProgram {
  id: string;
  family: string;
  release_offset_ms: number;
  plan_sha256: string;
  grant_set_sha256: string;
  privacy_partition: string;
  workspace_fixture_sha256: string;
  execution: {
    kind: string;
    cancel_point: string;
    source_program_id?: string;
    workflow_state_key?: string;
    verifier?: { source_sha256?: string; artifact_sha256?: string; profile_sha256?: string; environment_sha256?: string; policy_sha256?: string };
    resume?: { from_program_id?: string; state_key?: string; transition?: string };
    delegation?: { group_id?: string; parent_plan_role?: string; parent_plan_sha256?: string; max_delegated_calls?: number; child_reserved_calls?: number };
  };
  admission: string;
  sharing: string;
  disposition: string;
}

export interface CampaignProjection {
  schema_version: 'pysolate.transparent-campaign-public-projection.v1';
  source: {
    artifact_sha256: string;
    artifact_source_commit: string;
    campaign_source_commit: string;
    manifest_sha256: string;
    host: { goos: string; goarch: string; go_version: string; kernel: string };
    repetitions: number;
  };
  baseline: { physical_executions: MetricSummary; wall_ms: MetricSummary; process_cpu_ms: MetricSummary };
  qualified: { physical_executions: MetricSummary; wall_ms: MetricSummary; process_cpu_ms: MetricSummary };
  paired: { physical_reduction: MetricSummary; wall_reduction_ms: MetricSummary; cpu_reduction_ms: MetricSummary };
  runs: CampaignRun[];
  programs: CampaignProgram[];
  walkthrough_events: CampaignEvent[];
  valid_claim: string;
  invalid_inference: string;
}

const digest = /^sha256:[0-9a-f]{64}$/;
const programID = /^P(?:0[1-9]|1[0-9]|20)$/;
const families = new Set(['authority_bifurcation', 'authority_resume', 'delegation_attenuation', 'exact_sharing', 'root_verification']);
const privacyPartitions = new Set(['private-a', 'private-b', 'private-shared']);
const executionKinds = new Set(['consume_result', 'delegate_child', 'execute_python', 'exact_request', 'resume_workflow', 'start_workflow', 'verify_workspace', 'workspace_attempt']);
const cancelPoints = new Set(['after_parent_terminal', 'after_workspace_fork', 'none']);
const admissions = new Set(['admitted', 'authority_expired', 'authority_widening', 'delegation_budget', 'parent_terminal']);
const sharingStates = new Set(['exact_shared', 'independent', 'no_execution', 'root_exact_shared']);
const dispositions = new Set(['cancelled', 'complete', 'rejected']);
const canonicalValidClaim = 'For this fixed 20-program campaign on one recorded host, exact qualified sharing reduced physical executions while preserving every registered oracle and authority rejection.';
const canonicalInvalidInference = 'Do not generalize these five paired repetitions to arbitrary workloads, hosts, schedulers, or steady-state production throughput.';
const physicalID = /^campaign-[a-z]+-[0-9]+$/;
const eventTypes = new Set([
  'logical.released', 'admission.accepted', 'admission.rejected', 'logical.started',
  'physical.queued', 'physical.started', 'physical.ended', 'physical.cancelled', 'logical.terminal',
  'workspace.forked', 'workspace.sealed', 'workspace.discarded', 'verification.completed',
  'sharing.decided', 'workflow.waiting', 'workflow.resumed', 'authority.refreshed', 'delegation.child_started',
]);
const safeReason = /^(|manifest_offset|admitted|fifo|complete|cancelled|rejected|exact_shared|independent|root_exact_shared|private_attempt|after_workspace_fork|authority_expired|authority_widening|delegation_budget|parent_terminal|delegation-main|freshness_changed|plan_grant_changed|workflow-main|sha256:[0-9a-f]{64}(?::sha256:[0-9a-f]{64}:(?:independent|root_exact_shared))?)$/;

function validMetric(metric: MetricSummary | undefined, integer = false): metric is MetricSummary {
  return !!metric && [metric.min, metric.median, metric.max].every((value) => Number.isFinite(value) && (!integer || Number.isInteger(value))) && metric.min <= metric.median && metric.median <= metric.max;
}

function validEventReason(event: CampaignEvent, program: CampaignProgram) {
  const reason = event.reason ?? '';
  switch (event.type) {
    case 'logical.released': return reason === 'manifest_offset';
    case 'admission.accepted':
    case 'admission.rejected': return reason === program.admission;
    case 'logical.started':
    case 'physical.started':
    case 'physical.ended': return reason === '';
    case 'physical.queued': return reason === 'fifo';
    case 'physical.cancelled': return reason === 'cancelled';
    case 'logical.terminal': return reason === program.disposition;
    case 'sharing.decided': return reason === program.sharing;
    case 'workspace.forked': return reason === 'private_attempt';
    case 'workspace.sealed': return digest.test(reason);
    case 'workspace.discarded': return reason === program.execution.cancel_point;
    case 'verification.completed': return /^sha256:[0-9a-f]{64}:sha256:[0-9a-f]{64}:(?:independent|root_exact_shared)$/.test(reason);
    case 'workflow.waiting': return reason === program.execution.workflow_state_key;
    case 'workflow.resumed': return reason === program.execution.resume?.transition;
    case 'authority.refreshed': return reason === program.plan_sha256;
    case 'delegation.child_started': return reason === program.execution.delegation?.group_id;
    default: return false;
  }
}

// Shape and body-safety gate for a non-authoritative static projection. Cross-execution
// authority, sharing, lifecycle and root equivalence are verified by the Go projector
// before this file is generated; duplicating that registry in the browser is intentionally
// out of scope.
export function validateCampaignProjection(value: unknown): CampaignProjection {
  if (!value || typeof value !== 'object') throw new Error('campaign projection must be an object');
  const candidate = value as CampaignProjection;
  if (candidate.schema_version !== 'pysolate.transparent-campaign-public-projection.v1' ||
      !candidate.source || !digest.test(candidate.source.artifact_sha256) || !digest.test(candidate.source.manifest_sha256) ||
      !/^[0-9a-f]{40}$/.test(candidate.source.artifact_source_commit) || !/^[0-9a-f]{40}$/.test(candidate.source.campaign_source_commit) ||
      candidate.source.host?.goos !== 'darwin' || candidate.source.host?.goarch !== 'arm64' || !/^go[0-9]+(?:\.[0-9]+){0,2}$/.test(candidate.source.host?.go_version ?? '') || !Number.isInteger(candidate.source.repetitions) || candidate.source.repetitions < 1 ||
      !Array.isArray(candidate.runs) || candidate.runs.length !== candidate.source.repetitions * 2 ||
      !Array.isArray(candidate.programs) || candidate.programs.length !== 20 ||
      !Array.isArray(candidate.walkthrough_events) || candidate.walkthrough_events.length === 0 ||
      candidate.valid_claim !== canonicalValidClaim || candidate.invalid_inference !== canonicalInvalidInference) {
    throw new Error('campaign projection is invalid');
  }
  const physicalMetrics = [candidate.baseline?.physical_executions, candidate.qualified?.physical_executions];
  const continuousMetrics = [candidate.baseline?.wall_ms, candidate.baseline?.process_cpu_ms, candidate.qualified?.wall_ms, candidate.qualified?.process_cpu_ms, candidate.paired?.wall_reduction_ms, candidate.paired?.cpu_reduction_ms];
  if (physicalMetrics.some((metric) => !validMetric(metric, true) || metric.min < 0) || continuousMetrics.some((metric) => !validMetric(metric)) || !validMetric(candidate.paired?.physical_reduction, true)) {
    throw new Error('campaign metrics are invalid');
  }
  const treatments = new Set<string>();
  for (const run of candidate.runs) {
    const key = `${run.repetition}:${run.treatment}`;
    if (!Number.isInteger(run.repetition) || run.repetition < 0 || run.repetition >= candidate.source.repetitions || (run.treatment !== 'baseline' && run.treatment !== 'qualified') || treatments.has(key) || !Number.isInteger(run.physical_executions) || run.physical_executions < 0 || !Number.isFinite(run.wall_ms) || run.wall_ms < 0 || !Number.isFinite(run.process_cpu_ms) || run.process_cpu_ms < 0) {
      throw new Error('campaign run projection is invalid');
    }
    treatments.add(key);
  }
  const programs = new Map(candidate.programs.map((program) => [program.id, program]));
  const ids = new Set(programs.keys());
  if (ids.size !== 20 || candidate.programs.some((program) => {
    const execution = program.execution;
    const verifier = execution?.verifier;
    const resume = execution?.resume;
    const delegation = execution?.delegation;
    return !programID.test(program.id) || !Number.isInteger(program.release_offset_ms) || program.release_offset_ms < 0 || !families.has(program.family) ||
      !executionKinds.has(execution?.kind) || !cancelPoints.has(execution?.cancel_point) ||
      (execution.source_program_id !== undefined && !programID.test(execution.source_program_id)) ||
      (execution.workflow_state_key !== undefined && execution.workflow_state_key !== 'workflow-main') ||
      (verifier !== undefined && ![verifier.source_sha256, verifier.artifact_sha256, verifier.profile_sha256, verifier.environment_sha256, verifier.policy_sha256].every((value) => digest.test(value ?? ''))) ||
      (resume !== undefined && (resume.from_program_id !== 'P13' || resume.state_key !== 'workflow-main' || !['freshness_changed', 'plan_grant_changed', 'expired'].includes(resume.transition ?? ''))) ||
      (delegation !== undefined && (!['delegation-main', 'delegation-widening'].includes(delegation.group_id ?? '') || delegation.parent_plan_role !== 'consumer-left' || !digest.test(delegation.parent_plan_sha256 ?? '') || delegation.max_delegated_calls !== 1 || delegation.child_reserved_calls !== 1)) ||
      !digest.test(program.plan_sha256) || !digest.test(program.grant_set_sha256) || !digest.test(program.workspace_fixture_sha256) || !privacyPartitions.has(program.privacy_partition) || !admissions.has(program.admission) || !sharingStates.has(program.sharing) || !dispositions.has(program.disposition);
  })) {
    throw new Error('campaign program projection is invalid');
  }
  let lastNS = -1;
  for (const [index, event] of candidate.walkthrough_events.entries()) {
    const program = programs.get(event.program_id);
    if (event.sequence !== index + 1 || !program || !eventTypes.has(event.type) || !Number.isFinite(event.at_ns) || event.at_ns < lastNS || !safeReason.test(event.reason ?? '') || !validEventReason(event, program) || (event.physical_execution_id !== undefined && !physicalID.test(event.physical_execution_id))) {
      throw new Error('campaign event projection is invalid');
    }
    lastNS = event.at_ns;
  }
  for (const id of ids) {
    const program = programs.get(id)!;
    const events = candidate.walkthrough_events.filter((event) => event.program_id === id);
    if (events.filter((event) => event.type === 'logical.released').length !== 1 || events.filter((event) => event.type === 'logical.terminal').length !== 1) {
      throw new Error(`campaign program ${program.id} lifecycle is incomplete`);
    }
    if (program.execution.kind === 'verify_workspace') {
      const sealed = events.find((event) => event.type === 'workspace.sealed');
      const verified = events.find((event) => event.type === 'verification.completed');
      if (!sealed?.reason || !verified?.reason?.startsWith(`${sealed.reason}:`)) throw new Error(`campaign program ${program.id} verifier identity is inconsistent`);
    }
  }
  return candidate;
}

export async function loadCampaignProjection(url = '/lab-data/authority-transparent-campaign.json'): Promise<CampaignProjection> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`campaign projection load failed (${response.status})`);
  return validateCampaignProjection(await response.json());
}
