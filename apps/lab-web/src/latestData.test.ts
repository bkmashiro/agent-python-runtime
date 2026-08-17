import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { validateLatestSnapshot, type LatestSnapshot } from './latestData';

async function fixture(): Promise<LatestSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/latest.json'), 'utf8')) as LatestSnapshot;
}

const visibleMechanisms = [
  'source-prefix-overlap',
  'semantic-predispatch',
  'exact-request-sharing',
  'whole-run-retention',
  'cow-fresh-memory',
  'cold-io-continuation',
  'fresh-reevaluation',
  'source-mismatch-fallback',
];

describe('latest-only Lab snapshot', () => {
  it('loads eight evidence-bound mechanism examples without a cohort panel', async () => {
    const snapshot = await validateLatestSnapshot(await fixture());
    expect(snapshot.schema_version).toBe('pysolate.lab-latest.v2');
    expect(snapshot.demos.map((demo) => demo.id)).toEqual(visibleMechanisms);
    expect(snapshot.demos.map((demo) => demo.status)).toEqual(['measured', 'measured', 'measured', 'experimental', 'experimental', 'experimental', 'experimental', 'control']);
    expect(snapshot.demos.map((demo) => demo.view_kind)).toEqual(['timeline', 'timeline', 'timeline', 'timeline', 'state_flow', 'state_flow', 'timeline', 'timeline']);
    expect(snapshot.demos[0].metrics.map((metric) => metric.value)).toContain('1.923×');
    expect(snapshot.demos[1].metrics.map((metric) => metric.value)).toEqual(['3215 ms', '2196 ms', '1018 ms']);
    expect(snapshot.demos[2].metrics.slice(0, 2).map((metric) => metric.value)).toEqual(['2', '1']);
    expect(snapshot.demos[3].metrics.slice(0, 2).map((metric) => metric.value)).toEqual(['3', '1']);
    expect(snapshot.demos[4].metrics.map((metric) => metric.value)).toContain('384 MiB');
    expect(snapshot.demos[5].metrics.map((metric) => metric.value)).toContain('0 MiB');
    expect(snapshot.demos.flatMap((demo) => demo.metrics).some((metric) => /oracle|passed/i.test(`${metric.label} ${metric.value} ${metric.note}`))).toBe(false);
    expect(snapshot.demos.map((demo) => demo.source).join('\n')).not.toMatch(/\balpha\b|\bbeta\b|\bprofile\b|\bticket\b|inputs\[['\"]value['\"]\]/i);
    expect(snapshot).not.toHaveProperty('headline');
    expect(snapshot).not.toHaveProperty('boundary');
    expect(snapshot.demos.every((demo) => !('claim_boundary' in demo))).toBe(true);
  });

  it('rejects unknown fields, private paths and forged timeline bounds', async () => {
    const unknown = await fixture() as LatestSnapshot & { legacy?: boolean };
    unknown.legacy = true;
    await expect(validateLatestSnapshot(unknown)).rejects.toThrow(/unknown or missing fields/);

    const privatePath = await fixture();
    privatePath.demos[0].source = '/Users/private/source.py';
    await expect(validateLatestSnapshot(privatePath)).rejects.toThrow(/private body or path marker/);

    const forged = await fixture();
    forged.demos[0].lanes[0].segments[0].end_ns = forged.demos[0].lanes[0].duration_ns + 1;
    await expect(validateLatestSnapshot(forged)).rejects.toThrow(/segment is invalid/);

    const overlappingAnnotations = await fixture();
    overlappingAnnotations.demos[0].annotations[1].start_line = 1;
    await expect(validateLatestSnapshot(overlappingAnnotations)).rejects.toThrow(/annotations overlap/);

    const missingTimeline = await fixture();
    missingTimeline.demos[6].lanes = [];
    await expect(validateLatestSnapshot(missingTimeline)).rejects.toThrow(/view contract drifted/);

    const stateFlowWithTimeline = await fixture();
    stateFlowWithTimeline.demos[4].lanes = structuredClone(stateFlowWithTimeline.demos[0].lanes);
    await expect(validateLatestSnapshot(stateFlowWithTimeline)).rejects.toThrow(/view contract drifted/);

    const selfReported = await fixture();
    selfReported.identity = `sha256:${'0'.repeat(64)}`;
    selfReported.demos[0].metrics[0].value = '9999 ms';
    await expect(validateLatestSnapshot(selfReported)).rejects.toThrow(/build-pinned evidence/);
  });
});
