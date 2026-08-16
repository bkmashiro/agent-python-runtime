export const TRAJECTORY_SCHEMA = 'pysolate.causal-evidence.v1' as const;
export const TRAJECTORY_INDEX_SCHEMA = 'pysolate.causal-evidence-index.v1' as const;

export type EvidenceProfile = 'production_rollback' | 'experiment_full';
export type EventSource = 'system' | 'developer' | 'user' | 'memory' | 'skill' | 'harness' | 'model' | 'tool' | 'subagent' | 'runtime' | 'workspace';
export type EvidenceType = string;

export interface BodyRef { kind: string; sha256: string }
export interface RawEvidenceEvent {
  schema_version: typeof TRAJECTORY_SCHEMA;
  ordinal: number;
  event_id: string;
  occurred_nanos: number;
  type: EvidenceType;
  actor_id: string;
  parent_event_ids?: string[];
  payload: Record<string, unknown>;
  body?: BodyRef;
}
export interface RawEvidenceExport {
  schema_version: typeof TRAJECTORY_SCHEMA;
  profile: EvidenceProfile;
  privacy: 'private' | 'portable';
  trace_id: string;
  header_sha256: string;
  header: {
    schema_version: typeof TRAJECTORY_SCHEMA;
    trace_id: string;
    source_commit: string;
    root_execution_id: string;
    header_sha256: string;
  };
  events: RawEvidenceEvent[];
  seal_sha256: string;
}

// TrajectoryEvent is a deterministic read-only presentation over the current
// causal-evidence contract. It is not a compatibility decoder for v0.
export interface TrajectoryEvent extends RawEvidenceEvent {
  sequence: number;
  source: EventSource;
  occurred_millis: number;
  parent_event_id?: string;
  source_event_ids?: string[];
  status?: string;
  tool_call_id?: string;
  tool_name?: string;
  run_id?: string;
  span_id?: string;
  logical_request_id?: string;
  physical_execution_id?: string;
  child_session_id?: string;
  sha256: string;
  body_text?: string;
  content_type?: string;
  turn_id?: string;
  step_id?: string;
  provider?: string;
  model?: string;
  usage?: { input?: number; output?: number; reasoning?: number; cache_read?: number };
}
export interface TrajectoryExport extends Omit<RawEvidenceExport, 'events'> {
  events: TrajectoryEvent[];
  session: { session_id: string; source_commit: string };
}
export interface TrajectoryIndex {
  schema_version: typeof TRAJECTORY_INDEX_SCHEMA;
  default_view_id: string;
  views: Array<{ view_id: string; trace_id: string; label: string; kind: 'experiment' | 'production'; file: string }>;
}

const digestRE = /^sha256:[0-9a-f]{64}$/;
const commitRE = /^[0-9a-f]{40}$/;
const idRE = /^[a-z][a-z0-9_.:-]{7,127}$/;
const eventTypes = new Set<EvidenceType>([
  'trace.started', 'trace.ended', 'authority.snapshot', 'effect.transition', 'tool.decision', 'workspace.terminal', 'execution.attempt',
  'model.context', 'model.body', 'model.output', 'source.document', 'source.body', 'source.occurrence', 'source.decision', 'source.executed_line',
  'subagent.context', 'subagent.runtime', 'subagent.workspace', 'workspace.file', 'evidence.truncated', 'runtime.observation', 'resource.sample',
]);
const productionTypes = new Set<EvidenceType>([
  'trace.started', 'trace.ended', 'authority.snapshot', 'effect.transition', 'workspace.terminal', 'execution.attempt', 'evidence.truncated',
]);
const bodyTypes = new Set<EvidenceType>(['model.body', 'source.body', 'workspace.file', 'runtime.observation']);
const requiredBodyTypes = new Set<EvidenceType>(['model.body', 'source.body', 'workspace.file']);
const topKeys = ['schema_version', 'profile', 'privacy', 'trace_id', 'header_sha256', 'header', 'events', 'seal_sha256'];
const headerKeys = ['schema_version', 'trace_id', 'source_commit', 'root_execution_id', 'header_sha256'];
const eventKeys = ['schema_version', 'ordinal', 'event_id', 'occurred_nanos', 'type', 'actor_id', 'parent_event_ids', 'payload', 'body'];

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}
function object(value: unknown, label: string): Record<string, unknown> {
  assert(value !== null && typeof value === 'object' && !Array.isArray(value), `${label} must be an object`);
  return value as Record<string, unknown>;
}
function exactKeys(value: Record<string, unknown>, required: string[], optional: string[] = [], label = 'value') {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(value)) assert(allowed.has(key), `unknown causal evidence field ${label}.${key}`);
  for (const key of required) assert(Object.hasOwn(value, key), `missing causal evidence field ${label}.${key}`);
}
function uint(value: unknown): value is number { return Number.isSafeInteger(value) && Number(value) >= 0; }
async function sha256(text: string): Promise<string> {
  const bytes = new TextEncoder().encode(text);
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return `sha256:${Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}
async function evidenceHash(label: string, value: unknown) {
  return sha256(`pysolate.causal-evidence.v1\0${label}\0${JSON.stringify(value)}`);
}

function validatePayload(event: RawEvidenceEvent) {
  const payload = object(event.payload, `event[${event.ordinal}].payload`);
  const digest = (key: string) => assert(typeof payload[key] === 'string' && digestRE.test(payload[key] as string), `invalid ${event.type}.${key}`);
  switch (event.type) {
    case 'trace.started': exactKeys(payload, ['status']); assert(payload.status === 'running', 'invalid trace start'); break;
    case 'trace.ended': exactKeys(payload, ['status', 'evidence_complete']); assert(payload.status === 'completed' || payload.status === 'failed', 'invalid trace end'); assert(typeof payload.evidence_complete === 'boolean', 'invalid evidence completeness'); break;
    case 'authority.snapshot': exactKeys(payload, ['run_id', 'capability_plan_sha256', 'policy_sha256', 'freshness_sha256', 'grants_sha256']); digest('capability_plan_sha256'); digest('policy_sha256'); digest('freshness_sha256'); digest('grants_sha256'); break;
    case 'effect.transition': exactKeys(payload, ['call_id', 'state'], ['receipt_id', 'compensator', 'reconciliation_reason']); break;
    case 'tool.decision': exactKeys(payload, ['approval_disposition', 'arguments_sha256', 'broker_outcome', 'call_id', 'capability', 'capability_plan_sha256', 'mechanism', 'operation_index', 'receipt_id', 'run_id'], ['approval_request_id', 'parent_call_id', 'result_sha256']); digest('arguments_sha256'); digest('capability_plan_sha256'); if (payload.result_sha256 !== undefined) digest('result_sha256'); break;
    case 'workspace.terminal': exactKeys(payload, ['base_workspace_sha256', 'disposition'], ['result_workspace_sha256']); digest('base_workspace_sha256'); if (payload.result_workspace_sha256 !== undefined) digest('result_workspace_sha256'); break;
    case 'execution.attempt': exactKeys(payload, ['run_id', 'attempt_id', 'status'], ['prepared_image_sha256']); if (payload.prepared_image_sha256 !== undefined) digest('prepared_image_sha256'); break;
    case 'model.context': case 'model.body': exactKeys(payload, ['context_sha256', 'brief_sha256', 'availability']); digest('context_sha256'); digest('brief_sha256'); break;
    case 'model.output': exactKeys(payload, ['availability'], ['output_sha256']); if (payload.output_sha256 !== undefined) digest('output_sha256'); assert((payload.availability === 'available') === (payload.output_sha256 !== undefined), 'model output availability mismatch'); break;
    case 'source.document': exactKeys(payload, ['document_id', 'source_sha256', 'availability']); digest('document_id'); digest('source_sha256'); break;
    case 'source.body': exactKeys(payload, ['document_id', 'source_sha256', 'display_path', 'availability']); digest('document_id'); digest('source_sha256'); break;
    case 'source.occurrence': exactKeys(payload, ['document_id', 'source_sha256', 'occurrence_id', 'start_line', 'start_column', 'end_line', 'end_column', 'capability', 'dynamic_occurrence']); digest('document_id'); digest('source_sha256'); digest('occurrence_id'); break;
    case 'source.decision': {
      exactKeys(payload, ['decision_id', 'capability_plan_sha256', 'occurrence_id', 'dynamic_occurrence', 'claim_level', 'admitted'], ['reasons', 'receipt_id']);
      digest('decision_id'); digest('capability_plan_sha256'); digest('occurrence_id');
      const reasons = payload.reasons === undefined ? [] : payload.reasons;
      assert(Array.isArray(reasons) && reasons.every((reason) => typeof reason === 'string' && idRE.test(reason)) && reasons.every((reason, index) => index === 0 || reasons[index - 1] < reason), 'invalid source reasons');
      assert(payload.admitted === true ? reasons.length === 0 && typeof payload.receipt_id === 'string' && idRE.test(payload.receipt_id) : reasons.length > 0 && (payload.receipt_id === undefined || (typeof payload.receipt_id === 'string' && idRE.test(payload.receipt_id))), 'invalid source decision disposition');
      break;
    }
    case 'source.executed_line': { exactKeys(payload, ['source_sha256', 'availability'], ['instrumentation', 'instruction_offset', 'start_line', 'start_column', 'end_line', 'end_column']); digest('source_sha256'); const details = ['instrumentation','instruction_offset','start_line','start_column','end_line','end_column'].every((key) => payload[key] !== undefined); assert(payload.availability === 'available' ? details : !details, 'executed-line availability mismatch'); break; }
    case 'subagent.context': exactKeys(payload, ['child_id', 'context_sha256', 'brief_sha256', 'availability']); digest('context_sha256'); digest('brief_sha256'); break;
    case 'subagent.runtime': exactKeys(payload, ['brief_sha256', 'child_id', 'child_plan_sha256', 'context_sha256', 'depth', 'descriptor_sha256', 'execution_profile_sha256', 'fresh_run_id', 'inputs_sha256', 'parent_lineage_sha256', 'parent_live_state_inherited', 'parent_stream_epoch', 'parent_workspace_root_sha256', 'prepared_image_sha256', 'privacy_partition', 'source_occurrence', 'source_sha256']); for (const key of ['brief_sha256', 'child_plan_sha256', 'context_sha256', 'descriptor_sha256', 'execution_profile_sha256', 'inputs_sha256', 'parent_lineage_sha256', 'parent_workspace_root_sha256', 'prepared_image_sha256', 'source_sha256']) digest(key); assert(payload.parent_live_state_inherited === false && payload.parent_lineage_sha256 === payload.parent_workspace_root_sha256, 'parent live state/root inheritance forbidden'); break;
    case 'subagent.workspace': exactKeys(payload, ['child_id', 'base_root_sha256', 'result_root_sha256', 'workspace_sha256', 'depth', 'changed_entries', 'changed_bytes', 'disposition']); digest('base_root_sha256'); digest('result_root_sha256'); digest('workspace_sha256'); break;
    case 'workspace.file': exactKeys(payload, ['workspace_sha256', 'path', 'content_sha256', 'availability']); digest('workspace_sha256'); digest('content_sha256'); break;
    case 'evidence.truncated': exactKeys(payload, ['scope', 'reason', 'dropped_events']); assert(uint(payload.dropped_events) && payload.dropped_events > 0, 'invalid truncation'); break;
    case 'runtime.observation': exactKeys(payload, ['observation_type', 'sequence', 'observation_sha256'], ['parent_sequence']); digest('observation_sha256'); break;
    case 'resource.sample': exactKeys(payload, ['scope', 'wall_nanos', 'process_cpu_nanos', 'process_cpu_availability', 'peak_rss_bytes', 'peak_rss_availability']); assert(uint(payload.wall_nanos) && payload.wall_nanos > 0 && uint(payload.process_cpu_nanos) && uint(payload.peak_rss_bytes), 'invalid resource sample'); assert((payload.process_cpu_availability === 'available') === (Number(payload.process_cpu_nanos) > 0), 'CPU availability mismatch'); assert((payload.peak_rss_availability === 'available') === (Number(payload.peak_rss_bytes) > 0), 'RSS availability mismatch'); break;
  }
}

function validateRelations(event: RawEvidenceEvent, prior: Map<string, RawEvidenceEvent>) {
  const parents = (event.parent_event_ids ?? []).map((id) => prior.get(id)!);
  if (event.type === 'effect.transition') {
    assert(parents.length === 1, 'effect transition requires one predecessor');
    const parent = parents[0]; const state = String(event.payload.state); const priorState = String(parent.payload.state);
    if (state === 'intent') assert(parent.type === 'authority.snapshot', 'effect intent authority mismatch');
    else if (state === 'denied' && parent.type === 'authority.snapshot') { /* pre-dispatch denial */ }
    else {
      assert(parent.type === 'effect.transition' && parent.payload.call_id === event.payload.call_id, 'effect call chain mismatch');
      const allowed: Record<string, string[]> = { started:['intent'], committed:['intent','started'], failed:['intent','started'], timed_out:['intent','started'], ambiguous:['started'], denied:['intent'], reconciliation_required:['intent','started','failed','timed_out','ambiguous'], compensated:['committed'], cleanup_only:['started','failed','timed_out'] };
      assert((allowed[state] ?? []).includes(priorState), 'invalid effect lifecycle transition');
    }
  }
  if (event.type === 'tool.decision') {
    const effect = parents.find((item) => item.type === 'effect.transition'); const authority = parents.find((item) => item.type === 'authority.snapshot');
    assert(parents.length === 2 && effect && authority, 'tool decision parents mismatch');
    assert(effect.payload.call_id === event.payload.call_id && effect.payload.receipt_id === event.payload.receipt_id && authority.payload.run_id === event.payload.run_id && authority.payload.capability_plan_sha256 === event.payload.capability_plan_sha256, 'tool decision authority/effect identity mismatch');
  }
  if (event.type === 'source.body') {
    assert(parents.length === 1 && parents[0].type === 'source.document', 'source body parent mismatch');
    assert(parents[0].payload.document_id === event.payload.document_id && parents[0].payload.source_sha256 === event.payload.source_sha256, 'source body identity mismatch');
  }
  if (event.type === 'source.occurrence') {
    assert(parents.length === 1 && parents[0].type === 'source.document', 'source occurrence parent mismatch');
    assert(parents[0].payload.document_id === event.payload.document_id && parents[0].payload.source_sha256 === event.payload.source_sha256, 'source identity mismatch');
  }
  if (event.type === 'source.decision') {
    const occurrence = parents.find((item) => item.type === 'source.occurrence'); const effect = parents.find((item) => item.type === 'effect.transition'); const tool = parents.find((item) => item.type === 'tool.decision'); const authority = parents.find((item) => item.type === 'authority.snapshot');
    assert(occurrence && occurrence.payload.occurrence_id === event.payload.occurrence_id && occurrence.payload.dynamic_occurrence === event.payload.dynamic_occurrence, 'source decision occurrence mismatch');
    if (event.payload.receipt_id === undefined) {
      assert(event.payload.admitted === false && parents.length === 2 && authority && !effect && !tool && authority.payload.capability_plan_sha256 === event.payload.capability_plan_sha256, 'rejected source decision authority mismatch');
    } else {
      assert(parents.length === 3 && effect && tool && !authority && occurrence.payload.capability === tool.payload.capability, 'source decision parents/tool mismatch');
      assert(event.payload.receipt_id === effect.payload.receipt_id && event.payload.receipt_id === tool.payload.receipt_id && event.payload.capability_plan_sha256 === tool.payload.capability_plan_sha256, 'source decision receipt/Plan mismatch');
      assert(event.payload.admitted === true ? (effect.payload.state === 'committed' && tool.payload.broker_outcome === 'ok') : (effect.payload.state === 'denied' && tool.payload.broker_outcome === 'denied'), 'source decision disposition mismatch');
    }
  }
  if (event.type === 'subagent.runtime') {
    const context = parents.find((parent) => parent.type === 'subagent.context');
    const source = parents.find((parent) => parent.type === 'source.document');
    assert(parents.length === 2 && context && source && context.payload.child_id === event.payload.child_id && context.payload.context_sha256 === event.payload.context_sha256 && context.payload.brief_sha256 === event.payload.brief_sha256 && context.payload.availability === 'available' && source.payload.source_sha256 === event.payload.source_sha256 && source.payload.availability === 'available', 'subagent runtime context/source mismatch');
  }
  if (event.type === 'subagent.workspace') {
    assert(parents.length === 1 && parents[0].type === 'subagent.runtime' && parents[0].payload.child_id === event.payload.child_id && parents[0].payload.parent_workspace_root_sha256 === event.payload.base_root_sha256, 'subagent workspace parent/base mismatch');
  }
  if (event.type === 'workspace.file') {
    assert(parents.length === 1 && parents[0].type === 'subagent.workspace' && parents[0].payload.result_root_sha256 === event.payload.workspace_sha256 && parents[0].payload.disposition === 'selected', 'workspace file parent mismatch');
  }
}

export async function validateTrajectory(rawValue: unknown): Promise<TrajectoryExport> {
  const root = object(rawValue, 'root');
  exactKeys(root, topKeys, [], 'root');
  const raw = root as unknown as RawEvidenceExport;
  assert(raw.schema_version === TRAJECTORY_SCHEMA && (raw.profile === 'experiment_full' || raw.profile === 'production_rollback'), 'unsupported causal evidence profile');
  assert(raw.privacy === 'portable', 'Lab accepts body-safe portable evidence only');
  assert(idRE.test(raw.trace_id) && digestRE.test(raw.header_sha256), 'invalid trace identity');
  exactKeys(object(raw.header, 'header'), headerKeys, [], 'header');
  assert(raw.header.schema_version === TRAJECTORY_SCHEMA && raw.header.trace_id === raw.trace_id && raw.header.header_sha256 === raw.header_sha256, 'header cross-binding mismatch');
  assert(commitRE.test(raw.header.source_commit) && idRE.test(raw.header.root_execution_id), 'invalid header source identity');
  const unsignedHeader = { ...raw.header, header_sha256: '' };
  assert(await evidenceHash('header', unsignedHeader) === raw.header_sha256, 'header seal mismatch');
  assert(Array.isArray(raw.events) && raw.events.length > 0 && raw.events.length < 100_000, 'invalid event count');

  const prior = new Map<string, RawEvidenceEvent>();
  let previousOrdinal = 0;
  let truncated = false;
  let terminalComplete = false;
  let traceStarted = 0;
  let traceEnded = 0;
  const effectStates = new Map<string, string>();
  for (const [eventIndex, eventValue] of raw.events.entries()) {
    const event = object(eventValue, 'event') as unknown as RawEvidenceEvent;
    exactKeys(event as unknown as Record<string, unknown>, eventKeys.filter((key) => key !== 'parent_event_ids' && key !== 'body'), ['parent_event_ids', 'body'], `event[${event.ordinal}]`);
    assert(event.schema_version === TRAJECTORY_SCHEMA && uint(event.ordinal) && event.ordinal > previousOrdinal && eventTypes.has(event.type), 'invalid causal event envelope');
    assert(digestRE.test(event.event_id) && uint(event.occurred_nanos) && idRE.test(event.actor_id), 'invalid causal event identity');
    assert(Array.isArray(event.parent_event_ids ?? []) && (event.parent_event_ids ?? []).length <= 256, 'invalid event parents');
    for (const parent of event.parent_event_ids ?? []) assert(prior.has(parent), 'causal parent is not prior');
    if (event.body !== undefined) {
      exactKeys(object(event.body, 'body'), ['kind', 'sha256'], [], 'body');
      assert(bodyTypes.has(event.type) && digestRE.test(event.body.sha256), 'invalid body reference');
    }
    assert(event.body === undefined, 'portable evidence contains private body reference');
    assert(!requiredBodyTypes.has(event.type) && event.type !== 'runtime.observation', 'portable projection retained body-only event');
    if (raw.profile === 'production_rollback') assert(productionTypes.has(event.type), 'production profile leaked experiment telemetry');
    assert(!truncated || event.type === 'trace.ended', 'events follow truncation marker');
    validatePayload(event);
    validateRelations(event, prior);
    if (event.type === 'effect.transition') effectStates.set(String(event.payload.call_id), String(event.payload.state));
    if ((event.type === 'execution.attempt' || event.type === 'authority.snapshot') && event.payload.run_id !== raw.header.root_execution_id) {
      throw new Error('event is not bound to root execution');
    }
    if (event.type === 'subagent.runtime') {
      const payload = event.payload;
      assert(uint(payload.depth), 'invalid subagent depth');
      const descriptor = {
        schema_version: 'pysolate.subagent-descriptor.v1', child_id: payload.child_id, parent_stream_epoch: payload.parent_stream_epoch,
        parent_lineage_sha256: payload.parent_lineage_sha256, source_occurrence: payload.source_occurrence, source_sha256: payload.source_sha256,
        inputs_sha256: payload.inputs_sha256, artifact_sha256: payload.prepared_image_sha256, execution_profile_sha256: payload.execution_profile_sha256,
        child_plan_sha256: payload.child_plan_sha256, privacy_partition: payload.privacy_partition, depth: payload.depth,
      };
      assert(await sha256(JSON.stringify(descriptor)) === payload.descriptor_sha256, 'subagent descriptor identity mismatch');
    }
    if (event.type === 'subagent.workspace') {
      const payload = event.payload;
      assert(uint(payload.depth) && uint(payload.changed_entries) && uint(payload.changed_bytes), 'invalid workspace root counters');
      const root = {
        schema_version: 'pysolate.workspace-root.v1', workspace_sha256: payload.workspace_sha256,
        parent_identity_sha256: payload.base_root_sha256, depth: payload.depth,
        changed_entries: payload.changed_entries, changed_bytes: payload.changed_bytes,
      };
      assert(await sha256(JSON.stringify(root)) === payload.result_root_sha256, 'workspace root identity mismatch');
    }
    if (event.type === 'source.decision' && event.payload.receipt_id !== undefined) {
      const parents = (event.parent_event_ids ?? []).map((id) => prior.get(id)!);
      const occurrence = parents.find((parent) => parent.type === 'source.occurrence')!;
      const tool = parents.find((parent) => parent.type === 'tool.decision')!;
      const fields = [
        'pysolate-receipt-v3', tool.payload.run_id, tool.payload.capability_plan_sha256, tool.payload.call_id,
        tool.payload.parent_call_id ?? '', tool.payload.approval_request_id ?? '', tool.payload.capability,
        String(tool.payload.operation_index), String(tool.payload.arguments_sha256).slice(7),
        'pysolate.source-binding.v0', 'source_bound', occurrence.payload.document_id, occurrence.payload.source_sha256,
        occurrence.payload.occurrence_id, occurrence.payload.capability, String(occurrence.payload.dynamic_occurrence),
        String(occurrence.payload.start_line), String(occurrence.payload.start_column), String(occurrence.payload.end_line), String(occurrence.payload.end_column),
      ];
      const identity = await sha256(`${fields.join('\0')}\0`);
      assert(`rcpt_${identity.slice(7)}` === event.payload.receipt_id, 'source receipt identity mismatch');
    }
    const unsignedEvent = { ...event, event_id: '' };
    assert(await evidenceHash(`event\0${raw.header_sha256}`, unsignedEvent) === event.event_id, 'event identity mismatch');
    if (event.type === 'evidence.truncated') { assert(eventIndex === raw.events.length - 2, 'truncation marker is not terminal'); truncated = true; }
    if (event.type === 'trace.started') traceStarted += 1;
    if (event.type === 'trace.ended') {
      traceEnded += 1;
      terminalComplete = event.payload.evidence_complete === true;
    }
    prior.set(event.event_id, event);
    previousOrdinal = event.ordinal;
  }
  assert(traceStarted === 1 && traceEnded === 1 && raw.events.at(-1)?.type === 'trace.ended', 'invalid causal evidence lifecycle');
  assert(!(truncated && terminalComplete), 'truncated evidence claimed complete');
  if (terminalComplete) {
    for (const state of effectStates.values()) assert(!['intent', 'started', 'ambiguous'].includes(state), 'complete evidence has an outstanding effect');
  }
  const unsignedExport = { ...raw, seal_sha256: '' };
  assert(await evidenceHash('export', unsignedExport) === raw.seal_sha256, 'export seal mismatch');

  const events = raw.events.map(presentEvent);
  return { ...raw, events, session: { session_id: raw.trace_id, source_commit: raw.header.source_commit } };
}

function presentEvent(event: RawEvidenceEvent): TrajectoryEvent {
  const payload = event.payload;
  const statusValue = payload.status ?? payload.state ?? payload.disposition ?? payload.availability;
  const source = inferSource(event.type);
  return {
    ...event,
    sequence: event.ordinal,
    source,
    occurred_millis: event.occurred_nanos / 1_000_000,
    parent_event_id: event.parent_event_ids?.[0],
    source_event_ids: event.parent_event_ids,
    status: typeof statusValue === 'string' ? statusValue : undefined,
    tool_call_id: typeof payload.call_id === 'string' ? payload.call_id : undefined,
    tool_name: typeof payload.capability === 'string' ? payload.capability : undefined,
    run_id: typeof payload.run_id === 'string' ? payload.run_id : typeof payload.fresh_run_id === 'string' ? payload.fresh_run_id : undefined,
    child_session_id: typeof payload.child_id === 'string' ? payload.child_id : undefined,
    span_id: typeof payload.occurrence_id === 'string' ? payload.occurrence_id : undefined,
    sha256: event.event_id,
  };
}
function inferSource(type: EvidenceType): EventSource {
  if (type.startsWith('model')) return 'model';
  if (type.startsWith('subagent')) return 'subagent';
  if (type.startsWith('workspace')) return 'workspace';
  if (type.startsWith('runtime') || type.startsWith('execution') || type.startsWith('trace') || type.startsWith('evidence')) return 'runtime';
  if (type.startsWith('effect')) return 'tool';
  return 'harness';
}

export function filterTrajectory(trajectory: TrajectoryExport, filters: { query?: string; sources?: EventSource[]; toolCallID?: string } = {}) {
  const query = filters.query?.trim().toLowerCase();
  return trajectory.events.filter((event) => {
    if (filters.sources && !filters.sources.includes(event.source)) return false;
    if (filters.toolCallID && event.tool_call_id !== filters.toolCallID && !event.parent_event_ids?.some((id) => trajectory.events.find((candidate) => candidate.event_id === id)?.tool_call_id === filters.toolCallID)) return false;
    if (!query) return true;
    return JSON.stringify(event).toLowerCase().includes(query);
  });
}
export function modelContext(trajectory: TrajectoryExport, _eventID: string) {
  return trajectory.events.filter((event) => event.type === 'model.context');
}

export function validateTrajectoryIndex(rawValue: unknown): TrajectoryIndex {
  const raw = object(rawValue, 'trajectory index') as unknown as TrajectoryIndex;
  exactKeys(raw as unknown as Record<string, unknown>, ['schema_version', 'default_view_id', 'views'], [], 'trajectory index');
  assert(raw.schema_version === TRAJECTORY_INDEX_SCHEMA && idRE.test(raw.default_view_id), 'invalid trajectory index');
  assert(Array.isArray(raw.views) && raw.views.length > 0 && raw.views.length <= 8, 'invalid trajectory index views');
  const seen = new Set<string>();
  for (const view of raw.views) {
    exactKeys(object(view, 'trajectory index view'), ['view_id', 'trace_id', 'label', 'kind', 'file'], [], 'trajectory index view');
    assert(idRE.test(view.view_id) && !seen.has(view.view_id) && idRE.test(view.trace_id), 'invalid indexed trace identity');
    assert(typeof view.label === 'string' && view.label.length > 0 && view.label.length <= 120, 'invalid trace label');
    assert((view.kind === 'experiment' || view.kind === 'production') && /^[a-z0-9-]+\.json$/.test(view.file), 'invalid trace file');
    seen.add(view.view_id);
  }
  assert(seen.has(raw.default_view_id), 'default trace view missing');
  return raw;
}
export async function loadTrajectoryIndex(): Promise<TrajectoryIndex> {
  const response = await fetch('/lab-data/index.json', { cache: 'no-store' });
  if (!response.ok) throw new Error(`trajectory index fetch failed: ${response.status}`);
  const text = await response.text();
  assert(new TextEncoder().encode(text).length <= 64 * 1024, 'trajectory index exceeds byte budget');
  return validateTrajectoryIndex(JSON.parse(text));
}
export async function loadTrajectory(url = '/lab-data/experiment-full-public.json'): Promise<TrajectoryExport> {
  if (!/^\/lab-data\/[a-z0-9-]+\.json$/.test(url)) throw new Error('invalid trajectory URL');
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`trajectory fetch failed: ${response.status}`);
  const text = await response.text();
  assert(new TextEncoder().encode(text).length <= 16 * 1024 * 1024, 'trajectory exceeds byte budget');
  return validateTrajectory(JSON.parse(text));
}
