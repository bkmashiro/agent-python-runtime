import { useEffect, useMemo, useState } from 'react';
import { loadLatestSnapshot, type LatestCodeAnnotation, type LatestDemo, type LatestSnapshot } from './latestData';
import './styles.css';

function tierLabel(status: LatestDemo['status']) {
  return status === 'measured' ? 'MEASURED' : status === 'experimental' ? 'EXPERIMENTAL' : 'CONTROL';
}

function annotationForLine(demo: LatestDemo, line: number): LatestCodeAnnotation | undefined {
  return demo.annotations.find((annotation) => line >= annotation.start_line && line <= annotation.end_line);
}

function Source({ demo }: { demo: LatestDemo }) {
  const lines = demo.source.replace(/\n$/, '').split('\n');
  return (
    <section className="source-panel" aria-label="Python source">
      <div className="panel-title">Python</div>
      <div className="code-map">
        {lines.map((line, index) => {
          const lineNumber = index + 1;
          const annotation = annotationForLine(demo, lineNumber);
          return (
            <div className={`code-row ${annotation ? `annotated ${annotation.tone}` : ''}`} key={`${lineNumber}-${line}`}>
              <span className="line-number">{lineNumber}</span>
              <code>{line || ' '}</code>
              {annotation && lineNumber === annotation.start_line ? (
                <span className="code-note">
                  <b>{annotation.label}</b>
                  <span>{annotation.note}</span>
                </span>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function Execution({ demo }: { demo: LatestDemo }) {
  return (
    <section className="execution-panel" aria-label="Execution">
      <div className="panel-title">Execution</div>
      {demo.view_kind === 'timeline' ? (
        <div className="timeline" aria-label="Execution timeline">
          {demo.lanes.map((lane) => (
            <div className="timeline-lane" key={lane.label}>
              <div className="lane-label">{lane.label}</div>
              <div className="lane-track">
                {lane.segments.map((segment) => {
                  const left = (segment.start_ns / lane.duration_ns) * 100;
                  const width = ((segment.end_ns - segment.start_ns) / lane.duration_ns) * 100;
                  return (
                    <div
                      className={`segment ${segment.tone}`}
                      key={`${segment.label}-${segment.start_ns}`}
                      style={{ left: `${left}%`, width: `${Math.max(width, 1.2)}%` }}
                    >
                      <span>{segment.label}</span>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="state-flow">
          {demo.metrics.map((metric, index) => (
            <div className={`state-node ${metric.tone}`} key={metric.label}>
              <span>{metric.label}</span>
              <strong>{metric.value}</strong>
              {index < demo.metrics.length - 1 ? <i aria-hidden="true">→</i> : null}
            </div>
          ))}
        </div>
      )}
      <dl className="facts">
        {demo.facts.map((fact) => (
          <div key={fact.label}>
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function MetricStrip({ demo }: { demo: LatestDemo }) {
  return (
    <div className="metric-strip">
      {demo.metrics.map((metric) => (
        <div className={`metric ${metric.tone}`} key={metric.label}>
          <span>{metric.label}</span>
          <strong>{metric.value}</strong>
          <small>{metric.note}</small>
        </div>
      ))}
    </div>
  );
}

export default function App() {
  const [snapshot, setSnapshot] = useState<LatestSnapshot | null>(null);
  const [selectedID, setSelectedID] = useState<string>('source-prefix-overlap');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    loadLatestSnapshot().then(setSnapshot).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
  }, []);

  const selected = useMemo(() => snapshot?.demos.find((demo) => demo.id === selectedID) ?? snapshot?.demos[0], [snapshot, selectedID]);

  if (error) return <main className="load-state"><h1>Snapshot rejected</h1><p>{error}</p></main>;
  if (!snapshot || !selected) return <main className="load-state"><p>Loading mechanisms…</p></main>;

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="#top" aria-label="Pysolate Lab home">
          <span className="brand-mark">P</span>
          <span>Pysolate Lab</span>
        </a>
        <span className="topbar-label">Runtime inspection</span>
      </header>

      <main id="top">
        <section className="intro">
          <h1>Runtime mechanisms</h1>
          <p>Python and execution, side by side.</p>
        </section>

        <div className="workspace">
          <nav className="mechanism-nav" aria-label="Mechanisms">
            {snapshot.demos.map((demo, index) => (
              <button
                className={demo.id === selected.id ? 'active' : ''}
                key={demo.id}
                onClick={() => setSelectedID(demo.id)}
                type="button"
              >
                <span className="nav-index">{String(index + 1).padStart(2, '0')}</span>
                <span className="nav-copy">
                  <b>{demo.title}</b>
                  <small>{tierLabel(demo.status)}</small>
                </span>
              </button>
            ))}
          </nav>

          <article className="demo-card">
            <header className="demo-heading">
              <div>
                <span className={`demo-kind ${selected.status}`}>{selected.eyebrow} · {tierLabel(selected.status)}</span>
                <h2>{selected.title}</h2>
                <p>{selected.summary}</p>
              </div>
            </header>

            <MetricStrip demo={selected} />
            <div className="inspect-grid">
              <Source demo={selected} />
              <Execution demo={selected} />
            </div>
          </article>
        </div>
      </main>
    </div>
  );
}
