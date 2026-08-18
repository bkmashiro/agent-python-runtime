import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import type { ProviderDebug, ProviderDebugEvent } from './providerDebugData';
import type { UnifiedCandidate, UnifiedEvent, UnifiedFact, UnifiedSnapshot } from './unifiedCampaignData';

type DebugEvent = {
  id: string;
  order: number;
  label: string;
  type: string;
  actor: string;
  atNS?: number;
  raw: unknown;
  source?: string;
  sourceDelta?: string;
  sourceStep?: number;
  sourceStepCount?: number;
  sourcePrefixSHA256?: string;
  input?: unknown;
  output?: unknown;
  tool?: string;
  bindings: Record<string, unknown>;
};

type TraceGroup = { id: string; title: string; summary: string; facts: UnifiedFact[]; events: DebugEvent[]; icon: IconName; synthetic?: boolean; preferFlat?: boolean };
type InspectorTab = 'overview' | 'source' | 'io' | 'raw';

function seconds(value: number) { return `${(value / 1_000_000_000).toFixed(3)}s`; }
function pretty(value: unknown) { return JSON.stringify(value, null, 2); }
function actorLabel(actor: string) { return actor.replace(/^actor-/, '').replaceAll('-', ' '); }
function toolName(logicalID?: string) {
  if (logicalID?.endsWith('-weather')) return 'travel.weather';
  if (logicalID?.endsWith('-rail')) return 'travel.rail';
  if (logicalID?.endsWith('-attractions')) return 'travel.attractions';
  if (logicalID?.includes('origin')) return 'trip.origin';
  return undefined;
}
function sourceTool(delta?: string) {
  if (delta?.includes('travel.weather(')) return 'travel.weather';
  if (delta?.includes('travel.rail(')) return 'travel.rail';
  if (delta?.includes('travel.attractions(')) return 'travel.attractions';
  return undefined;
}

type IconName = 'tree' | 'list' | 'search' | 'actor' | 'model' | 'source' | 'request' | 'guest' | 'workspace' | 'control' | 'bindings' | 'io' | 'raw';
function Icon({ name, size = 14 }: { name: IconName; size?: number }) {
  const paths: Record<IconName, ReactNode> = {
    tree: <><path d="M4 4h5v4H4zM15 3v3M9 6h6v11M15 12h5v4h-5zM15 18h5v4h-5z" /></>,
    list: <><path d="M9 6h12M9 12h12M9 18h12" /><path d="M4 6h.01M4 12h.01M4 18h.01" /></>,
    search: <><circle cx="11" cy="11" r="6" /><path d="m16 16 4 4" /></>,
    actor: <><circle cx="12" cy="8" r="3" /><path d="M5 21a7 7 0 0 1 14 0" /></>,
    model: <><rect x="4" y="5" width="16" height="14" rx="3" /><path d="M9 10h.01M15 10h.01M8 15h8M12 2v3" /></>,
    source: <><path d="M8 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-3" /><path d="m17 3 4 4-9 9-4 1 1-4z" /></>,
    request: <><path d="M4 12h15M14 7l5 5-5 5" /><path d="M5 5h5M5 19h5" /></>,
    guest: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 3 3-3 3M13 15h4" /></>,
    workspace: <><path d="M3 7h7l2 2h9v11H3z" /><path d="M3 7V5h7l2 2" /></>,
    control: <><path d="M12 3v18M3 12h18" /><circle cx="12" cy="12" r="8" /></>,
    bindings: <><path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.1 1.1" /><path d="M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.1-1.1" /></>,
    io: <><path d="M7 7h10M7 17h10M10 4 7 7l3 3M14 14l3 3-3 3" /></>,
    raw: <><path d="M8 3h8l4 4v14H8z" /><path d="M16 3v5h5M4 8h4M4 13h4M4 18h4" /></>,
  };
  return <svg aria-hidden="true" className="ui-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}

function eventIcon(type: string): IconName {
  if (type.startsWith('model.')) return 'model';
  if (type.startsWith('source.') || type === 'semantic.qualified') return 'source';
  if (type === 'semantic.issue' || type === 'semantic.claim') return 'request';
  if (type.startsWith('request.') || type.startsWith('function.')) return 'request';
  if (type.startsWith('guest.')) return 'guest';
  if (type.startsWith('capsule.') || type.startsWith('cow.') || type.startsWith('cold_io.')) return 'workspace';
  if (type.startsWith('control.')) return 'control';
  return 'raw';
}

function candidateForEvent(event: UnifiedEvent, snapshot: UnifiedSnapshot): UnifiedCandidate | undefined {
  const id = event.actor_id === 'brighton' || event.actor_id === 'oxford'
    ? event.actor_id
    : event.logical_id?.startsWith('brighton-') ? 'brighton' : event.logical_id?.startsWith('oxford-') ? 'oxford' : undefined;
  return snapshot.candidates.find((candidate) => candidate.id === id);
}

function runtimeEvent(event: UnifiedEvent, snapshot: UnifiedSnapshot): DebugEvent {
  const candidate = candidateForEvent(event, snapshot);
  const result = candidate && typeof candidate.guest_response === 'object' && candidate.guest_response
    ? (candidate.guest_response as { result?: Record<string, unknown> }).result
    : undefined;
  const api = event.logical_id?.split('-').at(-1);
  const capabilityOutput = api === 'weather' ? result?.weather : api === 'rail' ? result?.rail : api === 'attractions' ? result?.attraction : undefined;
  const capabilityInput = candidate && api ? { destination: candidate.id, ...(api === 'rail' ? { travellers: 2 } : {}) } : undefined;
  return {
    id: event.id,
    order: event.sequence,
    label: `#${String(event.sequence).padStart(2, '0')}`,
    type: event.type,
    actor: event.actor_id,
    atNS: event.at_ns,
    raw: event,
    source: event.source_prefix ?? candidate?.executed_source,
    sourceDelta: event.source_delta,
    sourceStep: event.source_step,
    sourceStepCount: event.source_step_count,
    sourcePrefixSHA256: event.source_prefix_sha256,
    input: capabilityInput ?? (event.type === 'guest.start' && candidate ? { code: candidate.executed_source, candidate: candidate.id } : undefined),
    output: capabilityOutput ?? (event.type === 'guest.end' && candidate ? candidate.guest_response : event.actor_id === 'main' && event.type.startsWith('guest.') ? snapshot.main_guest_response : undefined),
    tool: toolName(event.logical_id) ?? sourceTool(event.source_delta),
    bindings: { logical_id: event.logical_id, physical_id: event.physical_id, identity_sha256: event.identity_sha256, outcome: event.outcome },
  };
}

function providerEvent(event: ProviderDebugEvent, debug: ProviderDebug): DebugEvent {
  const candidateID = event.actor_id.endsWith('brighton') ? 'brighton' : event.actor_id.endsWith('oxford') ? 'oxford' : undefined;
  const candidate = debug.harness_result.candidates.find((item) => item.candidate_id === candidateID);
  return {
    id: event.event_id,
    order: event.ordinal,
    label: `M${String(event.ordinal).padStart(2, '0')}`,
    type: event.type,
    actor: event.actor_id,
    raw: event,
    source: candidate?.python_source,
    input: event.type === 'model.body' ? event.body : undefined,
    output: event.type === 'model.output' ? event.body : undefined,
    bindings: { parent_event_ids: event.parent_event_ids, payload: event.payload },
  };
}

function buildGroups(snapshot: UnifiedSnapshot, debug: ProviderDebug): TraceGroup[] {
  const byID = new Map(snapshot.events.map((event) => [event.id, event]));
  const runtime = snapshot.events.map((event) => runtimeEvent(event, snapshot));
  const toolEvent = (event: DebugEvent) => event.type.startsWith('request.') || event.type.startsWith('function.') || event.type === 'semantic.qualified' || event.type === 'semantic.issue' || event.type === 'semantic.claim';
  return [
    {
      id: 'code-tools', title: 'Code + tool calls', icon: 'source', synthetic: true, preferFlat: true,
      summary: 'Source prefixes and the Host requests they unlock, interleaved by causal time. Use this view to see the exact statement after which each tool call is qualified and issued.',
      facts: [{ label: 'source increments', value: '18', note: 'each prefix shown explicitly' }, { label: 'travel requests', value: '6', note: 'weather, rail, attractions ×2' }],
      events: runtime.filter((event) => event.type.startsWith('source.') || event.type.startsWith('semantic.') || event.type.startsWith('request.')),
    },
    {
      id: 'tool-calls', title: 'Tool calls', icon: 'request', synthetic: true, preferFlat: true,
      summary: 'Runtime tool qualification, issue, physical request, completion, and exact claim events. Tool names are shown directly beside their typed event.',
      facts: [{ label: 'travel tools', value: '6', note: 'candidate read calls' }, { label: 'shared origin', value: '3 logical / 1 physical', note: 'sharing then retention' }],
      events: runtime.filter(toolEvent),
    },
    {
      id: 'provider-generation', title: 'Model generation', icon: 'model',
      summary: 'The complete provider requests and responses that produced the selected candidate Python. This first valid attempt was used; no performance-based retry or cherry-pick occurred.',
      facts: [{ label: 'provider calls', value: '4', note: 'candidate ×2, selection, final' }, { label: 'selection rule', value: 'first valid', note: 'contract + safety + Guest oracle' }],
      events: debug.events.map((event) => providerEvent(event, debug)),
    },
    ...snapshot.phases.map((phase) => ({ ...phase, icon: phase.id === 'source-predispatch' ? 'source' as const : phase.id === 'fresh-execution' ? 'guest' as const : phase.id === 'fail-closed' ? 'control' as const : 'workspace' as const, events: phase.event_ids.map((id) => byID.get(id)).filter((event): event is UnifiedEvent => Boolean(event)).map((event) => runtimeEvent(event, snapshot)) })),
  ];
}

function CompactSummary({ snapshot }: { snapshot: UnifiedSnapshot }) {
  const baseline = seconds(snapshot.matched_control.baseline_median_ns);
  const optimized = seconds(snapshot.matched_control.optimized_median_ns);
  const saving = seconds(snapshot.matched_control.median_savings_ns);
  return <section className="debug-summary" aria-label="Run summary">
    <div><span>Brighton</span><strong>£118.40</strong><small>discarded</small></div>
    <div className="selected"><span>Oxford</span><strong>£78.00</strong><small>selected by Main</small></div>
    <div><span>Matched ×3</span><strong>{baseline} → {optimized}</strong><small>−{saving}</small></div>
    <div><span>Evidence</span><strong>Linux / amd64</strong><small>{snapshot.provenance.source_commit.slice(0, 8)}</small></div>
  </section>;
}

function GroupNav({ groups, selected, onToggle, onAll }: { groups: TraceGroup[]; selected: Set<string>; onToggle: (id: string) => void; onAll: () => void }) {
  return <nav className="trace-group-nav" aria-label="Evidence filters">
    <button aria-pressed={selected.size === 0} className="all-filter" onClick={onAll} type="button"><Icon name="list" /><strong>All evidence</strong><small>clear filters</small></button>
    {groups.map((group) => <button aria-pressed={selected.has(group.id)} key={group.id} onClick={() => onToggle(group.id)} type="button">
      <Icon name={group.icon} /><strong>{group.title}</strong><small>{group.events.length} events</small>
    </button>)}
  </nav>;
}

function EventTree({ events, selected, onSelect, preferFlat = false }: { events: DebugEvent[]; selected: string; onSelect: (id: string) => void; preferFlat?: boolean }) {
  const [query, setQuery] = useState('');
  const [treeMode, setTreeMode] = useState(!preferFlat);
  const searchRef = useRef<HTMLInputElement>(null);
  const actors = [...new Set(events.map((event) => event.actor))];
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  useEffect(() => setCollapsed(new Set()), [events]);
  const normalized = query.trim().toLowerCase();
  const visible = events.filter((event) => !normalized || `${event.type} ${event.actor} ${event.label} ${pretty(event.bindings)}`.toLowerCase().includes(normalized));
  useEffect(() => {
    const focusFilter = (event: KeyboardEvent) => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); searchRef.current?.focus(); } };
    window.addEventListener('keydown', focusFilter);
    return () => window.removeEventListener('keydown', focusFilter);
  }, []);
  useEffect(() => {
    if (visible.length && !visible.some((event) => event.id === selected)) onSelect(visible[0].id);
  }, [normalized, events, selected]);
  const eventRow = (event: DebugEvent, nested: boolean) => <button aria-label={`${event.type} · ${actorLabel(event.actor)} · ${event.tool ?? event.label}`} className={`event-row-button ${nested ? 'nested' : ''} ${selected === event.id ? 'active' : ''}`} key={event.id} onClick={() => onSelect(event.id)} type="button">
    <span className={`event-kind kind-${eventIcon(event.type)}`}><Icon name={eventIcon(event.type)} size={13} /></span>
    <time>{event.atNS === undefined ? event.label : `+${seconds(event.atNS)}`}</time><span className="event-name">{event.type}{!nested && <em>{actorLabel(event.actor)}</em>}</span><small>{event.tool ?? (event.bindings.outcome ? String(event.bindings.outcome) : event.label)}</small>
  </button>;
  return <section className="trace-browser" aria-label="Grouped execution events">
    <div className="trace-toolbar">
      <div className="view-switch" role="group" aria-label="Trace organization"><button aria-pressed={treeMode} onClick={() => setTreeMode(true)} type="button"><Icon name="tree" /> By role</button><button aria-pressed={!treeMode} onClick={() => setTreeMode(false)} type="button"><Icon name="list" /> By time</button></div>
      <label className="trace-search"><Icon name="search" /><input aria-label="Filter trace events" onChange={(event) => setQuery(event.target.value)} placeholder="Filter events" ref={searchRef} value={query} /><kbd>⌘K</kbd></label>
      <span className="trace-count">{visible.length}/{events.length}</span>
    </div>
    <div className={`debug-event-tree ${treeMode ? 'tree-mode' : 'flat-mode'}`}>{treeMode ? actors.map((actor) => {
      const actorEvents = visible.filter((event) => event.actor === actor);
      if (!actorEvents.length) return null;
      const isCollapsed = collapsed.has(actor);
      return <section className="event-actor-group" key={actor}>
        <button className="actor-tree-row" aria-expanded={!isCollapsed} onClick={() => setCollapsed((current) => { const next = new Set(current); if (next.has(actor)) next.delete(actor); else next.add(actor); return next; })} type="button">
          <span className="tree-chevron">{isCollapsed ? '▸' : '▾'}</span><span className="actor-icon"><Icon name={actor.startsWith('actor-') ? 'model' : 'actor'} /></span><strong>{actorLabel(actor)}</strong><small>{actorEvents.length}</small>
        </button>
        {!isCollapsed && <div className="actor-tree-children">{actorEvents.map((event) => eventRow(event, true))}</div>}
      </section>;
    }) : visible.map((event) => eventRow(event, false))}</div>
  </section>;
}

function EventInspector({ event }: { event: DebugEvent }) {
  const [tab, setTab] = useState<InspectorTab>('overview');
  const sourceLines = event.source ? event.source.replace(/\n$/, '').split('\n') : [];
  const deltaLines = event.sourceDelta ? event.sourceDelta.replace(/\n$/, '').split('\n').length : 0;
  const deltaStart = Math.max(0, sourceLines.length - deltaLines);
  return <aside className="debug-inspector" aria-label="Execution event inspector">
    <header><span className={`inspector-event-icon kind-${eventIcon(event.type)}`}><Icon name={eventIcon(event.type)} size={17} /></span><div><span className="eyebrow">{event.label} · {actorLabel(event.actor)}</span><h3>{event.type}</h3><small>{event.bindings.outcome ? String(event.bindings.outcome) : 'recorded event'}</small></div>{event.atNS !== undefined && <time>+{seconds(event.atNS)}</time>}</header>
    <nav aria-label="Inspector details">{([['overview', 'bindings', 'Overview'], ['source', 'source', 'Python'], ['io', 'io', 'Input / output'], ['raw', 'raw', 'Raw event']] as Array<[InspectorTab, IconName, string]>).map(([name, icon, label]) => <button aria-pressed={tab === name} key={name} onClick={() => setTab(name)} type="button"><Icon name={icon} />{label}</button>)}</nav>
    <div className="inspector-body">
      {tab === 'overview' && <div className="binding-view"><div className="detail-strip"><span>actor<strong>{actorLabel(event.actor)}</strong></span><span>sequence<strong>{event.label}</strong></span><span>time<strong>{event.atNS === undefined ? 'model phase' : `+${seconds(event.atNS)}`}</strong></span></div><CodeBlock label="Exact bindings" value={event.bindings} /></div>}
      {tab === 'source' && (event.source ? <div className="source-view"><div className="code-label"><span>candidate.py {event.sourceStep ? `· prefix ${event.sourceStep}/${event.sourceStepCount}` : '· complete source'}</span><small>{event.sourceStep ? `+${deltaLines} line${deltaLines === 1 ? '' : 's'} · reconstructed` : `${sourceLines.length} lines`}</small></div>{event.sourcePrefixSHA256 && <div className="prefix-identity"><span>prefix identity</span><code>{event.sourcePrefixSHA256}</code></div>}<pre className="source-code">{sourceLines.map((line, index) => <code className={event.sourceStep && index >= deltaStart ? 'source-line-delta' : ''} key={index}><i>{index + 1}</i><span>{line || ' '}</span></code>)}</pre></div> : <p className="empty-detail"><Icon name="source" size={22} />This event has no Python source body.</p>)}
      {tab === 'io' && <div className="io-grid"><CodeBlock label="Input" value={event.input} /><CodeBlock label="Output" value={event.output} /></div>}
      {tab === 'raw' && <CodeBlock label="Recorded event" value={event.raw} />}
    </div>
  </aside>;
}

function CodeBlock({ label, value }: { label: string; value: unknown }) {
  const text = value === undefined ? '—' : pretty(value);
  return <section className="code-block"><div className="code-label"><span>{label}</span><small>{new Blob([text]).size} bytes</small></div><pre>{text}</pre></section>;
}

function combineGroupEvents(groups: TraceGroup[], selected: Set<string>) {
  const active = selected.size ? groups.filter((group) => selected.has(group.id)) : groups.filter((group) => !group.synthetic);
  const byID = new Map<string, DebugEvent>();
  for (const group of active) for (const event of group.events) byID.set(event.id, event);
  return [...byID.values()].sort((left, right) => {
    if (left.atNS === undefined && right.atNS !== undefined) return -1;
    if (left.atNS !== undefined && right.atNS === undefined) return 1;
    return left.atNS !== undefined && right.atNS !== undefined ? left.atNS - right.atNS || left.order - right.order : left.order - right.order;
  });
}

function TraceWorkbench({ groups, initialFilters, timeline = false }: { groups: TraceGroup[]; initialFilters: string[]; timeline?: boolean }) {
  const [selectedFilters, setSelectedFilters] = useState<Set<string>>(new Set(initialFilters));
  const activeGroups = groups.filter((group) => selectedFilters.has(group.id));
  const events = useMemo(() => combineGroupEvents(groups, selectedFilters), [groups, selectedFilters]);
  const filterKey = [...selectedFilters].sort().join('+') || 'all';
  const primary = activeGroups.length === 1 ? activeGroups[0] : undefined;
  const title = primary?.title ?? (selectedFilters.size ? 'Combined evidence filters' : 'All evidence');
  const summary = primary?.summary ?? (selectedFilters.size ? 'Events matching any selected filter are deduplicated and merged by causal time.' : 'The complete provider and runtime ledger, with synthetic shortcut filters excluded from duplication.');
  const facts = primary?.facts ?? [
    { label: 'active filters', value: selectedFilters.size ? String(selectedFilters.size) : 'none', note: selectedFilters.size ? [...selectedFilters].join(' + ') : 'showing the full ledger' },
    { label: 'visible events', value: String(events.length), note: `${new Set(events.map((event) => event.actor)).size} actors` },
  ];
  const [selectedByFilter, setSelectedByFilter] = useState<Record<string, string>>({});
  const selectedID = selectedByFilter[filterKey] ?? events[0]?.id ?? '';
  const event = events.find((item) => item.id === selectedID) ?? events[0];
  const toggleFilter = (id: string) => setSelectedFilters((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  return <>
    <GroupNav groups={groups} onAll={() => setSelectedFilters(new Set())} onToggle={toggleFilter} selected={selectedFilters} />
    <section className="group-context"><div><span className="eyebrow">{timeline ? 'Causal evidence timeline' : 'Evidence filters · multi-select'}</span><h2>{title}</h2><p>{summary}</p></div><div className="group-facts">{facts.map((fact) => <div key={fact.label}><span>{fact.label}</span><strong>{fact.value}</strong><small>{fact.note}</small></div>)}</div></section>
    <div className="debug-workbench">{timeline && <div className="timeline-ruler"><span>CAUSAL ORDER</span><i /></div>}<EventTree events={events} key={filterKey} onSelect={(id) => setSelectedByFilter((current) => ({ ...current, [filterKey]: id }))} preferFlat={activeGroups.some((group) => group.preferFlat)} selected={selectedID} />{event && <EventInspector event={event} />}</div>
  </>;
}

export function MechanismsView({ snapshot, debug }: { snapshot: UnifiedSnapshot; debug: ProviderDebug }) {
  const groups = useMemo(() => buildGroups(snapshot, debug), [snapshot, debug]);
  return <div className="unified-shell debug-shell"><CompactSummary snapshot={snapshot} /><TraceWorkbench groups={groups} initialFilters={['code-tools']} /></div>;
}

export function TimelineView({ snapshot, debug }: { snapshot: UnifiedSnapshot; debug: ProviderDebug }) {
  const groups = useMemo(() => buildGroups(snapshot, debug), [snapshot, debug]);
  return <div className="unified-shell debug-shell"><CompactSummary snapshot={snapshot} /><TraceWorkbench groups={groups} initialFilters={[]} timeline /></div>;
}
