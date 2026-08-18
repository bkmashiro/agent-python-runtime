import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { loadUnifiedSnapshot, validateUnifiedSnapshot, type UnifiedSnapshot } from './unifiedCampaignData';

async function fixture(): Promise<UnifiedSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/unified-campaign.json'), 'utf8')) as UnifiedSnapshot;
}
function stubFetch(value: unknown) {
  const raw = JSON.stringify(value);
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, text: async () => raw });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}
afterEach(() => vi.unstubAllGlobals());

describe('unified campaign decoder', () => {
  it('accepts the build-pinned projection and preserves its causal facts', async () => {
    const snapshot = await validateUnifiedSnapshot(await fixture());
    expect(snapshot.schema_version).toBe('pysolate.lab-unified-campaign.v1');
    expect(snapshot.candidates.map((candidate) => candidate.total_cost_gbp)).toEqual([118.4, 78]);
    expect(snapshot.matched_control.pair_count).toBe(3);
    expect(snapshot.matched_control.median_savings_ns).toBeGreaterThan(0);
    expect(snapshot.events.filter((event) => event.type === 'semantic.claim')).toHaveLength(6);
  });

  it('rejects arithmetic, event causality, identity, and duplicate-key mutations', async () => {
    const arithmetic = await fixture();
    arithmetic.matched_control.median_savings_ns += 1;
    await expect(validateUnifiedSnapshot(arithmetic)).rejects.toThrow(/arithmetic/);

    const causality = await fixture();
    const feed = causality.events.find((event) => event.type === 'source.feed.complete' && event.actor_id === 'brighton')!;
    const request = causality.events.find((event) => event.type === 'request.start' && event.logical_id?.startsWith('brighton-'))!;
    request.at_ns = feed.at_ns + 1;
    await expect(validateUnifiedSnapshot(causality)).rejects.toThrow(/causality|ledger order/);

    const raw = await readFile(join(process.cwd(), 'public/lab-data/unified-campaign.json'), 'utf8');
    const duplicate = raw.replace(/("title":\s*"[^"]+")/, '$1,\n  $1');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, text: async () => duplicate }));
    await expect(loadUnifiedSnapshot()).rejects.toThrow(/duplicate JSON key/);
  });
});

describe('Pysolate Lab unified campaign', () => {
  it('defaults to one campaign rather than independent mechanism cards', async () => {
    const fetchMock = stubFetch(await fixture());
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'One London day-trip campaign' })).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Oxford')).toBeVisible();
    expect(screen.getAllByText('£78.00')).toHaveLength(2);
    expect(screen.getByText('£118.40')).toBeVisible();
    expect(screen.getByText(/These are phases of this run/)).toBeVisible();
  });

  it('shows phase evidence and actual public raw events from the same projection', async () => {
    stubFetch(await fixture()); render(<App />);
    await screen.findByRole('heading', { name: 'One London day-trip campaign' });
    fireEvent.click(screen.getByRole('button', { name: 'Mechanisms' }));
    expect(screen.getByRole('navigation', { name: 'Unified campaign phases' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Select, seal, and resume/ }));
    expect(screen.getByRole('heading', { name: 'Select, seal, and resume' })).toBeVisible();
    expect(screen.getByText('TYPED EVENT EVIDENCE')).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: 'Timeline' }));
    expect(screen.getByRole('heading', { name: 'Execution timeline' })).toBeVisible();
    expect(screen.getByText('PUBLIC RAW EVENT')).toBeVisible();
    expect(screen.getByText(/no reconstructed provider messages/i)).toBeVisible();
  });

  it('fails closed instead of rendering a mutated projection', async () => {
    const invalid = await fixture();
    invalid.candidates[1].total_cost_gbp = 79;
    stubFetch(invalid); render(<App />);
    expect(await screen.findByRole('heading', { name: 'Lab data rejected' })).toBeVisible();
  });
});
