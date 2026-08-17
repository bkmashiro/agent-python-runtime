import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadTaskSnapshot, validateTaskSnapshot, type TaskSnapshot } from './taskData';

async function fixture(): Promise<TaskSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/task.json'), 'utf8')) as TaskSnapshot;
}

async function reseal(task: TaskSnapshot) {
  task.identity = '';
  const encoded = JSON.stringify(task).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`);
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(encoded));
  task.identity = `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

afterEach(() => vi.unstubAllGlobals());

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

  it('rejects identity-valid unknown agents and impossible causality or timing', async () => {
    const unknownAgent = await fixture();
    unknownAgent.events.find((event) => event.agent_id === 'researcher')!.agent_id = 'attacker';
    await reseal(unknownAgent);
    await expect(validateTaskSnapshot(unknownAgent)).rejects.toThrow(/event is invalid/);

    const futureParent = await fixture();
    futureParent.events[1].parent_sequence = futureParent.events.at(-1)!.sequence;
    await reseal(futureParent);
    await expect(validateTaskSnapshot(futureParent)).rejects.toThrow(/parent is not earlier/);

    const futureParentSpan = await fixture();
    futureParentSpan.events[1].parent_span_id = futureParentSpan.events.at(-1)!.span_id;
    await reseal(futureParentSpan);
    await expect(validateTaskSnapshot(futureParentSpan)).rejects.toThrow(/parent span is not earlier/);

    const duplicateSpan = await fixture();
    duplicateSpan.events[1].span_id = duplicateSpan.events[0].span_id;
    await reseal(duplicateSpan);
    await expect(validateTaskSnapshot(duplicateSpan)).rejects.toThrow(/event is invalid/);

    const negativeTime = await fixture();
    negativeTime.events[0].started_millis = -1;
    await reseal(negativeTime);
    await expect(validateTaskSnapshot(negativeTime)).rejects.toThrow(/event is invalid/);

    const elapsedRewind = await fixture();
    elapsedRewind.events[13].relative_elapsed_millis = 20_000;
    await reseal(elapsedRewind);
    await expect(validateTaskSnapshot(elapsedRewind)).rejects.toThrow(/elapsed time is not monotonic/);
  });

  it('rejects duplicate JSON keys before parsing', async () => {
    const raw = await readFile(join(process.cwd(), 'public/lab-data/task.json'), 'utf8');
    const duplicate = raw.replace(/("identity":\s*"[^"]+")/, '$1,\n  $1');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(duplicate, { status: 200 })));
    await expect(loadTaskSnapshot()).rejects.toThrow(/duplicate JSON key/);
  });
});
