import { describe, expect, it } from 'vitest';
import { validateDataset } from './Experiments';

const digest = `sha256:${'a'.repeat(64)}`;
const dataset = {
  schema_version: 'pysolate.lab-web-experiments.v1',
  report_sha256: digest,
  source_commit: 'b'.repeat(40),
  corpus_sha256: digest,
  model: 'example-model',
  study: { study_id: 'study-any', evidence_class: 'qualified_workload', workload_count: 1, treatment_count: 1, status_totals: [{ status: 'completed', count: 1 }], prohibited_claims: [] },
  runs: [{ run_id: 'run-any', workload_id: 'workload-any', treatment: 'cow', status: 'completed', oracle_status: 'passed', evidence_class: 'qualified_workload', evidence_completeness: 'complete', refs: [{ kind: 'result', sha256: digest, privacy: 'private', availability: 'unavailable' }], problem_codes: [] }],
  records: [{ run_id: 'run-any', workload_id: 'workload-any', treatment: 'cow', recorded_status: 'passed', guest_created: 1, guest_destroyed: 1, cache_hits: 0, flight_followers: 0, changed_bytes: 0, materialized_bytes: 0, relative_elapsed_millis: 1.5, terminal_disposition: 'discarded_after_single_use' }],
  scenarios: [{ id: 'workload-any', task: 'Inspect a bounded repository fixture.', files: ['a.go', 'a_test.go'], child_analyses: ['analysis A', 'analysis B'], repeated_transformation: 'normalize output', wait_boundary: 'after analysis', observation: 'source digest', selected_child: 0, expected_artifact: 'expected report', prohibited_outputs: [] }],
};

describe('generic experiment dataset', () => {
  it('accepts direct records without model or workload-specific interpretation', () => {
    expect(validateDataset(dataset).runs[0].workload_id).toBe('workload-any');
  });
  it('rejects unsupported schemas and invented statuses', () => {
    expect(() => validateDataset({ ...dataset, schema_version: 'spark-special-case' })).toThrow();
    expect(() => validateDataset({ ...dataset, runs: [{ ...dataset.runs[0], status: 'inferred_pass' }] })).toThrow();
  });
});
