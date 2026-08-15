import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  filterTrajectory, modelContext, validateTrajectory, validateTrajectoryIndex,
  type RawEvidenceExport, type TrajectoryIndex,
} from './trajectoryData';

async function rawFixture(name = 'experiment-full-public.json'): Promise<RawEvidenceExport> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data', name), 'utf8')) as RawEvidenceExport;
}

describe('dual-profile causal evidence ingestion', () => {
  it('loads only the two views of one real Guest trace', async () => {
    const raw = JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/index.json'), 'utf8')) as TrajectoryIndex;
    const index = validateTrajectoryIndex(raw);
    expect(index.default_view_id).toBe('experiment-full-public');
    expect(index.views.map((view) => view.file)).toEqual(['experiment-full-public.json', 'production-rollback.json']);
    expect(new Set(index.views.map((view) => view.trace_id))).toEqual(new Set(['trace-real-source-bound-0001']));
  });

  it('validates the body-safe real Guest experiment projection', async () => {
    const value = await validateTrajectory(await rawFixture());
    expect(value.profile).toBe('experiment_full');
    expect(value.privacy).toBe('portable');
    expect(value.events).toHaveLength(21);
    expect(value.events.filter((event) => event.type === 'source.decision')).toHaveLength(1);
    expect(value.events.filter((event) => event.type === 'source.executed_line')[0].payload.availability).toBe('not_recorded');
    expect(value.events.filter((event) => event.type === 'resource.sample')).toHaveLength(1);
    expect(value.events.filter((event) => event.type === 'tool.decision')).toHaveLength(1);
    expect(value.events.filter((event) => event.type === 'model.output')[0].payload.availability).toBe('not_recorded');
    expect(value.events.filter((event) => event.type === 'subagent.context')).toHaveLength(1);
    expect(value.events.filter((event) => event.type === 'subagent.runtime')).toHaveLength(1);
    expect(value.events.filter((event) => event.type === 'subagent.workspace')).toHaveLength(1);
    expect(value.events.some((event) => event.body !== undefined)).toBe(false);
    expect(value.events.some((event) => event.type === 'runtime.observation' || event.type === 'model.body')).toBe(false);
    expect(modelContext(value, 'unused')).toHaveLength(1);
  });

  it('validates a strict production subset with shared event identities', async () => {
    const full = await validateTrajectory(await rawFixture());
    const production = await validateTrajectory(await rawFixture('production-rollback.json'));
    expect(production.profile).toBe('production_rollback');
    expect(production.events).toHaveLength(9);
    expect(production.events.map((event) => event.type)).toEqual([
      'trace.started', 'execution.attempt', 'authority.snapshot', 'effect.transition', 'effect.transition', 'effect.transition', 'workspace.terminal', 'execution.attempt', 'trace.ended',
    ]);
    expect(production.events.filter((event) => event.type === 'effect.transition').map((event) => event.payload.state)).toEqual(['intent', 'started', 'committed']);
    const fullIDs = new Set(full.events.map((event) => event.event_id));
    expect(production.events.every((event) => fullIDs.has(event.event_id))).toBe(true);
    expect(production.header_sha256).toBe(full.header_sha256);
    expect(production.trace_id).toBe(full.trace_id);
  });

  it('filters source and receipt-linked effect evidence deterministically', async () => {
    const value = await validateTrajectory(await rawFixture());
    expect(filterTrajectory(value, { sources: ['subagent'] })).toHaveLength(3);
    const effect = value.events.find((event) => event.type === 'effect.transition')!;
    expect(filterTrajectory(value, { toolCallID: effect.tool_call_id }).some((event) => event.type === 'effect.transition')).toBe(true);
    expect(filterTrajectory(value, { query: 'source_bound' }).map((event) => event.type)).toEqual(['source.decision']);
  });

  it('fails closed on identity, relation, profile leakage and unknown fields', async () => {
    const identity = await rawFixture();
    identity.events[0].payload.availability = 'not_recorded';
    await expect(validateTrajectory(identity)).rejects.toThrow(/event identity|export seal/);

    const relation = await rawFixture();
    const runtime = relation.events.find((event) => event.type === 'subagent.runtime')!;
    runtime.payload.child_id = 'child-mismatch-0001';
    await expect(validateTrajectory(relation)).rejects.toThrow(/subagent runtime parent mismatch|event identity/);

    const leakage = await rawFixture();
    leakage.profile = 'production_rollback';
    await expect(validateTrajectory(leakage)).rejects.toThrow(/production profile leaked/);

    const unknown = await rawFixture() as RawEvidenceExport & { secret?: string };
    unknown.secret = 'not accepted';
    await expect(validateTrajectory(unknown)).rejects.toThrow(/unknown causal evidence field/);
  });
});
