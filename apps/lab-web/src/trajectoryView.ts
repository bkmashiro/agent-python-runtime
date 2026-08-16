import type { TrajectoryEvent, TrajectoryExport } from './trajectoryData';

export type InspectorTab = 'Overview' | 'Input' | 'Output' | 'Code' | 'Timeline' | 'Workspace' | 'Evidence' | 'Raw';

export interface CausalNode {
  id: string;
  kind: 'session' | 'phase' | 'call' | 'event';
  title: string;
  eyebrow: string;
  summary: string;
  status?: string;
  eventIDs: string[];
  children: CausalNode[];
}

const setupTypes = new Set(['trace.started', 'authority.snapshot']);
const completionTypes = new Set(['resource.sample', 'trace.ended']);
const preparationPrefixes = ['model.', 'subagent.'];

function node(id: string, kind: CausalNode['kind'], title: string, eyebrow: string, summary: string, events: TrajectoryEvent[], children: CausalNode[] = [], status?: string): CausalNode {
  return {
    id, kind, title, eyebrow, summary,
    status: status ?? [...events].reverse().find((event) => event.status)?.status,
    eventIDs: events.map((event) => event.event_id),
    children,
  };
}

function unique(events: TrajectoryEvent[]) {
  return [...new Map(events.map((event) => [event.event_id, event])).values()].sort((left, right) => left.ordinal - right.ordinal);
}

export function buildCausalTree(trajectory: TrajectoryExport): CausalNode {
  const preparation = trajectory.events.filter((event) => preparationPrefixes.some((prefix) => event.type.startsWith(prefix)) || (event.type === 'source.document' && event.ordinal < (trajectory.events.find((candidate) => candidate.type === 'trace.started')?.ordinal ?? Number.MAX_SAFE_INTEGER)));
  const attempts = trajectory.events.filter((event) => event.type === 'execution.attempt');
  const setup = trajectory.events.filter((event) => setupTypes.has(event.type) || (event.type === 'execution.attempt' && event.payload.status === 'started'));
  const completion = trajectory.events.filter((event) => completionTypes.has(event.type) || (event.type === 'execution.attempt' && event.payload.status !== 'started'));
  const workspace = trajectory.events.filter((event) => event.type.startsWith('workspace.'));

  const callIDs = [...new Set(trajectory.events.map((event) => event.tool_call_id).filter((value): value is string => !!value))];
  const callNodes = callIDs.map((callID) => {
    const direct = trajectory.events.filter((event) => event.tool_call_id === callID);
    const receiptIDs = new Set(direct.map((event) => event.payload.receipt_id).filter((value): value is string => typeof value === 'string'));
    const sourceDecisions = trajectory.events.filter((event) => event.type === 'source.decision' && receiptIDs.has(String(event.payload.receipt_id ?? '')));
    const occurrenceIDs = new Set(sourceDecisions.map((event) => event.payload.occurrence_id).filter((value): value is string => typeof value === 'string'));
    const occurrences = trajectory.events.filter((event) => event.type === 'source.occurrence' && occurrenceIDs.has(String(event.payload.occurrence_id ?? '')));
    const occurrenceEventIDs = new Set(occurrences.map((event) => event.event_id));
    const documentIDs = new Set(occurrences.map((event) => event.payload.document_id));
    const related = unique(trajectory.events.filter((event) => direct.includes(event) || sourceDecisions.includes(event) || occurrences.includes(event) ||
      documentIDs.has(event.payload.document_id) || event.parent_event_ids?.some((id) => occurrenceEventIDs.has(id))));
    const tool = related.find((event) => event.type === 'tool.decision');
    const source = related.find((event) => event.type === 'source.decision');
    const title = `${tool?.tool_name ?? 'Capability'} · ${source?.payload.claim_level === 'source_bound' ? 'source bound' : 'Host effect'}`;
    const terminalEffect = [...related].reverse().find((event) => event.type === 'effect.transition' && !['intent', 'started'].includes(String(event.payload.state)));
    return node(`call:${callID}`, 'call', title, 'Tool / Run step', `${related.length} typed records · ${source?.payload.admitted === true ? 'admitted' : tool?.payload.broker_outcome ?? 'recorded'}`, related, [], source?.payload.admitted === true ? 'admitted' : terminalEffect?.status);
  });

  const consumed = new Set([...preparation, ...setup, ...completion, ...workspace, ...callNodes.flatMap((item) => eventsForNode(trajectory, item))].map((event) => event.event_id));
  const remainder = trajectory.events.filter((event) => !consumed.has(event.event_id));
  const runChildren = [
    setup.length ? node('run:setup', 'phase', 'Execution setup', 'Host-frozen authority', `${setup.length} records · fresh attempt and authority snapshot`, setup) : null,
    ...callNodes,
    workspace.length ? node('run:workspace', 'phase', 'Workspace result', 'Terminal filesystem truth', `${workspace.length} records · recorded dispositions only`, workspace) : null,
    completion.length ? node('run:completion', 'phase', 'Run completion', 'Terminal evidence', `${completion.length} records · completion and resource availability`, completion) : null,
    remainder.length ? node('run:other', 'phase', 'Additional evidence', 'Recorded atoms', `${remainder.length} records`, remainder) : null,
  ].filter((value): value is CausalNode => value !== null);
  const runEvents = unique([...setup, ...callNodes.flatMap((item) => eventsForNode(trajectory, item)), ...workspace, ...completion, ...remainder]);
  const children = [
    preparation.length ? node('phase:preparation', 'phase', 'Child preparation', 'Context / source / workspace', `${preparation.length} records · independent context, runtime and workspace planes`, preparation) : null,
    runEvents.length ? node('phase:run', 'phase', 'Source-bound run', 'Fresh physical execution', `${attempts[0]?.run_id ?? trajectory.header.root_execution_id} · ${runEvents.length} records`, runEvents, runChildren) : null,
  ].filter((value): value is CausalNode => value !== null);
  return node('session:root', 'session', 'Real source-bound session', 'Session', `${trajectory.events.length} canonical records · ${trajectory.profile}`, trajectory.events, children);
}

export function eventsForNode(trajectory: TrajectoryExport, selected: CausalNode): TrajectoryEvent[] {
  const ids = new Set(selected.eventIDs);
  return trajectory.events.filter((event) => ids.has(event.event_id));
}

export function eventNode(event: TrajectoryEvent): CausalNode {
  return node(`event:${event.event_id}`, 'event', readableType(event.type), 'Evidence record', `#${event.ordinal} · ${event.source}`, [event]);
}

export function inspectorTabs(events: TrajectoryEvent[]): InspectorTab[] {
  const types = new Set(events.map((event) => event.type));
  const tabs: InspectorTab[] = ['Overview'];
  if ([...types].some((type) => type === 'model.context' || type === 'tool.decision')) tabs.push('Input');
  if ([...types].some((type) => type === 'model.output' || type === 'tool.decision' || type === 'effect.transition')) tabs.push('Output');
  if ([...types].some((type) => type.startsWith('source.') || type === 'tool.decision')) tabs.push('Code');
  tabs.push('Timeline');
  if ([...types].some((type) => type.startsWith('workspace.') || type === 'subagent.workspace' || type === 'subagent.runtime')) tabs.push('Workspace');
  tabs.push('Evidence', 'Raw');
  return tabs;
}

export function relatedEvents(trajectory: TrajectoryExport, selected: TrajectoryEvent): TrajectoryEvent[] {
  const parentIDs = new Set(selected.parent_event_ids ?? []);
  const receipt = typeof selected.payload.receipt_id === 'string' ? selected.payload.receipt_id : undefined;
  const occurrence = typeof selected.payload.occurrence_id === 'string' ? selected.payload.occurrence_id : undefined;
  return trajectory.events.filter((event) => {
    if (event.event_id === selected.event_id) return false;
    return parentIDs.has(event.event_id) || event.parent_event_ids?.includes(selected.event_id) || event.parent_event_ids?.some((id) => parentIDs.has(id)) ||
      (!!selected.tool_call_id && event.tool_call_id === selected.tool_call_id) ||
      (!!receipt && event.payload.receipt_id === receipt) ||
      (!!occurrence && event.payload.occurrence_id === occurrence);
  }).sort((left, right) => left.ordinal - right.ordinal);
}

export function actorLane(event: TrajectoryEvent): string {
  if (event.type.startsWith('model.')) return 'Model';
  if (event.type.startsWith('subagent.')) return 'Subagent Host';
  if (event.type.startsWith('source.')) return 'Pysolate Guest';
  if (event.type.startsWith('workspace.')) return 'Workspace Host';
  if (event.type.startsWith('effect.') || event.type === 'tool.decision') return 'Broker / Tool';
  return 'Runtime Host';
}

export function readableType(type: string) {
  return type.split('.').map((part) => part.replaceAll('_', ' ')).join(' · ');
}

export function decodeNestedJSON(value: unknown, limits: { maxDepth?: number; maxNodes?: number } = {}): unknown {
  const maxDepth = limits.maxDepth ?? 8;
  const maxNodes = limits.maxNodes ?? 1_000;
  let nodes = 0;
  const visit = (current: unknown, depth: number): unknown => {
    nodes += 1;
    if (nodes > maxNodes || depth > maxDepth) throw new Error('bounded nested JSON limit');
    if (typeof current === 'string' && /^[\[{]/.test(current.trim())) {
      try { return visit(JSON.parse(current), depth); } catch { return current; }
    }
    if (Array.isArray(current)) return current.map((item) => visit(item, depth + 1));
    if (current !== null && typeof current === 'object') return Object.fromEntries(Object.entries(current).map(([key, item]) => [key, visit(item, depth + 1)]));
    return current;
  };
  try { return visit(value, 0); } catch { return value; }
}
