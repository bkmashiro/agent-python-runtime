import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { validateTaskSnapshot, type TaskSnapshot } from './taskData';

async function fixture(): Promise<TaskSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/task.json'), 'utf8')) as TaskSnapshot;
}

describe('task inspector snapshot', () => {
  it('loads one real workspace task with source, timeline and workspace changes', async () => {
    const task = await validateTaskSnapshot(await fixture());
    expect(task.id).toBe('dev-workspace-summary');
    expect(task.sources.map((source) => source.id)).toEqual(['orchestrator', 'researcher', 'reviewer']);
    expect(task.events).toHaveLength(37);
    expect(task.stats).toEqual({ duration_millis: 37857, events: 37, agents: 4, workspace_changes: 2 });
    expect(task.events.flatMap((event) => event.workspace_changes ?? []).map((change) => change.path)).toEqual(['researcher.txt', 'reviewer.txt']);
  });

  it('rejects missing trace and private source markers', async () => {
    const missing = await fixture();
    missing.events = [];
    await expect(validateTaskSnapshot(missing)).rejects.toThrow(/events are invalid/);

    const contentTamper = await fixture();
    contentTamper.title = 'Forged workspace task';
    await expect(validateTaskSnapshot(contentTamper)).rejects.toThrow(/identity mismatch/);

    const privateSource = await fixture();
    privateSource.sources[0].source = '/Users/private/task.py';
    await expect(validateTaskSnapshot(privateSource)).rejects.toThrow(/private body or path marker/);

    const forgedSource = await fixture();
    const sourced = forgedSource.events.find((event) => event.source);
    if (!sourced?.source) throw new Error('fixture lost source reference');
    sourced.source.file = 'forged.py';
    await expect(validateTaskSnapshot(forgedSource)).rejects.toThrow(/source reference is invalid/);

    const traversal = await fixture();
    const workspaceEvent = traversal.events.find((event) => event.workspace_changes?.length);
    if (!workspaceEvent?.workspace_changes) throw new Error('fixture lost workspace change');
    workspaceEvent.workspace_changes[0].path = '../private.txt';
    await expect(validateTaskSnapshot(traversal)).rejects.toThrow(/workspace change is invalid/);
  });
});
