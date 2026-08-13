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
  span_id: string;
  parent_span_id?: string;
  agent_id: string;
  parent_agent_id?: string;
  agent_role: string;
  started_millis: number;
  ended_millis: number;
  source?: { source_id: string; file: string; start_line: number; end_line: number };
  workspace_id?: string;
  workspace_changes?: Array<{ path: string; kind: string; before_sha256?: string; after_sha256?: string; size?: number }>;
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
      span_id: event.span_id,
      parent_span_id: event.parent_span_id ?? null,
      agent_id: event.agent_id,
      parent_agent_id: event.parent_agent_id ?? null,
      agent_role: event.agent_role,
      started_millis: event.started_millis,
      ended_millis: event.ended_millis,
      source: event.source ?? null,
      workspace_id: event.workspace_id ?? null,
      workspace_changes: event.workspace_changes ?? [],
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
  return events.map((event) => eventNode(event, evidence, '', 0));
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
