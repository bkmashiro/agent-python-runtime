import { useEffect, useMemo, useState } from 'react';
import {
  Activity, ArrowRight, Bot, Braces, ChevronDown, ChevronRight,
  CircleDot, Code2, Copy, FileCode2, GitBranch, Network, Search, ShieldCheck,
  TerminalSquare, Wrench,
} from 'lucide-react';
import {
  filterTrajectory, loadTrajectory, loadTrajectoryIndex,
  type EventSource, type TrajectoryEvent, type TrajectoryExport, type TrajectoryIndex,
} from './trajectoryData';
import {
  actorLane, buildCausalTree, decodeNestedJSON, eventNode, eventsForNode, inspectorTabs,
  readableType, relatedEvents, type CausalNode, type InspectorTab,
} from './trajectoryView';
import CampaignApp from './CampaignApp';
import './styles.css';

const sourceOrder: EventSource[] = ['model', 'subagent', 'harness', 'tool', 'runtime', 'workspace'];

function shortIdentity(value?: unknown) {
  if (typeof value !== 'string') return '—';
  return value.length > 28 ? `${value.slice(0, 15)}…${value.slice(-8)}` : value;
}

function statusTone(value?: string) {
  if (['completed', 'committed', 'selected', 'finalized', 'available', 'admitted'].includes(value ?? '')) return 'good';
  if (['failed', 'denied', 'ambiguous', 'reconciliation_required', 'truncated'].includes(value ?? '')) return 'warn';
  return 'neutral';
}

function EventGlyph({ event }: { event: TrajectoryEvent }) {
  if (event.type.startsWith('source.')) return <Code2 size={14} />;
  if (event.type.startsWith('model.')) return <Bot size={14} />;
  if (event.type.startsWith('subagent.')) return <GitBranch size={14} />;
  if (event.type.startsWith('workspace.')) return <TerminalSquare size={14} />;
  if (event.type.startsWith('effect.') || event.type === 'tool.decision') return <Wrench size={14} />;
  return <Activity size={14} />;
}

function TreeNode({ node, level, selectedID, matches, onSelect }: { node: CausalNode; level: number; selectedID: string; matches: Set<string> | null; onSelect: (node: CausalNode) => void }) {
  const [open, setOpen] = useState(true);
  const matchesQuery = !matches || node.eventIDs.some((id) => matches.has(id));
  return (
    <div className={`tree-node level-${level} ${matchesQuery ? '' : 'dimmed'}`}>
      <div className="tree-row" style={{ paddingLeft: `${10 + level * 18}px` }}>
        {node.children.length > 0 ? (
          <button className="tree-toggle" aria-label={`${open ? 'Collapse' : 'Expand'} ${node.title}`} onClick={() => setOpen((value) => !value)}>{open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</button>
        ) : <span className="tree-guide"><CircleDot size={8} /></span>}
        <button className={`tree-select ${selectedID === node.id ? 'selected' : ''}`} onClick={() => onSelect(node)}>
          <span className="tree-title"><b>{node.title}</b>{node.status && <em className={statusTone(node.status)}>{node.status.replaceAll('_', ' ')}</em>}</span>
          <small>{node.eyebrow}</small>
          <span>{node.summary}</span>
        </button>
      </div>
      {open && node.children.map((child) => <TreeNode key={child.id} node={child} level={level + 1} selectedID={selectedID} matches={matches} onSelect={onSelect} />)}
    </div>
  );
}

function StructuredValue({ value }: { value: unknown }) {
  const decoded = decodeNestedJSON(value);
  return <pre className="structured-value">{JSON.stringify(decoded, null, 2)}</pre>;
}

function RecordList({ events, selectedEventID, onEvent }: { events: TrajectoryEvent[]; selectedEventID?: string; onEvent: (event: TrajectoryEvent) => void }) {
  return (
    <div className="record-list">
      {events.map((event) => (
        <button key={event.event_id} className={selectedEventID === event.event_id ? 'selected' : ''} onClick={() => onEvent(event)}>
          <span className={`event-icon source-${event.source}`}><EventGlyph event={event} /></span>
          <span><b>{readableType(event.type)}</b><small>#{event.ordinal} · {event.status?.replaceAll('_', ' ') ?? event.source}</small></span>
          <ChevronRight size={13} />
        </button>
      ))}
    </div>
  );
}

function OverviewPanel({ node, events, trajectory, onEvent }: { node: CausalNode; events: TrajectoryEvent[]; trajectory: TrajectoryExport; onEvent: (event: TrajectoryEvent) => void }) {
  const selected = events.length === 1 ? events[0] : undefined;
  const relations = selected ? relatedEvents(trajectory, selected) : [];
  const terminal = [...events].reverse().find((event) => event.type === 'effect.transition' && !['intent', 'started'].includes(String(event.payload.state))) ??
    [...events].reverse().find((event) => event.type === 'execution.attempt' || event.type === 'workspace.terminal') ??
    [...events].reverse().find((event) => event.status);
  return (
    <div className="panel-stack">
      <section className="summary-grid">
        <article><span>Scope</span><b>{node.kind}</b><small>{events.length} canonical record{events.length === 1 ? '' : 's'}</small></article>
        <article><span>Terminal truth</span><b className={statusTone(terminal?.status)}>{terminal?.status?.replaceAll('_', ' ') ?? 'point evidence'}</b><small>{terminal ? readableType(terminal.type) : 'No inferred interval'}</small></article>
        <article><span>Authority</span><b>Host projected</b><small>Read-only, non-authoritative view</small></article>
      </section>
      <section className="inspector-section">
        <header><div><span>Recorded flow</span><small>Start/end atoms are grouped; every record remains reachable.</small></div></header>
        <RecordList events={events} selectedEventID={selected?.event_id} onEvent={onEvent} />
      </section>
      {selected && <section className="inspector-section">
        <header><div><span>Causal neighbours</span><small>Typed parents, children and shared receipt/occurrence links.</small></div><b>{relations.length}</b></header>
        {relations.length ? <div className="relation-grid">{relations.map((event) => <button key={event.event_id} onClick={() => onEvent(event)}><small>#{event.ordinal}</small><b>{readableType(event.type)}</b><ArrowRight size={12} /></button>)}</div> : <p className="absence">No additional typed relation is recorded for this atom.</p>}
      </section>}
    </div>
  );
}

function PayloadPanel({ events, mode }: { events: TrajectoryEvent[]; mode: 'Input' | 'Output' }) {
  const relevant = events.filter((event) => mode === 'Input'
    ? event.type === 'model.context' || event.type === 'tool.decision'
    : event.type === 'model.output' || event.type === 'tool.decision' || event.type === 'effect.transition');
  return <div className="panel-stack">{relevant.map((event) => <section className="inspector-section" key={event.event_id}><header><div><span>{readableType(event.type)}</span><small>Recorded typed payload · raw bodies are not reconstructed.</small></div><b>#{event.ordinal}</b></header><StructuredValue value={event.payload} /></section>)}</div>;
}

function CodePanel({ events }: { events: TrajectoryEvent[] }) {
  const document = events.find((event) => event.type === 'source.document');
  const occurrence = events.find((event) => event.type === 'source.occurrence');
  const decision = events.find((event) => event.type === 'source.decision');
  const executed = events.find((event) => event.type === 'source.executed_line');
  return (
    <div className="panel-stack">
      <section className="source-banner">
        <div><FileCode2 size={18} /><span><b>Python source boundary</b><small>{shortIdentity(document?.payload.source_sha256)}</small></span></div>
        <em className={decision?.payload.admitted === true ? 'good' : 'neutral'}>{String(decision?.payload.claim_level ?? 'program range').replaceAll('_', ' ')}</em>
      </section>
      <section className="code-frame">
        <header><span>Generated source</span><small>{occurrence ? `L${occurrence.payload.start_line}:${occurrence.payload.start_column}–L${occurrence.payload.end_line}:${occurrence.payload.end_column}` : 'range not recorded'}</small></header>
        <div className="code-absence"><Code2 size={24} /><b>Source body omitted from portable projection</b><p>The exact digest and source-bound range are recorded. Lab does not infer or reconstruct private code.</p></div>
      </section>
      <section className="claim-grid">
        <article><span>Static source claim</span><b>{decision ? String(decision.payload.claim_level) : 'not recorded'}</b><small>{occurrence ? String(occurrence.payload.capability) : 'No occurrence in this selection'}</small></article>
        <article><span>Executed line</span><b>{executed ? String(executed.payload.availability).replaceAll('_', ' ') : 'not recorded'}</b><small>{executed?.payload.availability === 'available' ? 'Instrumentation-backed' : 'Static AST span is not execution evidence'}</small></article>
      </section>
    </div>
  );
}

function TimelinePanel({ trajectory, events, onEvent }: { trajectory: TrajectoryExport; events: TrajectoryEvent[]; onEvent: (event: TrajectoryEvent) => void }) {
  const eventIDs = new Set(events.map((event) => event.event_id));
  const minimum = Math.min(...trajectory.events.map((event) => event.occurred_nanos));
  const maximum = Math.max(...trajectory.events.map((event) => event.occurred_nanos));
  const width = Math.max(1, maximum - minimum);
  const lanes = [...new Set(trajectory.events.map(actorLane))];
  return (
    <section className="timeline-card">
      <header><div><span>Actor swimlanes</span><small>Every mark is a recorded point; no duration is inferred.</small></div><b>{trajectory.events.length} points</b></header>
      <div className="timeline-axis"><span>0 ms</span><span>{Math.round(width / 1_000_000)} ms</span></div>
      <div className="timeline-lanes">{lanes.map((lane) => <div className="timeline-lane" key={lane}><b>{lane}</b><div>{trajectory.events.filter((event) => actorLane(event) === lane).map((event) => <button key={event.event_id} className={`${eventIDs.has(event.event_id) ? 'active' : ''} source-${event.source}`} style={{ left: `${((event.occurred_nanos - minimum) / width) * 100}%` }} title={`#${event.ordinal} ${readableType(event.type)}`} aria-label={`Timeline event ${event.ordinal} ${readableType(event.type)}`} onClick={() => onEvent(event)} />)}</div></div>)}</div>
      <footer><span><i className="legend active" /> selected scope</span><span><i className="legend" /> other recorded point</span></footer>
    </section>
  );
}

function WorkspacePanel({ events }: { events: TrajectoryEvent[] }) {
  const items = events.filter((event) => event.type.startsWith('workspace.') || event.type === 'subagent.workspace' || event.type === 'subagent.runtime');
  return <div className="panel-stack">{items.map((event) => <section className="workspace-card" key={event.event_id}><header><div><TerminalSquare size={15} /><span><b>{readableType(event.type)}</b><small>#{event.ordinal}</small></span></div><em className={statusTone(event.status)}>{event.status?.replaceAll('_', ' ') ?? 'recorded'}</em></header><dl>{Object.entries(event.payload).filter(([key]) => key.includes('workspace') || key.includes('root') || key.includes('entries') || key.includes('bytes') || key === 'disposition' || key === 'depth').map(([key, value]) => <div key={key}><dt>{key.replaceAll('_', ' ')}</dt><dd>{shortIdentity(value)}</dd></div>)}</dl></section>)}{items.length === 0 && <p className="absence">No workspace checkpoint or terminal record is linked to this selection.</p>}<section className="body-policy"><ShieldCheck size={16} /><div><b>Portable body policy</b><p>Before/after file bodies are absent, not empty. Only recorded roots, counters and dispositions are shown.</p></div></section></div>;
}

function EvidencePanel({ trajectory, events, onEvent }: { trajectory: TrajectoryExport; events: TrajectoryEvent[]; onEvent: (event: TrajectoryEvent) => void }) {
  const copy = (value: string) => navigator.clipboard?.writeText(value);
  return <div className="panel-stack"><section className="evidence-header"><ShieldCheck size={18} /><div><b>Canonical evidence identities</b><p>Copy exact IDs here; summaries elsewhere remain human-first.</p></div></section>{events.map((event) => <section className="evidence-record" key={event.event_id}><header><button onClick={() => onEvent(event)}>#{event.ordinal} {readableType(event.type)}</button><span>{event.source}</span></header><dl><div><dt>Event identity</dt><dd><code>{event.event_id}</code><button aria-label={`Copy event ${event.ordinal} identity`} onClick={() => copy(event.event_id)}><Copy size={12} /></button></dd></div>{event.parent_event_ids?.length ? <div><dt>Causal parents</dt><dd><code>{event.parent_event_ids.join('\n')}</code></dd></div> : null}<div><dt>Actor</dt><dd><code>{event.actor_id}</code></dd></div></dl></section>)}</div>;
}

function Inspector({ trajectory, node, onEvent }: { trajectory: TrajectoryExport; node: CausalNode; onEvent: (event: TrajectoryEvent) => void }) {
  const events = eventsForNode(trajectory, node);
  const tabs = inspectorTabs(events);
  const [tab, setTab] = useState<InspectorTab>('Overview');
  useEffect(() => setTab('Overview'), [node.id]);
  return (
    <aside className="inspector" aria-label="Causal inspector">
      <header className="inspector-head"><div className="scope-icon"><Network size={17} /></div><div><p>{node.eyebrow}</p><h2>{node.title}</h2><span>{node.summary}</span></div><span className="integrity"><ShieldCheck size={13} /> sealed</span></header>
      <div className="tabs" role="tablist" aria-label="Inspector view">{tabs.map((item) => <button key={item} role="tab" aria-selected={tab === item} onClick={() => setTab(item)}>{item}</button>)}</div>
      <div className="inspect-body">
        {tab === 'Overview' && <OverviewPanel node={node} events={events} trajectory={trajectory} onEvent={onEvent} />}
        {(tab === 'Input' || tab === 'Output') && <PayloadPanel events={events} mode={tab} />}
        {tab === 'Code' && <CodePanel events={events} />}
        {tab === 'Timeline' && <TimelinePanel trajectory={trajectory} events={events} onEvent={onEvent} />}
        {tab === 'Workspace' && <WorkspacePanel events={events} />}
        {tab === 'Evidence' && <EvidencePanel trajectory={trajectory} events={events} onEvent={onEvent} />}
        {tab === 'Raw' && <pre className="raw-event">{JSON.stringify(events.length === 1 ? events[0] : events, null, 2)}</pre>}
      </div>
    </aside>
  );
}

function TrajectoryApp({ trajectory, index, viewID, onViewChange }: { trajectory: TrajectoryExport; index: TrajectoryIndex; viewID: string; onViewChange: (value: string) => void }) {
  const tree = useMemo(() => buildCausalTree(trajectory), [trajectory]);
  const [selected, setSelected] = useState<CausalNode>(() => tree.children[1]?.children[1] ?? tree);
  const [query, setQuery] = useState('');
  const [source, setSource] = useState<EventSource | undefined>();
  useEffect(() => { setSelected(tree.children[1]?.children[1] ?? tree); setQuery(''); setSource(undefined); }, [tree]);
  const matches = useMemo(() => {
    if (!query && !source) return null;
    return new Set(filterTrajectory(trajectory, { query, sources: source ? [source] : undefined }).map((event) => event.event_id));
  }, [trajectory, query, source]);
  const currentView = index.views.find((view) => view.view_id === viewID)!;
  return (
    <div className="app-shell">
      <header className="topbar"><div className="brand"><Braces size={19} /><div><span>Pysolate Lab</span><small>source-bound causal debugger</small></div></div><div className="session-strip"><span className={`fixture-badge ${currentView.kind}`}>{currentView.kind === 'experiment' ? 'REAL GUEST · PUBLIC' : 'PRODUCTION LEDGER'}</span><a className="lab-link" href="/?view=campaign">Campaign</a><select aria-label="Evidence view" value={viewID} onChange={(event) => onViewChange(event.target.value)}>{index.views.map((view) => <option key={view.view_id} value={view.view_id}>{view.label}</option>)}</select></div></header>
      <main>
        <section className="toolbar" aria-label="Causal tree filters"><label><Search size={15} /><input aria-label="Search evidence" placeholder="Find a capability, status, ID…" value={query} onChange={(event) => setQuery(event.target.value)} /></label><div className="source-filters"><button className={!source ? 'active' : ''} onClick={() => setSource(undefined)}>all</button>{sourceOrder.map((item) => <button key={item} className={source === item ? 'active' : ''} onClick={() => setSource(source === item ? undefined : item)}>{item}</button>)}</div><span>{matches ? `${matches.size} matching` : 'causal view'}</span></section>
        <section className="workspace causal-workspace">
          <nav className="causal-tree" aria-label="Session causal tree"><header><div><span>Session structure</span><small>Lifecycle atoms collapsed into tasks</small></div><Network size={15} /></header><TreeNode node={tree} level={0} selectedID={selected.id} matches={matches} onSelect={setSelected} />{matches?.size === 0 && <p className="empty">No recorded evidence matches this focus.</p>}</nav>
          <Inspector trajectory={trajectory} node={selected} onEvent={(event) => setSelected(eventNode(event))} />
        </section>
      </main>
    </div>
  );
}

function TrajectoryRoot() {
  const [index, setIndex] = useState<TrajectoryIndex | null>(null);
  const [viewID, setViewID] = useState('');
  const [trajectory, setTrajectory] = useState<TrajectoryExport | null>(null);
  const [error, setError] = useState('');
  useEffect(() => { loadTrajectoryIndex().then((value) => { setIndex(value); setViewID(value.default_view_id); }).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'evidence index load failed')); }, []);
  useEffect(() => {
    if (!index || !viewID) return;
    const view = index.views.find((item) => item.view_id === viewID);
    if (!view) { setError('evidence view is missing'); return; }
    let cancelled = false; setTrajectory(null); setError('');
    loadTrajectory(`/lab-data/${view.file}`).then((value) => { if (!cancelled) setTrajectory(value); }).catch((reason: unknown) => { if (!cancelled) setError(reason instanceof Error ? reason.message : 'evidence load failed'); });
    return () => { cancelled = true; };
  }, [index, viewID]);
  if (error) return <main className="state-page"><h1>Evidence unavailable</h1><pre>{error}</pre></main>;
  if (!trajectory || !index) return <main className="state-page"><h1>Loading causal evidence…</h1></main>;
  return <TrajectoryApp trajectory={trajectory} index={index} viewID={viewID} onViewChange={setViewID} />;
}

export default function App() {
  return new URLSearchParams(window.location.search).get('view') === 'campaign' ? <CampaignApp /> : <TrajectoryRoot />;
}
