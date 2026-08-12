import { readFile, realpath, stat } from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const APP_ROOT = fileURLToPath(new URL("..", import.meta.url));
const DEFAULT_DIST_ROOT = path.join(APP_ROOT, "dist");
const LOOPBACK_HOST = "127.0.0.1";
const DEFAULT_PORT = 4173;

const CONTENT_TYPES = new Map([
  [".css", "text/css; charset=utf-8"],
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".json", "application/json; charset=utf-8"],
  [".mjs", "text/javascript; charset=utf-8"],
  [".svg", "image/svg+xml"],
  [".txt", "text/plain; charset=utf-8"],
]);

function isInside(root, candidate) {
  const relative = path.relative(root, candidate);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
}

function sendText(response, statusCode, body, extraHeaders = {}) {
  const contents = Buffer.from(body, "utf8");
  response.writeHead(statusCode, {
    "Content-Type": "text/plain; charset=utf-8",
    "Content-Length": contents.length,
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
    ...extraHeaders,
  });
  response.end(contents);
}

export function safeRelativeRequestPath(requestTarget) {
  if (typeof requestTarget !== "string" || !requestTarget.startsWith("/")) return null;

  const queryIndex = requestTarget.indexOf("?");
  const rawPath = queryIndex >= 0 ? requestTarget.slice(0, queryIndex) : requestTarget;
  let decodedPath;
  try {
    decodedPath = decodeURIComponent(rawPath);
  } catch {
    return null;
  }

  if (decodedPath.includes("\0") || decodedPath.includes("\\")) return null;
  const segments = decodedPath.split("/");
  if (segments.some((segment) => segment === "." || segment === "..")) return null;

  let relativePath = decodedPath.slice(1);
  if (relativePath === "" || relativePath.endsWith("/")) {
    relativePath = path.posix.join(relativePath, "index.html");
  }
  if (relativePath.startsWith("/")) return null;

  return relativePath;
}

async function readPublicFile(distRoot, requestTarget) {
  const relativePath = safeRelativeRequestPath(requestTarget);
  if (relativePath === null) return { status: 400 };

  const unresolvedPath = path.resolve(distRoot, relativePath);
  if (!isInside(distRoot, unresolvedPath)) return { status: 400 };

  let resolvedPath;
  try {
    resolvedPath = await realpath(unresolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT" || error?.code === "ENOTDIR") return { status: 404 };
    throw error;
  }
  if (!isInside(distRoot, resolvedPath)) return { status: 400 };

  const metadata = await stat(resolvedPath);
  if (!metadata.isFile()) return { status: 404 };

  return {
    status: 200,
    contents: await readFile(resolvedPath),
    contentType: CONTENT_TYPES.get(path.extname(resolvedPath).toLowerCase()) ?? "application/octet-stream",
  };
}

export async function createStaticServer({ distRoot = DEFAULT_DIST_ROOT } = {}) {
  const resolvedDistRoot = await realpath(distRoot);
  const metadata = await stat(resolvedDistRoot);
  if (!metadata.isDirectory()) throw new Error("dist is not a directory; run the build first");

  const server = createServer(async (request, response) => {
    if (request.method !== "GET" && request.method !== "HEAD") {
      sendText(response, 405, "Method not allowed.\n", { Allow: "GET, HEAD" });
      return;
    }

    try {
      const result = await readPublicFile(resolvedDistRoot, request.url ?? "/");
      if (result.status === 400) {
        sendText(response, 400, "Invalid request path.\n");
        return;
      }
      if (result.status === 404) {
        sendText(response, 404, "Not found.\n");
        return;
      }

      response.writeHead(200, {
        "Content-Type": result.contentType,
        "Content-Length": result.contents.length,
        "Cache-Control": "no-store",
        "Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
      });
      response.end(request.method === "HEAD" ? undefined : result.contents);
    } catch {
      sendText(response, 500, "Internal server error.\n");
    }
  });

  server.on("clientError", (_error, socket) => {
    socket.end("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n");
  });

  return server;
}

export function parsePort(argumentsList = process.argv.slice(2), environment = process.env) {
  let rawPort = environment.LAB_WEB_PORT ?? String(DEFAULT_PORT);

  for (let index = 0; index < argumentsList.length; index += 1) {
    const argument = argumentsList[index];
    if (argument === "--port") {
      rawPort = argumentsList[index + 1];
      index += 1;
    } else if (argument.startsWith("--port=")) {
      rawPort = argument.slice("--port=".length);
    } else {
      throw new TypeError(`unknown argument: ${argument}`);
    }
  }

  if (!/^\d+$/.test(rawPort ?? "")) throw new TypeError("port must be an integer from 0 through 65535");
  const port = Number(rawPort);
  if (!Number.isSafeInteger(port) || port < 0 || port > 65535) {
    throw new TypeError("port must be an integer from 0 through 65535");
  }
  return port;
}

export async function serve(argumentsList = process.argv.slice(2)) {
  const port = parsePort(argumentsList);
  const server = await createStaticServer();

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, LOOPBACK_HOST, () => {
      server.off("error", reject);
      resolve();
    });
  });

  const address = server.address();
  const actualPort = typeof address === "object" && address !== null ? address.port : port;
  process.stdout.write(`Lab Web fixture server listening on ${LOOPBACK_HOST}:${actualPort}\n`);
  return server;
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : null;
if (invokedPath === fileURLToPath(import.meta.url)) {
  serve().catch((error) => {
    process.stderr.write(`Server failed: ${error.message}\n`);
    process.exitCode = 1;
  });
}
