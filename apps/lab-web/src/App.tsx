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
import { type TraceAdapterEvent, type TraceNode, buildTraceNodes } from './trace';
import { type LabDataset, type LabRun, validateDataset } from './debuggerData';

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

function AgentTimeline({ run, activeId, onSelect }: { run: LabRun; activeId: string; onSelect: (id: string) => void }) {
  const maxEnd = Math.max(1, ...run.trace.map((event) => event.ended_millis));
  const agentOrder = [
    'orchestrator',
    ...run.scenario.child_programs.map((child) => child.id),
    ...run.trace.map((event) => event.agent_id).filter((id) => id !== 'orchestrator' && !run.scenario.child_programs.some((child) => child.id === id)),
  ].filter((id, index, all) => all.indexOf(id) === index);
  return (
    <section className="panel trace-panel agent-timeline" aria-label="Agent execution timeline">
      <div className="panel-heading">
        <Group gap={8}><Workflow size={16} /><Text fw={700} size="sm">Agent execution</Text></Group>
        <Badge variant="outline" color="violet">{agentOrder.length} lanes · {run.trace.length} spans</Badge>
      </div>
      <div className="pipeline-flow" aria-label="Recorded pipeline">
        <span>Parent Python</span><b>→</b><span>Fan-out</span><b>→</b><span className="parallel-step">{run.scenario.child_programs.map((child) => child.role).join(' ∥ ')}</span><b>→</b><span>Select</span><b>→</b><span>Resume</span>
      </div>
      <div className="timeline-axis"><span>0 ms</span><span>time →</span><span>{maxEnd.toFixed(0)} ms</span></div>
      <div className="timeline-scroll">
        {agentOrder.map((agentID) => {
          const events = run.trace.filter((event) => event.agent_id === agentID);
          const role = events[0]?.agent_role ?? agentID;
          return (
            <div className="timeline-lane" key={agentID} data-agent-id={agentID}>
              <div className="lane-label"><strong>{agentID}</strong><span>{role}</span></div>
              <div className="lane-track">
                {events.map((event) => {
                  const left = (event.started_millis / maxEnd) * 100;
                  const width = Math.max(0.5, ((event.ended_millis - event.started_millis) / maxEnd) * 100);
                  const sourceLinked = !!event.source;
                  return <button
                    key={event.sequence}
                    className={`timeline-span ${sourceLinked ? 'source-linked' : ''} ${activeId === `event:${event.sequence}` ? 'active' : ''}`}
                    style={{ left: `${left}%`, width: `${width}%` }}
                    title={`${event.action} · ${event.started_millis.toFixed(1)}–${event.ended_millis.toFixed(1)} ms${event.source ? ` · ${event.source.file}:${event.source.start_line}-${event.source.end_line}` : ''}`}
                    onClick={() => onSelect(`event:${event.sequence}`)}
                    aria-label={`${agentID} ${event.action}`}
                  ><span>{sourceLinked ? event.action.replace('guest.', '').replace('agent.', '') : ''}</span></button>;
                })}
              </div>
            </div>
          );
        })}
      </div>
      <div className="timeline-legend"><span><i className="legend-source" />Python execution</span><span><i className="legend-runtime" />Runtime event</span></div>
    </section>
  );
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
            <Text size="xs" c="dimmed">{node.summary}</Text>
          </div>
        </Group>
        <Group gap={6}>
          <Badge color="blue" variant="light">{run.recorded_status}</Badge>
          <Badge variant="outline" color="gray">{node.id}</Badge>
        </Group>
      </div>
      <div className="operation-summary">
        <Text size="sm">{run.workload_id}</Text>
        <span>{node.duration}</span>
      </div>
      <Tabs value={tab} onChange={setTab} className="inspector-tabs">
        <Tabs.List>
          <Tabs.Tab value="source" leftSection={<FileJson2 size={14} />}>Python</Tabs.Tab>
          <Tabs.Tab value="context" leftSection={<Database size={14} />}>Scenario</Tabs.Tab>
          <Tabs.Tab value="io" leftSection={<Database size={14} />}>Input / output</Tabs.Tab>
          <Tabs.Tab value="details" leftSection={<Folder size={14} />}>Recorded event</Tabs.Tab>
          <Tabs.Tab value="checkpoint" leftSection={<Workflow size={14} />}>Checkpoint</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="source" className="tab-body source-tab">
          <div className="source-context">
            <Text size="xs" c="dimmed">{sourceRange ? `${sourceFile} · lines ${sourceRange.start_line}–${sourceRange.end_line} · ${node.rawEvent?.agent_id}` : 'No Python source range recorded for this Runtime event'}</Text>
            <Badge color={sourceRange ? 'green' : 'gray'} variant="light">
              {sourceRange ? 'RECORDED SOURCE LINK' : 'RUNTIME EVENT'}
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
        <Tabs.Panel value="io" className="tab-body io-grid">
          <JsonViewer label="Input digest" value={node.input} />
          <JsonViewer label="Output digest" value={node.output} />
        </Tabs.Panel>
        <Tabs.Panel value="details" className="tab-body details-tab">
          <div className="detail-block">
            <Text fw={700} size="sm">Event</Text>
            <Divider my="sm" />
            <div className="detail-row"><Text size="xs" c="dimmed">Type</Text><CodeMirror value={`\"${node.kind}\"`} height="28px" theme={vscodeDark} editable={false} /></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Outcome</Text><Text size="sm">{node.summary}</Text></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Evidence</Text><Text size="sm">{node.evidence}</Text></div>
            <div className="detail-row"><Text size="xs" c="dimmed">Run status</Text><Text size="sm">{run.recorded_status}</Text></div>
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
          <Text size="xs" c="dimmed">Workspace</Text>
          <Text fw={700} size="sm">{node.rawEvent?.workspace_id ?? 'No workspace linked'}</Text>
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
        <AgentTimeline run={selectedRun.run} activeId={activeNodeId} onSelect={setActiveNodeId} />
        <Divider orientation="vertical" />
        <Inspector node={selectedNode ?? selectedRun.trace[0]} run={selectedRun.run} />
        <Divider orientation="vertical" />
        <FilesystemPanel key={selectedRun.key} run={selectedRun.run} node={selectedNode ?? selectedRun.trace[0]} />
      </AppShell.Main>
    </AppShell>
  );
}
