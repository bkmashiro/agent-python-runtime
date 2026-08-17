import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { loadDayTripSnapshot, validateDayTripSnapshot, type DayTripSnapshot } from './dayTripData';

async function latestFixture() {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/latest.json'), 'utf8'));
}

async function dayTripFixture(): Promise<DayTripSnapshot> {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/day-trip.json'), 'utf8')) as DayTripSnapshot;
}

function stubLabFetch(latest: unknown, dayTrip: unknown) {
  const fetchMock = vi.fn().mockImplementation(async (input: string) => {
    const value = input.includes('day-trip.json') ? dayTrip : latest;
    return {
      ok: true,
      json: async () => value,
      text: async () => JSON.stringify(value),
    };
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => vi.unstubAllGlobals());

describe('public day-trip fixture decoder', () => {
  it('accepts the reviewed fixture and preserves the full public projection', async () => {
    const snapshot = await validateDayTripSnapshot(await dayTripFixture());
    expect(snapshot.schema_version).toBe('pysolate.public-day-trip.v1');
    expect(snapshot.source_commit).toMatch(/^[0-9a-f]{40}$/);
    expect(snapshot.input.skills).toHaveLength(3);
    expect(snapshot.input.public_system_instructions).toContain('Do not make network requests');
    expect(snapshot.agents.map((agent) => agent.id)).toEqual(['brighton', 'oxford']);
    expect(snapshot.agents.map((agent) => agent.runtime.api_waits)).toHaveLength(2);
    expect(snapshot.decision.selected_candidate_id).toBe('oxford');
    expect(snapshot.final_output.total_cost_gbp).toBe(78);
  });

  it('rejects unknown fields, duplicate JSON keys, and inconsistent public totals', async () => {
    const unknownField = await dayTripFixture() as DayTripSnapshot & { unexpected?: boolean };
    unknownField.unexpected = true;
    await expect(validateDayTripSnapshot(unknownField)).rejects.toThrow(/unknown or missing fields/);

    const duplicateRaw = await readFile(join(process.cwd(), 'public/lab-data/day-trip.json'), 'utf8');
    const duplicate = duplicateRaw.replace(/("title":\s*"[^"]+")/, '$1,\n  $1');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, text: async () => duplicate }));
    await expect(loadDayTripSnapshot()).rejects.toThrow(/duplicate JSON key/);
    vi.unstubAllGlobals();

    const selectedDrift = await dayTripFixture();
    selectedDrift.decision.selected_candidate_id = 'brighton';
    await expect(validateDayTripSnapshot(selectedDrift)).rejects.toThrow(/main Agent selection output|selected\/discarded candidate IDs/);

    const outputDrift = await dayTripFixture();
    outputDrift.final_output.total_cost_gbp = 77;
    await expect(validateDayTripSnapshot(outputDrift)).rejects.toThrow(/final output total or candidate is inconsistent/);

    const nestedUnknown = await dayTripFixture();
    (nestedUnknown.agents[0].runtime as DayTripSnapshot['agents'][number]['runtime'] & { leaked?: string }).leaked = 'private';
    await expect(validateDayTripSnapshot(nestedUnknown)).rejects.toThrow(/unknown or missing fields/);
  });
});

describe('Pysolate Lab day-trip inspector', () => {
  it('fetches latest mechanisms and the day-trip fixture in parallel, then defaults to the travel case', async () => {
    const fetchMock = stubLabFetch(await latestFixture(), await dayTripFixture());
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'A Saturday day trip under £100' })).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(expect.arrayContaining(['/lab-data/latest.json', '/lab-data/day-trip.json']));
    expect(screen.getByRole('navigation', { name: 'Travel trace groups' })).toBeVisible();
    expect(screen.getByRole('button', { name: /Public input/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /Candidate Agents/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /Fresh Guest execution/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /Main Agent decision/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /Final output/ })).toBeVisible();
  });

  it('keeps the public envelope explicit and renders the complete inspected evidence', async () => {
    stubLabFetch(await latestFixture(), await dayTripFixture());
    render(<App />);
    await screen.findByRole('heading', { name: 'A Saturday day trip under £100' });

    expect(screen.getByRole('heading', { name: 'Task summary' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Public input envelope' })).toBeVisible();
    expect(screen.getByText(/Do not make network requests or access credentials/)).toBeVisible();
    fireEvent.click(screen.getByText('3 skill bodies'));
    expect(screen.getByText(/# Budget checking skill/)).toBeVisible();
    fireEvent.click(screen.getByText('Workspace snapshot'));
    expect(screen.getByText('pysolate.day-trip-api-fixture.v1')).toBeVisible();
    expect(screen.getByText('Private fields withheld')).toBeVisible();

    expect(screen.getByRole('heading', { name: 'Brighton model output' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Oxford model output' })).toBeVisible();
    expect(screen.getAllByText('Generated Python')).toHaveLength(2);
    expect(screen.getAllByText('3 API waits/results')).toHaveLength(2);
    expect(screen.getAllByText('Workspace SHA')).toHaveLength(2);
    expect(screen.getByText('selected')).toBeVisible();
    expect(screen.getByText('discarded')).toBeVisible();
    expect(screen.getAllByText('Main Agent final output')).toHaveLength(2);
    expect(screen.getAllByText('£78')).toHaveLength(2);
    expect(screen.getAllByText('Oxford').length).toBeGreaterThan(0);
  });

  it('does not render an inspector when the day-trip projection is rejected', async () => {
    const invalid = await dayTripFixture();
    invalid.agents[1].runtime.observed_output.total_cost_gbp = 999;
    stubLabFetch(await latestFixture(), invalid);
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Lab data rejected' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'A Saturday day trip under £100' })).not.toBeInTheDocument();
  });

  it('keeps the old top-level timeline clearly marked as legacy', async () => {
    stubLabFetch(await latestFixture(), await dayTripFixture());
    render(<App />);
    await screen.findByRole('heading', { name: 'A Saturday day trip under £100' });
    fireEvent.click(screen.getByRole('button', { name: 'Timeline' }));

    expect(await screen.findByRole('heading', { name: 'Execution timeline' })).toBeVisible();
    expect(screen.getByText('LEGACY')).toBeVisible();
    expect(screen.getByText(/not the day-trip execution trace/)).toBeVisible();
  });

  it('still exposes the mechanisms surface without external icon or font dependencies', async () => {
    stubLabFetch(await latestFixture(), await dayTripFixture());
    render(<App />);
    await screen.findByRole('heading', { name: 'A Saturday day trip under £100' });
    fireEvent.click(screen.getByRole('button', { name: 'Mechanisms' }));

    expect(await screen.findByRole('heading', { name: 'Runtime mechanisms' })).toBeVisible();
    expect(screen.getByLabelText('Pysolate Lab home').querySelector('svg')).toBeTruthy();
    const font = getComputedStyle(document.querySelector('.code-row code')!).fontFamily;
    expect(font.toLowerCase()).toContain('monospace');
    expect(within(screen.getByRole('region', { name: 'Execution' })).getAllByText('finalize')).toHaveLength(2);
  });
});
