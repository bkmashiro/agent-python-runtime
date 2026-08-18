import { useMemo, useState } from 'react';
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
  input?: unknown;
  output?: unknown;
  bindings: Record<string, unknown>;
};

type TraceGroup = { id: string; index: number; title: string; summary: string; facts: UnifiedFact[]; events: DebugEvent[] };
type InspectorTab = 'overview' | 'source' | 'io' | 'raw';

function seconds(value: number) { return `${(value / 1_000_000_000).toFixed(3)}s`; }
function pretty(value: unknown) { return JSON.stringify(value, null, 2); }
function actorLabel(actor: string) { return actor.replace(/^actor-/, '').replaceAll('-', ' '); }

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
    source: candidate?.executed_source,
    input: capabilityInput ?? (event.type === 'guest.start' && candidate ? { code: candidate.executed_source, candidate: candidate.id } : undefined),
    output: capabilityOutput ?? (event.type === 'guest.end' && candidate ? candidate.guest_response : event.actor_id === 'main' && event.type.startsWith('guest.') ? snapshot.main_guest_response : undefined),
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
  return [
    {
      id: 'provider-generation', index: 0, title: 'Model source generation',
      summary: 'The complete provider requests and responses that produced the selected candidate Python. This first valid attempt was used; no performance-based retry or cherry-pick occurred.',
      facts: [{ label: 'provider calls', value: '4', note: 'candidate ×2, selection, final' }, { label: 'selection rule', value: 'first valid', note: 'contract + safety + Guest oracle' }],
      events: debug.events.map((event) => providerEvent(event, debug)),
    },
    ...snapshot.phases.map((phase) => ({ ...phase, events: phase.event_ids.map((id) => byID.get(id)).filter((event): event is UnifiedEvent => Boolean(event)).map((event) => runtimeEvent(event, snapshot)) })),
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

function GroupNav({ groups, selected, onSelect }: { groups: TraceGroup[]; selected: string; onSelect: (id: string) => void }) {
  return <nav className="trace-group-nav" aria-label="Execution groups">{groups.map((group) =>
    <button aria-pressed={selected === group.id} key={group.id} onClick={() => onSelect(group.id)} type="button">
      <span>{String(group.index).padStart(2, '0')}</span><strong>{group.title}</strong><small>{group.events.length} events</small>
    </button>)}</nav>;
}

function EventTree({ events, selected, onSelect }: { events: DebugEvent[]; selected: string; onSelect: (id: string) => void }) {
  const actors = [...new Set(events.map((event) => event.actor))];
  return <div className="debug-event-tree" aria-label="Grouped execution events">{actors.map((actor) => {
    const actorEvents = events.filter((event) => event.actor === actor);
    return <section className="event-actor-group" key={actor}>
      <header><span className="actor-dot" /> <strong>{actorLabel(actor)}</strong><small>{actorEvents.length}</small></header>
      {actorEvents.map((event) => <button className={selected === event.id ? 'active' : ''} key={event.id} onClick={() => onSelect(event.id)} type="button">
        <time>{event.atNS === undefined ? event.label : `+${seconds(event.atNS)}`}</time><span>{event.type}</span><small>{event.bindings.outcome ? String(event.bindings.outcome) : event.label}</small>
      </button>)}
    </section>;
  })}</div>;
}

function EventInspector({ event }: { event: DebugEvent }) {
  const [tab, setTab] = useState<InspectorTab>('overview');
  return <aside className="debug-inspector" aria-label="Execution event inspector">
    <header><div><span className="eyebrow">{event.label} · {actorLabel(event.actor)}</span><h3>{event.type}</h3></div>{event.atNS !== undefined && <time>+{seconds(event.atNS)}</time>}</header>
    <nav aria-label="Inspector details">{(['overview', 'source', 'io', 'raw'] as InspectorTab[]).map((name) => <button aria-pressed={tab === name} key={name} onClick={() => setTab(name)} type="button">{name === 'io' ? 'Input / output' : name}</button>)}</nav>
    <div className="inspector-body">
      {tab === 'overview' && <><h4>Bindings</h4><pre>{pretty(event.bindings)}</pre></>}
      {tab === 'source' && (event.source ? <pre className="source-code">{event.source}</pre> : <p className="empty-detail">This event has no Python source body.</p>)}
      {tab === 'io' && <div className="io-grid"><section><h4>Input</h4>{event.input === undefined ? <p>—</p> : <pre>{pretty(event.input)}</pre>}</section><section><h4>Output</h4>{event.output === undefined ? <p>—</p> : <pre>{pretty(event.output)}</pre>}</section></div>}
      {tab === 'raw' && <pre>{pretty(event.raw)}</pre>}
    </div>
  </aside>;
}

function TraceWorkbench({ groups, initialGroup, timeline = false }: { groups: TraceGroup[]; initialGroup: string; timeline?: boolean }) {
  const [groupID, setGroupID] = useState(initialGroup);
  const group = groups.find((item) => item.id === groupID) ?? groups[0];
  const [selectedByGroup, setSelectedByGroup] = useState<Record<string, string>>({});
  const selectedID = selectedByGroup[group.id] ?? group.events[0]?.id ?? '';
  const event = group.events.find((item) => item.id === selectedID) ?? group.events[0];
  const setGroup = (id: string) => setGroupID(id);
  return <>
    <GroupNav groups={groups} onSelect={setGroup} selected={group.id} />
    <section className="group-context"><div><span className="eyebrow">{timeline ? 'Grouped execution timeline' : `Mechanism ${String(group.index).padStart(2, '0')}`}</span><h2>{group.title}</h2><p>{group.summary}</p></div><div className="group-facts">{group.facts.map((fact) => <div key={fact.label}><span>{fact.label}</span><strong>{fact.value}</strong><small>{fact.note}</small></div>)}</div></section>
    <div className="debug-workbench">{timeline && <div className="timeline-ruler"><span>GROUPED BY ACTOR</span><i /></div>}<EventTree events={group.events} onSelect={(id) => setSelectedByGroup((current) => ({ ...current, [group.id]: id }))} selected={selectedID} />{event && <EventInspector event={event} />}</div>
  </>;
}

export function MechanismsView({ snapshot, debug }: { snapshot: UnifiedSnapshot; debug: ProviderDebug }) {
  const groups = useMemo(() => buildGroups(snapshot, debug), [snapshot, debug]);
  return <div className="unified-shell debug-shell"><CompactSummary snapshot={snapshot} /><TraceWorkbench groups={groups} initialGroup="source-predispatch" /></div>;
}

export function TimelineView({ snapshot, debug }: { snapshot: UnifiedSnapshot; debug: ProviderDebug }) {
  const groups = useMemo(() => buildGroups(snapshot, debug), [snapshot, debug]);
  return <div className="unified-shell debug-shell"><CompactSummary snapshot={snapshot} /><TraceWorkbench groups={groups} initialGroup="provider-generation" timeline /></div>;
}
