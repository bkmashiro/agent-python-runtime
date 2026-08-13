import { describe, expect, it } from 'vitest';
import { validateDataset } from './debuggerData';

const digest = `sha256:${'a'.repeat(64)}`;
const validTrace = [
  {
    sequence: 1,
    parent_sequence: null,
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
  schema_version: 'pysolate.lab-web-debugger.v3',
  report_sha256: digest,
  source_commit: 'b'.repeat(40),
  corpus_sha256: digest,
  model: 'example-model',
  runs: [baseRun],
};

describe('debugger dataset validation', () => {
  it('accepts the v3 per-run schema with complete Guest source and trace', () => {
    const dataset = validateDataset(JSON.parse(JSON.stringify(baseDataset)));
    expect(dataset.runs).toHaveLength(1);
    expect(dataset.runs[0].run_id).toBe('run-workload-one-fresh');
    expect(dataset.runs[0].trace).toHaveLength(3);
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
