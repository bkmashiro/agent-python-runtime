import { useMemo, useRef, useState } from 'react';
import {
  Accordion, ActionIcon, AppShell, Badge, Box, Button, Code, Divider, Group as MantineGroup,
  Paper, ScrollArea, SegmentedControl, Select, Stack, Tabs, Text, ThemeIcon, Title, Tooltip,
} from '@mantine/core';
import { useVirtualizer } from '@tanstack/react-virtual';
import CodeMirror from '@uiw/react-codemirror';
import { python } from '@codemirror/lang-python';
import { json } from '@codemirror/lang-json';
import { vscodeDark } from '@uiw/codemirror-theme-vscode';
import { Decoration, EditorView } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Group, Panel, Separator } from 'react-resizable-panels';
import {
  StaticTreeDataProvider, Tree, UncontrolledTreeEnvironment, type TreeItem,
} from 'react-complex-tree';
import {
  Bot, Braces, ChevronDown, ChevronRight, CircleCheck, Clock3, Code2, Database,
  FileCode2, FileJson2, Files, FolderTree, HardDrive, Network, Play, RotateCcw,
  Search, ShieldCheck, TerminalSquare, Workflow,
} from 'lucide-react';
import { checkpoints, pythonSource, reportContent, trace, type Evidence, type FsEntry, type TraceKind, type TraceNode } from './trace';
import 'react-complex-tree/lib/style-modern.css';
import './styles.css';

const evidenceColor: Record<Evidence, string> = {
  observed: 'teal',
  'verified-example': 'blue',
  'source-bound': 'violet',
  'instrumentation-preview': 'yellow',
};
const evidenceLabel: Record<Evidence, string> = {
  observed: 'OBSERVED',
  'verified-example': 'VERIFIED RUN',
  'source-bound': 'SOURCE BOUND',
  'instrumentation-preview': 'PREVIEW',
};
const kindIcon: Record<TraceKind, typeof Bot> = {
  agent: Bot,
  source: FileCode2,
  runtime: TerminalSquare,
  'typed-call': Braces,
  'python-fs': Files,
  abi: Network,
  wasi: HardDrive,
};

function stringify(value: unknown) {
  return JSON.stringify(value ?? null, null, 2);
}

function sourceHighlight(lines?: [number, number]) {
  if (!lines) return [];
  return [EditorView.decorations.compute([], (state) => {
    const decorations: Range<Decoration>[] = [];
    for (let line = lines[0]; line <= lines[1] && line <= state.doc.lines; line += 1) {
      decorations.push(Decoration.line({ class: 'cm-active-source-line' }).range(state.doc.line(line).from));
    }
    return Decoration.set(decorations);
  })];
}

function EvidenceBadge({ evidence }: { evidence: Evidence }) {
  return <Badge color={evidenceColor[evidence]} variant="light" size="xs">{evidenceLabel[evidence]}</Badge>;
}

function TraceRow({ node, active, expanded, hasChildren, onSelect, onToggle }: {
  node: TraceNode; active: boolean; expanded: boolean; hasChildren: boolean;
  onSelect: () => void; onToggle: () => void;
}) {
  const Icon = kindIcon[node.kind];
  return (
    <div className={`trace-row ${active ? 'active' : ''}`} style={{ paddingLeft: 10 + node.depth * 18 }}>
      <span className="trace-branch">
        {hasChildren ? <button className="trace-toggle" type="button" aria-label={`${expanded ? 'Collapse' : 'Expand'} ${node.title}`} onClick={onToggle}>{expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</button> : <span className="trace-dot" />}
      </span>
      <button className="trace-main" type="button" onClick={onSelect}>
        <ThemeIcon size={24} radius="sm" variant="light" color={node.kind === 'wasi' ? 'orange' : node.kind === 'abi' ? 'cyan' : node.kind === 'typed-call' ? 'violet' : 'gray'}><Icon size={14} /></ThemeIcon>
        <span className="trace-copy">
          <span className="trace-title">{node.title}</span>
          <span className="trace-summary">{node.summary}</span>
        </span>
        <span className="trace-tail"><EvidenceBadge evidence={node.evidence} />{node.duration && <small>{node.duration}</small>}</span>
      </button>
    </div>
  );
}

function TracePanel({ activeId, onSelect }: { activeId: string; onSelect: (id: string) => void }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(() => new Set(trace.filter((item) => item.defaultExpanded).map((item) => item.id)));
  const children = useMemo(() => new Map(trace.map((node) => [node.id, trace.filter((candidate) => candidate.parent === node.id)])), []);
  const visible = useMemo(() => trace.filter((node) => {
    let parent = node.parent;
    while (parent) {
      if (!expanded.has(parent)) return false;
      parent = trace.find((candidate) => candidate.id === parent)?.parent;
    }
    return true;
  }), [expanded]);
  const virtual = useVirtualizer({ count: visible.length, getScrollElement: () => parentRef.current, estimateSize: () => 54, overscan: 8 });
  return (
    <section className="panel trace-panel" aria-label="Agent workflow trace">
      <div className="panel-heading">
        <MantineGroup gap={8}><Workflow size={16} /><Text fw={700} size="sm">AGENT WORKFLOW TRACE</Text></MantineGroup>
        <Badge variant="outline" color="gray">{trace.length} nodes</Badge>
      </div>
      <div className="trace-toolbar">
        <Button size="compact-xs" variant="subtle" leftSection={<ChevronDown size={13} />} onClick={() => setExpanded(new Set(trace.map((item) => item.id)))}>Expand all</Button>
        <Button size="compact-xs" variant="subtle" leftSection={<RotateCcw size={13} />} onClick={() => setExpanded(new Set())}>Collapse</Button>
      </div>
      <div ref={parentRef} className="trace-scroll">
        <div style={{ height: virtual.getTotalSize(), position: 'relative' }}>
          {virtual.getVirtualItems().map((row) => {
            const node = visible[row.index];
            return <div key={node.id} style={{ position: 'absolute', transform: `translateY(${row.start}px)`, width: '100%' }}><TraceRow node={node} active={node.id === activeId} expanded={expanded.has(node.id)} hasChildren={(children.get(node.id)?.length ?? 0) > 0} onSelect={() => onSelect(node.id)} onToggle={() => setExpanded((current) => { const next = new Set(current); next.has(node.id) ? next.delete(node.id) : next.add(node.id); return next; })} /></div>;
          })}
        </div>
      </div>
      <div className="trace-legend"><EvidenceBadge evidence="observed" /><EvidenceBadge evidence="verified-example" /><EvidenceBadge evidence="instrumentation-preview" /></div>
    </section>
  );
}

function JsonViewer({ value, label }: { value: unknown; label: string }) {
  const text = stringify(value);
  return <div className="json-view"><div className="code-label"><span>{label}</span><span>{new Blob([text]).size} bytes</span></div><CodeMirror value={text} height="100%" theme={vscodeDark} extensions={[json(), EditorView.lineWrapping]} editable={false} basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }} /></div>;
}

function Inspector({ node }: { node: TraceNode }) {
  const Icon = kindIcon[node.kind];
  const [tab, setTab] = useState<string | null>('source');
  return (
    <section className="panel inspector-panel" aria-label="Selected operation inspector">
      <div className="operation-header">
        <MantineGroup gap={10} wrap="nowrap">
          <ThemeIcon size={32} variant="light" color={node.kind === 'wasi' ? 'orange' : node.kind === 'typed-call' ? 'violet' : 'blue'}><Icon size={17} /></ThemeIcon>
          <div><Text size="xs" c="dimmed">SELECTED OPERATION · {node.kind.toUpperCase()}</Text><Title order={3}>{node.title}</Title></div>
        </MantineGroup>
        <MantineGroup gap={7}><EvidenceBadge evidence={node.evidence} />{node.duration && <Badge variant="light" color="gray" leftSection={<Clock3 size={11} />}>{node.duration}</Badge>}</MantineGroup>
      </div>
      <div className="operation-summary"><Text size="sm">{node.summary}</Text><Code>{node.id}</Code></div>
      <Tabs value={tab} onChange={setTab} className="inspector-tabs">
        <Tabs.List>
          <Tabs.Tab value="source" leftSection={<Code2 size={14} />}>Python source</Tabs.Tab>
          <Tabs.Tab value="io" leftSection={<Braces size={14} />}>Input / output</Tabs.Tab>
          <Tabs.Tab value="atomic" leftSection={<Network size={14} />}>Atomic details</Tabs.Tab>
          <Tabs.Tab value="evidence" leftSection={<ShieldCheck size={14} />}>Evidence</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="source" className="tab-body source-tab">
          <div className="source-context"><Text size="xs" c="dimmed">Agent-generated Guest program · repository-bound source</Text>{node.sourceLines ? <Badge variant="light" color="violet">lines {node.sourceLines[0]}–{node.sourceLines[1]}</Badge> : <Badge variant="light" color="gray">full workflow</Badge>}</div>
          <CodeMirror value={pythonSource} height="100%" theme={vscodeDark} extensions={[python(), EditorView.lineWrapping, ...sourceHighlight(node.sourceLines)]} editable={false} basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }} />
        </Tabs.Panel>
        <Tabs.Panel value="io" className="tab-body io-grid"><JsonViewer label="INPUT / ARGUMENTS" value={node.input} /><JsonViewer label="OUTPUT / RESULT" value={node.output} /></Tabs.Panel>
        <Tabs.Panel value="atomic" className="tab-body details-tab">
          <Paper withBorder className="detail-block"><Text fw={700} size="sm">Operation contract</Text><Divider my="sm" />{Object.entries(node.params ?? {}).map(([key, value]) => <div className="detail-row" key={key}><Text c="dimmed" size="xs">{key}</Text><Code>{typeof value === 'string' ? value : stringify(value)}</Code></div>)}{!node.params && <Text c="dimmed" size="sm">Select a typed call, Pysolate ABI operation, or WASI atom for decoded parameters.</Text>}</Paper>
          <Paper withBorder className="detail-block"><Text fw={700} size="sm">Lowering boundary</Text><Divider my="sm" /><Text size="sm">{node.kind === 'typed-call' ? 'Semantic typed call → agent_runtime_v1.host_call. This is the Pysolate ABI, not WASI.' : node.kind === 'python-fs' ? 'Ordinary Python filesystem API → CPython/WASI → one or more WASI filesystem atoms.' : node.kind === 'wasi' ? 'Atomic WASI Preview1 host operation.' : node.kind === 'abi' ? 'Atomic Pysolate Guest↔Host ABI operation.' : 'No lower-level atom selected.'}</Text></Paper>
        </Tabs.Panel>
        <Tabs.Panel value="evidence" className="tab-body details-tab">
          <Paper withBorder className="detail-block"><MantineGroup justify="space-between"><Text fw={700}>Evidence classification</Text><EvidenceBadge evidence={node.evidence} /></MantineGroup><Divider my="sm" /><Text size="sm">{node.evidence === 'observed' ? 'Directly emitted by the current Runtime observation/workspace contract.' : node.evidence === 'verified-example' ? 'The example was executed through the real verified Guest; this UI does not claim its fine-grained subevents were captured.' : node.evidence === 'source-bound' ? 'Bound to the exact runnable Python source, but not emitted as a Runtime trace event.' : 'Target instrumentation shape. Parameters and intermediate checkpoints require future Runtime/Harness capture.'}</Text></Paper>
          <Paper withBorder className="detail-block"><Text fw={700}>Checkpoint relation</Text><Divider my="sm" /><Text size="sm">{checkpoints[node.checkpoint].label}</Text><Code>{checkpoints[node.checkpoint].identity ?? 'identity not captured'}</Code></Paper>
        </Tabs.Panel>
      </Tabs>
    </section>
  );
}

type TreeData = { name: string; entry?: FsEntry };
function treeProvider(entries: FsEntry[]) {
  const items: Record<string, TreeItem<TreeData>> = { root: { index: 'root', isFolder: true, children: ['workspace'], data: { name: '/' } } };
  for (const entry of entries) {
    items[entry.id] = { index: entry.id, isFolder: entry.kind === 'directory', children: entries.filter((child) => child.parent === entry.id).map((child) => child.id), data: { name: entry.name, entry } };
  }
  return new StaticTreeDataProvider<TreeData>(items, (item, newName) => ({ ...item, data: { ...item.data, name: newName } }));
}

function FilesystemPanel({ node }: { node: TraceNode }) {
  const checkpoint = checkpoints[node.checkpoint];
  const [selected, setSelected] = useState<string[]>(() => {
    const firstFile = checkpoint.entries.find((item) => item.kind === 'file');
    return firstFile ? [firstFile.id] : [];
  });
  const entry = checkpoint.entries.find((item) => item.id === selected[0]);
  const provider = useMemo(() => treeProvider(checkpoint.entries), [checkpoint]);
  return (
    <section className="panel fs-panel" aria-label="Filesystem explorer">
      <div className="panel-heading"><MantineGroup gap={8}><FolderTree size={16} /><Text fw={700} size="sm">FILESYSTEM</Text></MantineGroup><EvidenceBadge evidence={checkpoint.evidence} /></div>
      <div className="checkpoint-bar"><div><Text size="xs" c="dimmed">CHECKPOINT</Text><Text fw={700} size="sm">{checkpoint.label}</Text></div><Tooltip label={checkpoint.identity ?? 'Intermediate identity is not captured'}><Code>{checkpoint.identity ?? 'no identity'}</Code></Tooltip></div>
      <div className="fs-tree">
        <UncontrolledTreeEnvironment key={checkpoint.id} dataProvider={provider} getItemTitle={(item) => item.data.name} viewState={{ 'fs-tree': { expandedItems: ['root', 'workspace', 'reports'], selectedItems: selected } }} onSelectItems={(items) => setSelected(items.map(String))} canDragAndDrop={false} canRename={false}>
          <Tree treeId="fs-tree" rootItem="root" treeLabel="Workspace files at selected checkpoint" />
        </UncontrolledTreeEnvironment>
      </div>
      <Divider />
      <div className="file-preview">
        {entry?.kind === 'file' ? <><div className="file-heading"><FileJson2 size={15} /><Text fw={700} size="sm">{entry.path}</Text></div><MantineGroup gap={6}><EvidenceBadge evidence={entry.evidence} /><Badge variant="outline" color="gray">{entry.size} bytes</Badge><Badge variant="outline" color="gray">{entry.digest}</Badge></MantineGroup><CodeMirror value={entry.content ?? ''} height="100%" theme={vscodeDark} extensions={[json(), EditorView.lineWrapping]} editable={false} basicSetup={{ lineNumbers: true, foldGutter: true }} /></> : <div className="file-empty"><Files size={30} /><Text fw={600}>{entry ? entry.path : 'Select a file'}</Text><Text size="xs" c="dimmed">Browse the checkpoint tree and inspect captured content.</Text></div>}
      </div>
    </section>
  );
}

export default function App() {
  const [activeId, setActiveId] = useState('catalog');
  const node = trace.find((item) => item.id === activeId) ?? trace[0];
  return (
    <AppShell header={{ height: 52 }} padding={0}>
      <AppShell.Header className="app-header">
        <MantineGroup justify="space-between" h="100%" px="md" wrap="nowrap">
          <MantineGroup gap={10}><ThemeIcon variant="gradient" gradient={{ from: 'cyan', to: 'violet' }}><Database size={17} /></ThemeIcon><div><Text fw={800} size="sm">Pysolate Lab</Text><Text size="xs" c="dimmed">Agent trace debugger</Text></div></MantineGroup>
          <MantineGroup gap={8}><Badge color="yellow" variant="light">INSTRUMENTATION PREVIEW</Badge><Badge color="teal" variant="light" leftSection={<CircleCheck size={11} />}>REAL EXAMPLE VERIFIED</Badge><Tooltip label="Search trace (coming with live ingestion)"><ActionIcon variant="subtle" color="gray"><Search size={16} /></ActionIcon></Tooltip><Button size="compact-sm" variant="light" leftSection={<Play size={13} />}>Replay</Button></MantineGroup>
        </MantineGroup>
      </AppShell.Header>
      <AppShell.Main className="app-main">
        <div className="preview-notice"><ShieldCheck size={14} /><span><b>Target debugger experience.</b> Typed-call results and final workspace are verified by the real example; Agent turns, ABI/WASI atoms, and intermediate checkpoints are explicitly marked preview until captured by the Runtime/Harness.</span></div>
        <Group orientation="horizontal" className="panel-group">
          <Panel defaultSize={27} minSize={19}><TracePanel activeId={activeId} onSelect={setActiveId} /></Panel>
          <Separator className="resize-handle" />
          <Panel defaultSize={46} minSize={32}><Inspector key={node.id} node={node} /></Panel>
          <Separator className="resize-handle" />
          <Panel defaultSize={27} minSize={20}><FilesystemPanel key={`${node.id}-${node.checkpoint}`} node={node} /></Panel>
        </Group>
      </AppShell.Main>
    </AppShell>
  );
}
