import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { validateLatestSnapshot, type LatestSnapshot } from './latestData';

async function fixture(): Promise<LatestSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/latest.json'), 'utf8')) as LatestSnapshot;
}

describe('latest-only Lab snapshot', () => {
  it('loads three visible evidence-bound demos', async () => {
    const snapshot = await validateLatestSnapshot(await fixture());
    expect(snapshot.demos.map((demo) => demo.id)).toEqual(['source-prefix-overlap', 'exact-request-sharing', 'source-mismatch-fallback']);
    expect(snapshot.demos.map((demo) => demo.status)).toEqual(['optimized', 'optimized', 'safety_control']);
    expect(snapshot.demos[0].metrics.map((metric) => metric.value)).toContain('1.923×');
    expect(snapshot.demos[1].metrics.slice(0, 2).map((metric) => metric.value)).toEqual(['2', '1']);
    expect(snapshot.demos[1].annotations.map((annotation) => annotation.tone)).toContain('shared_skip');
    expect(snapshot.demos[2].source).toContain("pow(inputs['value'], 2)");
    expect(snapshot.boundary).toMatchObject({ events: 36, structurally_eligible: 0, performance_supported: false });
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

    const selfReported = await fixture();
    selfReported.identity = `sha256:${'0'.repeat(64)}`;
    selfReported.demos[0].metrics[0].value = '9999 ms';
    await expect(validateLatestSnapshot(selfReported)).rejects.toThrow(/build-pinned evidence/);
  });
});
