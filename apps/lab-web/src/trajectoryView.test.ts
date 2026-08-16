import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { buildCausalTree, decodeNestedJSON, eventsForNode, inspectorTabs, relatedEvents } from './trajectoryView';
import { validateTrajectory, type RawEvidenceExport } from './trajectoryData';

async function fixture() {
  const raw = JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/experiment-full-public.json'), 'utf8')) as RawEvidenceExport;
  return validateTrajectory(raw);
}

describe('human-first causal projection', () => {
  it('groups one real trace into preparation, run, call, workspace and completion tasks', async () => {
    const trajectory = await fixture();
    const tree = buildCausalTree(trajectory);
    expect(tree.title).toBe('Real source-bound session');
    expect(tree.children.map((node) => node.title)).toEqual(['Child preparation', 'Source-bound run']);
    expect(tree.children[1].children.map((node) => node.title)).toEqual([
      'Execution setup', 'tools.increment · source bound', 'Workspace result', 'Run completion',
    ]);
    expect(eventsForNode(trajectory, tree.children[1].children[1]).map((event) => event.type)).toContain('source.decision');
  });

  it('exposes only truthful inspector tabs and deterministic relation targets', async () => {
    const trajectory = await fixture();
    const sourceDecision = trajectory.events.find((event) => event.type === 'source.decision')!;
    expect(inspectorTabs([sourceDecision])).toEqual(['Overview', 'Code', 'Timeline', 'Evidence', 'Raw']);
    expect(relatedEvents(trajectory, sourceDecision).map((event) => event.type)).toEqual([
      'effect.transition', 'tool.decision', 'source.occurrence', 'source.executed_line', 'workspace.terminal',
    ]);
  });

  it('decodes complete nested JSON within bounds and preserves malformed strings', () => {
    expect(decodeNestedJSON('{"request":{"count":2}}')).toEqual({ request: { count: 2 } });
    expect(decodeNestedJSON('{broken')).toBe('{broken');
    expect(decodeNestedJSON('[[[[1]]]]', { maxDepth: 2 })).toBe('[[[[1]]]]');
  });
});
