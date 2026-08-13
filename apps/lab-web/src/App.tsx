import { type ChangeEvent, useEffect, useMemo, useRef, useState } from 'react';
import {
  AppShell, Alert, Badge, Button, Divider, Group, Select, Tabs, Text,
} from '@mantine/core';
import { useVirtualizer } from '@tanstack/react-virtual';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { python } from '@codemirror/lang-python';
import { vscodeDark } from '@uiw/codemirror-theme-vscode';
import { EditorView } from '@codemirror/view';
import {
  Folder, Workflow, FileJson2, Bot, Database, Play, HardDrive,
  GitFork, Radio, Layers, RefreshCw, Eye, ShieldCheck, PackageCheck, Copy, Ban,
} from 'lucide-react';
import { acceptanceSource } from 'virtual:pysolate-demo';
import { type CheckpointMetadata, type MechanismGroup, type TraceAdapterEvent, type TraceNode, buildTraceNodes, collectCheckpointMetadata } from './trace';
import { type LabDataset, type LabRun, validateDataset } from './debuggerData';

type RunSource = 'recorded';

type RunOption = {
  key: string;
  source: RunSource;
  label: string;
  run: LabRun;
  trace: TraceNode[];
  checkpoints: Record<string, CheckpointMetadata>;
};

const mechanismPresentation: Record<MechanismGroup, { label: string; color: string; icon: typeof Workflow }> = {
  'run-lifecycle': { label: 'Run lifecycle', color: 'gray', icon: Play },
  'guest-lifecycle': { label: 'Guest runtime', color: 'blue', icon: Bot },
  workspace: { label: 'Private workspace', color: 'indigo', icon: HardDrive },
  streaming: { label: 'Streaming', color: 'cyan', icon: Radio },
  fanout: { label: 'Fan-out', color: 'violet', icon: GitFork },
  cache: { label: 'Cache', color: 'orange', icon: Database },
  'single-flight': { label: 'Single-flight', color: 'grape', icon: Copy },
  'wait-resume': { label: 'Wait / resume', color: 'yellow', icon: RefreshCw },
  observation: { label: 'Observation', color: 'teal', icon: Eye },
  oracle: { label: 'Oracle', color: 'green', icon: ShieldCheck },
  prepared: { label: 'Prepared state', color: 'lime', icon: PackageCheck },
  cow: { label: 'Linux COW', color: 'pink', icon: Layers },
  cancellation: { label: 'Cancellation', color: 'red', icon: Ban },
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
    checkpoints: collectCheckpointMetadata(run.trace),
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

function TraceNodeRow({
  node,
  active,
  expanded,
  hasChildren,
  onSelect,
  onToggle,
}: {
  node: TraceNode;
  active: boolean;
  expanded: boolean;
  hasChildren: boolean;
  onSelect: () => void;
  onToggle: () => void;
}) {
  const presentation = mechanismPresentation[node.group];
  const Icon = node.synthetic && node.id === 'run' ? Workflow : presentation.icon;
  return (
    <div className={`trace-row ${active ? 'active' : ''} ${node.synthetic ? 'trace-group-row' : 'trace-event-row'}`} style={{ paddingLeft: 10 + node.depth * 18 }} data-testid="trace-node" data-node-id={node.id} data-node-kind={node.synthetic ? 'group' : 'event'}>
      <span className="trace-branch">
        {hasChildren ? (
          <button
            className="trace-toggle"
            type="button"
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${node.title}`}
            onClick={onToggle}
          >
            {expanded ? '▾' : '▸'}
          </button>
        ) : (
          <span className="trace-dot" />
        )}
      </span>
      <button className="trace-main" type="button" onClick={onSelect}>
        <div className="theme-icon" style={{ width: 24, height: 24, color: `var(--mantine-color-${presentation.color}-5)` }}><Icon size={13} /></div>
        <span className="trace-copy">
          <span className="trace-title" data-testid="trace-node-title">{node.synthetic ? (node.id === 'run' ? node.title : presentation.label) : node.title}</span>
          <span className="trace-summary">{node.summary}</span>
        </span>
        <span className="trace-tail">
          <Badge color={presentation.color} variant="light" size="xs">{node.synthetic ? presentation.label : node.kind.replaceAll('_', ' ')}</Badge>
          <small>{node.duration}</small>
        </span>
      </button>
    </div>
  );
}

function TracePanel({
  trace,
  activeId,
  onSelect,
}: {
  trace: TraceNode[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const rootIds = trace.filter((node) => !node.parent).map((node) => node.id);
  const [expanded, setExpanded] = useState(new Set<string>(rootIds));
  const byId = useMemo(() => new Map(trace.map((node) => [node.id, node])), [trace]);
  const children = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const node of trace) {
      if (node.parent) {
        const list = map.get(node.parent) ?? [];
        list.push(node.id);
        map.set(node.parent, list);
      }
    }
    return map;
  }, [trace]);

  useEffect(() => {
    setExpanded(new Set(trace.filter((node) => !node.parent).map((node) => node.id)));
  }, [trace]);

  const visible = useMemo(() => {
    return trace.filter((node) => {
      let parent = node.parent;
      while (parent) {
        if (!expanded.has(parent)) {
          return false;
        }
        parent = byId.get(parent)?.parent;
      }
      return true;
    });
  }, [byId, expanded, trace]);

  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 52,
    overscan: 8,
  });

  return (
    <section className="panel trace-panel" aria-label="Run trace">
      <div className="panel-heading">
        <Group gap={8}><Workflow size={16} /><Text fw={700} size="sm">Run trace</Text></Group>
        <Badge variant="outline" color="gray">{trace.filter((node) => !node.synthetic).length} events · {trace.filter((node) => node.synthetic && node.id !== 'run').length} groups</Badge>
      </div>
      <div className="trace-toolbar">
        <Button size="compact-xs" variant="subtle" onClick={() => setExpanded(new Set(trace.map((node) => node.id)))}>Expand all</Button>
        <Button size="compact-xs" variant="subtle" onClick={() => setExpanded(new Set(rootIds))}>Collapse</Button>
      </div>
      <div ref={parentRef} className="trace-scroll">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((virtualItem) => {
            const node = visible[virtualItem.index];
            if (!node) {
              return null;
            }
            const hasChildren = !!children.get(node.id)?.length;
            const rowChildren = children.get(node.id) ?? [];
            return (
              <div
                key={node.id}
                style={{ position: 'absolute', transform: `translateY(${virtualItem.start}px)`, width: '100%' }}
              >
                <TraceNodeRow
                  node={node}
                  active={node.id === activeId}
                  expanded={expanded.has(node.id)}
                  hasChildren={rowChildren.length > 0}
                  onSelect={() => onSelect(node.id)}
                  onToggle={() => setExpanded((current) => {
                    const next = new Set(current);
                    if (next.has(node.id)) {
                      next.delete(node.id);
                    } else {
                      next.add(node.id);
                    }
                    return next;
                  })}
                />
              </div>
            );
          })}
        </div>
      </div>
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
  const sourceText = acceptanceSource;
  const sourceLineCount = sourceText.split('\n').length;
  const sourceLabel = `Bundled public acceptance harness · complete runScenarioAllExecution function · ${sourceLineCount} lines`;

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
          <Tabs.Tab value="source" leftSection={<FileJson2 size={14} />}>Code</Tabs.Tab>
          <Tabs.Tab value="context" leftSection={<Database size={14} />}>Scenario</Tabs.Tab>
          <Tabs.Tab value="io" leftSection={<Database size={14} />}>Input / output</Tabs.Tab>
          <Tabs.Tab value="details" leftSection={<Folder size={14} />}>Recorded event</Tabs.Tab>
          <Tabs.Tab value="checkpoint" leftSection={<Workflow size={14} />}>Checkpoint</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="source" className="tab-body source-tab">
          <div className="source-context">
            <Text size="xs" c="dimmed">{sourceLabel}</Text>
            <Badge color="violet" variant="light">
              SOURCE-BOUND · NOT RUNTIME-CAPTURED
            </Badge>
          </div>
          <CodeMirror
            value={sourceText}
            height="100%"
            theme={vscodeDark}
            extensions={[python(), EditorView.lineWrapping]}
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
  checkpoints,
}: {
  run: LabRun;
  node: TraceNode;
  checkpoints: Record<string, CheckpointMetadata>;
}) {
  const [selectedCheckpoint, setSelectedCheckpoint] = useState('');
  const options = useMemo(() => Object.values(checkpoints), [checkpoints]);

  useEffect(() => {
    if (node.checkpoint && checkpoints[node.checkpoint]) {
      setSelectedCheckpoint(node.checkpoint);
      return;
    }
    setSelectedCheckpoint(options[0]?.identity ?? '');
  }, [node.checkpoint, options, checkpoints]);

  const selected = selectedCheckpoint ? checkpoints[selectedCheckpoint] : undefined;

  return (
    <section className="panel fs-panel" aria-label="Recorded identities">
      <div className="panel-heading">
        <Group gap={8}><Folder size={16} /><Text fw={700} size="sm">Recorded identities</Text></Group>
        <Badge color="teal" variant="light">digests only</Badge>
      </div>
      <div className="checkpoint-bar">
        <div>
          <Text size="xs" c="dimmed">Selected checkpoint identity</Text>
          <Text fw={700} size="sm">{selected ? selected.identity : 'none'}</Text>
        </div>
        <code>{selected ? selected.status : '-'}</code>
      </div>
      <div className="fs-tree">
        <Select
          data={options.map((item) => ({
            value: item.identity,
            label: `${item.identity.slice(0, 24)} (${item.status}, seq ${item.sequence})`,
          }))}
          value={selected?.identity ?? ''}
          onChange={(value) => setSelectedCheckpoint(value ?? '')}
          placeholder="Select checkpoint"
          size="xs"
          disabled={!options.length}
        />
      </div>
      <Divider />
      <div className="file-preview">
        <Text size="xs" c="dimmed">Captured references (digests)</Text>
        <JsonViewer label="run.refs" value={run.refs} />
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
        setDatasetSummary(`Showing ${recorded.length} all-on benchmark runs · ${parsed.runs.length} evidence rows retained`);
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
    setActiveNodeId(selectedRun?.trace[0]?.id ?? '');
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
      setDatasetSummary(`Showing ${recorded.length} all-on benchmark runs · ${parsed.runs.length} evidence rows retained`);
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
            <Button size="compact-xs" onClick={onUpload}>Load v2 JSON</Button>
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
              <Text size="xs" c="dimmed">Per-run recorded traces and captured identities</Text>
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
            <Button size="compact-xs" onClick={onUpload}>Load v2 JSON</Button>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json"
              style={{ display: 'none' }}
              onChange={handleLoad}
            />
            <div style={{ width: 190 }}>
              <Text size="xs" c="dimmed">{recordedRuns.length} all-on runs</Text>
              <Text size="xs" c="dimmed">{datasetSummary}</Text>
              {datasetError && <Text size="xs" c="red">{datasetError}</Text>}
            </div>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main className="app-main">
        <TracePanel trace={selectedRun.trace} activeId={activeNodeId} onSelect={setActiveNodeId} />
        <Divider orientation="vertical" />
        <Inspector node={selectedNode ?? selectedRun.trace[0]} run={selectedRun.run} />
        <Divider orientation="vertical" />
        <FilesystemPanel key={selectedRun.key} run={selectedRun.run} node={selectedNode ?? selectedRun.trace[0]} checkpoints={selectedRun.checkpoints} />
      </AppShell.Main>
    </AppShell>
  );
}
