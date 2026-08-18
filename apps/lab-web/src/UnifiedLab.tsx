import { useMemo, useState } from 'react';
import type { UnifiedEvent, UnifiedSnapshot } from './unifiedCampaignData';

function seconds(value: number) { return `${(value / 1_000_000_000).toFixed(3)}s`; }
function money(value: number) { return `£${value.toFixed(2)}`; }
function short(value: string) { return value.length > 22 ? `${value.slice(0, 18)}…` : value; }

function EvidenceHeader({ snapshot }: { snapshot: UnifiedSnapshot }) {
  const control = snapshot.matched_control;
  const saving = control.median_savings_ns / control.baseline_median_ns * 100;
  return (
    <>
      <section className="unified-hero">
        <div>
          <span className="unified-kicker">ONE TYPED CAMPAIGN · LINUX / AMD64 · PROVEN</span>
          <h1>{snapshot.title}</h1>
          <p>{snapshot.summary}</p>
        </div>
        <div className="unified-result">
          <span>MAIN SELECTED</span><strong>Oxford</strong><b>{money(snapshot.final_total_gbp)}</b>
        </div>
      </section>
      <div className="unified-proofbar">
        <span><b>{control.pair_count}</b> matched pairs</span>
        <span><b>{seconds(control.baseline_median_ns)}</b> baseline</span>
        <span><b>{seconds(control.optimized_median_ns)}</b> unified</span>
        <span className="proof-win"><b>−{seconds(control.median_savings_ns)}</b> · {saving.toFixed(1)}%</span>
      </div>
    </>
  );
}

export function CampaignView({ snapshot }: { snapshot: UnifiedSnapshot }) {
  return (
    <div className="unified-shell">
      <EvidenceHeader snapshot={snapshot} />
      <section className="campaign-overview">
        <header className="unified-section-heading"><div><span>OBSERVED GUEST OUTPUTS</span><h2>Two candidates, one decision</h2></div><p>Main selects only after both fresh Guests close.</p></header>
        <div className="candidate-comparison">
          {snapshot.candidates.map((candidate) => (
            <article className={`unified-candidate ${candidate.disposition}`} key={candidate.id}>
              <div className="candidate-topline"><span>{candidate.id.toUpperCase()}</span><b>{candidate.disposition}</b></div>
              <strong>{money(candidate.total_cost_gbp)}</strong>
              <dl><div><dt>Host reads</dt><dd>{candidate.physical_issues}</dd></div><div><dt>Exact claims</dt><dd>{candidate.logical_claims}</dd></div><div><dt>Memory</dt><dd>{candidate.cow_selected ? 'private COW' : 'fallback'}</dd></div></dl>
              <code title={candidate.source_sha256}>{short(candidate.source_sha256)}</code>
            </article>
          ))}
        </div>
      </section>
      <section className="campaign-overview">
        <header className="unified-section-heading"><div><span>ONE CAUSAL CHAIN</span><h2>From source generation to fresh resume</h2></div><p>These are phases of this run—not independent benchmark cards.</p></header>
        <div className="phase-ribbon">
          {snapshot.phases.map((phase) => <div className="phase-chip" key={phase.id}><i>{String(phase.index).padStart(2, '0')}</i><span>{phase.title}</span></div>)}
        </div>
      </section>
      <Provenance snapshot={snapshot} />
    </div>
  );
}

export function MechanismsView({ snapshot }: { snapshot: UnifiedSnapshot }) {
  const [selected, setSelected] = useState(snapshot.phases[0].id);
  const phase = snapshot.phases.find((item) => item.id === selected) ?? snapshot.phases[0];
  const relevant = useMemo(() => eventsForPhase(snapshot.events, phase.id), [snapshot.events, phase.id]);
  return (
    <div className="unified-shell">
      <EvidenceHeader snapshot={snapshot} />
      <section className="mechanism-workbench">
        <nav aria-label="Unified campaign phases" className="unified-phase-nav">
          <span className="phase-nav-label">Same campaign</span>
          {snapshot.phases.map((item) => <button aria-pressed={item.id === phase.id} key={item.id} onClick={() => setSelected(item.id)} type="button"><i>{String(item.index).padStart(2, '0')}</i><span><b>{item.title}</b><small>{item.facts[0].value}</small></span></button>)}
        </nav>
        <article className="phase-detail">
          <span className="unified-kicker">PHASE {String(phase.index).padStart(2, '0')} · SAME RUN</span>
          <h2>{phase.title}</h2><p>{phase.summary}</p>
          <div className="phase-facts">{phase.facts.map((fact) => <div key={fact.label}><span>{fact.label}</span><strong>{fact.value}</strong><small>{fact.note}</small></div>)}</div>
          <div className="phase-events"><header><span>TYPED EVENT EVIDENCE</span><b>{relevant.length} records</b></header>{relevant.slice(0, 8).map((event) => <EventRow event={event} key={event.id} />)}</div>
        </article>
      </section>
      <Provenance snapshot={snapshot} />
    </div>
  );
}

export function TimelineView({ snapshot }: { snapshot: UnifiedSnapshot }) {
  const actors = ['all', 'host', 'brighton', 'oxford', 'main'] as const;
  const [actor, setActor] = useState<(typeof actors)[number]>('all');
  const [selected, setSelected] = useState(snapshot.events[0].id);
  const events = actor === 'all' ? snapshot.events : snapshot.events.filter((event) => event.actor_id === actor || actor === 'host' && ['origin', 'control'].includes(event.actor_id));
  const chosen = snapshot.events.find((event) => event.id === selected) ?? events[0];
  return (
    <div className="unified-shell">
      <EvidenceHeader snapshot={snapshot} />
      <section className="typed-timeline">
        <header className="unified-section-heading"><div><span>ACTUAL TYPED LEDGER</span><h2>Execution timeline</h2></div><p>Raw public event records; no reconstructed provider messages.</p></header>
        <div className="timeline-filters">{actors.map((item) => <button aria-pressed={actor === item} key={item} onClick={() => setActor(item)} type="button">{item}</button>)}</div>
        <div className="timeline-grid">
          <div className="event-list">{events.map((event) => <button className={chosen?.id === event.id ? 'active' : ''} key={event.id} onClick={() => setSelected(event.id)} type="button"><EventRow event={event} /></button>)}</div>
          <aside className="raw-event"><span>PUBLIC RAW EVENT</span><pre>{JSON.stringify(chosen, null, 2)}</pre></aside>
        </div>
      </section>
      <Provenance snapshot={snapshot} />
    </div>
  );
}

function EventRow({ event }: { event: UnifiedEvent }) {
  return <div className="event-row"><time>+{(event.at_ns / 1_000_000).toFixed(1)} ms</time><b>{event.type}</b><span>{event.actor_id}</span><small>{event.outcome ?? event.logical_id ?? ''}</small></div>;
}

function Provenance({ snapshot }: { snapshot: UnifiedSnapshot }) {
  return <footer className="unified-provenance"><span>Evidence {short(snapshot.provenance.evidence_sha256)}</span><span>Commit {snapshot.provenance.source_commit.slice(0, 10)}</span><span>Artifact {short(snapshot.provenance.artifact_sha256)}</span><span>{snapshot.provenance.platform}</span></footer>;
}

function eventsForPhase(events: UnifiedEvent[], phase: string) {
  const types: Record<string, string[]> = {
    'source-predispatch': ['source.', 'semantic.issue', 'request.'],
    'fresh-execution': ['source.sealed', 'guest.', 'semantic.claim'],
    'sharing-retention': ['function.'],
    'branch-resume': ['branch.', 'capsule.'],
    'memory-io': ['cow.', 'cold_io.'],
    'fail-closed': ['control.'],
  };
  return events.filter((event) => types[phase].some((prefix) => event.type.startsWith(prefix)));
}
