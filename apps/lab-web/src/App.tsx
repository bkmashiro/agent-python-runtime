import { type ChangeEvent, useEffect, useMemo, useRef, useState } from 'react';
import {
  AppShell, Alert, Badge, Button, Divider, Group, Tabs, Text,
} from '@mantine/core';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { python } from '@codemirror/lang-python';
import { vscodeDark } from '@uiw/codemirror-theme-vscode';
import { Decoration, EditorView } from '@codemirror/view';
import { Folder, Workflow, FileJson2, Bot, Database } from 'lucide-react';
import { type TraceAdapterEvent, type TraceNode, buildTraceNodes, buildExecutionStageTree, describeEvent } from './trace';
import { type LabDataset, type LabRun, type LabSemanticRegionGraph, validateDataset } from './debuggerData';

type RunSource = 'recorded';

type RunOption = {
  key: string;
  source: RunSource;
  label: string;
  run: LabRun;
  trace: TraceNode[];
};

function runLabel(run: LabRun): string {
  return `${run.workload_id} · ${run.treatment}`;
}

function buildRunOption(run: LabRun, source: RunSource): RunOption {
  const trace = buildTraceNodes(run.trace as ReadonlyArray<TraceAdapterEvent>, 'observed');
  return {
    key: `${source}:${run.run_id}`,
    source,
    label: runLabel(run),
    run,
    trace,
  };
}

function toInputValue(value: unknown): string {
  return `${JSON.stringify(value, null, 2)}`;
}

function JsonViewer({ value, label }: { value: unknown; label: string }) {
  const text = toInputValue(value);
  return (
    <div className="json-view">
      <div className="code-label">
        <span>{label}</span>
        <span>{new Blob([text]).size} bytes</span>
      </div>
      <CodeMirror
        value={text}
        height="100%"
        theme={vscodeDark}
        extensions={[json(), EditorView.lineWrapping]}
        editable={false}
        basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }}
      />
    </div>
  );
}

function ExecutionPanel({ run, activeId, onSelect }: { run: LabRun; activeId: string; onSelect: (id: string) => void }) {
  const [view, setView] = useState<'timeline' | 'tree'>('timeline');
  const maxEnd = Math.max(1, ...run.trace.map((event) => event.ended_millis));
  const agentOrder = ['orchestrator', ...run.scenario.child_programs.map((child) => child.id), ...run.trace.map((event) => event.agent_id).filter((id) => id !== 'orchestrator' && !run.scenario.child_programs.some((child) => child.id === id))].filter((id, index, all) => all.indexOf(id) === index);
  const fanoutStart = run.trace.find((event) => event.action === 'fanout.select' && event.outcome === 'started')?.started_millis;
  const joinTime = run.trace.find((event) => event.action === 'fanout.select' && event.outcome === 'selected')?.started_millis;
  const phaseMarkers = [['Parent', run.trace.find((event) => event.action === 'stream.begin')?.started_millis], ['Fan-out', fanoutStart], ['Join + Host checks', joinTime], ['Resume', run.trace.find((event) => event.action === 'resume.fresh')?.started_millis]] as Array<[string, number | undefined]>;
  const tree = useMemo(() => buildExecutionStageTree(run.trace as ReadonlyArray<TraceAdapterEvent>, 'observed'), [run.trace]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  useEffect(() => {
    setExpanded(new Set(['stage:run', 'stage:parallel', 'stage:branch:researcher', 'stage:branch:reviewer']));
  }, [run.run_id]);
  const byID = useMemo(() => new Map(tree.map((node) => [node.id, node])), [tree]);
  const childCount = useMemo(() => tree.reduce((counts, node) => {
    if (node.parent) counts.set(node.parent, (counts.get(node.parent) ?? 0) + 1);
    return counts;
  }, new Map<string, number>()), [tree]);
  const visibleTree = tree.filter((node) => {
    let parent = node.parent;
    while (parent) {
      if (!expanded.has(parent)) return false;
      parent = byID.get(parent)?.parent;
    }
    return true;
  });

  return (
    <section className="panel trace-panel agent-timeline" aria-label="Execution trace">
      <div className="panel-heading execution-heading">
        <Group gap={8}><Workflow size={16} /><Text fw={700} size="sm">Execution</Text></Group>
        <div className="view-switch" role="tablist" aria-label="Execution view">
          <button className={view === 'timeline' ? 'active' : ''} role="tab" aria-selected={view === 'timeline'} onClick={() => setView('timeline')}>Timeline</button>
          <button className={view === 'tree' ? 'active' : ''} role="tab" aria-selected={view === 'tree'} onClick={() => setView('tree')}>Trace tree</button>
        </div>
      </div>
      {view === 'timeline' ? <>
        <div className="timeline-axis"><span>0 ms</span><span>Recorded time →</span><span>{maxEnd.toFixed(0)} ms</span></div>
        <div className="timeline-phase-rail" aria-label="Execution phases">{phaseMarkers.map(([label, at]) => at === undefined ? null : <span key={label} style={{ left: `${(at / maxEnd) * 100}%` }}>{label}</span>)}</div>
        <div className="timeline-scroll">
          {agentOrder.map((agentID) => {
            const events = run.trace.filter((event) => event.agent_id === agentID);
            const role = events[0]?.agent_role ?? agentID;
            const lifeStart = Math.min(...events.map((event) => event.started_millis));
            const lifeEnd = Math.max(...events.map((event) => event.ended_millis));
            const runtimeClusters = agentID === 'runtime' ? [...events].sort((a, b) => a.started_millis - b.started_millis).reduce((groups, event) => {
              const current = groups.at(-1);
              if (current && event.started_millis - current[0].started_millis <= 100) current.push(event);
              else groups.push([event]);
              return groups;
            }, [] as TraceAdapterEvent[][]) : [];
            return <div className={`timeline-lane ${agentID === 'runtime' ? 'runtime-lane' : ''}`} key={agentID} data-agent-id={agentID}>
              <div className="lane-label"><strong>{agentID}</strong><span>{role}</span><small>{lifeStart.toFixed(0)}–{lifeEnd.toFixed(0)} ms</small></div>
              <div className="lane-track">
                <div className={`lane-lifecycle ${agentID === 'runtime' ? 'runtime-life' : ''}`} style={{ left: `${(lifeStart / maxEnd) * 100}%`, width: `${Math.max(0.7, ((lifeEnd - lifeStart) / maxEnd) * 100)}%` }} />
                {fanoutStart !== undefined && <i className="causal-marker fanout-marker" style={{ left: `${(fanoutStart / maxEnd) * 100}%` }} title="Fan-out: child branches started" />}
                {joinTime !== undefined && <i className="causal-marker join-marker" style={{ left: `${(joinTime / maxEnd) * 100}%` }} title="Join: child branches completed" />}
                {agentID === 'runtime' ? runtimeClusters.map((cluster) => {
                  const first = cluster[0];
                  const phase = describeEvent(first).phase;
                  const actions = new Set(cluster.map((event) => event.action));
                  const label = actions.has('run.terminal') ? 'Resume + finish'
                    : actions.has('fanout.select') && cluster.some((event) => event.outcome === 'selected') ? 'Join + Host checks'
                      : actions.has('fanout.select') ? 'Parent done + fan-out'
                        : cluster.length === 1 ? describeEvent(first).label : `${phase} · ${cluster.length} events`;
                  const description = cluster.map((event) => describeEvent(event).label).join(', ');
                  const clusterLeft = (first.started_millis / maxEnd) * 100;
                  return <button key={`${first.started_millis}-${first.sequence}`} className={`runtime-cluster ${clusterLeft < 8 ? 'edge-start' : clusterLeft > 92 ? 'edge-end' : ''} ${cluster.some((event) => activeId === `event:${event.sequence}`) ? 'active' : ''}`} style={{ left: `${clusterLeft}%` }} title={`${label}: ${description}`} onClick={() => onSelect(`event:${first.sequence}`)} aria-label={`runtime cluster ${label}`}><span>{label}</span></button>;
                }) : events.map((event) => {
                  const left = (event.started_millis / maxEnd) * 100;
                  const width = Math.max(0.5, ((event.ended_millis - event.started_millis) / maxEnd) * 100);
                  const presentation = describeEvent(event);
                  return <button key={event.sequence} className={`timeline-span source-linked ${activeId === `event:${event.sequence}` ? 'active' : ''}`} style={{ left: `${left}%`, width: `${width}%` }} title={`${presentation.label}: ${presentation.description} (${event.started_millis.toFixed(1)}–${event.ended_millis.toFixed(1)} ms)`} onClick={() => onSelect(`event:${event.sequence}`)} aria-label={`${agentID} ${event.action}`}><span>{presentation.label.replace(' Python', '')}</span></button>;
                })}
              </div>
            </div>;
          })}
        </div>
        <div className="timeline-legend"><span><i className="legend-source" />Python lifetime</span><span><i className="legend-runtime" />Host event</span><span><i className="legend-fanout" />Fan-out</span><span><i className="legend-join" />Join</span></div>
      </> : <div className="trace-tree" role="tree" aria-label="Causal trace tree">
        <div className="tree-note">Recorded sequence stays inside expandable execution stages; child agents are explicit parallel branches.</div>
        {visibleTree.map((node) => {
          const event = node.rawEvent;
          const presentation = event ? describeEvent(event) : null;
          const hasChildren = (childCount.get(node.id) ?? 0) > 0;
          return <div role="treeitem" aria-level={node.depth + 1} aria-expanded={hasChildren ? expanded.has(node.id) : undefined} aria-selected={activeId === node.id} className={`trace-tree-row ${node.synthetic ? 'synthetic-tree-row' : ''} ${activeId === node.id ? 'active' : ''}`} style={{ paddingLeft: `${12 + node.depth * 22}px` }} key={node.id}>
            {hasChildren ? <button className="tree-toggle" aria-label={`${expanded.has(node.id) ? 'Collapse' : 'Expand'} ${node.title}`} onClick={() => setExpanded((current) => { const next = new Set(current); if (next.has(node.id)) next.delete(node.id); else next.add(node.id); return next; })}>{expanded.has(node.id) ? '▾' : '▸'}</button> : <span className="tree-branch">{node.depth ? '└' : '●'}</span>}
            {event && <span className="tree-sequence">{event.sequence}</span>}
            <button className="tree-select" disabled={!event} onClick={() => event && onSelect(node.id)}><span className="tree-copy"><strong>{presentation?.label ?? node.title}</strong><small>{event ? `${presentation?.phase} · ${event.agent_id} · ${event.outcome}` : node.summary}</small></span>{event && <code>{event.action}</code>}</button>
          </div>;
        })}
      </div>}
    </section>
  );
}

function SemanticRegionsPanel({ graph }: { graph: LabSemanticRegionGraph }) {
  const [selectedID, setSelectedID] = useState(graph.regions[0]?.id ?? '');
  useEffect(() => setSelectedID(graph.regions[0]?.id ?? ''), [graph]);
  const selected = graph.regions.find((region) => region.id === selectedID) ?? graph.regions[0];
  const source = graph.source_available ? graph.source ?? '' : '';
  const regionHighlight = useMemo(() => EditorView.decorations.compute(['doc'], (state) => {
    if (!selected) return Decoration.none;
    const ranges = [];
    for (let line = selected.span.start_line; line <= Math.min(selected.span.end_line, state.doc.lines); line += 1) {
      ranges.push(Decoration.line({ class: `semantic-region-line region-${selected.kind}` }).range(state.doc.line(line).from));
    }
    return Decoration.set(ranges);
  }), [selected]);
  const effectLabel = (region: LabSemanticRegionGraph['regions'][number]) => {
    if (region.effects.may_be_unknown) return 'unknown';
    if (region.effects.may_publish) return 'write';
    if (region.effects.may_observe_live) return 'read';
    return 'pure';
  };

  if (!selected) return <div className="absence-panel"><Text size="sm">No candidate regions were emitted.</Text></div>;
  return <div className="semantic-region-layout">
    <div className="semantic-region-list" aria-label="Semantic candidate region graph">
      <div className="region-graph-meta"><strong>{graph.regions.length} candidate regions</strong><small>Host-verified analysis · no execution authority</small></div>
      {graph.regions.map((region, index) => <button type="button" key={region.id} onClick={() => setSelectedID(region.id)} className={`semantic-region-card ${selected.id === region.id ? 'active' : ''}`}>
        {index > 0 && <i className="region-control-edge" aria-hidden="true" />}
        <span className="region-index">R{index + 1}</span>
        <span className="region-card-copy"><strong>lines {region.span.start_line}–{region.span.end_line}</strong><small>{region.kind.replace('_', ' ')} · {region.data_dependencies.length} data edges</small></span>
        <Badge size="xs" color={effectLabel(region) === 'pure' ? 'green' : effectLabel(region) === 'read' ? 'blue' : effectLabel(region) === 'write' ? 'orange' : 'red'}>{effectLabel(region)}</Badge>
      </button>)}
    </div>
    <div className="semantic-region-source">
      <div className="source-context"><div><Text size="xs" c="dimmed">Python candidate R{graph.regions.indexOf(selected) + 1} · lines {selected.span.start_line}–{selected.span.end_line}</Text><small className="recording-limit">Analysis overlay only — original Python remains the sole execution authority.</small></div><Badge className="region-state-badge" variant="light" color={selected.rejection_reasons.length ? 'red' : 'teal'}>{selected.rejection_reasons.length ? 'BLOCKED' : 'OPEN'}</Badge></div>
      <div className="semantic-region-code">{source ? <CodeMirror value={source} height="100%" theme={vscodeDark} extensions={[python(), EditorView.lineWrapping, regionHighlight]} editable={false} basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }} /> : <div className="semantic-source-omitted"><FileJson2 size={18} /><span>Source omitted by portable projection</span></div>}</div>
      <div className="region-detail-grid">
        <div><small>Live in</small><code>{selected.live_ins.join(', ') || '—'}</code></div>
        <div><small>Live out</small><code>{selected.live_outs.join(', ') || '—'}</code></div>
        <div><small>Data edges</small><code>{selected.data_dependencies.map((edge) => `${edge.name} ← ${edge.producer_region_id.slice(7, 15)}`).join(', ') || '—'}</code></div>
        <div><small>Capability occurrences</small><code>{selected.capability_occurrences.length || '—'}</code></div>
        <div className="region-rejections"><small>Barriers / rejection reasons</small><code>{[...selected.barriers, ...selected.rejection_reasons].join(', ') || 'none'}</code></div>
        <div className="region-consumers"><small>Region consumer decisions</small><span><b>reuse</b> not admitted · <b>pre-dispatch</b> not admitted · <b>placement</b> not admitted</span></div>
      </div>
    </div>
  </div>;
}

function Inspector({
  node,
  run,
}: {
  node: TraceNode;
  run: LabRun;
}) {
  const [tab, setTab] = useState<string | null>('source');
  const sourceRange = node.rawEvent?.source;
  const eventPresentation = node.rawEvent ? describeEvent(node.rawEvent) : null;
  const childProgram = sourceRange ? run.scenario.child_programs.find((child) => child.id === sourceRange.source_id) : undefined;
  const sourceText = childProgram?.source ?? run.scenario.guest_source;
  const sourceFile = sourceRange?.file ?? 'orchestrator.py';
  const sourceHighlight = useMemo(() => EditorView.decorations.compute(['doc'], (state) => {
    if (!sourceRange) return Decoration.none;
    const ranges = [];
    for (let line = sourceRange.start_line; line <= Math.min(sourceRange.end_line, state.doc.lines); line += 1) {
      ranges.push(Decoration.line({ class: 'source-line-linked' }).range(state.doc.line(line).from));
    }
    return Decoration.set(ranges);
  }), [sourceRange]);

  return (
    <section className="panel inspector-panel" aria-label="Selected operation inspector">
      <div className="operation-header">
        <Group gap={10}>
          <div className="theme-icon" style={{ width: 28, height: 28 }}><Bot size={16} /></div>
          <div>
            <Text fw={700} size="sm">{node.title}</Text>
            <Text size="xs" c="dimmed">{eventPresentation?.phase} · {node.rawEvent?.agent_id} · seq {node.rawEvent?.sequence}</Text>
          </div>
        </Group>
        <Group gap={6}>
          <Badge color="blue" variant="light">{run.recorded_status}</Badge>
          <Badge variant="outline" color="gray">{node.id}</Badge>
        </Group>
      </div>
      <div className="operation-summary event-explanation">
        <div><Text size="xs" c="dimmed">What happened</Text><Text size="sm">{eventPresentation?.description}</Text></div>
        <div className="event-timing"><span>{node.rawEvent ? `${node.rawEvent.started_millis.toFixed(1)}–${node.rawEvent.ended_millis.toFixed(1)} ms` : node.duration}</span><small>{node.rawEvent?.parent_span_id ? `caused by ${node.rawEvent.parent_span_id}` : 'run root'}</small></div>
      </div>
      <Tabs value={tab} onChange={setTab} className="inspector-tabs">
        <Tabs.List>
          <Tabs.Tab value="source" leftSection={<FileJson2 size={14} />}>Python</Tabs.Tab>
          {run.semantic_regions && <Tabs.Tab value="regions" leftSection={<Workflow size={14} />}>Regions</Tabs.Tab>}
          <Tabs.Tab value="context" leftSection={<Database size={14} />}>Scenario</Tabs.Tab>
          <Tabs.Tab value="conversation" leftSection={<Bot size={14} />}>LLM conversation</Tabs.Tab>
          <Tabs.Tab value="io" leftSection={<Database size={14} />}>Input / output</Tabs.Tab>
          <Tabs.Tab value="details" leftSection={<Folder size={14} />}>Recorded event</Tabs.Tab>
          <Tabs.Tab value="checkpoint" leftSection={<Workflow size={14} />}>Checkpoint</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="source" className="tab-body source-tab">
          <div className="source-context">
            <div><Text size="xs" c="dimmed">{sourceRange ? `${sourceFile} · lines ${sourceRange.start_line}–${sourceRange.end_line} · ${node.rawEvent?.agent_id}` : 'No Python source range recorded for this Runtime event'}</Text>{sourceRange && <small className="recording-limit">Program execution range only — AST node / statement execution was not recorded.</small>}</div>
            <Badge color={sourceRange ? 'green' : 'gray'} variant="light">
              {sourceRange ? 'RECORDED PROGRAM RANGE' : 'NO SOURCE SPAN'}
            </Badge>
          </div>
          <CodeMirror
            value={sourceText}
            height="100%"
            theme={vscodeDark}
            extensions={[python(), EditorView.lineWrapping, sourceHighlight]}
            editable={false}
            basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }}
          />
        </Tabs.Panel>
        {run.semantic_regions && <Tabs.Panel value="regions" className="tab-body semantic-region-tab"><SemanticRegionsPanel graph={run.semantic_regions} /></Tabs.Panel>}
        <Tabs.Panel value="context" className="tab-body source-tab">
          <JsonViewer
            label="Recorded scenario metadata"
            value={{
              id: run.scenario.id,
              file_count: run.scenario.file_count,
              child_analysis_count: run.scenario.child_analysis_count,
              selected_child: run.scenario.selected_child,
              has_repeated_transformation: run.scenario.has_repeated_transformation,
              has_wait_boundary: run.scenario.has_wait_boundary,
              has_observation: run.scenario.has_observation,
            }}
          />
        </Tabs.Panel>
        <Tabs.Panel value="conversation" className="tab-body absence-panel">
          <Bot size={22} />
          <Text fw={700} size="sm">LLM conversation not recorded in this dataset</Text>
          <Text size="sm" c="dimmed">This public acceptance recording contains Guest Python, Host/runtime events, digests, and child workspace deltas. It does not contain provider turns, message roles, prompt bodies, model responses, tool-call bodies, or final answer text.</Text>
          <Text size="xs" c="dimmed">A future Harness-owned conversation trace must correlate turns to these Runtime spans without putting provider semantics inside Pysolate.</Text>
        </Tabs.Panel>
        <Tabs.Panel value="io" className="tab-body io-grid">
          <JsonViewer label="Input digest" value={node.input} />
          <JsonViewer label="Output digest" value={node.output} />
        </Tabs.Panel>
        <Tabs.Panel value="details" className="tab-body details-tab">
          <div className="detail-block">
            <Text fw={700} size="sm">Event</Text>
            <Divider my="sm" />
            <div className="detail-row"><Text size="xs" c="dimmed">Recorded action</Text><Text size="sm"><code>{node.rawEvent?.action}</code></Text></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Outcome</Text><Text size="sm">{node.rawEvent?.outcome}</Text></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Agent</Text><Text size="sm">{node.rawEvent?.agent_id} ({node.rawEvent?.agent_role})</Text></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Causal parent</Text><Text size="sm">{node.rawEvent?.parent_span_id ?? 'none'}</Text></div>
          </div>
          <div className="detail-block">
            <Text fw={700} size="sm">Trace params</Text>
            <Divider my="sm" />
            <JsonViewer label="Params" value={node.params} />
          </div>
        </Tabs.Panel>
        <Tabs.Panel value="checkpoint" className="tab-body">
          <JsonViewer label="Checkpoint metadata" value={{
            identity: node.checkpoint,
            sequence: node.parent ? `child of ${node.parent}` : 'root',
          }} />
        </Tabs.Panel>
      </Tabs>
    </section>
  );
}

function FilesystemPanel({
  run,
  node,
}: {
  run: LabRun;
  node: TraceNode;
}) {
  return (
    <section className="panel fs-panel" aria-label="Filesystem changes">
      <div className="panel-heading">
        <Group gap={8}><Folder size={16} /><Text fw={700} size="sm">Filesystem changes</Text></Group>
        <Badge color={node.rawEvent?.workspace_changes?.length ? 'teal' : 'gray'} variant="light">{node.rawEvent?.workspace_changes?.length ?? 0} paths</Badge>
      </div>
      <div className="checkpoint-bar">
        <div>
          <Text size="xs" c="dimmed">Workspace evidence</Text>
          <Text fw={700} size="sm">{node.rawEvent?.workspace_id ?? 'No workspace linked'}</Text>
          <small className="recording-limit">{node.rawEvent?.workspace_changes?.length ? 'Base snapshot → child final snapshot delta. No intermediate filesystem checkpoints were recorded.' : 'No path-level delta or complete filesystem checkpoint is attached to this event.'}</small>
        </div>
        <code>{node.rawEvent?.agent_id ?? '-'}</code>
      </div>
      <div className="fs-tree">
        {(node.rawEvent?.workspace_changes ?? []).length ? (node.rawEvent?.workspace_changes ?? []).map((change) => (
          <button className="fs-change" key={change.path} type="button">
            <Badge size="xs" color={change.kind === 'added' ? 'green' : change.kind === 'deleted' ? 'red' : 'yellow'}>{change.kind}</Badge>
            <span>{change.path}</span>
            <code>{change.size ?? 0} B</code>
          </button>
        )) : <Text size="sm" c="dimmed">No path-level change recorded for this span.</Text>}
      </div>
      <Divider />
      <div className="file-preview ref-summary">
        <Text size="xs" c="dimmed">Run evidence identities</Text>
        {run.refs.map((ref) => <div className="ref-row" key={ref.kind}><span>{ref.kind}</span><code>{ref.sha256.slice(0, 18)}…</code></div>)}
      </div>
    </section>
  );
}

function mapDatasetRuns(dataset: LabDataset): RunOption[] {
  return dataset.runs
    .filter((run) => run.treatment === 'all')
    .map((run) => buildRunOption(run, 'recorded'));
}

export default function App() {
  const [runOptions, setRunOptions] = useState<RunOption[]>([]);
  const [selectedRunId, setSelectedRunId] = useState('');
  const [activeNodeId, setActiveNodeId] = useState('');
  const [searchText, setSearchText] = useState('');
  const [datasetError, setDatasetError] = useState('');
  const [datasetSummary, setDatasetSummary] = useState('Loading public dataset...');
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    async function loadDefault() {
      try {
        const response = await fetch('/lab-data/debugger.json');
        if (!response.ok) {
          throw new Error(`dataset load failed: ${response.status}`);
        }
        const body = await response.text();
        const parsed = validateDataset(JSON.parse(body) as LabDataset);
        const recorded = mapDatasetRuns(parsed);

        setRunOptions(recorded);
        if (recorded[0]) {
          setSelectedRunId(recorded[0].key);
        }
        setDatasetSummary(`Showing ${recorded.length} public development runs`);
        setDatasetError('');
      } catch (error) {
        setRunOptions([]);
        setSelectedRunId('');
        setDatasetError(error instanceof Error ? error.message : 'could not load dataset');
        setDatasetSummary('Recorded dataset unavailable');
      }
    }
    void loadDefault();
  }, []);

  const recordedRuns = useMemo(() => runOptions.filter((run) => run.source === 'recorded'), [runOptions]);
  const filteredRecorded = useMemo(() => {
    const query = searchText.trim().toLowerCase();
    if (!query) {
      return recordedRuns;
    }
    return recordedRuns.filter((run) => `${run.run.run_id} ${run.run.workload_id} ${run.run.treatment}`.toLowerCase().includes(query));
  }, [searchText, recordedRuns]);

  const selectableRuns = useMemo(() => {
    if (!searchText.trim()) {
      return runOptions;
    }
    return filteredRecorded;
  }, [filteredRecorded, searchText]);

  useEffect(() => {
    if (!runOptions.find((run) => run.key === selectedRunId)) {
      setSelectedRunId(runOptions[0]?.key ?? '');
    }
  }, [runOptions, selectedRunId]);

  const selectedRun = runOptions.find((run) => run.key === selectedRunId) ?? null;

  useEffect(() => {
    const defaultNode = selectedRun?.trace.find((node) => (node.rawEvent?.workspace_changes?.length ?? 0) > 0)
      ?? selectedRun?.trace.find((node) => node.rawEvent?.agent_id === 'orchestrator' && node.rawEvent.source)
      ?? selectedRun?.trace[0];
    setActiveNodeId(defaultNode?.id ?? '');
  }, [selectedRun]);

  const selectedNode = useMemo(() => {
    return selectedRun?.trace.find((node) => node.id === activeNodeId) ?? selectedRun?.trace[0];
  }, [activeNodeId, selectedRun]);

  const onUpload = async () => {
    fileInputRef.current?.click();
  };

  const handleLoad = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) {
      return;
    }

    try {
      const raw = await file.text();
      const parsed = validateDataset(JSON.parse(raw) as LabDataset);
      const recorded = mapDatasetRuns(parsed);
      setRunOptions(recorded);
      if (recorded[0]) {
        setSelectedRunId(recorded[0].key);
      }
      setDatasetSummary(`Showing ${recorded.length} public development runs`);
      setDatasetError('');
    } catch (error) {
      setDatasetError(error instanceof Error ? error.message : 'Invalid dataset');
      throw error;
    } finally {
      event.target.value = '';
    }
  };

  if (!selectedRun || !selectedNode) {
    return (
      <AppShell header={{ height: 72 }} padding={0}>
        <AppShell.Header className="app-header">
          <Group h="100%" px="sm" justify="space-between" wrap="nowrap">
            <div>
              <Text fw={800} size="sm">Pysolate Lab Debugger</Text>
              <Text size="xs" c="dimmed">{datasetSummary}</Text>
            </div>
            <Button size="compact-xs" onClick={onUpload}>Load v4 JSON</Button>
            <input ref={fileInputRef} type="file" accept="application/json,.json" hidden onChange={handleLoad} />
          </Group>
        </AppShell.Header>
        <AppShell.Main className="app-main">
          <Alert color={datasetError ? 'red' : 'blue'} title={datasetError ? 'Recorded dataset rejected' : 'Loading recorded dataset'}>
            {datasetError || 'Validating per-run traces…'}
          </Alert>
        </AppShell.Main>
      </AppShell>
    );
  }

  return (
    <AppShell header={{ height: 72 }} padding={0}>
      <AppShell.Header className="app-header">
        <Group h="100%" px="sm" justify="space-between" wrap="nowrap">
          <Group gap="sm">
            <div className="theme-icon" style={{ width: 28, height: 28 }}><Workflow size={16} /></div>
            <div>
              <Text fw={800} size="sm">Pysolate Lab Debugger</Text>
              <Text size="xs" c="dimmed">Agent causality, Python source spans, and workspace diffs</Text>
            </div>
          </Group>

          <Group gap="xs" wrap="nowrap">
            <input
              data-testid="run-search"
              value={searchText}
              onChange={(event) => setSearchText(event.currentTarget.value)}
              placeholder="Search run ID, workload, treatment"
              style={{ minWidth: 240 }}
            />
            <select
              data-testid="run-select"
              value={selectedRun.key}
              onChange={(event) => {
                setSelectedRunId(event.currentTarget.value);
              }}
            >
              {selectableRuns.map((run) => (
                <option
                  key={run.key}
                  value={run.key}
                  data-testid="run-option"
                  data-run-kind={run.source}
                  data-node-count={run.trace.length}
                >
                  {run.label}
                </option>
              ))}
            </select>
            <Button size="compact-xs" onClick={onUpload}>Load v4 JSON</Button>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json"
              style={{ display: 'none' }}
              onChange={handleLoad}
            />
            <div style={{ width: 190 }}>
              <Text size="xs" c="dimmed">{recordedRuns.length} development runs</Text>
              <Text size="xs" c="dimmed">{datasetSummary}</Text>
              {datasetError && <Text size="xs" c="red">{datasetError}</Text>}
            </div>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main className="app-main">
        <ExecutionPanel run={selectedRun.run} activeId={activeNodeId} onSelect={setActiveNodeId} />
        <Divider orientation="vertical" />
        <Inspector node={selectedNode ?? selectedRun.trace[0]} run={selectedRun.run} />
        <Divider orientation="vertical" />
        <FilesystemPanel key={selectedRun.key} run={selectedRun.run} node={selectedNode ?? selectedRun.trace[0]} />
      </AppShell.Main>
    </AppShell>
  );
}
