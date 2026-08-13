import { useEffect, useMemo, useState } from 'react';
import { Alert, Badge, Button, Code, Group, Paper, ScrollArea, Select, Stack, Table, Text, Title } from '@mantine/core';
import { FileJson2, Upload } from 'lucide-react';

export type LabRef = { kind: string; sha256: string; privacy: string; availability: string };
export type LabRun = {
  run_id: string;
  workload_id: string;
  treatment: string;
  status: string;
  oracle_status: string;
  evidence_class: string;
  evidence_completeness: string;
  refs: LabRef[];
  problem_codes: string[];
};
export type LabRecord = {
  run_id: string;
  workload_id: string;
  treatment: string;
  recorded_status: 'passed' | 'rejected' | 'skipped';
  guest_created: number;
  guest_destroyed: number;
  cache_hits: number;
  flight_followers: number;
  changed_bytes: number;
  materialized_bytes: number;
  relative_elapsed_millis: number;
  terminal_disposition: string;
};
export type DisplayRun = LabRun & LabRecord;
export type LabScenario = {
  id: string;
  task: string;
  files: string[];
  child_analyses: string[];
  repeated_transformation: string;
  wait_boundary: string;
  observation: string;
  selected_child: number;
  expected_artifact: string;
  prohibited_outputs: string[];
};
export type LabDataset = {
  schema_version: 'pysolate.lab-web-experiments.v1';
  report_sha256: string;
  source_commit: string;
  corpus_sha256: string;
  model: string;
  study: {
    study_id: string;
    evidence_class: string;
    workload_count: number;
    treatment_count: number;
    status_totals: Array<{ status: string; count: number }>;
    prohibited_claims: string[];
  };
  runs: LabRun[];
  records: LabRecord[];
  scenarios: LabScenario[];
};

export function validateDataset(value: unknown): LabDataset {
  const data = value as Partial<LabDataset>;
  if (data?.schema_version !== 'pysolate.lab-web-experiments.v1' || !/^sha256:[0-9a-f]{64}$/.test(data.report_sha256 ?? '') || !/^[0-9a-f]{40}$/.test(data.source_commit ?? '') || !/^sha256:[0-9a-f]{64}$/.test(data.corpus_sha256 ?? '') || !data.model || !data.study || !Array.isArray(data.runs) || !Array.isArray(data.records) || !Array.isArray(data.scenarios) || data.records.length !== data.runs.length) throw new Error('Unsupported experiment dataset');
  for (const [index, run] of data.runs.entries()) {
    const record = data.records[index];
    if (!run.run_id || !run.workload_id || !run.treatment || !['completed', 'failed', 'timed_out', 'unsupported'].includes(run.status) || !['passed', 'failed', 'not_run'].includes(run.oracle_status) || !Array.isArray(run.refs) || record.run_id !== run.run_id || record.workload_id !== run.workload_id || record.treatment !== run.treatment || !['passed', 'rejected', 'skipped'].includes(record.recorded_status) || !Number.isFinite(record.relative_elapsed_millis) || record.relative_elapsed_millis < 0) throw new Error('Invalid experiment run');
  }
  return data as LabDataset;
}

function RunRefs({ run }: { run: LabRun }) {
  return <Stack gap={4}>{run.refs.map((ref) => {
    const availability = ref.kind === 'result' ? 'inspectable' : ref.kind === 'workspace_tree' ? 'identity only' : ref.availability;
    const privacy = ref.kind === 'result' ? 'public fixture' : ref.kind === 'workspace_tree' ? 'not captured' : ref.privacy;
    return <Group key={ref.kind} gap={6} wrap="nowrap"><Badge size="xs" variant="outline">{ref.kind}</Badge><Code>{ref.sha256.slice(0, 18)}…</Code><Badge size="xs" color={availability === 'inspectable' ? 'teal' : 'gray'}>{privacy} · {availability}</Badge></Group>;
  })}</Stack>;
}

export function ExperimentsView() {
  const [dataset, setDataset] = useState<LabDataset | null>(null);
  const [error, setError] = useState('');
  const [workload, setWorkload] = useState<string | null>(null);
  const [treatment, setTreatment] = useState<string | null>(null);
  const [selectedRunID, setSelectedRunID] = useState<string | null>(null);
  useEffect(() => {
    fetch('/lab-data/experiments.json').then((response) => {
      if (!response.ok) throw new Error('No experiment dataset loaded');
      return response.json();
    }).then((value) => setDataset(validateDataset(value))).catch((reason) => setError(String(reason.message ?? reason)));
  }, []);
  const loadFile = async (file?: File) => {
    if (!file) return;
    try { setDataset(validateDataset(JSON.parse(await file.text()))); setError(''); }
    catch (reason) { setError(String((reason as Error).message)); }
  };
  const workloads = useMemo(() => [...new Set(dataset?.runs.map((run) => run.workload_id) ?? [])].sort(), [dataset]);
  const treatments = useMemo(() => [...new Set(dataset?.runs.map((run) => run.treatment) ?? [])].sort(), [dataset]);
  const runs = useMemo(() => dataset?.runs.map((run, index) => ({ ...run, ...dataset.records[index] })).filter((run) => (!workload || run.workload_id === workload) && (!treatment || run.treatment === treatment)) ?? [], [dataset, workload, treatment]);
  const selectedRun = runs.find((run) => run.run_id === selectedRunID) ?? runs[0];
  return <Stack className="experiments-view" gap="md">
    <Group justify="space-between"><div><Title order={2}>Experiment debugger</Title><Text c="dimmed" size="sm">Runs, scenario inputs, outcomes, and runtime metrics.</Text></div><Button component="label" variant="light" leftSection={<Upload size={14} />}>Load JSON<input hidden type="file" accept="application/json" onChange={(event) => void loadFile(event.target.files?.[0])} /></Button></Group>
    {error && !dataset && <Alert color="gray" icon={<FileJson2 size={16} />}>{error}. Load any schema-compatible dataset; this view contains no Spark-specific interpretation.</Alert>}
    {dataset && <>
      <Paper withBorder p="md"><Group justify="space-between"><Stack gap={3}><Text fw={700}>{dataset.study.study_id}</Text><Text size="xs" c="dimmed">Model</Text><Code>{dataset.model}</Code><Text size="xs" c="dimmed">Source / corpus / report</Text><Code>{dataset.source_commit}</Code><Code>{dataset.corpus_sha256}</Code><Code>{dataset.report_sha256}</Code></Stack><Group>{dataset.study.status_totals.map((item) => <Badge key={item.status} variant="light">{item.status}: {item.count}</Badge>)}</Group></Group></Paper>
      <Group><Select clearable label="Workload" placeholder="All recorded" data={workloads} value={workload} onChange={setWorkload} /><Select clearable label="Treatment" placeholder="All recorded" data={treatments} value={treatment} onChange={setTreatment} /><Select searchable label="Run" data={runs.map((run) => ({ value: run.run_id, label: `${run.workload_id} · ${run.treatment}` }))} value={selectedRun?.run_id ?? null} onChange={setSelectedRunID} /><Badge mt={24} variant="outline">{runs.length} recorded rows</Badge></Group>
      {selectedRun && (() => { const scenario = dataset.scenarios.find((item) => item.id === selectedRun.workload_id); return scenario ? <Paper withBorder p="md" className="experiment-inspector"><Group justify="space-between"><div><Text size="xs" c="dimmed">SELECTED EXPERIMENT</Text><Title order={3}>{selectedRun.treatment}</Title></div><Group><Badge color={selectedRun.recorded_status === 'rejected' ? 'yellow' : 'teal'}>{selectedRun.recorded_status}</Badge><Badge color="teal">oracle {selectedRun.oracle_status}</Badge></Group></Group><div className="experiment-detail-grid"><Stack gap={5}><Text size="xs" c="dimmed">Task</Text><Text size="sm">{scenario.task}</Text><Text size="xs" c="dimmed">Files</Text><Code block>{scenario.files.join('\n')}</Code><Text size="xs" c="dimmed">Wait / observation</Text><Text size="sm">{scenario.wait_boundary}</Text><Text size="sm">{scenario.observation}</Text></Stack><Stack gap={5}><Text size="xs" c="dimmed">Child analyses</Text>{scenario.child_analyses.map((analysis, index) => <Paper key={analysis} withBorder p="xs"><Badge size="xs" color={index === scenario.selected_child ? 'teal' : 'gray'}>{index === scenario.selected_child ? 'selected' : 'candidate'}</Badge><Text size="sm" mt={5}>{analysis}</Text></Paper>)}</Stack><Stack gap={5}><Text size="xs" c="dimmed">Expected result</Text><Code block className="inspect-body">{scenario.expected_artifact}</Code><Text size="xs" c="dimmed">Transformation</Text><Text size="sm">{scenario.repeated_transformation}</Text><Text size="xs" c="dimmed">Run metrics</Text><Text size="sm">guest {selectedRun.guest_created}/{selectedRun.guest_destroyed} · cache {selectedRun.cache_hits} · flight {selectedRun.flight_followers} · {selectedRun.relative_elapsed_millis.toFixed(2)} ms</Text><Code>{selectedRun.terminal_disposition}</Code></Stack></div></Paper> : null; })()}
      <Paper withBorder><ScrollArea><Table striped highlightOnHover><Table.Thead><Table.Tr><Table.Th>Workload</Table.Th><Table.Th>Treatment</Table.Th><Table.Th>Recorded status</Table.Th><Table.Th>Oracle</Table.Th><Table.Th>Metrics</Table.Th><Table.Th>Disposition</Table.Th><Table.Th>Evidence</Table.Th><Table.Th>Recorded refs</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{runs.map((run) => <Table.Tr key={run.run_id} className={run.run_id === selectedRun?.run_id ? 'selected-experiment-row' : ''} onClick={() => setSelectedRunID(run.run_id)}><Table.Td><Code>{run.workload_id}</Code></Table.Td><Table.Td><Badge variant="light">{run.treatment}</Badge></Table.Td><Table.Td><Badge color={run.recorded_status === 'passed' ? 'teal' : run.recorded_status === 'rejected' ? 'yellow' : 'gray'}>{run.recorded_status}</Badge></Table.Td><Table.Td><Badge color={run.oracle_status === 'passed' ? 'teal' : run.oracle_status === 'not_run' ? 'gray' : 'red'}>{run.oracle_status}</Badge></Table.Td><Table.Td><Text size="xs">guest {run.guest_created}/{run.guest_destroyed}</Text><Text size="xs">cache {run.cache_hits} · flight {run.flight_followers}</Text><Text size="xs">bytes Δ{run.changed_bytes} / mat {run.materialized_bytes}</Text><Text size="xs">{run.relative_elapsed_millis.toFixed(2)} ms</Text></Table.Td><Table.Td><Code>{run.terminal_disposition}</Code></Table.Td><Table.Td>{run.evidence_class}<br/><Text size="xs" c="dimmed">{run.evidence_completeness}</Text></Table.Td><Table.Td><RunRefs run={run} /></Table.Td></Table.Tr>)}</Table.Tbody></Table></ScrollArea></Paper>
    </>}
  </Stack>;
}
