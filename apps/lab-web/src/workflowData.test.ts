import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { validateWorkflowEvidence, type WorkflowEvidence } from './workflowData';

async function fixture(): Promise<WorkflowEvidence> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/workflow-benchmark-evidence-v0.json'), 'utf8')) as WorkflowEvidence;
}

describe('workflow evidence boundary', () => {
  it('accepts the sealed body-free paired fixture', async () => {
    const evidence = await validateWorkflowEvidence(await fixture());
    expect(evidence.divergences).toBe(0);
    expect(evidence.manifest.tasks).toHaveLength(14);
    expect(evidence.baseline_physical_executions).toBe(25);
    expect(evidence.optimized_physical_executions).toBe(23);
  });

  it('rejects mutation and private body fields', async () => {
    const mutated = await fixture();
    mutated.tasks[0].baseline_physical_executions += 1;
    await expect(validateWorkflowEvidence(mutated)).rejects.toThrow();

    const leaked = await fixture() as WorkflowEvidence & { prompt?: string };
    leaked.prompt = 'private fixture body';
    await expect(validateWorkflowEvidence(leaked)).rejects.toThrow(/private evidence key/);
  });

  it('rejects orphaned consumers and UI authority', async () => {
    const orphaned = await fixture();
    orphaned.reports[0].physical_executions[0].consumers = ['logical-0000000000000000'];
    await expect(validateWorkflowEvidence(orphaned)).rejects.toThrow(/orphan execution consumer/);

    const authority = await fixture() as WorkflowEvidence & { consumer_admitted?: boolean };
    (authority.reports[0] as unknown as { consumer_admitted: boolean }).consumer_admitted = true;
    await expect(validateWorkflowEvidence(authority)).rejects.toThrow(/Lab report acquired authority/);
  });
});
