import { useMemo, useState } from 'react';
import type { TaskEvent, TaskOutput, TaskSnapshot, TaskSource } from './taskData';

type InspectorTab = 'source' | 'io' | 'event' | 'workspace' | 'context';

const actionLabels: Record<string, string> = {
  'run.start': 'Start research run',
  'workspace.create': 'Create private workspace',
  'guest.prepare': 'Prepare fresh Guest',
  'guest.python': 'Execute agent Python',
  'guest.finalize': 'Finalize Guest result',
  'workspace.commit': 'Commit workspace attempt',
  'workspace.discard': 'Discard workspace attempt',
  'workspace.snapshot': 'Capture workspace checkpoint',
  'agent.execute': 'Run child agent',
  'singleflight.leader': 'Open shared execution',
  'singleflight.wait': 'Attach concurrent waiter',
  'singleflight.release': 'Release shared result',
  'retention.store': 'Retain exact result',
  'retention.reuse': 'Reuse retained result',
  'run.wait': 'Wait for stable observation',
  'run.release': 'Release waiting run',
  'run.resume': 'Resume in a fresh Guest',
  'run.terminal': 'Close research run',
};

function outputForEvent(task: TaskSnapshot, event: TaskEvent): TaskOutput | undefined {
  return task.outputs.find((output) => output.event_sequence === event.sequence);
}

function dispositionLabel(output: TaskOutput): string {
  if (output.disposition === 'selected_branch') return 'selected branch';
  if (output.disposition === 'discarded_branch') return 'discarded branch';
  return 'workflow result';
}

function visibleEvents(task: TaskSnapshot): TaskEvent[] {
  const outputSequences = new Set(task.outputs.map((output) => output.event_sequence));
  return task.events.filter((event) => event.type !== 'oracle' || outputSequences.has(event.sequence));
}

function eventTitle(task: TaskSnapshot, event: TaskEvent): string {
  const output = outputForEvent(task, event);
  if (output) return output.label;
  if (event.action === 'agent.execute') {
    const source = task.sources.find((item) => item.id === event.agent_id);
    return source ? `${source.role}: ${actionLabels[event.action]}` : actionLabels[event.action];
  }
  return actionLabels[event.action] ?? event.action.replaceAll('.', ' ');
}

function eventSummary(event: TaskEvent): string {
  if (event.workspace_changes?.length) return event.workspace_changes.map((change) => `${change.kind} ${change.path}`).join(' · ');
  if (event.parent_agent_id) return `${event.parent_agent_id} → ${event.agent_id}`;
  if (event.source) return `${event.source.file}:${event.source.start_line}-${event.source.end_line}`;
  return `${event.agent_role} · ${event.outcome}`;
}

function traceDepths(task: TaskSnapshot): Map<string, number> {
  const depths = new Map<string, number>();
  for (const event of task.events) depths.set(event.span_id, event.parent_span_id ? (depths.get(event.parent_span_id) ?? 0) + 1 : 0);
  return depths;
}

function sourceForEvent(task: TaskSnapshot, event: TaskEvent): TaskSource | undefined {
  if (event.source) return task.sources.find((source) => source.id === event.source?.source_id);
  if (event.agent_id !== 'runtime') return task.sources.find((source) => source.id === event.agent_id);
  return undefined;
}

function SourceInspector({ task, event }: { task: TaskSnapshot; event: TaskEvent }) {
  const source = sourceForEvent(task, event);
  if (!source) return <div className="inspector-empty"><strong>Host-owned operation</strong><p>This event has no Python source span. Select a Guest or child-agent event to inspect its program.</p></div>;
  const lines = source.source.split('\n');
  return (
    <div className="debug-source" aria-label="Selected Python source">
      <div className="debug-filebar"><span>{source.file}</span><span>{source.role}</span></div>
      <pre>{lines.map((line, index) => {
        const lineNumber = index + 1;
        const active = event.source && lineNumber >= event.source.start_line && lineNumber <= event.source.end_line;
        return <code className={active ? 'active' : ''} key={lineNumber}><i>{lineNumber}</i><span>{line || ' '}</span></code>;
      })}</pre>
    </div>
  );
}

function InputOutputInspector({ task, event }: { task: TaskSnapshot; event: TaskEvent }) {
  const output = outputForEvent(task, event);
  const sourceIndex = task.sources.findIndex((source) => source.id === event.agent_id) - 1;
  const input = event.agent_id === 'orchestrator'
    ? task.task
    : sourceIndex >= 0 ? task.context.analyses[sourceIndex] : event.action;
  return (
    <div className="io-inspector">
      <section><header>Agent input</header><pre>{input}</pre></section>
      <section className={output ? 'has-body' : ''}>
        <header>{output ? output.label : 'Recorded output'}</header>
        <pre>{output?.body ?? 'No text body belongs to this Host event.'}</pre>
        {output && <footer>{dispositionLabel(output)}{output.path ? ` · workspace attempt/${output.path}` : ''}</footer>}
      </section>
    </div>
  );
}

function EventInspector({ event }: { event: TaskEvent }) {
  return (
    <dl className="event-inspector">
      <div><dt>Sequence</dt><dd>{event.sequence}</dd></div>
      <div><dt>Actor</dt><dd>{event.agent_id}</dd></div>
      <div><dt>Role</dt><dd>{event.agent_role}</dd></div>
      <div><dt>Action</dt><dd>{event.action}</dd></div>
      <div><dt>Outcome</dt><dd>{event.outcome}</dd></div>
      <div><dt>Span</dt><dd>{event.span_id}</dd></div>
      <div><dt>Parent span</dt><dd>{event.parent_span_id ?? 'root'}</dd></div>
      <div><dt>Started</dt><dd>{event.started_millis.toFixed(1)} ms</dd></div>
      <div><dt>Ended</dt><dd>{event.ended_millis.toFixed(1)} ms</dd></div>
      <div><dt>Elapsed</dt><dd>{event.relative_elapsed_millis.toFixed(1)} ms</dd></div>
    </dl>
  );
}

function WorkspaceInspector({ task, event }: { task: TaskSnapshot; event: TaskEvent }) {
  const selected = outputForEvent(task, event);
  const artifacts = task.outputs.filter((output) => output.path);
  return (
    <div className="workspace-inspector">
      <div className="workspace-file-list">
        {artifacts.map((output) => <div className={output.path === selected?.path ? 'selected' : ''} key={output.path}><span>{output.path}</span><small>{dispositionLabel(output)} · {output.agent_id}</small></div>)}
      </div>
      <pre>{selected?.path ? selected.body : 'Select an agent output event to preview its workspace artifact.'}</pre>
    </div>
  );
}

function ContextInspector({ task }: { task: TaskSnapshot }) {
  const workflow = task.outputs.find((output) => output.disposition === 'workflow_result');
  const selected = task.outputs.find((output) => output.disposition === 'selected_branch');
  const discarded = task.outputs.find((output) => output.disposition === 'discarded_branch');
  return (
    <div className="context-inspector">
      <section><span>Files under review</span>{task.context.files.map((file) => <code key={file}>{file}</code>)}</section>
      <section><span>Parallel analyses</span>{task.context.analyses.map((analysis) => <p key={analysis}>{analysis}</p>)}</section>
      <section><span>Provider I/O</span><p>Not applicable — deterministic scripted Guest fixture</p></section>
      <section><span>Branch disposition</span><p>Selected: {selected?.label ?? 'none'}</p><p>Discarded: {discarded?.label ?? 'none'}</p></section>
      <section><span>Repeated transformation</span><p>{task.context.repeated_transformation}</p></section>
      <section><span>Wait boundary</span><p>{task.context.wait_boundary}</p></section>
      <section><span>Stable observation</span><p>{task.context.observation}</p></section>
      <section><span>Observed workflow output</span><pre>{workflow?.body ?? 'No observed workflow body recorded.'}</pre></section>
    </div>
  );
}

export default function TaskInspector({ task }: { task: TaskSnapshot }) {
  const events = useMemo(() => visibleEvents(task), [task]);
  const depths = useMemo(() => traceDepths(task), [task]);
  const initial = events.find((event) => outputForEvent(task, event)) ?? events[0];
  const [selected, setSelected] = useState(initial);
  const [tab, setTab] = useState<InspectorTab>('io');
  return (
    <section className="debugger-workbench" aria-labelledby="debugger-title">
      <header className="debugger-heading">
        <div><span>SCRIPTED GUEST · FULL RUNTIME RECORDING</span><h1 id="debugger-title">{task.title}</h1><p>{task.task}</p></div>
        <div className="recording-badge"><strong>{events.length}</strong><span>visible events</span></div>
      </header>
      <div className="debugger-grid">
        <aside className="execution-list" aria-label="Execution trace">
          <header><strong>Execution trace</strong><span>select an operation</span></header>
          <div>
            {events.map((event) => (
              <button className={selected.sequence === event.sequence ? 'selected' : ''} key={event.sequence} onClick={() => setSelected(event)} style={{ paddingLeft: 14 + (depths.get(event.span_id) ?? 0) * 14 }} type="button">
                <i>{String(event.sequence).padStart(2, '0')}</i>
                <span><strong>{eventTitle(task, event)}</strong><small>{eventSummary(event)}</small></span>
                <time>{event.relative_elapsed_millis.toFixed(0)} ms</time>
              </button>
            ))}
          </div>
        </aside>
        <article className="operation-inspector" aria-label="Selected operation inspector">
          <header className="operation-heading"><div><span>SELECTED OPERATION</span><h2>{eventTitle(task, selected)}</h2><p>{eventSummary(selected)}</p></div><time>{selected.relative_elapsed_millis.toFixed(1)} ms</time></header>
          <nav aria-label="Inspector tabs">
            {([['source', 'Python source'], ['io', 'Input / output'], ['event', 'Event'], ['workspace', 'Workspace'], ['context', 'Task context']] as [InspectorTab, string][]).map(([value, label]) => <button aria-selected={tab === value} className={tab === value ? 'active' : ''} key={value} onClick={() => setTab(value)} role="tab" type="button">{label}</button>)}
          </nav>
          <div className="inspector-body">
            {tab === 'source' && <SourceInspector event={selected} task={task} />}
            {tab === 'io' && <InputOutputInspector event={selected} task={task} />}
            {tab === 'event' && <EventInspector event={selected} />}
            {tab === 'workspace' && <WorkspaceInspector event={selected} task={task} />}
            {tab === 'context' && <ContextInspector task={task} />}
          </div>
        </article>
      </div>
    </section>
  );
}

export function TaskTimeline({ task }: { task: TaskSnapshot }) {
  const events = useMemo(() => visibleEvents(task), [task]);
  const agents = ['runtime', ...task.sources.map((source) => source.id)];
  const duration = Math.max(task.stats.duration_millis, 1);
  const [selected, setSelected] = useState(events.find((event) => outputForEvent(task, event)) ?? events[0]);
  const selectedOutput = outputForEvent(task, selected);
  return (
    <section className="timeline-workbench" aria-labelledby="timeline-title">
      <header className="debugger-heading"><div><span>HOST CLOCK</span><h1 id="timeline-title">Execution timeline</h1><p>{task.title} — every recorded Host and Guest event on one scale.</p></div><div className="recording-badge"><strong>{(duration / 1000).toFixed(1)}s</strong><span>wall time</span></div></header>
      <div className="timeline-scale"><span>0</span><span>{(duration / 2000).toFixed(1)}s</span><span>{(duration / 1000).toFixed(1)}s</span></div>
      <div className="full-timeline" aria-label="Full execution timeline">
        {agents.map((agent) => {
          const lane = events.filter((event) => event.agent_id === agent);
          const role = agent === 'runtime' ? 'Host runtime' : task.sources.find((source) => source.id === agent)?.role ?? agent;
          return <div className="full-timeline-lane" key={agent}><span>{role}</span><div>{lane.map((event, index) => {
            const left = (event.started_millis / duration) * 100;
            const width = Math.max(((event.ended_millis - event.started_millis) / duration) * 100, 0.7);
            const level = agent === 'runtime' ? index % 3 : 1;
            return <button aria-label={`${eventTitle(task, event)} at ${event.started_millis.toFixed(0)} ms`} className={`${selected.sequence === event.sequence ? 'selected' : ''} ${event.type}`} key={event.sequence} onClick={() => setSelected(event)} style={{ left: `${Math.min(left, 99.2)}%`, top: `${6 + level * 13}px`, width: `${Math.min(width, 100 - Math.min(left, 99.2))}%` }} title={eventTitle(task, event)} type="button" />;
          })}</div></div>;
        })}
      </div>
      <div className="timeline-selection"><div><span>SELECTED EVENT</span><h2>{eventTitle(task, selected)}</h2><p>{eventSummary(selected)}</p></div><pre>{selectedOutput?.body ?? `${selected.agent_role} · ${selected.action} · ${selected.outcome}`}</pre></div>
    </section>
  );
}
