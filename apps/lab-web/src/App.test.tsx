import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';

import { loadUnifiedSnapshot, validateUnifiedSnapshot, type UnifiedSnapshot } from './unifiedCampaignData';

async function fixture(): Promise<UnifiedSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/unified-campaign.json'), 'utf8')) as UnifiedSnapshot;
}

async function providerSummaryRaw(): Promise<string> { return readFile(join(process.cwd(), 'public/lab-data/provider-summary.json'), 'utf8'); }
function stubFetch(campaign: unknown, providerRaw: string) {
  const fetchMock = vi.fn().mockImplementation(async (url: string) => {
		const raw = url.includes('provider-summary') ? providerRaw : JSON.stringify(campaign);
		return { ok: true, text: async () => raw };
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}
afterEach(() => vi.unstubAllGlobals());

describe('unified campaign decoder', () => {
  it('accepts the build-pinned projection and preserves full source and Guest output', async () => {
    const snapshot = await validateUnifiedSnapshot(await fixture());
    expect(snapshot.candidates.map((candidate) => candidate.total_cost_gbp)).toEqual([118.4, 78]);
    expect(snapshot.candidates[0].model_source).toContain('travel.weather');
    expect(snapshot.candidates[0].guest_response).toMatchObject({ status: 'ok' });
    expect(snapshot.phases.every((phase) => phase.event_ids.length > 0)).toBe(true);
    expect(snapshot.matched_control.pair_count).toBe(3);
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

describe('Pysolate Lab development debugger', () => {
  it('opens directly on mechanisms with dense run context and no campaign landing page', async () => {
    const fetchMock = stubFetch(await fixture(), await providerSummaryRaw());
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Code + tool calls' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Campaign' })).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(screen.getByText('£118.40')).toBeVisible();
    expect(screen.getByText('£78.00')).toBeVisible();
    expect(screen.getByRole('complementary', { name: 'Execution event inspector' })).toBeVisible();
  });

  it('shows the complete recorded Harness model triplet and reasoning in the mechanism inspector', async () => {
    stubFetch(await fixture(), await providerSummaryRaw()); render(<App />);
    await screen.findByRole('heading', { name: 'Code + tool calls' });
    fireEvent.click(screen.getByRole('button', { name: /^Code \+ tool calls/ }));
    fireEvent.click(screen.getByRole('button', { name: /^Model generation/ }));
    expect(screen.getByRole('heading', { name: 'Model generation' })).toBeVisible();
    fireEvent.click(screen.getAllByRole('button', { name: /model\.candidate-generation/ })[0]);
    fireEvent.click(screen.getByRole('button', { name: 'Input / output' }));
    expect(screen.getByText(/required_tool_calls/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Python' }));
    expect(screen.getByText(/travel\.weather\("brighton"\)/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Raw event' }));
    const inspector = screen.getByRole('complementary', { name: 'Execution event inspector' });
    expect(inspector.textContent).toContain('"phase": "candidate-generation"');
    expect(inspector.textContent).toContain('reasoning_content');
    expect(inspector.textContent).toContain('provider_response_id');
    expect(inspector.textContent).toContain('model.context');
    expect(inspector.textContent).toContain('model.body');
    expect(inspector.textContent).toContain('model.output');
  });

  it('shows each source.statement.complete prefix and highlights only its appended chunk', async () => {
    stubFetch(await fixture(), await providerSummaryRaw()); render(<App />);
    await screen.findByRole('heading', { name: 'Code + tool calls' });
    const statements = screen.getAllByRole('button', { name: /source\.statement\.complete · oxford/ });
    fireEvent.click(statements[0]);
    fireEvent.click(screen.getByRole('button', { name: 'Python' }));
    expect(screen.getByText(/prefix 1\/6/)).toBeVisible();
    expect(screen.getByText('weather = travel.weather("oxford")')).toBeVisible();
    expect(screen.queryByText(/rail = travel\.rail\("oxford"/)).not.toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: 'Execution event inspector' }).querySelectorAll('.source-line-delta')).toHaveLength(1);
    fireEvent.click(statements[1]);
    expect(screen.getByText(/prefix 2\/6/)).toBeVisible();
    expect(screen.getByText('rail = travel.rail("oxford", travellers=2)')).toBeVisible();
    expect(screen.getByRole('complementary', { name: 'Execution event inspector' }).querySelectorAll('.source-line-delta')).toHaveLength(1);
  });

  it('shows semantic qualification against its triggering prefix rather than final source', async () => {
    stubFetch(await fixture(), await providerSummaryRaw()); render(<App />);
    await screen.findByRole('heading', { name: 'Code + tool calls' });
    fireEvent.click(screen.getAllByRole('button', { name: /semantic\.qualified · host · travel\.weather/ })[0]);
    fireEvent.click(screen.getByRole('button', { name: 'Python' }));
    expect(screen.getByText(/qualifying source prefix 1\/6/)).toBeVisible();
    expect(screen.getByText('weather = travel.weather("oxford")')).toBeVisible();
    expect(screen.queryByText(/rail = travel\.rail\("oxford"/)).not.toBeInTheDocument();
  });

  it('uses the map itself to drive focused model and tool evidence', async () => {
    stubFetch(await fixture(), await providerSummaryRaw()); render(<App />);
    await screen.findByRole('heading', { name: 'Code + tool calls' });
    fireEvent.click(screen.getByRole('button', { name: 'Timeline' }));
    const map = screen.getByRole('region', { name: 'Semantic causal lane timeline' });
    expect(screen.getByRole('heading', { name: 'Main orchestration, fan-out, and convergence' })).toBeVisible();
    expect(map.querySelectorAll('.lane-axis')).toHaveLength(4);
    expect(map.querySelectorAll('.lane-span')).toHaveLength(13);
    expect(map.querySelectorAll('.span-request')).toHaveLength(6);
    expect(map.querySelectorAll('.span-request rect')).toHaveLength(6);
    expect(map.querySelectorAll('.span-request circle')).toHaveLength(12);
    expect(map.querySelectorAll('.lane-node')).toHaveLength(0);
    expect(map.querySelectorAll('.lane-transition')).toHaveLength(19);
    expect(map.querySelectorAll('.lane-reuse')).toHaveLength(6);
    expect(map.querySelectorAll('.lane-orchestration')).toHaveLength(1);
    expect(map.querySelectorAll('.lane-orchestration-link')).toHaveLength(2);
    expect(map.textContent).toContain('orchestrates fan-out');
    expect(map.textContent).toContain('recorded by Harness · untimed in Runtime');
    expect(map.textContent).toContain('seal → export → import → bind');
    expect(screen.queryByRole('navigation', { name: 'Evidence filters' })).not.toBeInTheDocument();
    expect(screen.queryByRole('complementary', { name: 'Execution event inspector' })).not.toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: 'Selected timeline operation' })).toHaveTextContent('Select one operation');
    fireEvent.click(screen.getByRole('button', { name: /M01.*brighton.*candidate-generation/i }));
    const focused = screen.getByRole('complementary', { name: 'Selected timeline operation' });
    expect(focused).toHaveTextContent('Recorded model reasoning');
    expect(focused).toHaveTextContent('verbatim experiment trace');
    expect(focused).toHaveTextContent('produce JSON for the brighton candidate');
    expect(focused.querySelectorAll('.syntax-key').length).toBeGreaterThan(0);
    expect(focused.querySelectorAll('.syntax-string').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: /P00.*main.*orchestration/i }));
    expect(focused).toHaveTextContent('Plan a one-day Saturday trip for two people from London');
    fireEvent.click(map.querySelector('.span-request')!);
    expect(focused).toHaveTextContent('typed input → typed output');
    expect(focused).toHaveTextContent('Tool input');
    expect(focused).toHaveTextContent('Tool output');
    fireEvent.click(map.querySelector('.span-workspace')!);
    expect(focused).toHaveTextContent('host · +31.599s → +31.766s');
    expect(focused).toHaveTextContent('selected workspace capsule');
    expect(focused).toHaveTextContent('seal → export → import → bind');
  });

  it('combines filters and exposes an explicit tool-call-only view', async () => {
    stubFetch(await fixture(), await providerSummaryRaw()); render(<App />);
    await screen.findByRole('heading', { name: 'Code + tool calls' });
    const code = screen.getByRole('button', { name: /^Code \+ tool calls/ });
    const tools = screen.getByRole('button', { name: /^Tool calls/ });
    expect(code).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(tools);
    expect(screen.getByRole('heading', { name: 'Combined evidence filters' })).toBeVisible();
    expect(code).toHaveAttribute('aria-pressed', 'true');
    expect(tools).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(code);
    expect(screen.getByRole('heading', { name: 'Tool calls' })).toBeVisible();
    expect(screen.queryByRole('button', { name: /source\.statement\.complete/ })).not.toBeInTheDocument();
    expect(screen.getAllByText('travel.weather').length).toBeGreaterThan(0);
    expect(screen.getByRole('navigation', { name: 'Evidence filters' }).textContent).not.toMatch(/\b0[0-9]\b/);
  });

  it('fails closed instead of rendering a mutated campaign projection', async () => {
    const invalid = await fixture();
    invalid.candidates[1].total_cost_gbp = 79;
    stubFetch(invalid, await providerSummaryRaw()); render(<App />);
    expect(await screen.findByRole('heading', { name: 'Lab data rejected' })).toBeVisible();
  });
});
