import { useEffect, useState } from 'react';
import { CampaignView, MechanismsView, TimelineView } from './UnifiedLab';
import { loadUnifiedSnapshot, type UnifiedSnapshot } from './unifiedCampaignData';
import './styles.css';

function LabMark() {
  return <svg aria-hidden="true" fill="none" height="17" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.7" viewBox="0 0 24 24" width="17"><path d="m12 3 1.7 5.3L19 10l-5.3 1.7L12 17l-1.7-5.3L5 10l5.3-1.7z" /><path d="M19 16v5M21.5 18.5h-5M5 3v3M6.5 4.5h-3" /></svg>;
}

export default function App() {
  const [snapshot, setSnapshot] = useState<UnifiedSnapshot | null>(null);
  const [surface, setSurface] = useState<'campaign' | 'mechanisms' | 'timeline'>('campaign');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    loadUnifiedSnapshot().then((result) => { if (active) setSnapshot(result); }).catch((reason: unknown) => {
      if (active) setError(reason instanceof Error ? reason.message : String(reason));
    });
    return () => { active = false; };
  }, []);

  if (error) return <main className="load-state"><h1>Lab data rejected</h1><p>{error}</p></main>;
  if (!snapshot) return <main className="load-state"><p>Loading unified campaign evidence…</p></main>;

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="#top" aria-label="Pysolate Lab home"><span className="brand-mark" aria-hidden="true"><LabMark /></span><span>Pysolate Lab</span></a>
        <nav className="surface-nav" aria-label="Lab views">
          <button aria-pressed={surface === 'campaign'} onClick={() => setSurface('campaign')} type="button">Campaign</button>
          <button aria-pressed={surface === 'mechanisms'} onClick={() => setSurface('mechanisms')} type="button">Mechanisms</button>
          <button aria-pressed={surface === 'timeline'} onClick={() => setSurface('timeline')} type="button">Timeline</button>
        </nav>
      </header>
      <main id="top">
        {surface === 'campaign' ? <CampaignView snapshot={snapshot} /> : surface === 'mechanisms' ? <MechanismsView snapshot={snapshot} /> : <TimelineView snapshot={snapshot} />}
      </main>
    </div>
  );
}
