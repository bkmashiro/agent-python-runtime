export type DemoStatus = 'optimized' | 'safety_control';
export type SegmentTone = 'generation' | 'effect' | 'finalize' | 'shared' | 'physical' | 'fallback';

export interface LatestMetric {
  label: string;
  value: string;
  note: string;
  tone: 'baseline' | 'optimized' | 'win' | 'control';
}

export interface LatestSegment {
  label: string;
  start_ns: number;
  end_ns: number;
  tone: SegmentTone;
}

export interface LatestLane {
  label: string;
  duration_ns: number;
  segments: LatestSegment[];
}

export interface LatestDemo {
  id: 'source-prefix-overlap' | 'exact-request-sharing' | 'source-mismatch-fallback';
  title: string;
  eyebrow: string;
  status: DemoStatus;
  summary: string;
  source: string;
  annotations: {
    start_line: number;
    end_line: number;
    tone: 'effect_trigger' | 'overlapped_tail' | 'physical_owner' | 'shared_skip' | 'fresh_fallback';
    label: string;
    note: string;
  }[];
  metrics: LatestMetric[];
  lanes: LatestLane[];
  facts: { label: string; value: string }[];
  claim_boundary: string;
}

export interface LatestSnapshot {
  schema_version: 'pysolate.lab-latest.v1';
  identity: string;
  headline: { real_guest_demos: number; optimization_wins: number; safety_controls: number };
  demos: LatestDemo[];
  boundary: {
    events: number;
    unique_sources: number;
    structurally_eligible: number;
    timing_not_recorded: number;
    performance_supported: boolean;
    decision: string;
  };
  provenance: {
    source_prefix_evidence_sha256: string;
    census_evidence_sha256: string;
    campaign_projection_sha256: string;
    source_prefix_artifact_sha256: string;
    campaign_artifact_sha256: string;
    source_prefix_harness_commit: string;
    campaign_source_commit: string;
  };
}

const digest = /^sha256:[0-9a-f]{64}$/;
const commit = /^[0-9a-f]{40}$/;
const demoIDs = ['source-prefix-overlap', 'exact-request-sharing', 'source-mismatch-fallback'];
const tones = new Set(['generation', 'effect', 'finalize', 'shared', 'physical', 'fallback']);
const metricTones = new Set(['baseline', 'optimized', 'win', 'control']);
const annotationTones = new Set(['effect_trigger', 'overlapped_tail', 'physical_owner', 'shared_skip', 'fresh_fallback']);

function object(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, keys: string[], label: string) {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} contains unknown or missing fields`);
}

function nonempty(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

export function validateLatestSnapshot(value: unknown): LatestSnapshot {
  const root = object(value, 'latest Lab snapshot');
  exactKeys(root, ['schema_version', 'identity', 'headline', 'demos', 'boundary', 'provenance'], 'latest Lab snapshot');
  if (root.schema_version !== 'pysolate.lab-latest.v1' || !digest.test(String(root.identity))) throw new Error('latest Lab identity is invalid');

  const headline = object(root.headline, 'headline');
  exactKeys(headline, ['real_guest_demos', 'optimization_wins', 'safety_controls'], 'headline');
  if (headline.real_guest_demos !== 3 || headline.optimization_wins !== 2 || headline.safety_controls !== 1) throw new Error('latest Lab headline is invalid');

  if (!Array.isArray(root.demos) || root.demos.length !== 3) throw new Error('latest Lab requires exactly three demos');
  for (const [index, rawDemo] of root.demos.entries()) {
    const demo = object(rawDemo, 'demo');
    exactKeys(demo, ['id', 'title', 'eyebrow', 'status', 'summary', 'source', 'annotations', 'metrics', 'lanes', 'facts', 'claim_boundary'], 'demo');
    if (demo.id !== demoIDs[index] || !nonempty(demo.title) || !nonempty(demo.eyebrow) || !nonempty(demo.summary) || !nonempty(demo.source) || !nonempty(demo.claim_boundary) || (demo.status !== 'optimized' && demo.status !== 'safety_control') || String(demo.source).includes('/Users/') || String(demo.source).includes('.hermes')) throw new Error('latest Lab demo is invalid');
    if (!Array.isArray(demo.metrics) || demo.metrics.length < 3) throw new Error('latest Lab metrics are incomplete');
    const sourceLineCount = String(demo.source).split('\n').length;
    const annotatedLines = new Set<number>();
    if (!Array.isArray(demo.annotations) || demo.annotations.length === 0) throw new Error('latest Lab annotations are incomplete');
    for (const rawAnnotation of demo.annotations) {
      const annotation = object(rawAnnotation, 'annotation');
      exactKeys(annotation, ['start_line', 'end_line', 'tone', 'label', 'note'], 'annotation');
      if (!Number.isInteger(annotation.start_line) || !Number.isInteger(annotation.end_line) || Number(annotation.start_line) < 1 || Number(annotation.end_line) < Number(annotation.start_line) || Number(annotation.end_line) > sourceLineCount || !annotationTones.has(String(annotation.tone)) || !nonempty(annotation.label) || !nonempty(annotation.note)) throw new Error('latest Lab annotation is invalid');
      for (let line = Number(annotation.start_line); line <= Number(annotation.end_line); line += 1) {
        if (annotatedLines.has(line)) throw new Error('latest Lab annotations overlap');
        annotatedLines.add(line);
      }
    }
    for (const rawMetric of demo.metrics) {
      const metric = object(rawMetric, 'metric');
      exactKeys(metric, ['label', 'value', 'note', 'tone'], 'metric');
      if (!nonempty(metric.label) || !nonempty(metric.value) || !nonempty(metric.note) || !metricTones.has(String(metric.tone))) throw new Error('latest Lab metric is invalid');
    }
    if (!Array.isArray(demo.lanes) || demo.lanes.length === 0) throw new Error('latest Lab lanes are incomplete');
    for (const rawLane of demo.lanes) {
      const lane = object(rawLane, 'lane');
      exactKeys(lane, ['label', 'duration_ns', 'segments'], 'lane');
      if (!nonempty(lane.label) || !Number.isFinite(lane.duration_ns) || Number(lane.duration_ns) <= 0 || !Array.isArray(lane.segments) || lane.segments.length === 0) throw new Error('latest Lab lane is invalid');
      for (const rawSegment of lane.segments) {
        const segment = object(rawSegment, 'segment');
        exactKeys(segment, ['label', 'start_ns', 'end_ns', 'tone'], 'segment');
        if (!nonempty(segment.label) || !tones.has(String(segment.tone)) || !Number.isFinite(segment.start_ns) || !Number.isFinite(segment.end_ns) || Number(segment.start_ns) < 0 || Number(segment.end_ns) <= Number(segment.start_ns) || Number(segment.end_ns) > Number(lane.duration_ns)) throw new Error('latest Lab segment is invalid');
      }
    }
    if (!Array.isArray(demo.facts) || demo.facts.length === 0 || demo.facts.some((rawFact) => {
      const fact = object(rawFact, 'fact');
      exactKeys(fact, ['label', 'value'], 'fact');
      return !nonempty(fact.label) || !nonempty(fact.value);
    })) throw new Error('latest Lab facts are invalid');
  }

  const boundary = object(root.boundary, 'boundary');
  exactKeys(boundary, ['events', 'unique_sources', 'structurally_eligible', 'timing_not_recorded', 'performance_supported', 'decision'], 'boundary');
  if (boundary.events !== 36 || !Number.isInteger(boundary.unique_sources) || Number(boundary.unique_sources) <= 0 || boundary.structurally_eligible !== 0 || boundary.timing_not_recorded !== 36 || boundary.performance_supported !== false || !nonempty(boundary.decision)) throw new Error('latest Lab boundary is invalid');

  const provenance = object(root.provenance, 'provenance');
  exactKeys(provenance, ['source_prefix_evidence_sha256', 'census_evidence_sha256', 'campaign_projection_sha256', 'source_prefix_artifact_sha256', 'campaign_artifact_sha256', 'source_prefix_harness_commit', 'campaign_source_commit'], 'provenance');
  for (const key of ['source_prefix_evidence_sha256', 'census_evidence_sha256', 'campaign_projection_sha256', 'source_prefix_artifact_sha256', 'campaign_artifact_sha256']) {
    if (!digest.test(String(provenance[key]))) throw new Error('latest Lab provenance digest is invalid');
  }
  if (!commit.test(String(provenance.source_prefix_harness_commit)) || !commit.test(String(provenance.campaign_source_commit))) throw new Error('latest Lab provenance commit is invalid');
  return value as LatestSnapshot;
}

export async function loadLatestSnapshot(url = '/lab-data/latest.json'): Promise<LatestSnapshot> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`latest Lab snapshot load failed (${response.status})`);
  return validateLatestSnapshot(await response.json());
}
