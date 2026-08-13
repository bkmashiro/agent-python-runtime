import { describe, expect, it } from 'vitest';
import { buildTraceNodes, collectCheckpointMetadata, type TraceAdapterEvent } from './trace';

const trace: TraceAdapterEvent[] = [
  {
    sequence: 1,
    parent_sequence: null,
    span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
    type: 'run_start',
    action: 'run.start',
    outcome: 'started',
    count: 0,
    relative_elapsed_millis: 0,
    input_sha256: '',
    output_sha256: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    checkpoint_sha256: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    checkpoint_status: 'captured',
  },
  {
    sequence: 2,
    parent_sequence: 1,
    span_id: 'workspace', parent_span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0.1, ended_millis: 0.2,
    type: 'workspace',
    action: 'run.mechanism',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.2,
    input_sha256: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    output_sha256: 'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
    checkpoint_sha256: 'sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
    checkpoint_status: 'captured',
  },
  {
    sequence: 3,
    parent_sequence: 2,
    span_id: 'terminal', parent_span_id: 'workspace', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0.4, ended_millis: 0.4,
    type: 'run_terminal',
    action: 'run.terminal',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.4,
    input_sha256: 'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
    output_sha256: 'sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
    checkpoint_sha256: 'sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',
    checkpoint_status: 'captured',
    terminal_disposition: 'closed',
  },
];

describe('trace adapter', () => {
  it('preserves chronological recorded events for the timeline', () => {
    const nodes = buildTraceNodes(trace, 'observed');
    expect(nodes).toHaveLength(3);
    expect(nodes.map((node) => node.id)).toEqual(['event:1', 'event:2', 'event:3']);
    expect(nodes.every((node) => !node.synthetic)).toBe(true);
    expect(nodes[0].duration).toContain('ms');
    expect(nodes[0].input).toBe(null);
    expect(nodes[1].input).toMatchObject({ digest: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' });
  });

  it('collects checkpoint identities from trace metadata', () => {
    const checkpoints = collectCheckpointMetadata(trace);
    expect(Object.keys(checkpoints)).toHaveLength(3);
    expect(checkpoints['sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa']).toMatchObject({
      status: 'captured',
      sequence: 1,
    });
  });

});
