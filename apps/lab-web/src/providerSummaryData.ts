import type { UnifiedSnapshot } from './unifiedCampaignData';
import { expectedProviderSummarySHA256, expectedProviderSummaryURL } from './providerSummaryIdentity';

export interface ProviderSummaryCall {
  id: 'candidate-brighton' | 'candidate-oxford' | 'main-selection' | 'main-final';
  ordinal: number;
  actor: 'brighton' | 'oxford' | 'main';
  phase: 'candidate-generation' | 'selection' | 'final-response';
  purpose: string;
  model: string;
  token_usage: { prompt: number; completion: number; reasoning: number };
  input: unknown;
  thinking_summary: string[];
  output: unknown;
}

export interface ProviderSummary {
  schema_version: 'pysolate.lab-provider-summary.v1';
  source_trace_sha256: string;
  disclosure: string;
  calls: ProviderSummaryCall[];
}

const digest = /^sha256:[0-9a-f]{64}$/;
const expectedCallIDs = ['candidate-brighton', 'candidate-oxford', 'main-selection', 'main-final'];

export async function loadProviderSummary(url = expectedProviderSummaryURL): Promise<ProviderSummary> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`provider summary load failed (${response.status})`);
  const raw = await response.text();
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify provider summary data');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(raw));
  const identity = `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
  if (identity !== expectedProviderSummarySHA256) throw new Error('provider summary is not the build-pinned projection');
  if (/reasoning_content|request_id|authorization|bearer|system_fingerprint|trace_id/i.test(raw)) throw new Error('provider summary contains a forbidden raw field');
  const value = JSON.parse(raw) as ProviderSummary;
  if (value.schema_version !== 'pysolate.lab-provider-summary.v1' || !digest.test(value.source_trace_sha256) || value.calls.length !== 4) throw new Error('provider summary is invalid');
  value.calls.forEach((call, index) => {
    if (call.ordinal !== index + 1 || call.id !== expectedCallIDs[index] || !call.purpose || !call.model || !call.thinking_summary.length) throw new Error('provider summary call order is invalid');
  });
  return value;
}

export function validateProviderSummaryBinding(summary: ProviderSummary, campaign: UnifiedSnapshot): void {
  for (const id of ['brighton', 'oxford'] as const) {
    const call = summary.calls.find((item) => item.id === `candidate-${id}`);
    const output = call?.output as { candidate_id?: string; python_source?: string } | undefined;
    const runtime = campaign.candidates.find((candidate) => candidate.id === id);
    if (!call || !output || !runtime || output.candidate_id !== id || output.python_source !== runtime.model_source) throw new Error(`provider/runtime summary binding failed for ${id}`);
  }
  const selection = summary.calls.find((call) => call.id === 'main-selection')?.output as { selected_candidate_id?: string } | undefined;
  const final = summary.calls.find((call) => call.id === 'main-final')?.output as { selected_candidate_id?: string; total_cost_gbp?: number } | undefined;
  if (selection?.selected_candidate_id !== campaign.selected || final?.selected_candidate_id !== campaign.selected || final?.total_cost_gbp !== campaign.final_total_gbp) throw new Error('provider/runtime final summary binding failed');
}
