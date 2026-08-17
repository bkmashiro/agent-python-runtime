import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App';

async function snapshot() {
  return JSON.parse(await readFile(join(process.cwd(), 'public/lab-data/latest.json'), 'utf8'));
}

afterEach(() => vi.unstubAllGlobals());

describe('latest Pysolate Lab', () => {
  it('shows source-prefix measurements and switches to exact sharing', async () => {
    const value = await snapshot();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Start the READ before the model finishes' })).toBeVisible();
    expect(screen.getByText('1.923×')).toBeVisible();
    expect(screen.getByText("record = slow.lookup('alpha')")).toBeVisible();
    expect(screen.getByRole('region', { name: 'Measured execution timeline' })).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: /Two agents, one physical Guest/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Two agents, one physical Guest' })).toBeVisible());
    expect(screen.getByText('exact_shared')).toBeVisible();
    expect(screen.getByText('same sealed physical ID')).toBeVisible();
  });

  it('shows the source-mismatch fail-closed control', async () => {
    const value = await snapshot();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
    render(<App />);
    await screen.findByRole('heading', { name: 'Start the READ before the model finishes' });
    fireEvent.click(screen.getByRole('button', { name: /Different source stays fresh/ }));
    expect(await screen.findByText('safe fallback observed')).toBeVisible();
    expect(screen.getByText('reject_source_mismatch')).toBeVisible();
    expect(screen.getByText("result = {'square': pow(inputs['value'], 2)}")).toBeVisible();
  });
});
