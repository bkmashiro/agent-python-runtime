import { useEffect, useMemo, useState } from 'react';
import {
  Activity, ArrowRight, Braces, CheckCircle2, ChevronRight, CircleGauge,
  Clock3, Copy, Database, GitCompareArrows, Layers3, ShieldCheck, Sparkles,
} from 'lucide-react';
import { loadLatestSnapshot, type LatestDemo, type LatestLane, type LatestSnapshot } from './latestData';
import './styles.css';

function compact(value: string) {
  return value.length > 30 ? `${value.slice(0, 15)}…${value.slice(-10)}` : value;
}

function Timeline({ lanes }: { lanes: LatestLane[] }) {
  const duration = Math.max(...lanes.map((lane) => lane.duration_ns));
  const millis = duration / 1e6;
  return (
    <section className="timeline" aria-label="Measured execution timeline">
      <header>
        <div><span>MEASURED EXECUTION</span><b>What actually overlapped</b></div>
        <small>0 → {millis < 100 ? millis.toFixed(1) : Math.round(millis)} ms</small>
      </header>
      <div className="timeline-axis"><span>0</span><span>{Math.round(millis / 2)} ms</span><span>{Math.round(millis)} ms</span></div>
      <div className="timeline-rows">
        {lanes.map((lane) => (
          <div className="timeline-row" key={lane.label}>
            <b>{lane.label}</b>
            <div className="timeline-track">
              {lane.segments.map((segment, index) => (
                <i
                  className={`segment ${segment.tone}`}
                  key={`${segment.label}-${index}`}
                  style={{ left: `${segment.start_ns / duration * 100}%`, width: `${Math.max(.5, (segment.end_ns - segment.start_ns) / duration * 100)}%` }}
                  title={`${segment.label}: ${(segment.start_ns / 1e6).toFixed(1)}–${(segment.end_ns / 1e6).toFixed(1)} ms`}
                ><span>{segment.label}</span></i>
              ))}
            </div>
          </div>
        ))}
      </div>
      <footer><span><i className="legend generation" /> source generation</span><span><i className="legend effect" /> Host READ / physical Guest</span><span><i className="legend fallback" /> independent fallback</span></footer>
    </section>
  );
}

function SourceCard({ demo }: { demo: LatestDemo }) {
  const lines = demo.source.trimEnd().split('\n');
  return (
    <section className="source-card">
      <header><div><Braces size={16} /><span><b>Authored Python</b><small>Exact public fixture used by the evidence</small></span></div><em>VISIBLE SOURCE</em></header>
      <pre>{lines.map((line, index) => <span key={index}><i>{String(index + 1).padStart(2, '0')}</i><code>{line || ' '}</code></span>)}</pre>
    </section>
  );
}

function DemoView({ demo }: { demo: LatestDemo }) {
  return (
    <div className="demo-view">
      <section className="demo-head">
        <div><p>{demo.eyebrow}</p><h1>{demo.title}</h1><span>{demo.summary}</span></div>
        <em className={demo.status}><CheckCircle2 size={14} />{demo.status === 'optimized' ? 'optimization observed' : 'safe fallback observed'}</em>
      </section>
      <section className="metric-grid" aria-label="Demo metrics">
        {demo.metrics.map((metric) => <article className={metric.tone} key={metric.label}><span>{metric.label}</span><b>{metric.value}</b><small>{metric.note}</small></article>)}
      </section>
      <section className="demo-grid">
        <SourceCard demo={demo} />
        <Timeline lanes={demo.lanes} />
      </section>
      <section className="fact-strip">
        {demo.facts.map((fact) => <article key={fact.label}><span>{fact.label}</span><b>{fact.value}</b></article>)}
      </section>
      <section className="claim"><ShieldCheck size={17} /><div><b>Claim boundary</b><p>{demo.claim_boundary}</p></div></section>
    </div>
  );
}

function Provenance({ snapshot }: { snapshot: LatestSnapshot }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(snapshot.identity);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };
  return (
    <details className="provenance">
      <summary><Database size={14} /> Evidence identities <ChevronRight size={13} /></summary>
      <div>
        <dl>
          {Object.entries(snapshot.provenance).map(([key, value]) => <div key={key}><dt>{key.replaceAll('_', ' ')}</dt><dd>{compact(value)}</dd></div>)}
        </dl>
        <button onClick={copy}><Copy size={13} />{copied ? 'copied' : 'copy snapshot identity'}</button>
      </div>
    </details>
  );
}

function Lab({ snapshot }: { snapshot: LatestSnapshot }) {
  const [selectedID, setSelectedID] = useState(snapshot.demos[0].id);
  const selected = useMemo(() => snapshot.demos.find((demo) => demo.id === selectedID) ?? snapshot.demos[0], [snapshot, selectedID]);
  return (
    <div className="lab-shell">
      <header className="topbar">
        <div className="brand"><Sparkles size={18} /><div><b>Pysolate Lab</b><span>latest mechanism workspace</span></div></div>
        <div className="top-status"><span><Activity size={12} /> REAL GUEST · VERIFIED</span><code>{compact(snapshot.identity)}</code></div>
      </header>
      <main>
        <section className="lab-intro">
          <div><p>THREE SMALL PROGRAMS · REAL EXECUTION EVIDENCE</p><h2>See the optimization, not just the claim.</h2><span>Every card shows authored Python, the measured execution shape, and the identity boundary that decides whether Pysolate may optimize.</span></div>
          <div className="headline-stats"><article><b>{snapshot.headline.real_guest_demos}</b><span>visible demos</span></article><article><b>{snapshot.headline.optimization_wins}</b><span>optimizations</span></article><article><b>{snapshot.headline.safety_controls}</b><span>fail-closed control</span></article></div>
        </section>
        <section className="lab-workspace">
          <nav aria-label="Optimization demos">
            <header><Layers3 size={15} /><span>Mechanism examples</span></header>
            {snapshot.demos.map((demo, index) => (
              <button key={demo.id} className={demo.id === selected.id ? 'selected' : ''} onClick={() => setSelectedID(demo.id)}>
                <i>{String(index + 1).padStart(2, '0')}</i><span><small>{demo.eyebrow}</small><b>{demo.title}</b><em className={demo.status}>{demo.status === 'optimized' ? 'WIN' : 'CONTROL'}</em></span><ChevronRight size={15} />
              </button>
            ))}
            <section className="cohort-boundary">
              <header><CircleGauge size={14} /><span>Natural-cohort boundary</span></header>
              <div><b>{snapshot.boundary.structurally_eligible}/{snapshot.boundary.events}</b><span>structurally eligible</span></div>
              <p>{snapshot.boundary.decision}</p>
            </section>
            <Provenance snapshot={snapshot} />
          </nav>
          <DemoView demo={selected} />
        </section>
        <footer className="lab-footer"><span><ShieldCheck size={13} /> Host-authored receipts and exact identities remain authoritative.</span><span><Clock3 size={13} /> Timelines use recorded intervals; no synthetic duration is shown.</span><span><GitCompareArrows size={13} /> Controls demonstrate why similar is not identical.</span></footer>
      </main>
    </div>
  );
}

export default function App() {
  const [snapshot, setSnapshot] = useState<LatestSnapshot | null>(null);
  const [error, setError] = useState('');
  useEffect(() => { loadLatestSnapshot().then(setSnapshot).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'latest Lab load failed')); }, []);
  if (error) return <main className="state-page"><h1>Latest evidence rejected</h1><pre>{error}</pre></main>;
  if (!snapshot) return <main className="state-page"><Sparkles size={24} /><h1>Loading latest Pysolate evidence…</h1></main>;
  return <Lab snapshot={snapshot} />;
}
