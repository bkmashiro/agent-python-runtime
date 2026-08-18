import { expectedUnifiedSnapshotIdentity } from './unifiedIdentity';

export interface UnifiedFact { label: string; value: string; note: string }
export interface UnifiedPhase { id: string; index: number; title: string; summary: string; facts: UnifiedFact[] }
export interface UnifiedCandidate {
  id: 'brighton' | 'oxford';
  total_cost_gbp: number;
  disposition: 'selected' | 'discarded';
  physical_issues: number;
  logical_claims: number;
  source_sha256: string;
  cow_selected: boolean;
}
export interface UnifiedEvent {
  sequence: number;
  id: string;
  at_ns: number;
  type: string;
  actor_id: string;
  logical_id?: string;
  physical_id?: string;
  identity_sha256?: string;
  outcome?: string;
}
export interface UnifiedSnapshot {
  schema_version: 'pysolate.lab-unified-campaign.v1';
  identity: string;
  title: string;
  summary: string;
  selected: 'oxford';
  final_total_gbp: 78;
  candidates: [UnifiedCandidate, UnifiedCandidate];
  phases: UnifiedPhase[];
  events: UnifiedEvent[];
  matched_control: {
    pair_count: 3;
    baseline_median_ns: number;
    optimized_median_ns: number;
    median_savings_ns: number;
    equivalent_results: true;
  };
  provenance: {
    evidence_sha256: string;
    source_commit: string;
    artifact_sha256: string;
    fixture_sha256: string;
    platform: string;
  };
}

const digest = /^sha256:[0-9a-f]{64}$/;
const commit = /^[0-9a-f]{40}$/;
const phaseIDs = ['source-predispatch', 'fresh-execution', 'sharing-retention', 'branch-resume', 'memory-io', 'fail-closed'];
const eventTypes = new Set([
  'source.generation.start', 'source.statement.complete', 'source.feed.complete', 'source.sealed',
  'semantic.qualified', 'semantic.issue', 'semantic.claim', 'request.start', 'request.finish',
  'guest.start', 'guest.end', 'guest.complete', 'function.logical', 'function.leader', 'function.waiter',
  'function.retained', 'function.physical.start', 'function.physical.end', 'branch.discard', 'branch.seal',
  'cow.selected', 'capsule.export', 'capsule.import', 'capsule.bind', 'cold_io.resume',
  'control.argument_mismatch', 'control.source_mismatch',
]);
const privateMarkers = ['/users/', '/home/', '\\\\users\\\\', '.hermes', 'file://', 'private://', 'bearer ', 'api_key', 'password', 'secret', 'provider_request', 'provider_response'];

function object(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value as Record<string, unknown>;
}
function exactKeys(value: Record<string, unknown>, keys: string[], label: string) {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} contains unknown or missing fields`);
}
function allowedKeys(value: Record<string, unknown>, required: string[], optional: string[], label: string) {
  const allowed = new Set([...required, ...optional]);
  if (required.some((key) => !(key in value)) || Object.keys(value).some((key) => !allowed.has(key))) throw new Error(`${label} contains unknown or missing fields`);
}
function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length === 0) throw new Error(`${label} must be non-empty text`);
  return value;
}
function number(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`${label} must be finite`);
  return value;
}
function integer(value: unknown, label: string): number {
  const result = number(value, label);
  if (!Number.isInteger(result)) throw new Error(`${label} must be an integer`);
  return result;
}
function assertDigest(value: unknown, label: string): string {
  const result = text(value, label);
  if (!digest.test(result)) throw new Error(`${label} must be a SHA-256 digest`);
  return result;
}

function validateShape(value: unknown): UnifiedSnapshot {
  const root = object(value, 'unified campaign snapshot');
  if (privateMarkers.some((marker) => JSON.stringify(root).toLowerCase().includes(marker))) throw new Error('unified campaign snapshot contains a private marker');
  exactKeys(root, ['schema_version', 'identity', 'title', 'summary', 'selected', 'final_total_gbp', 'candidates', 'phases', 'events', 'matched_control', 'provenance'], 'unified campaign snapshot');
  if (root.schema_version !== 'pysolate.lab-unified-campaign.v1' || root.selected !== 'oxford' || root.final_total_gbp !== 78) throw new Error('unified campaign headline is invalid');
  assertDigest(root.identity, 'snapshot identity');
  text(root.title, 'title'); text(root.summary, 'summary');

  if (!Array.isArray(root.candidates) || root.candidates.length !== 2) throw new Error('two candidates are required');
  const candidates = root.candidates.map((item, index) => {
    const candidate = object(item, `candidate ${index + 1}`);
    exactKeys(candidate, ['id', 'total_cost_gbp', 'disposition', 'physical_issues', 'logical_claims', 'source_sha256', 'cow_selected'], 'candidate');
    const expectedID = index === 0 ? 'brighton' : 'oxford';
    if (candidate.id !== expectedID || candidate.disposition !== (expectedID === 'oxford' ? 'selected' : 'discarded') || candidate.cow_selected !== true || candidate.physical_issues !== 3 || candidate.logical_claims !== 3) throw new Error('candidate mechanism facts are invalid');
    const total = number(candidate.total_cost_gbp, 'candidate total');
    if (total !== (expectedID === 'oxford' ? 78 : 118.4)) throw new Error('candidate total drifted');
    assertDigest(candidate.source_sha256, 'candidate source');
    return candidate;
  });

  if (!Array.isArray(root.phases) || root.phases.length !== phaseIDs.length) throw new Error('six unified phases are required');
  root.phases.forEach((item, index) => {
    const phase = object(item, `phase ${index + 1}`);
    exactKeys(phase, ['id', 'index', 'title', 'summary', 'facts'], 'phase');
    if (phase.id !== phaseIDs[index] || phase.index !== index + 1 || !Array.isArray(phase.facts) || phase.facts.length !== 2) throw new Error('phase order or facts drifted');
    text(phase.title, 'phase title'); text(phase.summary, 'phase summary');
    phase.facts.forEach((factValue) => {
      const fact = object(factValue, 'phase fact');
      exactKeys(fact, ['label', 'value', 'note'], 'phase fact');
      text(fact.label, 'fact label'); text(fact.value, 'fact value'); text(fact.note, 'fact note');
    });
  });

  if (!Array.isArray(root.events) || root.events.length < 50) throw new Error('typed campaign event ledger is incomplete');
  let previousAt = -1;
  const events = root.events.map((item, index) => {
    const event = object(item, `event ${index + 1}`);
    allowedKeys(event, ['sequence', 'id', 'at_ns', 'type', 'actor_id'], ['logical_id', 'physical_id', 'identity_sha256', 'outcome'], 'event');
    const sequence = integer(event.sequence, 'event sequence');
    const at = integer(event.at_ns, 'event timestamp');
    if (sequence !== index + 1 || at < previousAt || !eventTypes.has(String(event.type))) throw new Error('event ledger order or type is invalid');
    previousAt = at;
    text(event.id, 'event id'); text(event.actor_id, 'event actor');
    if (event.identity_sha256 !== undefined) assertDigest(event.identity_sha256, 'event identity');
    return event as unknown as UnifiedEvent;
  });
  validateCausality(events);

  const matched = object(root.matched_control, 'matched control');
  exactKeys(matched, ['pair_count', 'baseline_median_ns', 'optimized_median_ns', 'median_savings_ns', 'equivalent_results'], 'matched control');
  const baseline = integer(matched.baseline_median_ns, 'baseline median');
  const optimized = integer(matched.optimized_median_ns, 'optimized median');
  const savings = integer(matched.median_savings_ns, 'median savings');
  if (matched.pair_count !== 3 || matched.equivalent_results !== true || savings !== baseline - optimized || savings <= 0) throw new Error('matched control arithmetic is invalid');

  const provenance = object(root.provenance, 'provenance');
  exactKeys(provenance, ['evidence_sha256', 'source_commit', 'artifact_sha256', 'fixture_sha256', 'platform'], 'provenance');
  assertDigest(provenance.evidence_sha256, 'evidence'); assertDigest(provenance.artifact_sha256, 'artifact'); assertDigest(provenance.fixture_sha256, 'fixture');
  if (!commit.test(text(provenance.source_commit, 'source commit')) || provenance.platform !== 'linux/amd64') throw new Error('campaign provenance is invalid');
  return value as UnifiedSnapshot;
}

function validateCausality(events: UnifiedEvent[]) {
  for (const candidate of ['brighton', 'oxford']) {
    const feed = events.find((event) => event.type === 'source.feed.complete' && event.actor_id === candidate);
    const seal = events.find((event) => event.type === 'source.sealed' && event.actor_id === candidate);
    const guest = events.find((event) => event.type === 'guest.start' && event.actor_id === candidate);
    const starts = events.filter((event) => event.type === 'request.start' && event.logical_id?.startsWith(`${candidate}-`));
    const claims = events.filter((event) => event.type === 'semantic.claim' && event.logical_id?.startsWith(`${candidate}-`));
    if (!feed || !seal || !guest || starts.length !== 3 || claims.length !== 3 || starts.some((event) => event.at_ns >= feed.at_ns) || feed.at_ns > seal.at_ns || seal.at_ns > guest.at_ns) throw new Error(`campaign causality failed for ${candidate}`);
  }
  if (!events.some((event) => event.type === 'control.source_mismatch') || !events.some((event) => event.type === 'control.argument_mismatch')) throw new Error('fail-closed controls are missing');
}

function identityDocument(snapshot: UnifiedSnapshot): ArrayBuffer {
  const clone = JSON.parse(JSON.stringify(snapshot)) as UnifiedSnapshot;
  clone.identity = '';
  const encoded = JSON.stringify(clone).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`);
  const bytes = new TextEncoder().encode(encoded);
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
}

export async function validateUnifiedSnapshot(value: unknown): Promise<UnifiedSnapshot> {
  const snapshot = validateShape(value);
  if (snapshot.identity !== expectedUnifiedSnapshotIdentity) throw new Error('unified snapshot is not the build-pinned evidence');
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify unified evidence');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', identityDocument(snapshot));
  const identity = `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
  if (identity !== snapshot.identity) throw new Error('unified snapshot identity mismatch');
  return snapshot;
}

export async function loadUnifiedSnapshot(url = '/lab-data/unified-campaign.json'): Promise<UnifiedSnapshot> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`unified Lab snapshot load failed (${response.status})`);
  const raw = await response.text();
	assertUniqueJSONKeys(raw);
  return validateUnifiedSnapshot(JSON.parse(raw));
}

function assertUniqueJSONKeys(raw: string) {
  let offset = 0;
  const whitespace = () => { while (/\s/.test(raw[offset] ?? '')) offset += 1; };
  const string = () => {
    whitespace();
    if (raw[offset] !== '"') throw new Error('invalid unified Lab JSON');
    const start = offset++;
    while (offset < raw.length) {
      if (raw[offset] === '\\') { offset += 2; continue; }
      if (raw[offset++] === '"') return JSON.parse(raw.slice(start, offset)) as string;
    }
    throw new Error('invalid unified Lab JSON');
  };
  const value = (): void => {
    whitespace();
    if (raw[offset] === '{') { objectValue(); return; }
    if (raw[offset] === '[') { arrayValue(); return; }
    if (raw[offset] === '"') { string(); return; }
    const start = offset;
    while (offset < raw.length && !/[\s,}\]]/.test(raw[offset])) offset += 1;
    if (start === offset) throw new Error('invalid unified Lab JSON');
  };
  const objectValue = () => {
    offset += 1;
    const keys = new Set<string>();
    whitespace();
    if (raw[offset] === '}') { offset += 1; return; }
    while (offset < raw.length) {
      const key = string();
      if (keys.has(key)) throw new Error(`unified Lab contains duplicate JSON key: ${key}`);
      keys.add(key);
      whitespace();
      if (raw[offset++] !== ':') throw new Error('invalid unified Lab JSON');
      value(); whitespace();
      if (raw[offset] === '}') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid unified Lab JSON');
    }
    throw new Error('invalid unified Lab JSON');
  };
  const arrayValue = () => {
    offset += 1; whitespace();
    if (raw[offset] === ']') { offset += 1; return; }
    while (offset < raw.length) {
      value(); whitespace();
      if (raw[offset] === ']') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid unified Lab JSON');
    }
    throw new Error('invalid unified Lab JSON');
  };
  value(); whitespace();
  if (offset !== raw.length) throw new Error('invalid unified Lab JSON');
}
