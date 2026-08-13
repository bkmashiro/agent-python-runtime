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
