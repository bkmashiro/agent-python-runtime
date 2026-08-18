import type { UnifiedSnapshot } from './unifiedCampaignData';
import { expectedProviderSummarySHA256, expectedProviderSummaryURL } from './providerSummaryIdentity';

export interface RecordedHarnessEvent {
  ordinal: number;
  event_id: string;
  type: string;
  actor_id: string;
  parent_event_ids?: string[];
  payload?: unknown;
  body?: unknown;
}

export interface ProviderSummaryCall {
  id: 'candidate-brighton' | 'candidate-oxford' | 'main-selection' | 'main-final';
  ordinal: number;
  actor: 'brighton' | 'oxford' | 'main';
  phase: 'candidate-generation' | 'selection' | 'final-response';
  purpose: string;
  model: string;
  provider_response_id: string;
  system_fingerprint: string;
  token_usage: { prompt: number; completion: number; reasoning: number };
  reasoning: string;
  input: unknown;
  output: unknown;
  recorded: { context: RecordedHarnessEvent; body: RecordedHarnessEvent; output: RecordedHarnessEvent };
}

export interface ProviderSummary {
  schema_version: 'pysolate.lab-harness-trajectory.v1';
  source_trace_sha256: string;
  disclosure: string;
  orchestration: { id: 'harness-planning'; actor: 'main'; phase: 'orchestration'; purpose: string; planning: unknown };
  calls: ProviderSummaryCall[];
  raw_trace: { schema_version: string; events: RecordedHarnessEvent[]; harness_result: unknown };
}

const digest = /^sha256:[0-9a-f]{64}$/;
const expectedCallIDs = ['candidate-brighton', 'candidate-oxford', 'main-selection', 'main-final'];
const credentialMaterial = /authorization|bearer\s|api[_-]?key|client[_-]?secret|\/Users\//i;

export async function loadProviderSummary(url = expectedProviderSummaryURL): Promise<ProviderSummary> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`Harness trajectory load failed (${response.status})`);
  const raw = await response.text();
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify Harness trajectory data');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(raw));
  const identity = `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
  if (identity !== expectedProviderSummarySHA256) throw new Error('Harness trajectory is not the build-pinned projection');
  if (credentialMaterial.test(raw)) throw new Error('Harness trajectory contains credential or machine-local material');
  const value = JSON.parse(raw) as ProviderSummary;
  if (value.schema_version !== 'pysolate.lab-harness-trajectory.v1' || !digest.test(value.source_trace_sha256) || value.calls.length !== 4 || value.raw_trace?.events?.length !== 14) throw new Error('Harness trajectory is invalid');
  value.calls.forEach((call, index) => {
    if (call.ordinal !== index + 1 || call.id !== expectedCallIDs[index] || !call.purpose || !call.model || !call.reasoning || !call.recorded?.context || !call.recorded?.body || !call.recorded?.output) throw new Error('Harness model call order is invalid');
  });
  return value;
}

export function validateProviderSummaryBinding(summary: ProviderSummary, campaign: UnifiedSnapshot): void {
  for (const id of ['brighton', 'oxford'] as const) {
    const call = summary.calls.find((item) => item.id === `candidate-${id}`);
    const output = call?.output as { candidate_id?: string; python_source?: string } | undefined;
    const runtime = campaign.candidates.find((candidate) => candidate.id === id);
    if (!call || !output || !runtime || output.candidate_id !== id || output.python_source !== runtime.model_source) throw new Error(`Harness/runtime binding failed for ${id}`);
  }
  const selection = summary.calls.find((call) => call.id === 'main-selection')?.output as { selected_candidate_id?: string } | undefined;
  const final = summary.calls.find((call) => call.id === 'main-final')?.output as { selected_candidate_id?: string; total_cost_gbp?: number } | undefined;
  if (selection?.selected_candidate_id !== campaign.selected || final?.selected_candidate_id !== campaign.selected || final?.total_cost_gbp !== campaign.final_total_gbp) throw new Error('Harness/runtime final binding failed');
}
