export type LabRef = {
  kind: string;
  sha256: string;
};

export type LabSourceRange = { source_id: string; file: string; start_line: number; end_line: number };
export type LabWorkspaceChange = { path: string; kind: string; before_sha256?: string; after_sha256?: string; size?: number };

export type LabTraceEvent = {
  sequence: number;
  parent_sequence?: number | null;
  span_id: string;
  parent_span_id?: string;
  agent_id: string;
  parent_agent_id?: string;
  agent_role: string;
  started_millis: number;
  ended_millis: number;
  source?: LabSourceRange;
  workspace_id?: string;
  workspace_changes?: LabWorkspaceChange[];
  type: string;
  action: string;
  outcome: string;
  count: number;
  relative_elapsed_millis: number;
  input_sha256?: string;
  output_sha256?: string;
  checkpoint_sha256?: string;
  checkpoint_status?: string;
  terminal_disposition?: string;
};

export type LabMetrics = {
  guest_created: number;
  guest_destroyed: number;
  cache_hits: number;
  flight_followers: number;
  changed_bytes: number;
  materialized_bytes: number;
  relative_elapsed_millis: number;
};

export type LabChildProgram = { id: string; role: string; source: string; expected_result: string; output_path: string };

export type LabSemanticRegion = {
  id: string;
  kind: 'straight_line' | 'opaque_control' | 'declaration';
  span: { start_line: number; start_column: number; end_line: number; end_column: number };
  control_predecessors: string[];
  data_dependencies: Array<{ name: string; producer_region_id: string }>;
  live_ins: string[];
  live_outs: string[];
  live_ins_canonical: boolean;
  live_outs_canonical: boolean;
  effects: { may_publish: boolean; may_observe_live: boolean; may_suspend: boolean; may_be_unknown: boolean };
  capability_occurrences: string[];
  barriers: string[];
  rejection_reasons: string[];
};

export type LabSemanticRegionGraph = {
  schema_version: 'pysolate.lab-semantic-regions.v0';
  analysis_sha256: string;
  source_sha256: string;
  analyzer_sha256: string;
  source_privacy: 'private' | 'portable';
  source_available: boolean;
  source?: string;
  regions: LabSemanticRegion[];
};

export type LabScenario = {
  id: string;
  guest_source: string;
  child_programs: LabChildProgram[];
  file_count: number;
  child_analysis_count: number;
  selected_child: number;
  has_repeated_transformation: boolean;
  has_wait_boundary: boolean;
  has_observation: boolean;
};

export type LabRun = {
  run_id: string;
  workload_id: string;
  treatment: string;
  recorded_status: 'passed' | 'rejected' | 'skipped';
  terminal_disposition: string;
  refs: LabRef[];
  metrics: LabMetrics;
  scenario: LabScenario;
  trace: LabTraceEvent[];
  semantic_regions?: LabSemanticRegionGraph;
};

export type LabDataset = {
  schema_version: 'pysolate.lab-web-debugger.v4';
  report_sha256: string;
  source_commit: string;
  corpus_sha256: string;
  model: string;
  runs: LabRun[];
};

const digestRE = /^sha256:[0-9a-f]{64}$/;
const idRE = /^[a-z0-9][a-z0-9._:-]{0,127}$/;
const traceType = new Set([
  'run_start',
  'observation',
  'streaming',
  'workspace',
  'guest_lifecycle',
  'prepared',
  'wait_resume',
  'cow',
  'cache',
  'single_flight',
  'fanout',
  'cancellation',
  'oracle',
  'run_terminal',
]);
const terminalOutcomes = new Set(['ok', 'rejected', 'skipped']);

function assert(condition: boolean, message: string): void {
  if (!condition) {
    throw new Error(message);
  }
}

function assertSha256(value: string, message: string) {
  assert(digestRE.test(value), message);
}

function isFiniteNonNegative(value: number): boolean {
  return Number.isFinite(value) && value >= 0;
}

function isValidTrace(trace: LabTraceEvent[]): { kind: 'ok' | 'skip' } {
  assert(trace.length >= 2, 'run trace must be non-empty and include terminal');
  assert(trace[0].sequence === 1 && trace[0].type === 'run_start' && trace[0].action === 'run.start' && trace[0].outcome === 'started', 'invalid trace start event');
  assert(trace[0].parent_sequence == null, 'run start must not have a parent');

  const last = trace[trace.length - 1];
  assert(last.type === 'run_terminal' && last.action === 'run.terminal', 'invalid terminal event');
  const seenSpans = new Set<string>();
  for (const event of trace) {
    assert(event.sequence >= 1, 'invalid event sequence');
    assert(Number.isInteger(event.sequence), 'invalid event sequence');
    assert(Number.isInteger(event.count), 'invalid event count');
    assert(event.count >= 0, 'invalid event count');
    assert(traceType.has(event.type), 'invalid event type');
    assert(event.action.length > 0, 'invalid event action');
    assert(idRE.test(event.span_id) && idRE.test(event.agent_id) && idRE.test(event.agent_role), 'invalid agent span');
    assert(!seenSpans.has(event.span_id), 'duplicate span id');
    if (event.parent_span_id) assert(seenSpans.has(event.parent_span_id), 'missing parent span');
    seenSpans.add(event.span_id);
    assert(isFiniteNonNegative(event.started_millis) && isFiniteNonNegative(event.ended_millis) && event.ended_millis >= event.started_millis, 'invalid span timing');
    if (event.source) {
      assert(idRE.test(event.source.source_id) && event.source.file.length > 0 && event.source.start_line >= 1 && event.source.end_line >= event.source.start_line, 'invalid source range');
    }
    for (const change of event.workspace_changes ?? []) {
      assert(change.path.length > 0 && ['added', 'modified', 'deleted'].includes(change.kind), 'invalid workspace change');
    }
    assert(isFiniteNonNegative(event.relative_elapsed_millis), 'invalid elapsed time');
    if (event.type === 'run_terminal') {
      assert(terminalOutcomes.has(event.outcome), 'invalid terminal outcome');
    }
    if (event.input_sha256) {
      assertSha256(event.input_sha256, 'invalid input digest');
    }
    if (event.output_sha256) {
      assertSha256(event.output_sha256, 'invalid output digest');
    }
    if (event.checkpoint_sha256) {
      assertSha256(event.checkpoint_sha256, 'invalid checkpoint digest');
      assert(Boolean(event.checkpoint_status), 'missing checkpoint status');
    }
    if (!event.checkpoint_sha256 && event.checkpoint_status) {
      assert(false, 'invalid checkpoint metadata');
    }
  }

  const sequenceByIndex = new Set(trace.map((event) => event.sequence));
  for (const event of trace.slice(1)) {
    if (event.type !== 'run_terminal') {
      if (event.parent_sequence == null) {
        throw new Error('event missing parent sequence');
      }
      assert(sequenceByIndex.has(event.parent_sequence), 'invalid parent sequence');
      assert(event.parent_sequence < event.sequence, 'parent sequence must be earlier');
    }
    if (event.type === 'run_terminal' && event.parent_sequence != null) {
      assert(sequenceByIndex.has(event.parent_sequence), 'invalid terminal parent sequence');
    }
  }

  return last.outcome === 'ok' || last.outcome === 'rejected' || last.outcome === 'skipped' ? { kind: 'ok' } : { kind: 'skip' };
}

function sha256(value: string): string {
  const bytes = new TextEncoder().encode(value);
  const padded = new Uint8Array(Math.ceil((bytes.length + 9) / 64) * 64);
  padded.set(bytes);
  padded[bytes.length] = 0x80;
  const view = new DataView(padded.buffer);
  const bitLength = bytes.length * 8;
  view.setUint32(padded.length - 8, Math.floor(bitLength / 0x100000000));
  view.setUint32(padded.length - 4, bitLength >>> 0);
  const k = new Uint32Array([
    0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
    0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
    0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
    0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
    0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
    0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
    0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
    0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2,
  ]);
  const h = new Uint32Array([0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19]);
  const w = new Uint32Array(64);
  const ror = (x: number, n: number) => (x >>> n) | (x << (32 - n));
  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let i = 0; i < 16; i += 1) w[i] = view.getUint32(offset + i * 4);
    for (let i = 16; i < 64; i += 1) {
      const s0 = ror(w[i - 15], 7) ^ ror(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = ror(w[i - 2], 17) ^ ror(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let [a,b,c,d,e,f,g,hh] = h;
    for (let i = 0; i < 64; i += 1) {
      const s1 = ror(e, 6) ^ ror(e, 11) ^ ror(e, 25);
      const t1 = (hh + s1 + ((e & f) ^ (~e & g)) + k[i] + w[i]) >>> 0;
      const s0 = ror(a, 2) ^ ror(a, 13) ^ ror(a, 22);
      const t2 = (s0 + ((a & b) ^ (a & c) ^ (b & c))) >>> 0;
      hh=g; g=f; f=e; e=(d+t1)>>>0; d=c; c=b; b=a; a=(t1+t2)>>>0;
    }
    h[0]=(h[0]+a)>>>0; h[1]=(h[1]+b)>>>0; h[2]=(h[2]+c)>>>0; h[3]=(h[3]+d)>>>0;
    h[4]=(h[4]+e)>>>0; h[5]=(h[5]+f)>>>0; h[6]=(h[6]+g)>>>0; h[7]=(h[7]+hh)>>>0;
  }
  return `sha256:${Array.from(h, (word) => word.toString(16).padStart(8, '0')).join('')}`;
}

function validateSemanticRegions(value: LabSemanticRegionGraph, scenarioSource: string): LabSemanticRegionGraph {
  assert(value?.schema_version === 'pysolate.lab-semantic-regions.v0', 'invalid semantic region schema');
  assertSha256(value.analysis_sha256, 'invalid semantic analysis digest');
  assertSha256(value.source_sha256, 'invalid semantic source digest');
  assertSha256(value.analyzer_sha256, 'invalid semantic analyzer digest');
  assert(value.source_privacy === 'private' || value.source_privacy === 'portable', 'invalid semantic source privacy');
  assert(typeof value.source_available === 'boolean' && Array.isArray(value.regions) && value.regions.length <= 256, 'invalid semantic region envelope');
  if (value.source_available) {
    assert(value.source_privacy === 'private' && typeof value.source === 'string' && value.source.length > 0 && value.source === scenarioSource, 'invalid private semantic source');
    assert(sha256(value.source!) === value.source_sha256, 'semantic source digest mismatch');
  } else {
    assert(value.source_privacy === 'portable' && !value.source, 'portable semantic projection leaked source');
  }
  const seen = new Set<string>();
  const barrierValues = new Set(['dynamic_call','dynamic_import','eval_exec','tool_rebinding','unsupported_decorator','unknown_wasi','unsupported_control_flow']);
  const rejectionValues = new Set(['opaque_control','declaration','heap_mutation','may_raise','unknown_effect','live_in_not_canonical','live_out_not_canonical']);
  const sortedUnique = (values: string[]) => values.every((item, index) => typeof item === 'string' && item.length > 0 && (index === 0 || values[index - 1] < item));
  value.regions.forEach((region, index) => {
    assert(digestRE.test(region.id), 'invalid semantic region id');
    assert(!seen.has(region.id), 'duplicate semantic region id');
    assert(['straight_line', 'opaque_control', 'declaration'].includes(region.kind), 'invalid semantic region kind');
    assert(Number.isInteger(region.span.start_line) && Number.isInteger(region.span.end_line) && Number.isInteger(region.span.start_column) && Number.isInteger(region.span.end_column) && region.span.start_line >= 1 && region.span.start_column >= 0 && region.span.end_column >= 0 && region.span.end_line >= region.span.start_line && (region.span.end_line > region.span.start_line || region.span.end_column >= region.span.start_column), 'invalid semantic region span');
    assert(Array.isArray(region.control_predecessors) && (index === 0 ? region.control_predecessors.length === 0 : region.control_predecessors.length === 1 && region.control_predecessors[0] === value.regions[index - 1].id), 'invalid semantic control edge');
    for (const field of [region.live_ins, region.live_outs, region.capability_occurrences, region.barriers, region.rejection_reasons]) {
      assert(Array.isArray(field) && field.length <= 256 && sortedUnique(field), 'invalid semantic region list');
    }
    assert(region.capability_occurrences.every((id) => digestRE.test(id)), 'invalid semantic capability occurrence');
    assert(region.barriers.every((value) => barrierValues.has(value)), 'invalid semantic barrier');
    assert(region.rejection_reasons.every((value) => rejectionValues.has(value)), 'invalid semantic rejection');
    assert(typeof region.live_ins_canonical === 'boolean' && typeof region.live_outs_canonical === 'boolean', 'invalid semantic canonicality');
    assert(typeof region.effects?.may_publish === 'boolean' && typeof region.effects.may_observe_live === 'boolean' && typeof region.effects.may_suspend === 'boolean' && typeof region.effects.may_be_unknown === 'boolean', 'invalid semantic effects');
    assert(Array.isArray(region.data_dependencies) && region.data_dependencies.length <= 256, 'invalid semantic data edges');
    region.data_dependencies.forEach((edge, edgeIndex) => {
      assert(typeof edge.name === 'string' && region.live_ins.includes(edge.name) && digestRE.test(edge.producer_region_id) && seen.has(edge.producer_region_id), 'invalid semantic data edge');
      if (edgeIndex > 0) {
        const previous = region.data_dependencies[edgeIndex - 1];
        assert(previous.name < edge.name || previous.name === edge.name && previous.producer_region_id < edge.producer_region_id, 'non-canonical semantic data edges');
      }
    });
    seen.add(region.id);
  });
  return value;
}

export function validateDataset(value: unknown): LabDataset {
  const raw = value as Partial<LabDataset>;
  const schemaValid = raw.schema_version === 'pysolate.lab-web-debugger.v4';
  if (!schemaValid || typeof raw.report_sha256 !== 'string' || !digestRE.test(raw.report_sha256) ||
      typeof raw.source_commit !== 'string' || !/^[0-9a-f]{40}$/.test(raw.source_commit) ||
      typeof raw.corpus_sha256 !== 'string' || !digestRE.test(raw.corpus_sha256) ||
      typeof raw.model !== 'string' || !raw.model ||
      !Array.isArray(raw.runs)) {
    throw new Error('Unsupported experiment dataset');
  }

  const normalized: LabDataset = {
    schema_version: 'pysolate.lab-web-debugger.v4',
    report_sha256: raw.report_sha256,
    source_commit: raw.source_commit,
    corpus_sha256: raw.corpus_sha256,
    model: raw.model,
    runs: [],
  };

  const seenRuns = new Set<string>();
  for (const run of raw.runs) {
    assert(typeof run.run_id === 'string' && idRE.test(run.run_id), 'invalid run id');
    assert(!seenRuns.has(run.run_id), 'duplicate run id');
    seenRuns.add(run.run_id);

    assert(typeof run.workload_id === 'string' && idRE.test(run.workload_id), 'invalid workload id');
    assert(typeof run.treatment === 'string' && run.treatment.length > 0, 'invalid treatment');
    assert(typeof run.recorded_status === 'string' && (run.recorded_status === 'passed' || run.recorded_status === 'rejected' || run.recorded_status === 'skipped'), 'invalid recorded status');
    assert(typeof run.terminal_disposition === 'string' && run.terminal_disposition.length > 0, 'invalid terminal disposition');

    const scenario = run.scenario;
    assert(typeof scenario?.id === 'string' && scenario.id === run.workload_id, 'scenario/workload mismatch');
    assert(typeof scenario.guest_source === 'string' && scenario.guest_source.length >= 20 && scenario.guest_source.length <= 32_768, 'invalid Guest source');
    assert(Array.isArray(scenario.child_programs) && scenario.child_programs.length === 2, 'invalid child programs');
    for (const child of scenario.child_programs) {
      assert(idRE.test(child.id) && idRE.test(child.role) && child.source.length >= 20 && child.output_path.length > 0, 'invalid child program');
    }
    assert(typeof scenario.file_count === 'number' && Number.isInteger(scenario.file_count) && scenario.file_count >= 0, 'invalid file count');
    assert(typeof scenario.child_analysis_count === 'number' && Number.isInteger(scenario.child_analysis_count) && scenario.child_analysis_count >= 0, 'invalid child analysis count');
    assert(typeof scenario.selected_child === 'number' && Number.isInteger(scenario.selected_child), 'invalid selected child');
    assert(typeof scenario.has_repeated_transformation === 'boolean', 'invalid repeated transformation flag');
    assert(typeof scenario.has_wait_boundary === 'boolean', 'invalid wait boundary flag');
    assert(typeof scenario.has_observation === 'boolean', 'invalid observation flag');

    const refs = run.refs;
    assert(Array.isArray(refs) && refs.length >= 1, 'invalid refs');
    for (const ref of refs) {
      assert(typeof ref.kind === 'string' && ref.kind.length > 0, 'invalid ref kind');
      assert(typeof ref.sha256 === 'string' && digestRE.test(ref.sha256), 'invalid ref digest');
    }

    const metrics = run.metrics;
    assert(typeof metrics === 'object' && metrics !== null, 'invalid metrics');
    assert(metrics.guest_created >= 0 && metrics.guest_destroyed >= 0 && metrics.cache_hits >= 0 && metrics.flight_followers >= 0, 'invalid metrics');
    assert(metrics.changed_bytes >= 0 && metrics.materialized_bytes >= 0 && metrics.relative_elapsed_millis >= 0, 'invalid metrics');
    assert(metrics.guest_destroyed <= metrics.guest_created || run.recorded_status === 'skipped', 'invalid guest counts');

    assert(Array.isArray(run.trace) && run.trace.length > 0, 'run trace missing');
    const traceCheck = isValidTrace(run.trace);
    if (run.recorded_status !== 'skipped') {
      const mechanisms = run.trace.filter((event) => event.type !== 'run_start' && event.type !== 'run_terminal');
      assert(mechanisms.length > 0, 'non-skipped runs require mechanism events');
      assert(traceCheck.kind === 'ok', 'terminal state invalid');
    }

    const normalizedTrace = run.trace.map((event) => {
      assert(Number.isFinite(event.relative_elapsed_millis) && event.relative_elapsed_millis >= 0, 'invalid trace elapsed time');
      if (event.input_sha256 !== undefined && event.input_sha256 !== '') {
        assertSha256(event.input_sha256, 'invalid trace input digest');
      }
      if (event.output_sha256 !== undefined && event.output_sha256 !== '') {
        assertSha256(event.output_sha256, 'invalid trace output digest');
      }
      if (event.checkpoint_sha256 !== undefined && event.checkpoint_sha256 !== '') {
        assertSha256(event.checkpoint_sha256, 'invalid checkpoint digest');
      }
      return event;
    });

    normalized.runs.push({
      run_id: run.run_id,
      workload_id: run.workload_id,
      treatment: run.treatment,
      recorded_status: run.recorded_status,
      terminal_disposition: run.terminal_disposition,
      refs,
      metrics,
      scenario,
      trace: normalizedTrace,
      semantic_regions: run.semantic_regions ? validateSemanticRegions(run.semantic_regions, scenario.guest_source) : undefined,
    });
  }

  return normalized;
}
