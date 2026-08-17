import type { TaskSnapshot } from './taskData';
import type { DayTripSnapshot } from './dayTripData';
import DayTripInspector from './DayTripInspector';

export default function TaskInspector({ snapshot }: { snapshot: DayTripSnapshot }) {
  return <DayTripInspector snapshot={snapshot} />;
}

/**
 * The historical trace remains reachable from the top-level nav, but it is
 * intentionally not presented as the exact execution trace for the public
 * day-trip projection.
 */
export function TaskTimeline({ task }: { task?: TaskSnapshot }) {
  return (
    <section className="legacy-timeline" aria-labelledby="timeline-title">
      <header className="legacy-timeline-heading">
        <div><span className="legacy-label">LEGACY · HISTORICAL RELEASE RECORDING</span><h1 id="timeline-title">Execution timeline</h1><p>{task ? `${task.title} is retained as a legacy recording.` : 'The older release-readiness recording is retained as a legacy surface, not the day-trip execution trace.'}</p></div>
        <span className="legacy-pill">LEGACY</span>
      </header>
      <div className="legacy-timeline-body"><strong>Historical data only</strong><p>This view is not a precise timeline for the public day-trip fixture. Use Inspector for the reviewed Brighton and Oxford evidence chain.</p></div>
    </section>
  );
}
