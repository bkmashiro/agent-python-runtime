import { useEffect, useMemo, useState } from 'react';
import { Activity, ArrowLeft, ShieldCheck } from 'lucide-react';
import { loadCampaignProjection, type CampaignProgram, type CampaignProjection } from './campaignData';

function short(value: string) {
  return value.length > 18 ? `${value.slice(0, 11)}…${value.slice(-6)}` : value;
}

function ProgramCard({ program, selected, onSelect }: { program: CampaignProgram; selected: boolean; onSelect: () => void }) {
  return (
    <button className={`campaign-program ${selected ? 'selected' : ''}`} onClick={onSelect}>
      <span>{program.id}</span>
      <b>{program.execution.kind.replaceAll('_', ' ')}</b>
      <small>+{program.release_offset_ms} ms · {program.disposition}</small>
    </button>
  );
}

function CampaignLanes({ data, onSelect }: { data: CampaignProjection; onSelect: (id: string) => void }) {
  const maxNS = Math.max(1, ...data.walkthrough_events.map((event) => event.at_ns));
  return (
    <section className="campaign-panel campaign-lanes">
      <header><div><p className="eyebrow">QUALIFIED REPETITION 0 · LINEAR WALL TIME</p><h2>Campaign wall-time lanes</h2></div><span>release → physical work → terminal</span></header>
      <div className="lane-axis"><span>0 s</span><span>{(maxNS / 2e9).toFixed(1)} s</span><span>{(maxNS / 1e9).toFixed(1)} s</span></div>
      <div className="lane-list">
        {data.programs.map((program) => {
          const events = data.walkthrough_events.filter((event) => event.program_id === program.id);
          const ended = new Map(events.filter((event) => event.type === 'physical.ended').map((event) => [event.physical_execution_id, event]));
          const intervals = events.filter((event) => event.type === 'physical.started').map((start) => ({ start, end: ended.get(start.physical_execution_id) })).filter((value) => value.end);
          const mechanisms = events.filter((event) => /^(workspace|workflow|verification|sharing|authority|delegation)\./.test(event.type));
          const terminal = events.find((event) => event.type === 'logical.terminal');
          return <div className="campaign-lane" key={program.id}>
            <button onClick={() => onSelect(program.id)}>{program.id}</button>
            <div className="lane-track">
              <i className="release-dot" style={{ left: `${program.release_offset_ms * 1e6 / maxNS * 100}%` }} title={`released +${program.release_offset_ms} ms`} />
              {intervals.map(({ start, end }) => <i key={start.physical_execution_id} className="physical-bar" style={{ left: `${start.at_ns / maxNS * 100}%`, width: `${Math.max(0.35, ((end!.at_ns - start.at_ns) / maxNS * 100))}%` }} title={`${start.physical_execution_id}: ${(start.at_ns / 1e6).toFixed(2)}–${(end!.at_ns / 1e6).toFixed(2)} ms`} />)}
              {mechanisms.map((event) => <i key={event.sequence} className="mechanism-dot" style={{ left: `${event.at_ns / maxNS * 100}%` }} title={`${event.type}: ${event.reason}`} />)}
              {terminal && <i className={`terminal-dot ${program.disposition}`} style={{ left: `${terminal.at_ns / maxNS * 100}%` }} title={`${program.disposition} at ${(terminal.at_ns / 1e6).toFixed(2)} ms`} />}
            </div>
          </div>;
        })}
      </div>
      <footer><span><i className="legend physical-bar" /> physical Guest/verifier interval</span><span><i className="legend mechanism-dot" /> mechanism event</span><span><i className="legend terminal-dot" /> terminal</span></footer>
    </section>
  );
}

export default function CampaignApp() {
  const [data, setData] = useState<CampaignProjection | null>(null);
  const [error, setError] = useState('');
  const [selectedID, setSelectedID] = useState('P01');
  useEffect(() => {
    loadCampaignProjection().then(setData).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'campaign load failed'));
  }, []);
  const events = useMemo(() => data?.walkthrough_events.filter((event) => event.program_id === selectedID) ?? [], [data, selectedID]);
  const peakOccupancy = useMemo(() => {
    let active = 0;
    let peak = 0;
    for (const event of data?.walkthrough_events ?? []) {
      if (event.type === 'physical.started') active += 1;
      if (event.type === 'physical.ended') active -= 1;
      peak = Math.max(peak, active);
    }
    return peak;
  }, [data]);
  if (error) return <main className="state-page"><h1>Campaign unavailable</h1><pre>{error}</pre></main>;
  if (!data) return <main className="state-page"><h1>Loading campaign…</h1></main>;
  const reduction = data.paired.physical_reduction.median;
  const selectedProgram = data.programs.find((program) => program.id === selectedID)!;
  const percent = reduction / data.baseline.physical_executions.median * 100;
  return (
    <div className="app-shell campaign-shell">
      <header className="topbar">
        <div className="brand"><Activity size={19} /><div><span>Pysolate Lab</span><small>transparent execution campaign</small></div></div>
        <div className="session-strip"><span className="fixture-badge experiment">REAL GUEST EXPERIMENT</span><a className="lab-link" href="/"><ArrowLeft size={14} />Trajectory</a></div>
      </header>
      <main>
        <section className="hero campaign-hero">
          <div><p className="eyebrow">20 FIXED PYTHON WORKLOADS · 3 PHYSICAL SLOTS</p><h1>Authority-transparent campaign</h1><p>Every release, admission, physical execution, exact share, wait/resume, workspace verification, rejection and terminal outcome is projected from sealed real-Guest evidence.</p></div>
          <div className="hero-metrics"><article><b>{data.qualified.physical_executions.median}</b><span>qualified physical runs</span></article><article><b>{Math.abs(reduction)}</b><span>median {reduction >= 0 ? 'fewer' : 'more'} physical runs</span></article><article><b>{Math.abs(percent).toFixed(1)}%</b><span>bounded {reduction >= 0 ? 'reduction' : 'increase'}</span></article></div>
        </section>
        <section className="campaign-provenance">
          <span><ShieldCheck size={14} /> {data.source.repetitions} paired repetitions</span>
          <code>{short(data.source.artifact_sha256)}</code><code>{data.source.host.goos}/{data.source.host.goarch}</code><code>peak {peakOccupancy}/3 slots</code>
        </section>
        <section className="campaign-grid">
          <article className="campaign-panel">
            <header><div><p className="eyebrow">PAIRED RESULT</p><h2>Physical executions</h2></div><span>lower is better</span></header>
            <div className="paired-runs">
              {Array.from({ length: data.source.repetitions }, (_, repetition) => {
                const baseline = data.runs.find((run) => run.repetition === repetition && run.treatment === 'baseline')!;
                const qualified = data.runs.find((run) => run.repetition === repetition && run.treatment === 'qualified')!;
                return <div className="paired-run" key={repetition}><span>rep {repetition}</span><div><i style={{ width: `${baseline.physical_executions / 20 * 100}%` }}>{baseline.physical_executions}</i><i className="qualified" style={{ width: `${qualified.physical_executions / 20 * 100}%` }}>{qualified.physical_executions}</i></div></div>;
              })}
            </div>
            <p className="claim-boundary"><b>Can claim:</b> {data.valid_claim}</p>
            <p className="claim-boundary warning"><b>Cannot infer:</b> {data.invalid_inference}</p>
          </article>
          <article className="campaign-panel timeline-panel">
            <header><div><p className="eyebrow">QUALIFIED REPETITION 0</p><h2>{selectedID} causal flow</h2></div><span>{events.length} events</span></header>
            <div className="campaign-events">
              {events.map((event) => <div key={`${event.sequence}-${event.type}`}><time>+{(event.at_ns / 1e6).toFixed(2)} ms</time><b>{event.type.replaceAll('.', ' ')}</b><small title={event.reason || event.physical_execution_id}>{event.reason || short(event.physical_execution_id ?? '')}</small></div>)}
            </div>
            <div className="campaign-identities"><code>plan {short(selectedProgram.plan_sha256)}</code><code>grants {short(selectedProgram.grant_set_sha256)}</code><code>{selectedProgram.privacy_partition}</code><code>workspace {short(selectedProgram.workspace_fixture_sha256)}</code></div>
            <details className="campaign-contract"><summary>Projected typed contract</summary><pre>{JSON.stringify(selectedProgram, null, 2)}</pre></details>
          </article>
        </section>
        <CampaignLanes data={data} onSelect={setSelectedID} />
        <section className="campaign-programs" aria-label="Campaign programs">
          {data.programs.map((program) => <ProgramCard key={program.id} program={program} selected={selectedID === program.id} onSelect={() => setSelectedID(program.id)} />)}
        </section>
      </main>
    </div>
  );
}
