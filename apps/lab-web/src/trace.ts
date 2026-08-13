import { pythonSource } from 'virtual:pysolate-demo';
import type { LabRun } from './debuggerData';

export type Evidence = 'observed' | 'verified-example' | 'source-bound' | 'instrumentation-preview';

export interface TraceNode {
  id: string;
  parent?: string;
  depth: number;
  kind: string;
  title: string;
  summary: string;
  evidence: Evidence;
  duration: string;
  params: Record<string, unknown>;
  input: unknown;
  output: unknown;
  checkpoint: string;
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

export function buildTraceNodes(events: ReadonlyArray<TraceAdapterEvent>, evidence: Evidence): TraceNode[] {
  const nodes = events.map((event) => ({
    id: String(event.sequence),
    parent: event.parent_sequence == null ? undefined : String(event.parent_sequence),
    kind: event.type,
    title: event.action,
    summary: `${event.type} · ${event.outcome}`,
    evidence,
    duration: `${event.relative_elapsed_millis.toFixed(2)} ms`,
    params: {
      relative_elapsed_millis: event.relative_elapsed_millis,
      count: event.count,
      ...(event.checkpoint_status ? { checkpoint_status: event.checkpoint_status } : {}),
      ...(event.terminal_disposition ? { terminal_disposition: event.terminal_disposition } : {}),
    },
    input: toDigestField(event.input_sha256),
    output: toDigestField(event.output_sha256),
    checkpoint: event.checkpoint_sha256 ?? '',
  }));

  const byId = new Map(nodes.map((node) => [node.id, node]));

  const depthById = new Map<string, number>();
  const computeDepth = (id: string, seen: Set<string>): number => {
    if (depthById.has(id)) {
      return depthById.get(id) ?? 0;
    }
    if (seen.has(id)) {
      return 0;
    }
    const node = byId.get(id);
    if (!node || !node.parent) {
      return 0;
    }
    seen.add(id);
    const depth = computeDepth(node.parent, seen) + 1;
    depthById.set(id, depth);
    return depth;
  };

  return nodes.map((node) => ({
    ...node,
    depth: computeDepth(node.id, new Set()),
  }));
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
