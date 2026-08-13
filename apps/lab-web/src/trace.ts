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

export type EventPresentation = {
  phase: string;
  label: string;
  description: string;
};

export function describeEvent(event: TraceAdapterEvent): EventPresentation {
  if (event.action === 'agent.execute') return {
    phase: 'Child agents', label: `${event.agent_id} Python`,
    description: `${event.agent_id} ran its recorded Python task in a private workspace.`,
  };
  const exact: Record<string, EventPresentation> = {
    'run.start': { phase: 'Run', label: 'Run started', description: 'The Host started recording this run.' },
    'workspace.fork': { phase: 'Workspace', label: 'Workspace forked', description: 'The Host created a private workspace for the run.' },
    'stream.begin': { phase: 'Parent agent', label: 'Parent started', description: 'The parent Guest execution stream started.' },
    'stream.prepare': { phase: 'Parent agent', label: 'Source prepared', description: 'A recorded Python source chunk was prepared for the parent Guest.' },
    'guest.python': { phase: 'Parent agent', label: 'Parent Python', description: 'The parent Guest executed the recorded Python program.' },
    'stream.seal': { phase: 'Parent agent', label: 'Parent sealed', description: 'No more input could be sent to the parent Guest.' },
    'stream.end': { phase: 'Parent agent', label: 'Parent finished', description: 'The parent Guest execution stream completed.' },
    'workspace.commit': { phase: 'Workspace', label: 'Parent workspace committed', description: 'The parent workspace result was committed.' },
    'guest.close': { phase: 'Parent agent', label: 'Parent closed', description: 'The parent Guest was closed.' },
    'fanout.select': event.outcome === 'started'
      ? { phase: 'Child agents', label: 'Children started', description: 'The Host launched the recorded child-agent branches.' }
      : { phase: 'Join', label: 'Workspace selected', description: 'The Host selected the winning child workspace after both children finished.' },
    'fanout.discard': { phase: 'Join', label: 'Other workspace discarded', description: 'The non-selected child workspace was discarded.' },
    'fanout.selected_root': { phase: 'Join', label: 'Selected workspace verified', description: 'The selected child workspace identity was verified.' },
    'cache.lookup': { phase: 'Cache', label: event.outcome === 'hit' ? 'Cache hit' : 'Cache miss', description: `The Host cache lookup returned ${event.outcome}.` },
    'cache.compute': { phase: 'Cache', label: 'Cache value computed', description: 'The Host computed and stored a missing cache value.' },
    'cache.hit': { phase: 'Cache', label: 'Cached result reused', description: 'A previously recorded result was reused.' },
    'single_flight.leader': { phase: 'Cache', label: 'Leader elected', description: 'One caller became the single-flight leader.' },
    'single_flight.follower': { phase: 'Cache', label: 'Follower joined', description: 'Another caller joined the in-flight computation.' },
    'single_flight.compute': { phase: 'Cache', label: 'Shared computation finished', description: 'The single-flight computation completed for all callers.' },
    'wait.begin': { phase: 'Wait / resume', label: 'Wait started', description: 'The run entered a recorded wait boundary.' },
    'observation.initial': { phase: 'Wait / resume', label: 'Initial state observed', description: 'The Host recorded the state before releasing the wait.' },
    'wait.release': event.outcome === 'started'
      ? { phase: 'Wait / resume', label: 'Release requested', description: 'The Host requested release of the waiting run.' }
      : { phase: 'Wait / resume', label: 'Wait released', description: 'The wait boundary was released.' },
    'observation.changed': { phase: 'Wait / resume', label: 'Change observed', description: 'The Host recorded the state change after release.' },
    'resume.fresh': { phase: 'Wait / resume', label: 'Fresh Guest resumed', description: 'Execution resumed in a fresh Guest.' },
    'oracle.compare': { phase: 'Verification', label: 'Result checked', description: 'The recorded result was compared with its expected identity.' },
    'run.terminal': { phase: 'Run', label: 'Run finished', description: `The run reached terminal disposition ${event.terminal_disposition ?? event.outcome}.` },
  };
  return exact[event.action] ?? {
    phase: mechanismGroup(event.type).replaceAll('-', ' '),
    label: event.action.replaceAll('.', ' '),
    description: `${event.action} recorded outcome ${event.outcome}.`,
  };
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
  const presentation = describeEvent(event);
  return {
    id: `event:${event.sequence}`,
    parent,
    depth,
    kind: event.type,
    group: mechanismGroup(event.type),
    title: presentation.label,
    summary: `${presentation.phase} · ${event.outcome} · seq ${event.sequence}`,
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

export function buildCausalTraceTree(events: ReadonlyArray<TraceAdapterEvent>, evidence: Evidence): TraceNode[] {
  const bySpan = new Map(events.map((event) => [event.span_id, event]));
  return [...events].sort((a, b) => a.sequence - b.sequence).map((event) => {
    let depth = 0;
    let parentSpan = event.parent_span_id;
    const seen = new Set<string>();
    while (parentSpan && bySpan.has(parentSpan) && !seen.has(parentSpan)) {
      seen.add(parentSpan);
      depth += 1;
      parentSpan = bySpan.get(parentSpan)?.parent_span_id;
    }
    const parentEvent = event.parent_span_id ? bySpan.get(event.parent_span_id) : undefined;
    return eventNode(event, evidence, parentEvent ? `event:${parentEvent.sequence}` : '', depth);
  });
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
