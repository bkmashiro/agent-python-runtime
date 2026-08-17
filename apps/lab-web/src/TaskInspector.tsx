import { useMemo, useState } from 'react';
import type { TaskEvent, TaskSnapshot } from './taskData';

type InspectorTab = 'python' | 'task' | 'io' | 'workspace';
type ExecutionView = 'timeline' | 'trace';

const agentOrder = ['runtime', 'orchestrator', 'researcher', 'reviewer'];

function eventLabel(event: TaskEvent) {
  const labels: Record<string, string> = {
    'run.start': 'Start task',
    'workspace.fork': 'Create private workspace',
    'stream.begin': 'Begin streamed program',
    'stream.prepare': 'Prepare Guest program',
    'guest.python': 'Execute orchestrator.py',
    'stream.seal': 'Seal program',
    'stream.end': 'Finish stream',
    'workspace.commit': 'Commit orchestrator workspace',
    'guest.close': 'Release Guest',
    'fanout.select': event.outcome === 'started' ? 'Start parallel analyses' : 'Select analysis branch',
    'agent.execute': event.agent_id === 'researcher' ? 'Research workspace shape' : 'Review branch readiness',
    'fanout.discard': 'Discard unselected branch',
    'fanout.selected_root': 'Publish selected branch',
    'cache.lookup': event.outcome === 'hit' ? 'Reuse retained report' : 'Check retained report',
    'cache.compute': 'Build report transformation',
    'cache.hit': 'Read retained report',
    'single_flight.leader': 'Lead shared computation',
    'single_flight.follower': 'Join shared computation',
    'single_flight.compute': 'Complete shared computation',
    'wait.begin': 'Wait for stable workspace',
    'observation.initial': 'Record initial workspace state',
    'wait.release': event.outcome === 'started' ? 'Release waiting execution' : 'Workspace became stable',
    'observation.changed': 'Observe stable inputs',
    'resume.fresh': 'Resume in fresh Guest',
    'run.terminal': 'Close task',
  };
  return labels[event.action] ?? event.action;
}

function eventTone(event: TaskEvent) {
  if (event.agent_id === 'researcher') return 'researcher';
  if (event.agent_id === 'reviewer') return 'reviewer';
  if (event.agent_id === 'orchestrator') return 'orchestrator';
  if (event.type === 'wait_resume' || event.type === 'observation') return 'wait';
  return 'runtime';
}

function formatTime(value: number) {
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value.toFixed(value % 1 ? 1 : 0)} ms`;
}

function TaskTimeline({ task, selected, onSelect }: { task: TaskSnapshot; selected: TaskEvent; onSelect: (event: TaskEvent) => void }) {
  const events = task.events.filter((event) => event.type !== 'oracle');
  const duration = Math.max(...events.map((event) => event.relative_elapsed_millis), 1);
  return (
    <div className="task-timeline" aria-label="Task execution timeline">
      {agentOrder.map((agent) => {
        const laneEvents = events.filter((event) => event.agent_id === agent);
        return (
          <div className="task-lane" key={agent}>
            <span>{agent === 'runtime' ? 'Host runtime' : agent}</span>
            <div className="task-lane-track">
              {laneEvents.map((event) => {
                const left = (event.started_millis / duration) * 96;
                const elapsed = event.ended_millis - event.started_millis;
                const width = Math.max((elapsed / duration) * 96, 0.9);
                return (
                  <button
                    aria-label={`${event.sequence}. ${eventLabel(event)}`}
                    className={`task-event ${eventTone(event)} ${selected.sequence === event.sequence ? 'selected' : ''}`}
                    key={event.sequence}
                    onClick={() => onSelect(event)}
                    style={{ left: `${left}%`, width: `${width}%` }}
                    title={eventLabel(event)}
                    type="button"
                  />
                );
              })}
            </div>
          </div>
        );
      })}
      <div className="task-axis"><span>0</span><span>{formatTime(duration)}</span></div>
    </div>
  );
}

function TraceTree({ task, selected, onSelect }: { task: TaskSnapshot; selected: TaskEvent; onSelect: (event: TaskEvent) => void }) {
  return (
    <div className="trace-tree" aria-label="Task trace tree">
      {task.events.filter((event) => event.type !== 'oracle').map((event) => (
        <button
          className={`${selected.sequence === event.sequence ? 'selected' : ''} ${eventTone(event)}`}
          key={event.sequence}
          onClick={() => onSelect(event)}
          style={{ paddingLeft: event.agent_id === 'runtime' ? 14 : event.agent_id === 'orchestrator' ? 30 : 46 }}
          type="button"
        >
          <span>{String(event.sequence).padStart(2, '0')}</span>
          <b>{eventLabel(event)}</b>
          <small>{formatTime(event.started_millis)}</small>
        </button>
      ))}
    </div>
  );
}

function PythonInspector({ task, selected }: { task: TaskSnapshot; selected: TaskEvent }) {
  const sourceID = selected.source?.source_id ?? 'orchestrator';
  const source = task.sources.find((value) => value.id === sourceID) ?? task.sources[0];
  const lines = source.source.split('\n');
  return (
    <div className="task-source">
      <div className="task-file">{source.file}<span>{source.role}</span></div>
      <pre>
        {lines.map((line, index) => {
          const lineNumber = index + 1;
          const active = selected.source?.source_id === source.id && lineNumber >= selected.source.start_line && lineNumber <= selected.source.end_line;
          return <code className={active ? 'active' : ''} key={`${lineNumber}-${line}`}><i>{lineNumber}</i>{line || ' '}</code>;
        })}
      </pre>
    </div>
  );
}

function TaskInspectorPanel({ task, selected, tab }: { task: TaskSnapshot; selected: TaskEvent; tab: InspectorTab }) {
  if (tab === 'python') return <PythonInspector task={task} selected={selected} />;
  if (tab === 'task') return (
    <div className="task-copy">
      <span>Task</span><p>{task.task}</p>
      <span>Result</span><code>{task.expected_artifact}</code>
      <span>Selected event</span><p>{eventLabel(selected)}</p>
    </div>
  );
  if (tab === 'io') return (
    <div className="task-copy task-io">
      <span>Action</span><code>{selected.action}</code>
      <span>Started</span><p>{formatTime(selected.started_millis)}</p>
      <span>Duration</span><p>{formatTime(selected.ended_millis - selected.started_millis)}</p>
      <span>Data flow</span><p>{selected.input_sha256 ? 'bound input' : 'no external input'} → {selected.output_sha256 ? 'published output' : 'no output body'}</p>
    </div>
  );
  const changes = task.events.flatMap((event) => (event.workspace_changes ?? []).map((change) => ({ ...change, agent: event.agent_id })));
  return (
    <div className="workspace-changes">
      {changes.map((change) => (
        <div key={`${change.agent}-${change.path}`}><span>+ {change.path}</span><b>{change.agent}</b><small>{change.size} bytes</small></div>
      ))}
    </div>
  );
}

export default function TaskInspector({ task }: { task: TaskSnapshot }) {
  const initial = useMemo(() => task.events.find((event) => event.action === 'agent.execute') ?? task.events[0], [task]);
  const [selectedSequence, setSelectedSequence] = useState(initial.sequence);
  const [view, setView] = useState<ExecutionView>('timeline');
  const [tab, setTab] = useState<InspectorTab>('python');
  const selected = task.events.find((event) => event.sequence === selectedSequence) ?? initial;
  return (
    <section className="task-inspector" aria-labelledby="task-inspector-title">
      <header className="task-inspector-heading">
        <div><h2 id="task-inspector-title">{task.title}</h2><p>{task.task}</p></div>
        <div className="task-summary"><strong>{task.sources.length}</strong><span>Python agents</span><strong>{task.events.filter((event) => event.type !== 'oracle').length}</strong><span>visible events</span></div>
      </header>
      <div className="task-viewbar">
        <div role="tablist" aria-label="Execution view">
          {(['timeline', 'trace'] as ExecutionView[]).map((value) => <button aria-selected={view === value} key={value} onClick={() => setView(value)} role="tab" type="button">{value === 'timeline' ? 'Timeline' : 'Trace tree'}</button>)}
        </div>
        <span>{eventLabel(selected)}</span>
      </div>
      <div className="task-execution">
        {view === 'timeline' ? <TaskTimeline task={task} selected={selected} onSelect={(event) => setSelectedSequence(event.sequence)} /> : <TraceTree task={task} selected={selected} onSelect={(event) => setSelectedSequence(event.sequence)} />}
      </div>
      <div className="task-inspection">
        <nav aria-label="Inspector tabs">
          {(['python', 'task', 'io', 'workspace'] as InspectorTab[]).map((value) => <button className={tab === value ? 'active' : ''} key={value} onClick={() => setTab(value)} type="button">{value === 'io' ? 'I/O' : value[0].toUpperCase() + value.slice(1)}</button>)}
        </nav>
        <TaskInspectorPanel task={task} selected={selected} tab={tab} />
      </div>
    </section>
  );
}
