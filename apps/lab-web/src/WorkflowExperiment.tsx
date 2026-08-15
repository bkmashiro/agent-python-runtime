import { useMemo, useState } from 'react';
import { Alert, Badge, Group, Text } from '@mantine/core';
import { Activity, GitCompareArrows, Link2, ShieldCheck } from 'lucide-react';
import type {
  WorkflowDecision, WorkflowEvidence, WorkflowLogicalRequest, WorkflowPhysicalExecution,
  WorkflowReport, WorkflowRun, WorkflowSpan,
} from './workflowData';

type TimelineItem = {
  id: string;
  lane: 'Model fixture' | 'Logical requests' | 'Guest WASM' | 'Host tool';
  label: string;
  started: number;
  ended: number;
  className: string;
  request?: WorkflowLogicalRequest;
};

function short(value: string): string {
  return value.length > 22 ? `${value.slice(0, 11)}…${value.slice(-7)}` : value;
}

function millis(value: number): string {
  return `${(value / 1_000_000).toFixed(2)} ms`;
}

function itemsFor(report: WorkflowReport, run: WorkflowRun): TimelineItem[] {
  const spans = report.spans.filter((span) => span.run_id === run.run_id).map((span: WorkflowSpan): TimelineItem => ({
    id: span.span_id,
    lane: span.kind.startsWith('model.') ? 'Model fixture' : span.kind === 'guest.wasm' ? 'Guest WASM' : 'Host tool',
    label: `${span.label}${span.evidence_class === 'replayed' ? ' · replayed' : ''}`,
    started: span.started_nanos,
    ended: span.ended_nanos,
    className: span.kind.replace('.', '-'),
  }));
  const requests = report.logical_requests.filter((request) => request.run_id === run.run_id).map((request): TimelineItem => ({
    id: request.logical_request_id,
    lane: 'Logical requests',
    label: short(request.logical_request_id),
    started: request.qualified_nanos,
    ended: request.completed_nanos,
    className: 'logical-request',
    request,
  }));
  return [...spans, ...requests];
}

function decisionFor(decisions: WorkflowDecision[], logicalID: string): WorkflowDecision | undefined {
  return decisions.find((decision) => decision.logical_request_ids.includes(logicalID));
}

function TreatmentTimeline({ report, treatment, selected, onSelect }: {
  report: WorkflowReport;
  treatment: 'baseline' | 'optimized';
  selected: string;
  onSelect: (id: string) => void;
}) {
  const run = report.runs.find((candidate) => candidate.treatment === treatment)!;
  const items = itemsFor(report, run);
  const lanes: TimelineItem['lane'][] = ['Model fixture', 'Logical requests', 'Guest WASM', 'Host tool'];
  const start = run.started_nanos;
  const duration = Math.max(1, run.ended_nanos - start);
  const decisions = treatment === 'optimized' ? report.decisions : [];
  return <section className="workflow-treatment" data-treatment={treatment}>
    <header><div><strong>{treatment === 'baseline' ? 'Baseline · all mechanisms off' : 'Optimized · explicit treatment'}</strong><small>{millis(duration)} measured critical path</small></div><Badge color={treatment === 'baseline' ? 'gray' : 'teal'}>{run.terminal_disposition}</Badge></header>
    <div className="workflow-axis"><span>0</span><span>monotonic time →</span><span>{millis(duration)}</span></div>
    <div className="workflow-lanes">
      {lanes.map((lane) => <div className="workflow-lane" key={lane}>
        <div className="workflow-lane-label">{lane}</div>
        <div className="workflow-lane-track">
          {items.filter((item) => item.lane === lane).map((item) => {
            const left = Math.max(0, ((item.started - start) / duration) * 100);
            const width = Math.max(0.8, ((item.ended - item.started) / duration) * 100);
            const decision = item.request ? decisionFor(decisions, item.request.logical_request_id) : undefined;
            return <button
              type="button"
              key={item.id}
              className={`workflow-span ${item.className} ${selected === item.id ? 'active' : ''} ${decision ? `decision-${decision.outcome}` : ''}`}
              style={{ left: `${left}%`, width: `${width}%` }}
              onClick={() => item.request && onSelect(item.id)}
              title={`${item.label} · ${millis(item.ended - item.started)}${decision ? ` · ${decision.kind}: ${decision.reason}` : ''}`}
            ><span>{item.label}</span>{decision && <i>{decision.outcome === 'admitted' ? decision.kind : 'rejected'}</i>}</button>;
          })}
        </div>
      </div>)}
    </div>
    <footer>{decisions.length === 0 ? <span>Ordinary fresh execution only</span> : decisions.map((decision) => <Badge key={decision.decision_id} color={decision.outcome === 'admitted' ? 'teal' : 'orange'} variant="light">{decision.outcome} · {decision.kind} · {decision.reason}</Badge>)}</footer>
  </section>;
}

function Provenance({ report, logicalID }: { report: WorkflowReport; logicalID: string }) {
  const request = report.logical_requests.find((candidate) => candidate.logical_request_id === logicalID) ?? report.logical_requests[0];
  const physical = report.physical_executions.find((candidate) => candidate.physical_execution_id === request?.physical_execution_id);
  const decision = request ? decisionFor(report.decisions, request.logical_request_id) : undefined;
  if (!request || !physical) return <Alert color="gray">No logical request selected.</Alert>;
  const producer = report.logical_requests.find((candidate) => candidate.logical_request_id === physical.producer_logical_request_id);
  return <section className="workflow-provenance">
    <header><Group gap={8}><Link2 size={16} /><Text fw={700} size="sm">Logical → physical provenance</Text></Group><Badge variant="outline">read only</Badge></header>
    <div className="provenance-chain">
      <article><small>Logical claimant</small><code>{short(request.logical_request_id)}</code><span>{short(request.workflow_node_id)} · {request.capability}</span></article>
      <b>→</b>
      <article><small>Physical producer</small><code>{short(physical.physical_execution_id)}</code><span>{short(producer?.logical_request_id ?? '')} · {physical.terminal_disposition}</span></article>
      <b>→</b>
      <article><small>Consumers</small><code>{physical.consumers.length} logical request{physical.consumers.length === 1 ? '' : 's'}</code><span>{physical.consumers.map(short).join(', ')}</span></article>
    </div>
    <div className="provenance-fields">
      <div><small>Boundary identity</small><code>{short(request.boundary_identity_sha256)}</code></div>
      <div><small>Demand → complete</small><code>{millis(request.completed_nanos - request.demanded_nanos)}</code></div>
      <div><small>Optimization decision</small><code>{decision ? `${decision.outcome}/${decision.kind}/${decision.reason}` : 'ordinary fresh execution'}</code></div>
      <div><small>Authority</small><code>Host-recorded evidence · no Lab authority</code></div>
    </div>
  </section>;
}

export function WorkflowExperiment({ evidence }: { evidence: WorkflowEvidence }) {
  const [selectedTaskID, setSelectedTaskID] = useState(evidence.manifest.tasks[0].task_id);
  const selectedIndex = evidence.manifest.tasks.findIndex((task) => task.task_id === selectedTaskID);
  const task = evidence.manifest.tasks[Math.max(0, selectedIndex)];
  const metrics = evidence.tasks[Math.max(0, selectedIndex)];
  const report = evidence.reports[Math.max(0, selectedIndex)];
  const [selectedLogical, setSelectedLogical] = useState('');
  const firstOptimizedLogical = useMemo(() => {
    const optimizedRun = report.runs.find((run) => run.treatment === 'optimized');
    return report.logical_requests.find((request) => request.run_id === optimizedRun?.run_id)?.logical_request_id ?? '';
  }, [report]);
  const activeLogical = report.logical_requests.some((request) => request.logical_request_id === selectedLogical) ? selectedLogical : firstOptimizedLogical;
  const saved = evidence.baseline_physical_executions - evidence.optimized_physical_executions;
  return <main className="workflow-experiment" data-testid="workflow-experiment">
    <section className="workflow-overview">
      <div className="workflow-title"><div className="theme-icon"><Activity size={18} /></div><div><Text fw={800}>Workflow-boundary experiment</Text><Text size="xs" c="dimmed">Seeded paired treatments · sealed portable evidence · no execution controls</Text></div></div>
      <div className="workflow-summary-cards">
        <article><small>Shuffle seed</small><strong>{evidence.manifest.seed}</strong><span>{evidence.manifest.tasks.length} prepared tasks</span></article>
        <article><small>Physical reads</small><strong>{evidence.baseline_physical_executions} → {evidence.optimized_physical_executions}</strong><span>{saved} exact executions avoided</span></article>
        <article><small>Observable divergence</small><strong>{evidence.divergences}</strong><span>output and read-only effects equivalent</span></article>
        <article><small>Privacy / authority</small><strong>sealed out</strong><span>no prompt, output body, Python source or private reasoning</span></article>
      </div>
    </section>
    <section className="workflow-arrivals" aria-label="Shuffled task arrivals">
      <header><Group gap={8}><GitCompareArrows size={15} /><Text fw={700} size="sm">Seeded submission order</Text></Group><Badge color="teal" leftSection={<ShieldCheck size={11} />}>verified complete</Badge></header>
      <div>{evidence.manifest.tasks.map((candidate) => <button type="button" key={candidate.task_id} className={candidate.task_id === task.task_id ? 'active' : ''} onClick={() => { setSelectedTaskID(candidate.task_id); setSelectedLogical(''); }}><i>{candidate.submission_order}</i><span>{candidate.class.replaceAll('_', ' ')}</span>{candidate.negative_dimension && <small>{candidate.negative_dimension} mismatch</small>}</button>)}</div>
    </section>
    <section className="workflow-task-heading">
      <div><Text fw={800}>{task.class.replaceAll('_', ' ')}</Text><Text size="xs" c="dimmed">arrival {task.submission_order} · {short(task.workload_id)} · {task.negative_dimension ? `${task.negative_dimension} near-match negative` : 'optimization-positive prepared fixture'}</Text></div>
      <div className="workflow-task-metrics"><Badge color={metrics.admitted_decisions ? 'teal' : metrics.rejected_decisions ? 'orange' : 'gray'}>{metrics.admitted_decisions ? `${metrics.admitted_decisions} admitted` : metrics.rejected_decisions ? `${metrics.rejected_decisions} rejected` : 'ordinary'}</Badge><span>physical {metrics.baseline_physical_executions} → {metrics.optimized_physical_executions}</span><span>critical path {millis(metrics.baseline_duration_nanos)} → {millis(metrics.optimized_duration_nanos)}</span></div>
    </section>
    <div className="workflow-pair">
      <TreatmentTimeline report={report} treatment="baseline" selected={activeLogical} onSelect={setSelectedLogical} />
      <TreatmentTimeline report={report} treatment="optimized" selected={activeLogical} onSelect={setSelectedLogical} />
    </div>
    <Provenance report={report} logicalID={activeLogical} />
    <footer className="workflow-evidence-note"><ShieldCheck size={14} /><span>Evidence complete. Model intervals are labelled replayed; Host tool and Guest WASM intervals are measured. Optimized-away logical requests remain visible and linked to their producer.</span><code>{short(evidence.seal_sha256)}</code></footer>
  </main>;
}
