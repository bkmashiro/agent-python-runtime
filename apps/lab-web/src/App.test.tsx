import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';

async function snapshot() {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/latest.json'), 'utf8'));
}

async function taskSnapshot() {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/task.json'), 'utf8'));
}

afterEach(() => vi.unstubAllGlobals());

describe('latest Pysolate Lab', () => {
  it('shows a quiet mechanism browser with eight examples', async () => {
    const value = await snapshot();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Runtime mechanisms' })).toBeVisible();
    expect(screen.getByLabelText('Pysolate Lab home').querySelector('svg')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /measured|experimental|control/i })).toHaveLength(8);
    expect(screen.queryByText(/THREE SMALL PROGRAMS/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/REAL GUEST · VERIFIED/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Claim boundary/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Natural-cohort/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/0\s*\/\s*36/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^sha256:/)).not.toBeInTheDocument();
    expect(screen.queryByText('Calls')).not.toBeInTheDocument();
    expect(screen.queryByText('same oracle')).not.toBeInTheDocument();
  });

  it('switches between measured optimization examples', async () => {
    const value = await snapshot();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Start the READ before generation finishes' })).toBeVisible();
    expect(screen.getByText('1.923×')).toBeVisible();
    expect(screen.getByRole('region', { name: 'Execution' })).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: /Pre-dispatch one proven READ/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Pre-dispatch one proven READ' })).toBeVisible());
    expect(screen.getByText('1018 ms')).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: /Compute once, reuse twice/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Compute once, reuse twice' })).toBeVisible());
    expect(screen.getByText('retained hit')).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: /Share the baseline, keep writes private/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Share the baseline, keep writes private' })).toBeVisible());
    expect(screen.getAllByText('384 MiB')[0]).toBeVisible();
  });

  it('restores a real task timeline, trace tree and inspector', async () => {
    const latest = await snapshot();
    const task = await taskSnapshot();
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: string) => ({ ok: true, json: async () => input.includes('task.json') ? task : latest })));
    render(<App />);
    await screen.findByRole('heading', { name: 'Runtime mechanisms' });

    fireEvent.click(screen.getByRole('button', { name: 'Task Inspector' }));
    expect(await screen.findByRole('heading', { name: 'Summarize a development workspace' })).toBeVisible();
    expect(screen.getByRole('tab', { name: 'Timeline' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: /Research workspace shape/ })).toBeVisible();

    fireEvent.click(screen.getByRole('tab', { name: 'Trace tree' }));
    expect(screen.getByRole('button', { name: /Start parallel analyses/ })).toBeVisible();
    expect(screen.queryByText(/oracle .*passed/i)).not.toBeInTheDocument();
  });

  it('shows source mismatch as the single explicit control', async () => {
    const value = await snapshot();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
    render(<App />);
    await screen.findByRole('heading', { name: 'Runtime mechanisms' });
    fireEvent.click(screen.getByRole('button', { name: /Different source stays fresh/ }));
    expect(await screen.findByRole('heading', { name: 'Different source stays fresh' })).toBeVisible();
    expect(screen.getByText('Unsafe reuse')).toBeVisible();
    expect(screen.getByText('0')).toBeVisible();
  });
});
