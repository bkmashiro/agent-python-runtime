import { describe, expect, it } from 'vitest';
import { validateDataset } from './debuggerData';

const digest = `sha256:${'a'.repeat(64)}`;
const validTrace = [
  {
    sequence: 1,
    parent_sequence: null,
    span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
    type: 'run_start',
    action: 'run.start',
    outcome: 'started',
    count: 0,
    relative_elapsed_millis: 0,
    input_sha256: '',
    output_sha256: digest,
    checkpoint_sha256: digest,
    checkpoint_status: 'captured',
  },
  {
    sequence: 2,
    parent_sequence: 1,
    span_id: 'workspace', parent_span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
    type: 'workspace',
    action: 'run.mechanism',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.2,
    input_sha256: digest,
    output_sha256: digest,
    checkpoint_sha256: digest,
    checkpoint_status: 'captured',
  },
  {
    sequence: 3,
    parent_sequence: 2,
    span_id: 'terminal', parent_span_id: 'workspace', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
    type: 'run_terminal',
    action: 'run.terminal',
    outcome: 'ok',
    count: 1,
    relative_elapsed_millis: 0.5,
    input_sha256: digest,
    output_sha256: digest,
    checkpoint_sha256: digest,
    checkpoint_status: 'captured',
    terminal_disposition: 'closed',
  },
];

const scenario = {
  id: 'workload-one',
  guest_source: 'values = [3, 1, 2]\nresult = sorted(values)',
  child_programs: [
    { id: 'researcher', role: 'researcher', source: "result = 'research result'", expected_result: 'research result', output_path: 'researcher.txt' },
    { id: 'reviewer', role: 'reviewer', source: "result = 'review result'", expected_result: 'review result', output_path: 'reviewer.txt' },
  ],
  file_count: 2,
  child_analysis_count: 2,
  selected_child: 0,
  has_repeated_transformation: true,
  has_wait_boundary: true,
  has_observation: true,
};

const refs = [
  { kind: 'artifact', sha256: digest },
  { kind: 'capability_plan', sha256: digest },
  { kind: 'execution', sha256: digest },
  { kind: 'execution_profile', sha256: digest },
  { kind: 'invocation', sha256: digest },
  { kind: 'result', sha256: digest },
  { kind: 'workspace_tree', sha256: digest },
];

const metrics = {
  guest_created: 1,
  guest_destroyed: 1,
  cache_hits: 0,
  flight_followers: 0,
  changed_bytes: 0,
  materialized_bytes: 0,
  relative_elapsed_millis: 5,
};

const baseRun = {
  run_id: 'run-workload-one-fresh',
  workload_id: 'workload-one',
  treatment: 'fresh',
  recorded_status: 'passed' as const,
  terminal_disposition: 'closed',
  refs,
  metrics,
  scenario,
  trace: validTrace,
};

const baseDataset = {
  schema_version: 'pysolate.lab-web-debugger.v4',
  report_sha256: digest,
  source_commit: 'b'.repeat(40),
  corpus_sha256: digest,
  model: 'example-model',
  runs: [baseRun],
};

describe('debugger dataset validation', () => {
  it('accepts the v4 per-run schema with agent spans, Guest source, and trace', () => {
    const dataset = validateDataset(JSON.parse(JSON.stringify(baseDataset)));
    expect(dataset.runs).toHaveLength(1);
    expect(dataset.runs[0].run_id).toBe('run-workload-one-fresh');
    expect(dataset.runs[0].trace).toHaveLength(3);
  });

  it('accepts a Host-projected semantic region graph and rejects forged graph edges', () => {
    const withRegions = JSON.parse(JSON.stringify(baseDataset));
    withRegions.runs[0].semantic_regions = {
      schema_version: 'pysolate.lab-semantic-regions.v0',
      analysis_sha256: digest,
      source_sha256: 'sha256:a98e4a70c088db8e63c375d93a2cd8e7f44cd9040faeb149699ae1eabfbc185d',
      analyzer_sha256: digest,
      source_privacy: 'private',
      source_available: true,
      source: scenario.guest_source,
      regions: [
        {
          id: digest,
          kind: 'straight_line',
          span: { start_line: 1, start_column: 0, end_line: 1, end_column: 18 },
          control_predecessors: [], data_dependencies: [], live_ins: [], live_outs: ['values'],
          live_ins_canonical: true, live_outs_canonical: true,
          effects: { may_publish: false, may_observe_live: false, may_suspend: false, may_be_unknown: false },
          capability_occurrences: [], barriers: [], rejection_reasons: [],
        },
        {
          id: `sha256:${'c'.repeat(64)}`,
          kind: 'straight_line',
          span: { start_line: 2, start_column: 0, end_line: 2, end_column: 23 },
          control_predecessors: [digest], data_dependencies: [{ name: 'values', producer_region_id: digest }],
          live_ins: ['values'], live_outs: ['result'], live_ins_canonical: true, live_outs_canonical: true,
          effects: { may_publish: false, may_observe_live: false, may_suspend: false, may_be_unknown: false },
          capability_occurrences: [], barriers: [], rejection_reasons: [],
        },
      ],
    };
    expect(validateDataset(withRegions).runs[0].semantic_regions?.regions).toHaveLength(2);
    const digestMismatch = JSON.parse(JSON.stringify(withRegions));
    digestMismatch.runs[0].semantic_regions.source_sha256 = digest;
    expect(() => validateDataset(digestMismatch)).toThrow(/source digest/i);
    const portable = JSON.parse(JSON.stringify(withRegions));
    portable.runs[0].semantic_regions.source_available = false;
    portable.runs[0].semantic_regions.source_privacy = 'portable';
    delete portable.runs[0].semantic_regions.source;
    expect(validateDataset(portable).runs[0].semantic_regions?.source_available).toBe(false);
    const oversized = JSON.parse(JSON.stringify(withRegions));
    oversized.runs[0].semantic_regions.regions[0].live_ins = Array.from({ length: 257 }, (_, index) => `name_${String(index).padStart(3, '0')}`);
    expect(() => validateDataset(oversized)).toThrow(/invalid semantic region list/);
    const emptyName = JSON.parse(JSON.stringify(withRegions));
    emptyName.runs[0].semantic_regions.regions[0].live_ins = [''];
    expect(() => validateDataset(emptyName)).toThrow(/invalid semantic region list/);
    withRegions.runs[0].semantic_regions.regions[1].control_predecessors = [`sha256:${'d'.repeat(64)}`];
    expect(() => validateDataset(withRegions)).toThrow(/control edge/i);
  });

  it('rejects v1 fixture layout', () => {
    const legacy = {
      schema_version: 'pysolate.lab-web-experiments.v1',
      report_sha256: digest,
      source_commit: 'b'.repeat(40),
      corpus_sha256: digest,
      model: 'example-model',
      study: {},
      runs: [],
      records: [],
      scenarios: [],
    };
    expect(() => validateDataset(legacy)).toThrow(/Unsupported/);
  });

  it('rejects non-skipped runs without mechanism events', () => {
    const invalid = JSON.parse(JSON.stringify(baseDataset));
    invalid.runs[0].trace = [
      validTrace[0],
      {
        ...validTrace[2],
        parent_sequence: 1,
    span_id: 'workspace', parent_span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
      },
    ];
    expect(() => validateDataset(invalid)).toThrow(/mechanism/i);
  });

  it('allows skipped runs with start + terminal only', () => {
    const skipped = JSON.parse(JSON.stringify(baseDataset));
    skipped.runs[0].recorded_status = 'skipped';
    skipped.runs[0].terminal_disposition = 'closed';
    skipped.runs[0].trace = [
      validTrace[0],
      {
        ...validTrace[2],
        action: 'run.terminal',
        sequence: 2,
        parent_sequence: 1,
    span_id: 'workspace', parent_span_id: 'run', agent_id: 'runtime', agent_role: 'host-runtime', started_millis: 0, ended_millis: 0,
        outcome: 'skipped',
      },
    ];
    expect(() => validateDataset(skipped)).not.toThrow();
  });

  it('accepts runs in any order', () => {
    const alt = JSON.parse(JSON.stringify(baseDataset));
    alt.runs = [
      {
        ...baseRun,
        run_id: 'run-workload-two',
        workload_id: 'workload-two',
        treatment: 'streaming',
        scenario: {
          ...scenario,
          id: 'workload-two',
        },
      },
      baseRun,
    ];
    expect(validateDataset(alt).runs).toHaveLength(2);
  });

  it('rejects duplicate run ids', () => {
    const duplicate = JSON.parse(JSON.stringify(baseDataset));
    duplicate.runs = [baseRun, { ...baseRun, run_id: baseRun.run_id }];
    expect(() => validateDataset(duplicate)).toThrow(/duplicate run id/i);
  });
});
