import { useState, type ReactNode } from 'react';
import type {
  DayTripAgent,
  DayTripApiWait,
  DayTripGroup,
  DayTripSnapshot,
  DestinationID,
} from './dayTripData';

const destinationNames: Record<DestinationID, string> = { brighton: 'Brighton', oxford: 'Oxford' };

type IconName = DayTripGroup['icon'];

function Icon({ name }: { name: IconName }) {
  const common = { fill: 'none', stroke: 'currentColor', strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, strokeWidth: 1.7, viewBox: '0 0 24 24' };
  if (name === 'inbox') return <svg aria-hidden="true" className="inline-icon" {...common}><path d="M4 5.5h16v13H4z" /><path d="M4 14h4l2 2h4l2-2h4" /><path d="M8 9h8" /></svg>;
  if (name === 'split') return <svg aria-hidden="true" className="inline-icon" {...common}><path d="M6 5v5c0 1.1.9 2 2 2h8c1.1 0 2 .9 2 2v5" /><path d="m8 3-2 2 2 2M16 17l2 2-2 2" /></svg>;
  if (name === 'terminal') return <svg aria-hidden="true" className="inline-icon" {...common}><rect height="15" rx="2" width="18" x="3" y="4.5" /><path d="m7 9 2.5 2L7 13M12 13h4" /></svg>;
  if (name === 'check') return <svg aria-hidden="true" className="inline-icon" {...common}><circle cx="12" cy="12" r="8.5" /><path d="m8 12 2.5 2.5L16 9" /></svg>;
  return <svg aria-hidden="true" className="inline-icon" {...common}><path d="M5 19V5h14v14z" /><path d="M8 9h8M8 13h5M8 17h8" /></svg>;
}

function JsonBlock({ value }: { value: unknown }) {
  return <pre className="daytrip-json">{JSON.stringify(value, null, 2)}</pre>;
}

function Disclosure({ title, children, open = false }: { title: string; children: ReactNode; open?: boolean }) {
  return <details className="daytrip-disclosure" open={open}><summary>{title}</summary><div className="daytrip-disclosure-body">{children}</div></details>;
}

function PublicInput({ snapshot }: { snapshot: DayTripSnapshot }) {
  return (
    <section className="daytrip-section" id="daytrip-input" aria-labelledby="daytrip-input-title">
      <div className="daytrip-section-heading">
        <div><span className="daytrip-kicker">01 · PUBLIC INPUT</span><h2 id="daytrip-input-title">Public input</h2></div>
        <p>{snapshot.groups[0].summary}</p>
      </div>
      <div className="daytrip-input-grid">
        <article className="daytrip-card task-summary-card">
          <span className="card-eyebrow">PUBLIC TASK</span>
          <h3>Task summary</h3>
          <p>{snapshot.input.task_summary}</p>
        </article>
        <article className="daytrip-card envelope-card">
          <span className="card-eyebrow">REVIEWED ENVELOPE</span>
          <h3>Public input envelope</h3>
          <dl className="daytrip-facts">
            <div><dt>Provider</dt><dd>{snapshot.provider.name} · {snapshot.provider.model}</dd></div>
            <div><dt>Schema</dt><dd><code>{snapshot.schema_version}</code></dd></div>
            <div><dt>Source commit</dt><dd><code>{snapshot.source_commit}</code></dd></div>
          </dl>
        </article>
      </div>
      <div className="daytrip-disclosure-stack">
        <Disclosure title="Public system instructions" open>
          <pre className="daytrip-prose">{snapshot.input.public_system_instructions}</pre>
        </Disclosure>
        <Disclosure title="3 skill bodies">
          <div className="skill-list">{snapshot.input.skills.map((skill) => <article className="skill-body" key={skill.id}><h3>{skill.id}</h3><pre className="daytrip-prose">{skill.body}</pre></article>)}</div>
        </Disclosure>
        <Disclosure title="Tool contracts">
          <div className="contract-list">{snapshot.input.tool_contracts.map((contract) => <div className="contract-row" key={contract.name}><div><strong>{contract.name}</strong><span>{contract.kind}</span></div><code>{contract.latency_ms} ms</code></div>)}</div>
        </Disclosure>
        <Disclosure title="Workspace snapshot">
          <p className="workspace-contract-label">Deterministic API fixture <code>{snapshot.input.workspace_snapshot.api_fixture.schema_version}</code></p>
          <JsonBlock value={snapshot.input.workspace_snapshot} />
        </Disclosure>
      </div>
      <div className="withheld-callout" role="note">
        <strong>Private fields withheld</strong>
        <p>Only the reviewed public projection is rendered. Raw envelopes, reasoning, credentials, private CAS paths, and unreviewed tool I/O are withheld.</p>
        <div className="withheld-tags">{snapshot.input.private_fields_withheld.map((field) => <code key={field}>{field}</code>)}</div>
      </div>
    </section>
  );
}

function CandidateCard({ agent }: { agent: DayTripAgent }) {
  const name = destinationNames[agent.id];
  return (
    <article className={`candidate-card ${agent.disposition}`} aria-labelledby={`${agent.id}-model-output`}>
      <header className="candidate-card-heading">
        <div><span className="card-eyebrow">CANDIDATE AGENT · {name.toUpperCase()}</span><h3 id={`${agent.id}-model-output`}>{name} model output</h3></div>
        <span className={`status-chip ${agent.disposition}`}>{agent.disposition}</span>
      </header>
      <p className="candidate-summary">{agent.model_output.summary}</p>
      <Disclosure title="Generated Python" open>
        <pre className="daytrip-code">{agent.model_output.python_source}</pre>
      </Disclosure>
      <div className="capture-line"><span>Provider capture</span><strong>{agent.model_output.capture}</strong></div>
    </article>
  );
}

function Candidates({ snapshot }: { snapshot: DayTripSnapshot }) {
  return (
    <section className="daytrip-section" id="daytrip-candidates" aria-labelledby="daytrip-candidates-title">
      <div className="daytrip-section-heading">
        <div><span className="daytrip-kicker">02 · INDEPENDENT BRANCHES</span><h2 id="daytrip-candidates-title">Candidate Agents</h2></div>
        <p>{snapshot.groups[1].summary}</p>
      </div>
      <div className="candidate-grid">{snapshot.agents.map((agent) => <CandidateCard agent={agent} key={agent.id} />)}</div>
    </section>
  );
}

function WaitCard({ wait }: { wait: DayTripApiWait }) {
  return (
    <li className="wait-card">
      <div className="wait-card-heading"><strong>{wait.capability}</strong><span>waited {wait.latency_ms} ms</span></div>
      <div className="wait-result"><span>Observed result</span><JsonBlock value={wait.observed} /></div>
    </li>
  );
}

function GuestRun({ agent }: { agent: DayTripAgent }) {
  const output = agent.runtime.observed_output;
  return (
    <article className="guest-run">
      <header className="guest-run-heading">
        <div><span className="card-eyebrow">{destinationNames[agent.id].toUpperCase()} · PRIVATE ATTEMPT</span><h3>{destinationNames[agent.id]} fresh Guest</h3></div>
        <span className="guest-badge">{agent.runtime.execution}</span>
      </header>
      <div className="wait-summary"><strong>3 API waits/results</strong><span>Deterministic Host capabilities, observed in order</span></div>
      <ol className="wait-list">{agent.runtime.api_waits.map((wait) => <WaitCard key={wait.capability} wait={wait} />)}</ol>
      <div className="guest-output-grid">
        <div className="guest-output-card"><span className="card-eyebrow">OBSERVED GUEST OUTPUT</span><h4>{destinationNames[output.candidate_id]} result</h4><JsonBlock value={output} /></div>
        <div className="guest-output-card sha-card"><span className="card-eyebrow">WORKSPACE CHECKPOINT</span><h4>Workspace SHA</h4><code>{agent.runtime.workspace_sha256}</code><p className="cost-line">Observed total <strong>£{output.total_cost_gbp.toFixed(2)}</strong></p></div>
      </div>
    </article>
  );
}

function Runtime({ snapshot }: { snapshot: DayTripSnapshot }) {
  return (
    <section className="daytrip-section" id="daytrip-runtime" aria-labelledby="daytrip-runtime-title">
      <div className="daytrip-section-heading">
        <div><span className="daytrip-kicker">03 · ISOLATED EXECUTION</span><h2 id="daytrip-runtime-title">Fresh Guest execution</h2></div>
        <p>{snapshot.groups[2].summary}</p>
      </div>
      <div className="guest-run-list">{snapshot.agents.map((agent) => <GuestRun agent={agent} key={agent.id} />)}</div>
    </section>
  );
}

function Decision({ snapshot }: { snapshot: DayTripSnapshot }) {
  const selected = snapshot.agents.find((agent) => agent.id === snapshot.decision.selected_candidate_id)!;
  const discarded = snapshot.agents.find((agent) => agent.id !== snapshot.decision.selected_candidate_id)!;
  return (
    <section className="daytrip-section" id="daytrip-decision" aria-labelledby="daytrip-decision-title">
      <div className="daytrip-section-heading">
        <div><span className="daytrip-kicker">04 · SYNTHESIS</span><h2 id="daytrip-decision-title">Main Agent decision</h2></div>
        <p>{snapshot.groups[3].summary}</p>
      </div>
      <article className="decision-card">
        <div className="decision-result"><span className="card-eyebrow">MODEL OUTPUT · SELECTION</span><h3>{destinationNames[selected.id]} selected</h3><p>{snapshot.decision.model_output.justification}</p></div>
        <div className="decision-disposition"><div><span>Selected</span><strong>{selected.label}</strong><em>£{selected.runtime.observed_output.total_cost_gbp.toFixed(2)}</em></div><div><span>Discarded</span><strong>{discarded.label}</strong><em>£{discarded.runtime.observed_output.total_cost_gbp.toFixed(2)}</em></div></div>
        <div className="decision-sha"><span>Selected root SHA</span><code>{snapshot.decision.selected_root_sha256}</code></div>
      </article>
    </section>
  );
}

function FinalOutput({ snapshot }: { snapshot: DayTripSnapshot }) {
  return (
    <section className="daytrip-section final-output-section" id="daytrip-output" aria-labelledby="daytrip-output-title">
      <div className="daytrip-section-heading">
        <div><span className="daytrip-kicker">05 · PUBLISHED RESULT</span><h2 id="daytrip-output-title">Final output</h2></div>
        <p>{snapshot.groups[4].summary}</p>
      </div>
      <article className="final-output-card">
        <div className="final-output-lead"><span>Main Agent final output</span><strong>£{snapshot.final_output.total_cost_gbp}</strong><b>{destinationNames[snapshot.final_output.selected_candidate_id]}</b></div>
        <div className="itinerary-copy"><span className="card-eyebrow">OBSERVED ITINERARY</span><p>{snapshot.final_output.itinerary}</p></div>
      </article>
    </section>
  );
}

export default function DayTripInspector({ snapshot }: { snapshot: DayTripSnapshot }) {
  const [activeGroup, setActiveGroup] = useState<DayTripGroup['id']>('input');
  const selectGroup = (group: DayTripGroup) => {
    setActiveGroup(group.id);
    document.getElementById(`daytrip-${group.id}`)?.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
  };
  return (
    <section className="daytrip-inspector" aria-labelledby="daytrip-title">
      <header className="daytrip-hero">
        <div className="daytrip-hero-copy"><span className="daytrip-kicker">PUBLIC DAY-TRIP FIXTURE · VERIFIED PROJECTION</span><h1 id="daytrip-title">{snapshot.title}</h1><p>{snapshot.subtitle}</p></div>
        <div className="hero-final"><span>Main Agent final output</span><strong>£{snapshot.final_output.total_cost_gbp}</strong><b>{destinationNames[snapshot.final_output.selected_candidate_id]}</b></div>
      </header>
      <div className="daytrip-provenance"><span>Public evidence</span><code>{snapshot.artifact_sha256}</code><span>{snapshot.privacy.public_projection}</span></div>
      <div className="daytrip-layout">
        <nav className="daytrip-groups" aria-label="Travel trace groups">
          <div className="daytrip-groups-label">Evidence map</div>
          {snapshot.groups.map((group, index) => <button aria-current={activeGroup === group.id ? 'step' : undefined} className={activeGroup === group.id ? 'active' : ''} key={group.id} onClick={() => selectGroup(group)} type="button"><span className="group-index">0{index + 1}</span><Icon name={group.icon} /><span className="group-copy"><strong>{group.label}</strong><small>{group.summary}</small></span></button>)}
          <div className="groups-note"><span className="inline-dot" /> Public fields only</div>
        </nav>
        <div className="daytrip-content"><PublicInput snapshot={snapshot} /><Candidates snapshot={snapshot} /><Runtime snapshot={snapshot} /><Decision snapshot={snapshot} /><FinalOutput snapshot={snapshot} /></div>
      </div>
    </section>
  );
}
