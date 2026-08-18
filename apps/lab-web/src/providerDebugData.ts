import type { UnifiedSnapshot } from './unifiedCampaignData';

export interface ProviderDebugEvent {
  ordinal: number;
  event_id: string;
  type: string;
  actor_id: string;
  parent_event_ids?: string[];
  payload: unknown;
  body?: unknown;
}

export interface ProviderHarnessResult {
  planning: unknown;
  candidates: Array<{ candidate_id: 'brighton' | 'oxford'; summary: string; python_source: string }>;
  executions: Array<{ candidate_id: 'brighton' | 'oxford'; output: unknown }>;
  selection: unknown;
  final: unknown;
}

export interface ProviderDebug {
  schema_version: 'pysolate.lab-provider-debug.v1';
  trace_id: string;
  header_sha256: string;
  seal_sha256: string;
  events: ProviderDebugEvent[];
  harness_result: ProviderHarnessResult;
}

const digest = /^sha256:[0-9a-f]{64}$/;
const expectedProviderDebugSHA256 = 'sha256:7eb2b9c908de8fa218000a5cd03377fd38767c3bf28a7df0280c07fa6749c8f3';

export async function loadProviderDebug(url = '/lab-data/provider-debug.json'): Promise<ProviderDebug> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`provider debug trace load failed (${response.status})`);
  const raw = await response.text();
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify provider debug data');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(raw));
  const identity = `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
  if (identity !== expectedProviderDebugSHA256) throw new Error('provider debug trace is not the build-pinned recording');
  const value = JSON.parse(raw) as ProviderDebug;
  if (value.schema_version !== 'pysolate.lab-provider-debug.v1' || !digest.test(value.header_sha256) || !digest.test(value.seal_sha256) || value.events.length !== 14 || value.harness_result.candidates.length !== 2) throw new Error('provider debug trace is invalid');
  value.events.forEach((event, index) => {
    if (event.ordinal !== index + 1 || !digest.test(event.event_id) || !event.type || !event.actor_id) throw new Error('provider debug event order is invalid');
  });
  return value;
}

export function validateProviderDebugBinding(debug: ProviderDebug, campaign: UnifiedSnapshot): void {
  for (const id of ['brighton', 'oxford'] as const) {
    const generated = debug.harness_result.candidates.find((candidate) => candidate.candidate_id === id);
    const execution = debug.harness_result.executions.find((item) => item.candidate_id === id);
    const runtime = campaign.candidates.find((candidate) => candidate.id === id);
    const runtimeResult = runtime?.guest_response && typeof runtime.guest_response === 'object' ? (runtime.guest_response as { result?: unknown }).result : undefined;
    if (!generated || !execution || !runtime || generated.python_source !== runtime.model_source || canonicalJSON(execution.output) !== canonicalJSON(runtimeResult)) throw new Error(`provider/runtime trace binding failed for ${id}`);
  }
  const selection = debug.harness_result.selection as { selected_candidate_id?: string };
  const final = debug.harness_result.final as { selected_candidate_id?: string; total_cost_gbp?: number };
  if (selection.selected_candidate_id !== campaign.selected || final.selected_candidate_id !== campaign.selected || final.total_cost_gbp !== campaign.final_total_gbp) throw new Error('provider/runtime final selection binding failed');
  }

  function canonicalJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  if (value && typeof value === 'object') {
  const record = value as Record<string, unknown>;
  return `{${Object.keys(record).sort().map((key) => `${JSON.stringify(key)}:${canonicalJSON(record[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
  }
