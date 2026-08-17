import { expectedTaskSnapshotIdentity } from './taskIdentity';

export interface TaskSource {
  id: string;
  role: string;
  file: string;
  source: string;
}

export interface TaskSourceRef {
  source_id: string;
  file: string;
  start_line: number;
  end_line: number;
}

export interface TaskWorkspaceChange {
  path: string;
  kind: string;
  after_sha256: string;
  size: number;
}

export interface TaskEvent {
  sequence: number;
  parent_sequence?: number;
  span_id: string;
  parent_span_id?: string;
  agent_id: string;
  parent_agent_id?: string;
  agent_role: string;
  started_millis: number;
  ended_millis: number;
  source?: TaskSourceRef;
  workspace_id?: string;
  workspace_changes?: TaskWorkspaceChange[];
  type: string;
  action: string;
  outcome: string;
  checkpoint_sha256?: string;
  checkpoint_status?: string;
  input_sha256?: string;
  output_sha256?: string;
  count: number;
  relative_elapsed_millis: number;
}

export interface TaskSnapshot {
  schema_version: 'pysolate.lab-task.v1';
  identity: string;
  id: 'dev-workspace-summary';
  title: string;
  task: string;
  status: 'passed';
  expected_artifact: string;
  sources: TaskSource[];
  events: TaskEvent[];
  stats: {
    duration_millis: number;
    events: number;
    agents: number;
    workspace_changes: number;
  };
}

const digest = /^sha256:[0-9a-f]{64}$/;
const taskID = /^[a-z][a-z0-9_-]*$/;
const privateMarkers = ['/users/', '/home/', '\\users\\', '.hermes', 'file://', 'private://', 'prompt_body', 'provider_request', 'provider_response', 'trace_body', 'workspace_body'];

function object(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} is invalid`);
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, keys: string[], label: string) {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} has unknown or missing fields`);
}

function nonempty(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

function relativePath(value: unknown): value is string {
  return nonempty(value) && !value.startsWith('/') && !value.startsWith('\\') && !value.split(/[\\/]/).includes('..');
}

function assertUniqueJSONKeys(raw: string) {
  let offset = 0;
  const whitespace = () => { while (/\s/.test(raw[offset] ?? '')) offset += 1; };
  const string = () => {
    whitespace();
    if (raw[offset] !== '"') throw new Error('invalid task snapshot JSON');
    const start = offset++;
    while (offset < raw.length) {
      if (raw[offset] === '\\') { offset += 2; continue; }
      if (raw[offset++] === '"') return JSON.parse(raw.slice(start, offset)) as string;
    }
    throw new Error('invalid task snapshot JSON');
  };
  const value = (): void => {
    whitespace();
    if (raw[offset] === '{') { objectValue(); return; }
    if (raw[offset] === '[') { arrayValue(); return; }
    if (raw[offset] === '"') { string(); return; }
    const start = offset;
    while (offset < raw.length && !/[\s,}\]]/.test(raw[offset])) offset += 1;
    if (start === offset) throw new Error('invalid task snapshot JSON');
  };
  const objectValue = () => {
    offset += 1;
    const keys = new Set<string>();
    whitespace();
    if (raw[offset] === '}') { offset += 1; return; }
    while (offset < raw.length) {
      const key = string();
      if (keys.has(key)) throw new Error(`task snapshot contains duplicate JSON key: ${key}`);
      keys.add(key);
      whitespace();
      if (raw[offset++] !== ':') throw new Error('invalid task snapshot JSON');
      value();
      whitespace();
      if (raw[offset] === '}') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid task snapshot JSON');
    }
    throw new Error('invalid task snapshot JSON');
  };
  const arrayValue = () => {
    offset += 1;
    whitespace();
    if (raw[offset] === ']') { offset += 1; return; }
    while (offset < raw.length) {
      value();
      whitespace();
      if (raw[offset] === ']') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid task snapshot JSON');
    }
    throw new Error('invalid task snapshot JSON');
  };
  value();
  whitespace();
  if (offset !== raw.length) throw new Error('invalid task snapshot JSON');
}

function validateTaskSnapshotShape(raw: unknown): TaskSnapshot {
  const root = object(raw, 'task snapshot');
  exactKeys(root, ['schema_version', 'identity', 'id', 'title', 'task', 'status', 'expected_artifact', 'sources', 'events', 'stats'], 'task snapshot');
  if (root.schema_version !== 'pysolate.lab-task.v1' || typeof root.identity !== 'string' || !digest.test(root.identity) || root.id !== 'dev-workspace-summary' || root.status !== 'passed' || !nonempty(root.title) || !nonempty(root.task) || !nonempty(root.expected_artifact)) throw new Error('task snapshot envelope is invalid');
  const serialized = JSON.stringify(root).toLowerCase();
  if (privateMarkers.some((marker) => serialized.includes(marker))) throw new Error('task snapshot contains private body or path marker');
  if (!Array.isArray(root.sources) || root.sources.length !== 3) throw new Error('task sources are invalid');
  const sourceIDs = new Set<string>();
  const sourceFiles = new Map<string, { file: string; lines: number }>();
  for (const rawSource of root.sources) {
    const source = object(rawSource, 'task source');
    exactKeys(source, ['id', 'role', 'file', 'source'], 'task source');
    if (!nonempty(source.id) || !taskID.test(source.id) || !nonempty(source.role) || !relativePath(source.file) || !nonempty(source.source) || sourceIDs.has(source.id)) throw new Error('task source is invalid');
    sourceIDs.add(source.id);
    sourceFiles.set(source.id, { file: source.file, lines: source.source.split('\n').length });
  }
  if (!Array.isArray(root.events) || root.events.length < 10) throw new Error('task events are invalid');
  let lastSequence = 0;
  let lastElapsed = 0;
  let workspaceChanges = 0;
  const agents = new Set<string>();
  const sequences = new Set<number>();
  const spans = new Set<string>();
  for (const rawEvent of root.events) {
    const event = object(rawEvent, 'task event');
    const allowed = ['sequence', 'parent_sequence', 'span_id', 'parent_span_id', 'agent_id', 'parent_agent_id', 'agent_role', 'started_millis', 'ended_millis', 'source', 'workspace_id', 'workspace_changes', 'type', 'action', 'outcome', 'checkpoint_sha256', 'checkpoint_status', 'input_sha256', 'output_sha256', 'count', 'relative_elapsed_millis'];
    const actual = Object.keys(event);
    if (actual.some((key) => !allowed.includes(key))) throw new Error('task event has unknown fields');
    if (!Number.isInteger(event.sequence) || (event.sequence as number) <= lastSequence || !nonempty(event.span_id) || spans.has(event.span_id) || !nonempty(event.agent_id) || (event.agent_id !== 'runtime' && !sourceIDs.has(event.agent_id)) || !nonempty(event.agent_role) || !nonempty(event.type) || !nonempty(event.action) || !nonempty(event.outcome) || typeof event.started_millis !== 'number' || !Number.isFinite(event.started_millis) || event.started_millis < 0 || typeof event.ended_millis !== 'number' || !Number.isFinite(event.ended_millis) || event.ended_millis < event.started_millis || typeof event.relative_elapsed_millis !== 'number' || !Number.isFinite(event.relative_elapsed_millis) || event.relative_elapsed_millis < event.ended_millis || !Number.isInteger(event.count) || (event.count as number) < 1) throw new Error('task event is invalid');
    if (event.relative_elapsed_millis < lastElapsed) throw new Error('task event elapsed time is not monotonic');
    if (event.parent_sequence !== undefined && (!Number.isInteger(event.parent_sequence) || !sequences.has(event.parent_sequence as number))) throw new Error('task event parent is not earlier');
    if (event.parent_span_id !== undefined && (!nonempty(event.parent_span_id) || !spans.has(event.parent_span_id))) throw new Error('task event parent span is not earlier');
    if (event.parent_agent_id !== undefined && (!nonempty(event.parent_agent_id) || (event.parent_agent_id !== 'runtime' && !sourceIDs.has(event.parent_agent_id)))) throw new Error('task event parent agent is invalid');
    if (event.workspace_id !== undefined && typeof event.workspace_id !== 'string') throw new Error('task event workspace is invalid');
    if (event.checkpoint_status !== undefined && typeof event.checkpoint_status !== 'string') throw new Error('task event checkpoint status is invalid');
    for (const key of ['checkpoint_sha256', 'input_sha256', 'output_sha256'] as const) {
      if (event[key] !== undefined && event[key] !== '' && (typeof event[key] !== 'string' || !digest.test(event[key] as string))) throw new Error('task event digest is invalid');
    }
    if (event.source !== undefined) {
      const source = object(event.source, 'task source reference');
      exactKeys(source, ['source_id', 'file', 'start_line', 'end_line'], 'task source reference');
      const pinnedSource = typeof source.source_id === 'string' ? sourceFiles.get(source.source_id) : undefined;
      if (!nonempty(source.source_id) || !pinnedSource || source.file !== pinnedSource.file || !Number.isInteger(source.start_line) || !Number.isInteger(source.end_line) || (source.start_line as number) < 1 || (source.end_line as number) < (source.start_line as number) || (source.end_line as number) > pinnedSource.lines) throw new Error('task source reference is invalid');
    }
    if (event.workspace_changes !== undefined) {
      if (!Array.isArray(event.workspace_changes)) throw new Error('task workspace changes are invalid');
      for (const rawChange of event.workspace_changes) {
        const change = object(rawChange, 'task workspace change');
        exactKeys(change, ['path', 'kind', 'after_sha256', 'size'], 'task workspace change');
        if (!relativePath(change.path) || !nonempty(change.kind) || typeof change.after_sha256 !== 'string' || !digest.test(change.after_sha256) || !Number.isInteger(change.size) || (change.size as number) < 0) throw new Error('task workspace change is invalid');
        workspaceChanges += 1;
      }
    }
    lastSequence = event.sequence as number;
    lastElapsed = event.relative_elapsed_millis as number;
    agents.add(event.agent_id as string);
    sequences.add(event.sequence as number);
    spans.add(event.span_id as string);
  }
  const stats = object(root.stats, 'task stats');
  exactKeys(stats, ['duration_millis', 'events', 'agents', 'workspace_changes'], 'task stats');
  if (stats.duration_millis !== lastElapsed || stats.events !== root.events.length || stats.agents !== agents.size || agents.size < 4 || stats.workspace_changes !== workspaceChanges || workspaceChanges !== 2) throw new Error('task workspace projection drifted');
  return root as unknown as TaskSnapshot;
}

function goCompatibleTaskIdentityDocument(snapshot: TaskSnapshot): ArrayBuffer {
  const clone = JSON.parse(JSON.stringify(snapshot)) as TaskSnapshot;
  clone.identity = '';
  const encoded = JSON.stringify(clone).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`);
  const bytes = new TextEncoder().encode(encoded);
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
}

async function taskSnapshotIdentity(snapshot: TaskSnapshot): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify the task snapshot');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', goCompatibleTaskIdentityDocument(snapshot));
  return `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

export async function validateTaskSnapshot(raw: unknown): Promise<TaskSnapshot> {
  const snapshot = validateTaskSnapshotShape(raw);
  if (await taskSnapshotIdentity(snapshot) !== snapshot.identity) throw new Error('task snapshot identity mismatch');
  if (snapshot.identity !== expectedTaskSnapshotIdentity) throw new Error('task snapshot is not the build-pinned run');
  return snapshot;
}

export async function loadTaskSnapshot(): Promise<TaskSnapshot> {
  const response = await fetch('/lab-data/task.json', { cache: 'no-store' });
  if (!response.ok) throw new Error(`task snapshot unavailable (${response.status})`);
  const raw = await response.text();
  assertUniqueJSONKeys(raw);
  return validateTaskSnapshot(JSON.parse(raw));
}
