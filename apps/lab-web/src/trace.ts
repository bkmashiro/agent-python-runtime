import { benchmark, catalog, pythonSource } from 'virtual:pysolate-demo';

export type Evidence = 'observed' | 'verified-example' | 'instrumentation-preview' | 'source-bound';
export type TraceKind = 'agent' | 'source' | 'runtime' | 'typed-call' | 'python-fs' | 'abi' | 'wasi';

export interface TraceNode {
  id: string;
  parent?: string;
  depth: number;
  kind: TraceKind;
  title: string;
  summary: string;
  evidence: Evidence;
  duration?: string;
  sourceLines?: [number, number];
  input?: unknown;
  output?: unknown;
  params?: Record<string, unknown>;
  checkpoint: string;
  defaultExpanded?: boolean;
}

export interface FsEntry {
  id: string;
  name: string;
  path: string;
  parent: string | null;
  kind: 'directory' | 'file';
  size?: number;
  digest?: string;
  content?: string;
  evidence: Evidence;
}

export interface Checkpoint {
  id: string;
  label: string;
  evidence: Evidence;
  identity?: string;
  entries: FsEntry[];
}

const result = {
  case_id: 'workspace-summary',
  ranked: [
    { id: 'gamma', normalized_score: 0.1 },
    { id: 'alpha', normalized_score: 0.07 },
    { id: 'beta', normalized_score: 0.04 },
  ],
  suite_id: 'pysolate-core',
};

export const reportContent = `${JSON.stringify(result, null, 2)}\n`;
export { pythonSource };

export const trace: TraceNode[] = [
  { id: 'agent', depth: 0, kind: 'agent', title: 'Agent workflow', summary: 'Plan a bounded two-source ranking workflow', evidence: 'instrumentation-preview', input: { task: 'Rank catalog items against the benchmark quality bound and persist the report.' }, output: { placement: 'pysolate_guest', capability_plan: ['sources.demo_catalog', 'sources.benchmark_manifest'] }, checkpoint: 'initial', defaultExpanded: true },
  { id: 'source', parent: 'agent', depth: 1, kind: 'source', title: 'Generate Guest Python', summary: '04-workflow-with-workspace.py · 25 lines', evidence: 'source-bound', input: { run_id: 'example-04-workflow-with-workspace', inputs: {} }, output: { language: 'python', bytes: pythonSource.length }, checkpoint: 'initial', sourceLines: [1, 25] },
  { id: 'run', depth: 0, kind: 'runtime', title: 'Pysolate Run', summary: 'Fresh CPython/WASI Guest · frozen authority', evidence: 'verified-example', input: { max_tool_calls: 2, workspace_policy: 'discard' }, output: { status: 'ok', capability_calls: 2, receipts: 2 }, checkpoint: 'initial', defaultExpanded: true },
  { id: 'catalog', parent: 'run', depth: 1, kind: 'typed-call', title: 'sources.demo_catalog()', summary: 'Typed source read · operation 0', evidence: 'verified-example', duration: '4.1 ms', sourceLines: [6, 6], input: {}, output: catalog.items, params: { capability: 'sources.demo_catalog', operation_index: 0, grant: 'frozen', outcome: 'ok' }, checkpoint: 'after-catalog', defaultExpanded: true },
  { id: 'catalog-abi', parent: 'catalog', depth: 2, kind: 'abi', title: 'agent_runtime_v1.host_call', summary: 'Pysolate ABI · decoded request/response', evidence: 'instrumentation-preview', input: { capability: 'sources.demo_catalog', arguments: {} }, output: { items: catalog.items }, params: { request_memory: 'not captured', response_memory: 'not captured', broker_validation: ['schema', 'grant', 'budget'] }, checkpoint: 'after-catalog' },
  { id: 'manifest', parent: 'run', depth: 1, kind: 'typed-call', title: 'sources.benchmark_manifest()', summary: 'Typed source read · operation 1', evidence: 'verified-example', duration: '3.7 ms', sourceLines: [7, 7], input: {}, output: benchmark, params: { capability: 'sources.benchmark_manifest', operation_index: 1, grant: 'frozen', outcome: 'ok' }, checkpoint: 'after-manifest', defaultExpanded: true },
  { id: 'manifest-abi', parent: 'manifest', depth: 2, kind: 'abi', title: 'agent_runtime_v1.host_call', summary: 'Pysolate ABI · decoded request/response', evidence: 'instrumentation-preview', input: { capability: 'sources.benchmark_manifest', arguments: {} }, output: benchmark, params: { request_memory: 'not captured', response_memory: 'not captured', broker_validation: ['schema', 'grant', 'budget'] }, checkpoint: 'after-manifest' },
  { id: 'mkdir', parent: 'run', depth: 1, kind: 'python-fs', title: 'Path.mkdir(parents=True)', summary: '/workspace/reports', evidence: 'source-bound', sourceLines: [24, 24], input: { path: '/workspace/reports', parents: true, exist_ok: true }, output: { created: true }, checkpoint: 'after-mkdir', defaultExpanded: true },
  { id: 'mkdir-wasi', parent: 'mkdir', depth: 2, kind: 'wasi', title: 'path_create_directory', summary: 'WASI filesystem atom', evidence: 'instrumentation-preview', input: { dirfd: '/workspace', path: 'reports' }, output: { errno: 'ESUCCESS' }, params: { rights: 'PATH_CREATE_DIRECTORY', follow_symlinks: false }, checkpoint: 'after-mkdir' },
  { id: 'write', parent: 'run', depth: 1, kind: 'python-fs', title: 'Path.write_text()', summary: 'reports/ranking.json · 279 bytes', evidence: 'source-bound', sourceLines: [25, 25], input: { path: '/workspace/reports/ranking.json', encoding: 'utf-8', bytes: 279 }, output: { written: 279 }, checkpoint: 'final', defaultExpanded: true },
  { id: 'open-wasi', parent: 'write', depth: 2, kind: 'wasi', title: 'path_open', summary: 'Create/truncate report file', evidence: 'instrumentation-preview', input: { dirfd: '/workspace', path: 'reports/ranking.json', oflags: ['CREAT', 'TRUNC'] }, output: { fd: 'logical:file#1', errno: 'ESUCCESS' }, params: { rights: ['FD_WRITE', 'FD_FILESTAT_GET'], follow_symlinks: false }, checkpoint: 'after-mkdir' },
  { id: 'write-wasi', parent: 'write', depth: 2, kind: 'wasi', title: 'fd_write', summary: 'Write UTF-8 report body', evidence: 'instrumentation-preview', input: { fd: 'logical:file#1', iovecs: [{ bytes: 279, preview: reportContent.slice(0, 96) }] }, output: { nwritten: 279, errno: 'ESUCCESS' }, checkpoint: 'final' },
  { id: 'close-wasi', parent: 'write', depth: 2, kind: 'wasi', title: 'fd_close', summary: 'Close report descriptor', evidence: 'instrumentation-preview', input: { fd: 'logical:file#1' }, output: { errno: 'ESUCCESS' }, checkpoint: 'final' },
  { id: 'complete', parent: 'run', depth: 1, kind: 'runtime', title: 'Run completed', summary: 'Result + receipts + final workspace identity', evidence: 'observed', input: { capability_calls: 2 }, output: { result, receipts: 2, workspace: { entries: 2, bytes: 279, disposition: 'discarded', final_tree_sha256: 'sha256:ca41f2feeaac…dc68a900' } }, checkpoint: 'final' },
];

const root: FsEntry = { id: 'workspace', name: 'workspace', path: '/workspace', parent: null, kind: 'directory', evidence: 'observed' };
const reports: FsEntry = { id: 'reports', name: 'reports', path: '/workspace/reports', parent: 'workspace', kind: 'directory', evidence: 'instrumentation-preview' };
const report: FsEntry = { id: 'ranking', name: 'ranking.json', path: '/workspace/reports/ranking.json', parent: 'reports', kind: 'file', size: 279, digest: 'sha256:2c6700d8ea97…181a7b783', content: reportContent, evidence: 'instrumentation-preview' };

export const checkpoints: Record<string, Checkpoint> = {
  initial: { id: 'initial', label: 'Run start', evidence: 'observed', identity: 'sha256:98139030…368f7e7', entries: [root] },
  'after-catalog': { id: 'after-catalog', label: 'After typed call 0', evidence: 'instrumentation-preview', entries: [root] },
  'after-manifest': { id: 'after-manifest', label: 'After typed call 1', evidence: 'instrumentation-preview', entries: [root] },
  'after-mkdir': { id: 'after-mkdir', label: 'After mkdir', evidence: 'instrumentation-preview', entries: [root, reports] },
  final: { id: 'final', label: 'Run completion', evidence: 'observed', identity: 'sha256:34bbb870…23aba37', entries: [root, reports, report] },
};
