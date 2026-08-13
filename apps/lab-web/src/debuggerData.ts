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
    });
  }

  return normalized;
}
