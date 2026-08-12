import { describe, expect, it } from 'vitest';
import { checkpoints, pythonSource, trace } from './trace';

describe('target trace contract', () => {
  it('binds the complete runnable workflow source', () => {
    expect(pythonSource).toContain('sources.demo_catalog()');
    expect(pythonSource).toContain('sources.benchmark_manifest()');
    expect(pythonSource).toContain('report_path.write_text');
  });

  it('separates semantic typed calls, Pysolate ABI, and WASI atoms', () => {
    expect(trace.filter((node) => node.kind === 'typed-call').map((node) => node.title)).toEqual([
      'sources.demo_catalog()', 'sources.benchmark_manifest()',
    ]);
    expect(trace.filter((node) => node.kind === 'abi')).toHaveLength(2);
    expect(trace.filter((node) => node.kind === 'wasi').map((node) => node.title)).toEqual([
      'path_create_directory', 'path_open', 'fd_write', 'fd_close',
    ]);
  });

  it('does not mislabel preview checkpoints as observed', () => {
    expect(checkpoints.initial.evidence).toBe('observed');
    expect(checkpoints.final.evidence).toBe('observed');
    expect(checkpoints['after-catalog'].evidence).toBe('instrumentation-preview');
    expect(checkpoints['after-manifest'].evidence).toBe('instrumentation-preview');
  });

  it('exposes every trace node input and output', () => {
    for (const node of trace) {
      expect(node).toHaveProperty('input');
      expect(node).toHaveProperty('output');
    }
  });
});
