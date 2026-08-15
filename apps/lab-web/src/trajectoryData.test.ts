import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { filterTrajectory, modelContext, validateTrajectory, validateTrajectoryIndex, type TrajectoryExport, type TrajectoryIndex } from './trajectoryData';

async function fixture(): Promise<TrajectoryExport> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/trajectory.json'), 'utf8')) as TrajectoryExport;
}

describe('private development trajectory', () => {
  it('loads a closed local session index', async () => {
    const raw = JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/index.json'), 'utf8')) as TrajectoryIndex;
    const index = validateTrajectoryIndex(raw);
    expect(index.default_session_id).toBe('workflow-experiment-v1');
    expect(index.sessions.map((session) => session.file)).toEqual(['experiment.json', 'trajectory.json']);
  });

  it('loads one append-only session containing every inspectable source', async () => {
    const value = await validateTrajectory(await fixture());
    expect(value.privacy).toBe('private');
    expect(value.events).toHaveLength(28);
    expect(new Set(value.events.map((event) => event.source))).toEqual(expect.objectContaining(new Set(['system', 'developer', 'user', 'memory', 'skill', 'harness', 'model', 'tool', 'subagent', 'runtime', 'workspace'])));
    expect(value.events.at(-1)?.type).toBe('session.end');
  });

  it('validates the reset real-Guest experiment trajectory with CPU accounting', async () => {
    const raw = JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/experiment.json'), 'utf8')) as TrajectoryExport;
    const value = await validateTrajectory(raw);
    expect(value.events).toHaveLength(243);
    expect(value.events.filter((event) => event.type === 'tool.call')).toHaveLength(14);
    expect(value.events.filter((event) => event.type === 'runtime.event')).toHaveLength(76);
    expect(value.events.filter((event) => event.type === 'assistant.chunk')).toHaveLength(44);
    expect(value.events.at(-3)?.body_text).toContain('Process CPU: 30782849000 ns baseline, 30802806000 ns optimized');
  });

  it('reconstructs the exact ordered context of each model request', async () => {
    const value = await validateTrajectory(await fixture());
    const requests = value.events.filter((event) => event.type === 'model.request');
    expect(requests).toHaveLength(2);
    expect(modelContext(value, requests[0].event_id).map((event) => event.source)).toEqual(['harness', 'system', 'developer', 'memory', 'skill', 'user']);
    expect(modelContext(value, requests[1].event_id).map((event) => event.type)).toEqual([
      'request.header', 'context.inject', 'context.inject', 'context.inject', 'context.inject', 'user.message',
      'assistant.output', 'tool.call', 'tool.result', 'subagent.result',
    ]);
  });

  it('links a tool call to Runtime, workspace and result records', async () => {
    const value = await validateTrajectory(await fixture());
    const call = value.events.find((event) => event.type === 'tool.call');
    expect(call).toBeDefined();
    const linked = filterTrajectory(value, { toolCallID: call!.tool_call_id });
    expect(linked.map((event) => event.type)).toEqual(['tool.call', 'runtime.event', 'runtime.event', 'workspace.change', 'tool.result']);
    expect(linked.find((event) => event.type === 'runtime.event')?.physical_execution_id).toBeTruthy();
  });

  it('fails closed on chain, context, body and unknown-field mutation', async () => {
    const chain = await fixture();
    chain.events[4].previous_sha256 = chain.session.header_sha256;
    await expect(validateTrajectory(chain)).rejects.toThrow(/hash chain|export seal/);

    const future = await fixture();
    const request = future.events.find((event) => event.type === 'model.request')!;
    request.context_event_ids = [future.events.at(-1)!.event_id];
    await expect(validateTrajectory(future)).rejects.toThrow(/prior context|export seal/);

    const body = await fixture();
    delete body.events.find((event) => event.type === 'tool.result')!.body_text;
    await expect(validateTrajectory(body)).rejects.toThrow(/materialized body|export seal/);

    const citations = await fixture();
    const header = citations.events.find((event) => event.type === 'request.header')!;
    citations.events.find((event) => event.type === 'assistant.output')!.source_event_ids = [header.event_id];
    await expect(validateTrajectory(citations)).rejects.toThrow(/chunk citations|export seal/);

    const unknown = await fixture() as TrajectoryExport & { secret?: string };
    unknown.secret = 'not accepted';
    await expect(validateTrajectory(unknown)).rejects.toThrow(/unknown trajectory field|export seal/);

    const materialized = await fixture();
    materialized.events.find((event) => event.type === 'assistant.output')!.body_text = 'tampered after export';
    await expect(validateTrajectory(materialized)).rejects.toThrow(/export seal/);
  });
});
