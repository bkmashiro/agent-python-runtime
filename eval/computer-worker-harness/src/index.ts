import { DurableObject } from "cloudflare:workers";

import {
  type DurableObjectStorageLike,
  getWorkspace,
  type WorkspaceRuntimeValue,
  withWorkspace,
} from "@cloudflare/computer";
import { WorkerJavaScriptBackend } from "@cloudflare/computer/backends/worker-javascript";

interface Env {
  LOADER: WorkspaceLoader;
  PlacementHarness: DurableObjectNamespace<PlacementHarness>;
}

interface WorkspaceLoader {
  load(code: {
    compatibilityDate: string;
    compatibilityFlags?: string[];
    limits?: { cpuMs?: number };
    mainModule: string;
    modules: Record<string, string | { js?: string }>;
    globalOutbound?: Fetcher | null;
  }): { getEntrypoint(name?: string, options?: { limits?: { cpuMs?: number } }): unknown };
}

interface ExpectedCall {
  name: string;
  arguments: WorkspaceRuntimeValue;
  result: WorkspaceRuntimeValue;
}

interface Fixture {
  schema_version: "placement-computer-tool-fixture/v1";
  calls: ExpectedCall[];
}

interface TraceCall {
  sequence: number;
  name: string;
  arguments: WorkspaceRuntimeValue;
  matched: boolean;
}

const FIXTURE_KEY = "placement:fixture";
const TRACE_KEY = "placement:trace";
const CURSOR_KEY = "placement:cursor";
const MAX_CALLS = 64;
const MAX_BODY = 1024 * 1024;
const MOUNT_ROOT = "/workspace";

function stable(value: WorkspaceRuntimeValue): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(stable).join(",")}]`;
  return `{${Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${stable(value[key] as WorkspaceRuntimeValue)}`)
    .join(",")}}`;
}

function validValue(value: unknown, depth = 0): value is WorkspaceRuntimeValue {
  if (depth > 32 || value === undefined || typeof value === "bigint" || typeof value === "function") return false;
  if (value === null || typeof value === "string" || typeof value === "boolean") return true;
  if (typeof value === "number") return Number.isFinite(value);
  if (Array.isArray(value)) return value.length <= 256 && value.every((item) => validValue(item, depth + 1));
  if (typeof value !== "object") return false;
  const entries = Object.entries(value as Record<string, unknown>);
  return entries.length <= 256 && entries.every(([key, item]) => key.length <= 256 && validValue(item, depth + 1));
}

async function invokeTrusted(storage: DurableObjectStorage, method: string, args: WorkspaceRuntimeValue[]): Promise<WorkspaceRuntimeValue> {
  if (method !== "invoke" || args.length !== 2 || typeof args[0] !== "string" || !validValue(args[1])) {
    throw new Error("invalid trusted capability invocation");
  }
  const fixture = await storage.get<Fixture>(FIXTURE_KEY);
  const cursor = (await storage.get<number>(CURSOR_KEY)) ?? 0;
  const trace = (await storage.get<TraceCall[]>(TRACE_KEY)) ?? [];
  if (!fixture || cursor >= fixture.calls.length || trace.length >= MAX_CALLS) {
    throw new Error("trusted capability call exceeds frozen fixture");
  }
  const expected = fixture.calls[cursor];
  const matched = expected.name === args[0] && stable(expected.arguments) === stable(args[1]);
  const call: TraceCall = { sequence: cursor, name: args[0], arguments: args[1], matched };
  await storage.put(TRACE_KEY, [...trace, call]);
  if (!matched) throw new Error("trusted capability call does not match frozen fixture");
  await storage.put(CURSOR_KEY, cursor + 1);
  return expected.result;
}

const WorkspaceBase = withWorkspace(class extends DurableObject<Env> {}, (self) => {
  const { ctx, env } = self as unknown as { ctx: DurableObjectState; env: Env };
  return {
    storage: ctx.storage as unknown as DurableObjectStorageLike,
    waitUntil: ctx.waitUntil.bind(ctx),
    backends: [
      new WorkerJavaScriptBackend({
        loader: env.LOADER,
        root: MOUNT_ROOT,
        access: "read-write",
        globalOutbound: null,
        allowGitNetwork: false,
        allowArtifactNetwork: false,
        maxSourceBytes: 64 * 1024,
        maxInputBytes: 64 * 1024,
        maxResultBytes: 1024 * 1024,
        maxStdioBytes: 64 * 1024,
        maxCapabilityBytes: 64 * 1024,
        maxConcurrentCapabilityCalls: 1,
        maxConcurrentExecutions: 1,
        defaultTimeoutMs: 60_000,
        maxTimeoutMs: 120_000,
        trustedModules: {
          "ws:tools": {
            call: (method, args) => invokeTrusted(ctx.storage, method, args),
          },
        },
      }),
    ],
  };
});

export class PlacementHarness extends WorkspaceBase {
  async setFixture(fixture: Fixture): Promise<void> {
    if (
      fixture.schema_version !== "placement-computer-tool-fixture/v1" ||
      !Array.isArray(fixture.calls) ||
      fixture.calls.length > MAX_CALLS ||
      !fixture.calls.every(
        (call) =>
          typeof call?.name === "string" &&
          call.name.length > 0 &&
          call.name.length <= 128 &&
          validValue(call.arguments) &&
          validValue(call.result),
      )
    ) {
      throw new Error("invalid trusted capability fixture");
    }
    await this.ctx.storage.put(FIXTURE_KEY, fixture);
    await this.ctx.storage.put(TRACE_KEY, []);
    await this.ctx.storage.put(CURSOR_KEY, 0);
  }

  async getTrace(): Promise<{ calls: TraceCall[]; cursor: number; expected_count: number; complete: boolean }> {
    const fixture = await this.ctx.storage.get<Fixture>(FIXTURE_KEY);
    const calls = (await this.ctx.storage.get<TraceCall[]>(TRACE_KEY)) ?? [];
    const cursor = (await this.ctx.storage.get<number>(CURSOR_KEY)) ?? 0;
    const expectedCount = fixture?.calls.length ?? 0;
    return { calls, cursor, expected_count: expectedCount, complete: cursor === expectedCount && calls.every((call) => call.matched) };
  }
}

interface ExecRequest {
  source?: string;
  input?: WorkspaceRuntimeValue;
  cwd?: string;
}

function resolveMountPath(rest: string): string | null {
  const candidate = `/${rest}`;
  if (candidate !== MOUNT_ROOT && !candidate.startsWith(`${MOUNT_ROOT}/`)) return null;
  if (candidate.split("/").includes("..")) return null;
  return candidate;
}

async function boundedJSON(request: Request): Promise<unknown> {
  const bytes = new Uint8Array(await request.arrayBuffer());
  if (bytes.byteLength === 0 || bytes.byteLength > MAX_BODY) throw new Error("invalid bounded JSON body");
  return JSON.parse(new TextDecoder().decode(bytes));
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === "/") return new Response("pysolate Cloudflare Computer placement harness\n");
    const match = url.pathname.match(/^\/c\/([a-z0-9][a-z0-9-]{0,62})\/(.+)$/);
    if (!match) return new Response("not found", { status: 404 });
    const stub = env.PlacementHarness.get(env.PlacementHarness.idFromName(match[1]));
    const route = match[2];
    try {
      if (route === "fixture" && request.method === "PUT") {
        await stub.setFixture((await boundedJSON(request)) as Fixture);
        return new Response(null, { status: 204 });
      }
      if (route === "trace" && request.method === "GET") return Response.json(await stub.getTrace());
      if (route === "exec" && request.method === "POST") {
        const body = (await boundedJSON(request)) as ExecRequest;
        if (typeof body.source !== "string" || body.source.length === 0) throw new Error("must provide source");
        const workspace = await getWorkspace(stub as unknown as Parameters<typeof getWorkspace>[0]);
        const handle = await workspace.runtime.exec(body.source, {
          backend: "worker-javascript",
          cwd: body.cwd ?? MOUNT_ROOT,
          input: body.input,
          encoding: "utf8",
        });
        return Response.json(await handle.result());
      }
      const file = route.match(/^file\/(.+)$/);
      if (file) {
        const path = resolveMountPath(file[1]);
        if (path === null) return Response.json({ error: "path outside workspace" }, { status: 400 });
        const workspace = await getWorkspace(stub as unknown as Parameters<typeof getWorkspace>[0]);
        if (request.method === "PUT") {
          const bytes = new Uint8Array(await request.arrayBuffer());
          if (bytes.byteLength > MAX_BODY) throw new Error("file too large");
          const parent = path.slice(0, path.lastIndexOf("/")) || MOUNT_ROOT;
          await workspace.fs.mkdir(parent, { recursive: true });
          await workspace.fs.writeFile(path, bytes);
          return new Response(null, { status: 204 });
        }
        if (request.method === "GET") {
          const stream = await workspace.fs.readFile(path, {});
          return new Response(stream, { headers: { "content-type": "application/octet-stream" } });
        }
      }
      return new Response("method not allowed", { status: 405 });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : String(error) }, { status: 500 });
    }
  },
} satisfies ExportedHandler<Env>;
