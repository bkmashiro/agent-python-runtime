import { describe, expect, it } from 'vitest';
import { buildTraceNodes, collectCheckpointMetadata, exampleTrace, pythonSource, type TraceAdapterEvent } from './trace';

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
  it('builds one node per recorded event with parent-driven depth', () => {
    const nodes = buildTraceNodes(trace, 'observed');
    expect(nodes).toHaveLength(3);
    expect(nodes[0].id).toBe('1');
    expect(nodes[1].parent).toBe('1');
    expect(nodes[2].parent).toBe('2');
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

  it('exposes example trace metadata', () => {
    expect(pythonSource).toContain('sources.demo_catalog');
    expect(exampleTrace.length).toBeGreaterThan(0);
    expect(exampleTrace[0].action).toBe('run.start');
  });
});
