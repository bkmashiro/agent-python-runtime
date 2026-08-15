export const TRAJECTORY_SCHEMA = 'pysolate.agent-trajectory.v0' as const;

export type EventType =
  | 'session.start' | 'session.end' | 'turn.start' | 'turn.end' | 'context.inject' | 'user.message'
  | 'model.request' | 'assistant.reasoning' | 'assistant.output' | 'tool.call' | 'tool.result'
  | 'subagent.dispatch' | 'subagent.result' | 'runtime.event' | 'workspace.change';

export type EventSource =
  | 'system' | 'developer' | 'user' | 'memory' | 'skill' | 'harness'
  | 'model' | 'tool' | 'subagent' | 'runtime' | 'workspace';

export interface ContentRef { kind: string; sha256: string }
export interface TokenUsage { input?: number; output?: number; reasoning?: number; cache_read?: number; cache_write?: number }

export interface TrajectoryEvent {
  sequence: number;
  event_id: string;
  previous_sha256: string;
  sha256: string;
  occurred_millis: number;
  type: EventType;
  source: EventSource;
  actor_id?: string;
  parent_event_id?: string;
  turn_id?: string;
  step_id?: string;
  model_visible: boolean;
  context_event_ids?: string[];
  body?: ContentRef;
  body_text?: string;
  content_type?: string;
  provider?: string;
  model?: string;
  finish_reason?: string;
  tool_call_id?: string;
  tool_name?: string;
  child_session_id?: string;
  run_id?: string;
  logical_request_id?: string;
  physical_execution_id?: string;
  span_id?: string;
  status?: string;
  duration_nanos?: number;
  usage?: TokenUsage;
}

export interface TrajectoryExport {
  schema_version: typeof TRAJECTORY_SCHEMA;
  privacy: 'private';
  session: {
    schema_version: typeof TRAJECTORY_SCHEMA;
    session_id: string;
    source_commit: string;
    header_sha256: string;
  };
  events: TrajectoryEvent[];
  seal_sha256: string;
}

export interface TrajectoryFilter {
  query?: string;
  sources?: EventSource[];
  types?: EventType[];
  toolCallID?: string;
  actorID?: string;
}

export interface TrajectoryIndex {
  schema_version: 'pysolate.trajectory-index.v0';
  default_session_id: string;
  sessions: Array<{
    session_id: string;
    label: string;
    kind: 'scripted' | 'experiment';
    file: string;
  }>;
}

const digestRE = /^sha256:[0-9a-f]{64}$/;
const eventIDRE = /^event-[0-9a-f]{16}$/;
const commitRE = /^[0-9a-f]{40}$/;
const sources = new Set<EventSource>(['system', 'developer', 'user', 'memory', 'skill', 'harness', 'model', 'tool', 'subagent', 'runtime', 'workspace']);
const types = new Set<EventType>(['session.start', 'session.end', 'turn.start', 'turn.end', 'context.inject', 'user.message', 'model.request', 'assistant.reasoning', 'assistant.output', 'tool.call', 'tool.result', 'subagent.dispatch', 'subagent.result', 'runtime.event', 'workspace.change']);
const bodyRequired = new Set<EventType>(['context.inject', 'user.message', 'assistant.reasoning', 'assistant.output', 'tool.call', 'tool.result', 'runtime.event', 'workspace.change']);

const topKeys = ['schema_version', 'privacy', 'session', 'events', 'seal_sha256'] as const;
const sessionKeys = ['schema_version', 'session_id', 'source_commit', 'header_sha256'] as const;
const eventKeys = [
  'sequence', 'event_id', 'previous_sha256', 'sha256', 'occurred_millis', 'type', 'source', 'actor_id',
  'parent_event_id', 'turn_id', 'step_id', 'model_visible', 'context_event_ids', 'body', 'body_text', 'content_type',
  'provider', 'model', 'finish_reason', 'tool_call_id', 'tool_name', 'child_session_id', 'run_id',
  'logical_request_id', 'physical_execution_id', 'span_id', 'status', 'duration_nanos', 'usage',
] as const;
const bodyKeys = ['kind', 'sha256'] as const;
const usageKeys = ['input', 'output', 'reasoning', 'cache_read', 'cache_write'] as const;

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

function exactKeys(value: unknown, allowed: readonly string[], label: string): asserts value is Record<string, unknown> {
  assert(value !== null && typeof value === 'object' && !Array.isArray(value), `${label} must be an object`);
  const allowedSet = new Set(allowed);
  for (const key of Object.keys(value)) assert(allowedSet.has(key), `unknown trajectory field ${label}.${key}`);
}

function uint(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

async function sha256(value: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value));
  return `sha256:${Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

export async function validateTrajectory(raw: TrajectoryExport): Promise<TrajectoryExport> {
  exactKeys(raw, topKeys, 'root');
  assert(raw.schema_version === TRAJECTORY_SCHEMA && raw.privacy === 'private', 'unsupported or non-private trajectory');
  exactKeys(raw.session, sessionKeys, 'session');
  assert(raw.session.schema_version === TRAJECTORY_SCHEMA && /^session-[0-9a-z-]{8,128}$/.test(raw.session.session_id), 'invalid trajectory session');
  assert(commitRE.test(raw.session.source_commit) && digestRE.test(raw.session.header_sha256), 'invalid trajectory identity');
  assert(digestRE.test(raw.seal_sha256), 'invalid private export seal');
  assert(Array.isArray(raw.events) && raw.events.length >= 2 && raw.events.length <= 100_000, 'invalid trajectory event count');

  const prior = new Map<string, TrajectoryEvent>();
  const calls = new Map<string, TrajectoryEvent>();
  let previous = raw.session.header_sha256;
  for (const [index, event] of raw.events.entries()) {
    exactKeys(event, eventKeys, `event[${index}]`);
    assert(event.sequence === index + 1 && event.event_id === `event-${(index + 1).toString(16).padStart(16, '0')}` && eventIDRE.test(event.event_id), 'invalid trajectory sequence');
    assert(event.previous_sha256 === previous && digestRE.test(event.sha256), 'trajectory hash chain mismatch');
    assert(uint(event.occurred_millis) && (index === 0 || event.occurred_millis >= raw.events[index - 1].occurred_millis), 'invalid trajectory time');
    assert(types.has(event.type) && sources.has(event.source) && typeof event.model_visible === 'boolean', 'invalid trajectory classification');
    if (event.parent_event_id) assert(prior.has(event.parent_event_id), 'trajectory parent is not prior');
    if (event.body !== undefined) {
      exactKeys(event.body, bodyKeys, `event[${index}].body`);
      assert(typeof event.body.kind === 'string' && event.body.kind.length > 0 && digestRE.test(event.body.sha256), 'invalid trajectory body reference');
      assert(typeof event.body_text === 'string', 'trajectory materialized body is missing');
    } else {
      assert(event.body_text === undefined && !bodyRequired.has(event.type) && !event.model_visible, 'trajectory materialized body is missing');
    }
    if (event.usage !== undefined) {
      exactKeys(event.usage, usageKeys, `event[${index}].usage`);
      for (const value of Object.values(event.usage)) assert(uint(value), 'invalid token usage');
    }
    if (event.type === 'model.request') {
      assert(Array.isArray(event.context_event_ids) && event.context_event_ids.length > 0 && typeof event.provider === 'string' && event.provider.length > 0 && typeof event.model === 'string' && event.model.length > 0, 'incomplete model request');
      const seen = new Set<string>();
      for (const id of event.context_event_ids) {
        const context = prior.get(id);
        assert(context !== undefined && context.body !== undefined && !seen.has(id), 'model request references non-prior context');
        seen.add(id);
      }
    } else assert(event.context_event_ids === undefined, 'context IDs outside model request');
    if (event.type === 'tool.call') {
      assert(typeof event.tool_call_id === 'string' && /^call-[0-9a-z-]{8,128}$/.test(event.tool_call_id) && typeof event.tool_name === 'string' && event.tool_name.length > 0 && !calls.has(event.tool_call_id), 'invalid or duplicate tool call');
      calls.set(event.tool_call_id, event);
    }
    if (event.type === 'tool.result' || event.type === 'runtime.event' || event.type === 'workspace.change') {
      const call = event.tool_call_id ? calls.get(event.tool_call_id) : undefined;
      assert(call !== undefined && (!event.tool_name || event.tool_name === call.tool_name), 'tool-linked trajectory event is orphaned');
    }
    if (event.type === 'runtime.event') assert(Boolean(event.tool_call_id && event.run_id) && Boolean(event.logical_request_id) === Boolean(event.physical_execution_id), 'runtime trajectory identity is incomplete');
    prior.set(event.event_id, event);
    previous = event.sha256;
  }
  assert(raw.events[0].type === 'session.start' && raw.events.at(-1)?.type === 'session.end', 'trajectory session is not terminal');
  const { seal_sha256: _seal, ...unsigned } = raw;
  const actualSeal = await sha256(`pysolate.agent-trajectory.event.v0\0private-export\0${JSON.stringify(unsigned)}`);
  assert(actualSeal === raw.seal_sha256, 'private export seal mismatch');
  return raw;
}

export function modelContext(value: TrajectoryExport, requestEventID: string): TrajectoryEvent[] {
  const request = value.events.find((event) => event.event_id === requestEventID && event.type === 'model.request');
  if (!request?.context_event_ids) throw new Error('model request not found');
  const byID = new Map(value.events.map((event) => [event.event_id, event]));
  return request.context_event_ids.map((id) => {
    const event = byID.get(id);
    if (!event) throw new Error('model context event not found');
    return event;
  });
}

export function filterTrajectory(value: TrajectoryExport, filter: TrajectoryFilter): TrajectoryEvent[] {
  const query = filter.query?.trim().toLowerCase();
  const sourceSet = filter.sources?.length ? new Set(filter.sources) : undefined;
  const typeSet = filter.types?.length ? new Set(filter.types) : undefined;
  return value.events.filter((event) => {
    if (sourceSet && !sourceSet.has(event.source)) return false;
    if (typeSet && !typeSet.has(event.type)) return false;
    if (filter.toolCallID && event.tool_call_id !== filter.toolCallID) return false;
    if (filter.actorID && event.actor_id !== filter.actorID) return false;
    if (query) {
      const haystack = [event.type, event.source, event.actor_id, event.tool_name, event.status, event.body_text, event.run_id, event.logical_request_id, event.physical_execution_id].filter(Boolean).join('\n').toLowerCase();
      if (!haystack.includes(query)) return false;
    }
    return true;
  });
}

export function validateTrajectoryIndex(raw: TrajectoryIndex): TrajectoryIndex {
  exactKeys(raw, ['schema_version', 'default_session_id', 'sessions'], 'trajectory index');
  assert(raw.schema_version === 'pysolate.trajectory-index.v0' && /^[-a-z0-9]{8,80}$/.test(raw.default_session_id), 'invalid trajectory index');
  assert(Array.isArray(raw.sessions) && raw.sessions.length > 0 && raw.sessions.length <= 32, 'invalid trajectory session count');
  const seen = new Set<string>();
  for (const session of raw.sessions) {
    exactKeys(session, ['session_id', 'label', 'kind', 'file'], 'trajectory index session');
    assert(/^[-a-z0-9]{8,80}$/.test(session.session_id) && !seen.has(session.session_id), 'invalid trajectory session identity');
    assert(typeof session.label === 'string' && session.label.length > 0 && session.label.length <= 120, 'invalid trajectory session label');
    assert((session.kind === 'scripted' || session.kind === 'experiment') && /^[a-z0-9-]+\.json$/.test(session.file), 'invalid trajectory session file');
    seen.add(session.session_id);
  }
  assert(seen.has(raw.default_session_id), 'trajectory default session is missing');
  return raw;
}

export async function loadTrajectoryIndex(): Promise<TrajectoryIndex> {
  const response = await fetch('/lab-data/index.json', { cache: 'no-store' });
  if (!response.ok) throw new Error(`trajectory index fetch failed: ${response.status}`);
  return validateTrajectoryIndex(await response.json() as TrajectoryIndex);
}

export async function loadTrajectory(url = '/lab-data/trajectory.json'): Promise<TrajectoryExport> {
  if (!/^\/lab-data\/[a-z0-9-]+\.json$/.test(url)) throw new Error('invalid trajectory URL');
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`trajectory fetch failed: ${response.status}`);
  return validateTrajectory(await response.json() as TrajectoryExport);
}
