import { describe, expect, it } from 'vitest';
import { buildTraceNodes, collectCheckpointMetadata, exampleTrace, type TraceAdapterEvent } from './trace';

const trace: TraceAdapterEvent[] = [
  {
    sequence: 1,
    parent_sequence: null,
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
  it('groups recorded events by mechanism without losing raw events', () => {
    const nodes = buildTraceNodes(trace, 'observed');
    expect(nodes).toHaveLength(6);
    expect(nodes[0]).toMatchObject({ id: 'run', synthetic: true, depth: 0 });
    expect(nodes[1]).toMatchObject({ id: 'group:run-lifecycle', parent: 'run', synthetic: true, depth: 1 });
    expect(nodes[2]).toMatchObject({ id: 'event:1', parent: 'group:run-lifecycle', synthetic: false, depth: 2 });
    expect(nodes[3]).toMatchObject({ id: 'event:3', parent: 'group:run-lifecycle', synthetic: false, depth: 2 });
    expect(nodes[4]).toMatchObject({ id: 'group:workspace', parent: 'run', synthetic: true, depth: 1 });
    expect(nodes[5]).toMatchObject({ id: 'event:2', parent: 'group:workspace', synthetic: false, depth: 2 });
    expect(nodes[2].duration).toContain('ms');
    expect(nodes[2].input).toBe(null);
    expect(nodes[5].input).toMatchObject({ digest: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' });
  });

  it('collects checkpoint identities from trace metadata', () => {
    const checkpoints = collectCheckpointMetadata(trace);
    expect(Object.keys(checkpoints)).toHaveLength(3);
    expect(checkpoints['sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa']).toMatchObject({
      status: 'captured',
      sequence: 1,
    });
  });

  it('exposes example trace metadata', () => {
    expect(exampleTrace.length).toBeGreaterThan(0);
    expect(exampleTrace[0].action).toBe('run.start');
  });
});
