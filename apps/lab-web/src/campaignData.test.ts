import { describe, expect, it } from 'vitest';
import { validateCampaignProjection } from './campaignData';

const digest = `sha256:${'a'.repeat(64)}`;

function projection() {
  const metric = { median: 1, min: 1, max: 1 };
  return {
    schema_version: 'pysolate.transparent-campaign-public-projection.v1',
    source: { artifact_sha256: digest, artifact_source_commit: 'a'.repeat(40), campaign_source_commit: 'b'.repeat(40), manifest_sha256: digest, host: { goos: 'darwin', goarch: 'arm64', go_version: 'go1', kernel: 'Darwin' }, repetitions: 1 },
    baseline: { physical_executions: metric, wall_ms: metric, process_cpu_ms: metric },
    qualified: { physical_executions: metric, wall_ms: metric, process_cpu_ms: metric },
    paired: { physical_reduction: metric, wall_reduction_ms: metric, cpu_reduction_ms: metric },
    runs: [
      { repetition: 0, treatment: 'baseline', physical_executions: 19, wall_ms: 20, process_cpu_ms: 60 },
      { repetition: 0, treatment: 'qualified', physical_executions: 17, wall_ms: 18, process_cpu_ms: 55 },
    ],
    programs: Array.from({ length: 20 }, (_, index) => ({ id: `P${String(index + 1).padStart(2, '0')}`, family: 'authority_bifurcation', release_offset_ms: index, plan_sha256: digest, grant_set_sha256: digest, privacy_partition: 'private-a', workspace_fixture_sha256: digest, execution: { kind: 'execute_python', cancel_point: 'none' }, admission: 'admitted', sharing: 'independent', disposition: 'complete' })),
    walkthrough_events: Array.from({ length: 20 }, (_, index) => [
      { sequence: index * 2 + 1, program_id: `P${String(index + 1).padStart(2, '0')}`, type: 'logical.released', at_ns: index * 2, reason: 'manifest_offset' },
      { sequence: index * 2 + 2, program_id: `P${String(index + 1).padStart(2, '0')}`, type: 'logical.terminal', at_ns: index * 2 + 1, reason: 'complete', physical_execution_id: `campaign-guest-${index + 1}` },
    ]).flat(),
    valid_claim: 'For this fixed 20-program campaign on one recorded host, exact qualified sharing reduced physical executions while preserving every registered oracle and authority rejection.', invalid_inference: 'Do not generalize these five paired repetitions to arbitrary workloads, hosts, schedulers, or steady-state production throughput.',
  };
}

describe('campaign projection', () => {
  it('accepts the bounded public projection', () => {
    expect(validateCampaignProjection(projection()).programs).toHaveLength(20);
  });

  it('rejects noncanonical IDs and body-bearing event reasons', () => {
    const wrongID = projection();
    wrongID.programs[19].id = 'P21';
    expect(() => validateCampaignProjection(wrongID)).toThrow();
    const allowedButWrong = projection();
    allowedButWrong.walkthrough_events[1].reason = 'cancelled';
    expect(() => validateCampaignProjection(allowedButWrong)).toThrow();
    const fields = [
      (value: ReturnType<typeof projection>) => { value.programs[0].family = 'private body'; },
      (value: ReturnType<typeof projection>) => { value.programs[0].privacy_partition = 'private body'; },
      (value: ReturnType<typeof projection>) => { value.programs[0].execution.cancel_point = 'private body'; },
      (value: ReturnType<typeof projection>) => { value.valid_claim = 'private body'; },
    ];
    for (const mutate of fields) {
      const value = projection(); mutate(value);
      expect(() => validateCampaignProjection(value)).toThrow();
    }
    const body = projection();
    body.walkthrough_events[0].reason = 'private body';
    expect(() => validateCampaignProjection(body)).toThrow();
  });

  it('rejects invalid physical counts', () => {
    const negative = projection();
    negative.runs[0].physical_executions = -1;
    expect(() => validateCampaignProjection(negative)).toThrow();
    const fractional = projection();
    fractional.runs[0].physical_executions = 1.5;
    expect(() => validateCampaignProjection(fractional)).toThrow();
  });

  it('rejects missing workloads and forged digests', () => {
    const missing = projection();
    missing.programs.pop();
    expect(() => validateCampaignProjection(missing)).toThrow();
    const forged = projection();
    forged.source.artifact_sha256 = 'sha256:forged';
    expect(() => validateCampaignProjection(forged)).toThrow();
  });
});
