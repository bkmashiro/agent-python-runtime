import { pythonSource } from 'virtual:pysolate-demo';
import type { LabRun } from './debuggerData';

export type Evidence = 'observed' | 'verified-example' | 'source-bound' | 'instrumentation-preview';

export type MechanismGroup =
  | 'run-lifecycle'
  | 'guest-lifecycle'
  | 'workspace'
  | 'streaming'
  | 'fanout'
  | 'cache'
  | 'single-flight'
  | 'wait-resume'
  | 'observation'
  | 'oracle'
  | 'prepared'
  | 'cow'
  | 'cancellation';

export interface TraceNode {
  id: string;
  parent?: string;
  depth: number;
  kind: string;
  group: MechanismGroup;
  title: string;
  summary: string;
  evidence: Evidence;
  duration: string;
  params: Record<string, unknown>;
  input: unknown;
  output: unknown;
  checkpoint: string;
  synthetic: boolean;
  rawEvent?: TraceAdapterEvent;
}

export interface CheckpointMetadata {
  id: string;
  identity: string;
  status: string;
  sequence: number;
}

export interface TraceAdapterEvent {
  sequence: number;
  parent_sequence?: number | null;
  type: string;
  action: string;
  outcome: string;
  count: number;
  relative_elapsed_millis: number;
  input_sha256?: string;
  output_sha256?: string;
  checkpoint_sha256?: string;
  checkpoint_status?: string;
  terminal_disposition?: string;
}

function toDigestField(raw?: string): Record<string, string> | null {
  if (!raw) {
    return null;
  }
  return { digest: raw };
}

const mechanismOrder: MechanismGroup[] = [
  'run-lifecycle', 'guest-lifecycle', 'workspace', 'streaming', 'fanout', 'cache',
  'single-flight', 'wait-resume', 'observation', 'oracle', 'prepared', 'cow', 'cancellation',
];

function mechanismGroup(type: string): MechanismGroup {
  switch (type) {
    case 'run_start':
    case 'run_terminal':
      return 'run-lifecycle';
    case 'guest_lifecycle': return 'guest-lifecycle';
    case 'single_flight': return 'single-flight';
    case 'wait_resume': return 'wait-resume';
    default:
      return mechanismOrder.includes(type as MechanismGroup) ? type as MechanismGroup : 'run-lifecycle';
  }
}

function eventNode(event: TraceAdapterEvent, evidence: Evidence, parent: string, depth: number): TraceNode {
  return {
    id: `event:${event.sequence}`,
    parent,
    depth,
    kind: event.type,
    group: mechanismGroup(event.type),
    title: event.action,
    summary: `${event.outcome} · seq ${event.sequence}`,
    evidence,
    duration: `${event.relative_elapsed_millis.toFixed(2)} ms`,
    params: {
      sequence: event.sequence,
      parent_sequence: event.parent_sequence ?? null,
      relative_elapsed_millis: event.relative_elapsed_millis,
      count: event.count,
      ...(event.checkpoint_status ? { checkpoint_status: event.checkpoint_status } : {}),
      ...(event.terminal_disposition ? { terminal_disposition: event.terminal_disposition } : {}),
    },
    input: toDigestField(event.input_sha256),
    output: toDigestField(event.output_sha256),
    checkpoint: event.checkpoint_sha256 ?? '',
    synthetic: false,
    rawEvent: event,
  };
}

export function buildTraceNodes(events: ReadonlyArray<TraceAdapterEvent>, evidence: Evidence): TraceNode[] {
  if (!events.length) return [];

  const grouped = new Map<MechanismGroup, TraceAdapterEvent[]>();
  for (const event of events) {
    const group = mechanismGroup(event.type);
    grouped.set(group, [...(grouped.get(group) ?? []), event]);
  }

  const root: TraceNode = {
    id: 'run', depth: 0, kind: 'run', group: 'run-lifecycle', title: 'Recorded run',
    summary: `${events.length} recorded events`, evidence, duration: `${events.at(-1)?.relative_elapsed_millis.toFixed(2) ?? '0.00'} ms`,
    params: { event_count: events.length }, input: null, output: null, checkpoint: '', synthetic: true,
  };
  const nodes = [root];

  for (const group of mechanismOrder) {
    const groupEvents = grouped.get(group);
    if (!groupEvents?.length) continue;
    const groupId = `group:${group}`;
    nodes.push({
      id: groupId, parent: root.id, depth: 1, kind: group, group,
      title: group, summary: `${groupEvents.length} recorded ${groupEvents.length === 1 ? 'event' : 'events'}`,
      evidence, duration: `${groupEvents.at(-1)?.relative_elapsed_millis.toFixed(2) ?? '0.00'} ms`,
      params: { event_count: groupEvents.length, event_type: group }, input: null, output: null,
      checkpoint: '', synthetic: true,
    });
    nodes.push(...groupEvents.map((event) => eventNode(event, evidence, groupId, 2)));
  }
  return nodes;
}

export function collectCheckpointMetadata(events: ReadonlyArray<TraceAdapterEvent>): Record<string, CheckpointMetadata> {
  const checkpoints: Record<string, CheckpointMetadata> = {};
  for (const event of events) {
    if (!event.checkpoint_sha256) {
      continue;
    }
    if (checkpoints[event.checkpoint_sha256]) {
      continue;
    }
    checkpoints[event.checkpoint_sha256] = {
      id: event.checkpoint_sha256,
      identity: event.checkpoint_sha256,
      status: event.checkpoint_status ?? 'captured',
      sequence: event.sequence,
    };
  }
  return checkpoints;
}

export const exampleRun: Omit<LabRun, 'trace'> = {
  run_id: 'example-workflow',
  workload_id: 'workspace-summary',
  treatment: 'fresh',
  recorded_status: 'passed',
  terminal_disposition: 'discarded',
  refs: [
    { kind: 'artifact', sha256: 'sha256:1111111111111111111111111111111111111111111111111111111111111111' },
    { kind: 'capability_plan', sha256: 'sha256:2222222222222222222222222222222222222222222222222222222222222222' },
    { kind: 'execution', sha256: 'sha256:3333333333333333333333333333333333333333333333333333333333333333' },
    { kind: 'workspace_tree', sha256: 'sha256:4444444444444444444444444444444444444444444444444444444444444444' },
  ],
  metrics: {
    guest_created: 1,
    guest_destroyed: 1,
    cache_hits: 0,
    flight_followers: 0,
    changed_bytes: 279,
    materialized_bytes: 279,
    relative_elapsed_millis: 0.16,
  },
  scenario: {
    id: 'workspace-summary',
    file_count: 1,
    child_analysis_count: 2,
    selected_child: 0,
    has_repeated_transformation: true,
    has_wait_boundary: true,
    has_observation: true,
  },
};

export const exampleTrace: TraceAdapterEvent[] = [
  {
    sequence: 1,
    parent_sequence: null,
    type: 'run_start',
    action: 'run.start',
    outcome: 'started',
    count: 0,
    relative_elapsed_millis: 0.01,
    input_sha256: '',
    output_sha256: 'sha256:1111111111111111111111111111111111111111111111111111111111111111',
    checkpoint_sha256: 'sha256:aaaa111111111111111111111111111111111111111111111111111111111111aa',
    checkpoint_status: 'captured',
  },
  {
    sequence: 2,
    parent_sequence: 1,
    type: 'observation',
    action: 'sources.demo_catalog',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.08,
    input_sha256: 'sha256:1111111111111111111111111111111111111111111111111111111111111111',
    output_sha256: 'sha256:2222222222222222222222222222222222222222222222222222222222222222',
    checkpoint_sha256: 'sha256:bb11222222222222222222222222222222222222222222222222222222222222bb',
    checkpoint_status: 'captured',
  },
  {
    sequence: 3,
    parent_sequence: 2,
    type: 'workspace',
    action: 'sources.benchmark_manifest',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.12,
    input_sha256: 'sha256:2222222222222222222222222222222222222222222222222222222222222222',
    output_sha256: 'sha256:3333333333333333333333333333333333333333333333333333333333333333',
    checkpoint_sha256: 'sha256:cc33333333333333333333333333333333333333333333333333333333333333cc',
    checkpoint_status: 'captured',
  },
  {
    sequence: 4,
    parent_sequence: 3,
    type: 'run_terminal',
    action: 'run.terminal',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.16,
    input_sha256: 'sha256:3333333333333333333333333333333333333333333333333333333333333333',
    output_sha256: 'sha256:4444444444444444444444444444444444444444444444444444444444444444',
    checkpoint_sha256: 'sha256:dd44444444444444444444444444444444444444444444444444444444444444dd',
    checkpoint_status: 'captured',
    terminal_disposition: 'discarded',
  },
];

export { pythonSource };
