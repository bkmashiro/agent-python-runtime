import { useEffect, useMemo, useState } from 'react';
import {
  Activity, Bot, Braces, ChevronRight, CircleDot, GitBranch, Search,
  ShieldCheck, TerminalSquare, Wrench,
} from 'lucide-react';
import {
  filterTrajectory, loadTrajectory, loadTrajectoryIndex, modelContext,
  type EventSource, type TrajectoryEvent, type TrajectoryExport, type TrajectoryIndex,
} from './trajectoryData';
import CampaignApp from './CampaignApp';
import './styles.css';

const sourceOrder: EventSource[] = ['system', 'developer', 'user', 'memory', 'skill', 'harness', 'model', 'tool', 'subagent', 'runtime', 'workspace'];

const sourceLabel: Record<EventSource, string> = {
  system: 'system', developer: 'developer', user: 'user', memory: 'memory', skill: 'skill',
  harness: 'harness', model: 'model', tool: 'tool', subagent: 'subagent', runtime: 'runtime', workspace: 'workspace',
};

function eventTitle(event: TrajectoryEvent): string {
  if (event.type === 'step.start') return `Step ${event.step_id?.replace('step-', '') ?? event.sequence} started`;
  if (event.type === 'step.end') return `Step ${event.step_id?.replace('step-', '') ?? event.sequence} ended`;
  if (event.type === 'request.header') return 'Request header';
  if (event.type === 'model.request') return `Model request · ${event.step_id?.replace('step-', '') ?? event.sequence}`;
  if (event.type === 'assistant.chunk') return 'Raw assistant chunk';
  if (event.type === 'tool.call') return `${event.tool_name ?? 'Tool'} tool call`;
  if (event.type === 'tool.result') return `${event.tool_name ?? 'Tool'} result`;
  if (event.type === 'assistant.reasoning') return 'Assistant reasoning';
  if (event.type === 'assistant.output') return 'Assistant output';
  if (event.type === 'subagent.dispatch') return 'Subagent dispatch';
  if (event.type === 'subagent.result') return 'Subagent result';
  if (event.type === 'runtime.event') return event.span_id?.includes('host-tool') ? 'Host tool execution' : 'Pysolate Run';
  if (event.type === 'workspace.change') return 'Workspace change';
  return event.type.replace('.', ' ');
}

function eventIcon(event: TrajectoryEvent) {
  if (event.type.startsWith('assistant') || event.type === 'model.request') return <Bot size={15} />;
  if (event.type.startsWith('tool')) return <Wrench size={15} />;
  if (event.type === 'runtime.event') return <Activity size={15} />;
  if (event.type === 'workspace.change') return <TerminalSquare size={15} />;
  if (event.type.startsWith('subagent')) return <GitBranch size={15} />;
  return <CircleDot size={13} />;
}

function shortDigest(value?: string) {
  return value ? `${value.slice(0, 14)}…${value.slice(-7)}` : '—';
}

function DetailField({ label, value }: { label: string; value?: string | number }) {
  if (value === undefined || value === '') return null;
  return <div className="detail-field"><dt>{label}</dt><dd>{value}</dd></div>;
}

function ContextRegion({ trajectory, event }: { trajectory: TrajectoryExport; event: TrajectoryEvent }) {
  if (event.type !== 'model.request') return null;
  const context = modelContext(trajectory, event.event_id);
  return (
    <section className="context-region" aria-label="Exact model context">
      <div className="section-title"><span>Exact model context</span><small>{context.length} ordered items</small></div>
      <p className="section-note">This is the exact ordered context declared for this request. No reconstructed or inferred items.</p>
      <div className="context-stack">
        {context.map((item, index) => (
          <article className="context-item" key={item.event_id}>
            <header><span>{index + 1}</span><b className={`source-${item.source}`}>{item.source}</b><code>{item.type}</code>{item.tool_name && <strong>{item.tool_name}</strong>}</header>
            <pre>{item.body_text}</pre>
          </article>
        ))}
      </div>
    </section>
  );
}

function LinkedExecution({ trajectory, event, onSelect }: { trajectory: TrajectoryExport; event: TrajectoryEvent; onSelect: (event: TrajectoryEvent) => void }) {
  if (!event.tool_call_id) return null;
  const linked = filterTrajectory(trajectory, { toolCallID: event.tool_call_id });
  if (linked.length < 2) return null;
  return (
    <section className="linked-region" aria-label="Linked execution">
      <div className="section-title"><span>Linked execution</span><small>{event.tool_call_id}</small></div>
      <div className="linked-flow">
        {linked.map((item, index) => (
          <button key={item.event_id} onClick={() => onSelect(item)}>
            <span>{index + 1}</span><b>{item.type}</b><small>{item.span_id ?? item.status ?? item.source}</small>
          </button>
        ))}
      </div>
      {linked.filter((item) => item.type === 'runtime.event').map((item) => (
        <dl className="runtime-identities" key={item.event_id}>
          <DetailField label="Run" value={item.run_id} />
          <DetailField label="Logical request" value={item.logical_request_id} />
          <DetailField label="Physical execution" value={item.physical_execution_id} />
          <DetailField label="Span" value={item.span_id} />
        </dl>
      ))}
    </section>
  );
}

function Inspector({ trajectory, event, onSelect }: { trajectory: TrajectoryExport; event: TrajectoryEvent; onSelect: (event: TrajectoryEvent) => void }) {
  const [tab, setTab] = useState<'inspect' | 'raw'>('inspect');
  useEffect(() => setTab('inspect'), [event.event_id]);
  return (
    <aside className="inspector" aria-label="Event inspector">
      <header className="inspector-head">
        <div className={`event-icon source-${event.source}`}>{eventIcon(event)}</div>
        <div><p>{event.source} · #{event.sequence}</p><h2>{eventTitle(event)}</h2></div>
        <span className="integrity"><ShieldCheck size={14} /> sealed</span>
      </header>
      <div className="tabs" role="tablist" aria-label="Inspector view">
        <button role="tab" aria-selected={tab === 'inspect'} onClick={() => setTab('inspect')}>Inspect</button>
        <button role="tab" aria-selected={tab === 'raw'} onClick={() => setTab('raw')}>Raw event</button>
      </div>
      {tab === 'raw' ? (
        <pre className="raw-event">{JSON.stringify(event, null, 2)}</pre>
      ) : (
        <div className="inspect-body">
          {event.body_text !== undefined && <section className="body-card"><div className="section-title"><span>Body</span><small>{event.content_type ?? 'opaque'}</small></div><pre>{event.body_text}</pre></section>}
          <dl className="detail-grid">
            <DetailField label="Event" value={event.event_id} />
            <DetailField label="Parent" value={event.parent_event_id} />
            <DetailField label="Turn" value={event.turn_id} />
            <DetailField label="Step" value={event.step_id} />
            <DetailField label="Actor" value={event.actor_id} />
            <DetailField label="Status" value={event.status} />
            <DetailField label="Provider" value={event.provider} />
            <DetailField label="Model" value={event.model} />
            <DetailField label="Tool call" value={event.tool_call_id} />
            <DetailField label="Tool" value={event.tool_name} />
            <DetailField label="Child session" value={event.child_session_id} />
            <DetailField label="Source events" value={event.source_event_ids?.join(', ')} />
            <DetailField label="Body identity" value={shortDigest(event.body?.sha256)} />
            <DetailField label="Event seal" value={shortDigest(event.sha256)} />
          </dl>
          {event.usage && <section className="usage"><span>input {event.usage.input ?? 0}</span><span>output {event.usage.output ?? 0}</span><span>reasoning {event.usage.reasoning ?? 0}</span><span>cache read {event.usage.cache_read ?? 0}</span></section>}
          <ContextRegion trajectory={trajectory} event={event} />
          <LinkedExecution trajectory={trajectory} event={event} onSelect={onSelect} />
        </div>
      )}
    </aside>
  );
}

function TrajectoryApp({ trajectory, index, sessionID, onSessionChange }: { trajectory: TrajectoryExport; index: TrajectoryIndex; sessionID: string; onSessionChange: (value: string) => void }) {
  const [query, setQuery] = useState('');
  const [source, setSource] = useState<EventSource | undefined>();
  const [selectedID, setSelectedID] = useState(() => trajectory.events.find((event) => event.type === 'model.request')?.event_id ?? trajectory.events[0].event_id);
  useEffect(() => {
    setQuery(''); setSource(undefined);
    setSelectedID(trajectory.events.find((event) => event.type === 'model.request')?.event_id ?? trajectory.events[0].event_id);
  }, [trajectory]);
  const filtered = useMemo(() => filterTrajectory(trajectory, { query, sources: source ? [source] : undefined }), [trajectory, query, source]);
  useEffect(() => {
    if (!filtered.some((event) => event.event_id === selectedID) && filtered[0]) setSelectedID(filtered[0].event_id);
  }, [filtered, selectedID]);
  const selected = trajectory.events.find((event) => event.event_id === selectedID) ?? trajectory.events[0];
  const requestCount = trajectory.events.filter((event) => event.type === 'model.request').length;
  const toolCount = trajectory.events.filter((event) => event.type === 'tool.call').length;
  const session = index.sessions.find((item) => item.session_id === sessionID)!;

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand"><Braces size={19} /><div><span>Pysolate Lab</span><small>private development trace</small></div></div>
        <div className="session-strip">
          <span className={`fixture-badge ${session.kind}`}>{session.kind === 'experiment' ? 'REAL GUEST EXPERIMENT' : 'SCRIPTED DEVELOPMENT FIXTURE'}</span>
          <a className="lab-link" href="/?view=campaign">Campaign</a>
          <select aria-label="Trajectory session" value={sessionID} onChange={(event) => onSessionChange(event.target.value)}>
            {index.sessions.map((item) => <option key={item.session_id} value={item.session_id}>{item.label}</option>)}
          </select>
          <code>{trajectory.session.session_id}</code><span>{trajectory.session.source_commit.slice(0, 8)}</span>
        </div>
      </header>
      <main>
        <section className="hero">
          <div><p className="eyebrow">MODEL-VISIBLE MEANS LOGGED</p><h1>Trajectory</h1><p>Inspect every context injection, model emission, tool boundary, subagent handoff and Pysolate execution from one append-only session.</p></div>
          <div className="hero-metrics"><article><b data-testid="event-count">{trajectory.events.length} events</b><span>hash chained</span></article><article><b>{requestCount}</b><span>model requests</span></article><article><b>{toolCount}</b><span>tool calls</span></article></div>
        </section>
        <section className="toolbar" aria-label="Trajectory filters">
          <label><Search size={15} /><input aria-label="Search trajectory" placeholder="Search bodies, IDs, tools…" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
          <div className="source-filters">
            {sourceOrder.map((item) => <button key={item} className={source === item ? 'active' : ''} onClick={() => setSource(source === item ? undefined : item)}>{sourceLabel[item]}</button>)}
          </div>
          <span data-testid="filtered-count">{filtered.length} shown</span>
        </section>
        <section className="workspace">
          <nav className="event-list" aria-label="Session trajectory">
            {filtered.map((event) => (
              <button key={event.event_id} aria-label={eventTitle(event)} className={selected.event_id === event.event_id ? 'selected' : ''} onClick={() => setSelectedID(event.event_id)}>
                <span className="event-sequence">{String(event.sequence).padStart(2, '0')}</span>
                <span className={`event-icon source-${event.source}`}>{eventIcon(event)}</span>
                <span className="event-copy"><b>{eventTitle(event)}</b><small><em>{event.source}</em>{event.tool_name ?? event.status ?? event.actor_id}</small><span>{event.body_text?.replace(/\s+/g, ' ').slice(0, 96) ?? event.event_id}</span></span>
                <time>+{event.occurred_millis}ms</time><ChevronRight size={14} />
              </button>
            ))}
            {filtered.length === 0 && <p className="empty">No events match this filter.</p>}
          </nav>
          <Inspector trajectory={trajectory} event={selected} onSelect={(event) => setSelectedID(event.event_id)} />
        </section>
      </main>
    </div>
  );
}

function TrajectoryRoot() {
  const [index, setIndex] = useState<TrajectoryIndex | null>(null);
  const [sessionID, setSessionID] = useState('');
  const [trajectory, setTrajectory] = useState<TrajectoryExport | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    loadTrajectoryIndex().then((value) => { setIndex(value); setSessionID(value.default_session_id); }).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'trajectory index load failed'));
  }, []);
  useEffect(() => {
    if (!index || !sessionID) return;
    const session = index.sessions.find((item) => item.session_id === sessionID);
    if (!session) { setError('trajectory session is missing'); return; }
    let cancelled = false;
    setTrajectory(null); setError('');
    loadTrajectory(`/lab-data/${session.file}`).then((value) => { if (!cancelled) setTrajectory(value); }).catch((reason: unknown) => { if (!cancelled) setError(reason instanceof Error ? reason.message : 'trajectory load failed'); });
    return () => { cancelled = true; };
  }, [index, sessionID]);
  if (error) return <main className="state-page"><h1>Trajectory unavailable</h1><pre>{error}</pre></main>;
  if (!trajectory || !index) return <main className="state-page"><h1>Loading trajectory…</h1></main>;
  return <TrajectoryApp trajectory={trajectory} index={index} sessionID={sessionID} onSessionChange={setSessionID} />;
}

export default function App() {
  return new URLSearchParams(window.location.search).get('view') === 'campaign' ? <CampaignApp /> : <TrajectoryRoot />;
}
