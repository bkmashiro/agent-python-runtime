import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import CampaignApp from './CampaignApp';

const fixture = JSON.parse(readFileSync(join(process.cwd(), 'public/lab-data/authority-transparent-campaign.json'), 'utf8'));

describe('CampaignApp', () => {
  afterEach(() => vi.restoreAllMocks());

  it('does not render unknown projection fields', async () => {
    const body = structuredClone(fixture);
    body.programs[0].untrusted_body = 'private body';
    body.programs[0].execution.untrusted_body = 'nested private body';
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => body })));
    const { container } = render(<CampaignApp />);
    expect(await screen.findByRole('heading', { name: 'P01 causal flow' })).toBeInTheDocument();
    expect(container.textContent).not.toContain('private body');
  });

  it('switches the causal flow to the selected program', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => fixture })));
    render(<CampaignApp />);
    expect(await screen.findByRole('heading', { name: 'P01 causal flow' })).toBeInTheDocument();
    expect(screen.getByText('peak 3/3 slots')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Campaign wall-time lanes' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /P18delegate child/i }));
    expect(screen.getByRole('heading', { name: 'P18 causal flow' })).toBeInTheDocument();
    expect(screen.getByText('authority_widening')).toBeInTheDocument();
  });
});
