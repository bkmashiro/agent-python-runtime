import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const APP_ROOT = fileURLToPath(new URL("..", import.meta.url));
const SOURCE_EXTENSIONS = new Set([".css", ".html", ".js", ".json", ".mjs"]);
const EXCLUDED_DIRECTORIES = new Set([".artifacts", "dist", "test"]);

async function readRequired(relativePath) {
  const absolutePath = path.join(APP_ROOT, relativePath);
  try {
    return await readFile(absolutePath, "utf8");
  } catch (error) {
    assert.fail(`required Lab Web source is missing: ${relativePath} (${error.code ?? error.message})`);
  }
}

async function sourceFiles(directory = APP_ROOT) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];

  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    if (entry.isDirectory() && EXCLUDED_DIRECTORIES.has(entry.name)) continue;
    const absolutePath = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await sourceFiles(absolutePath));
    if (entry.isFile() && SOURCE_EXTENSIONS.has(path.extname(entry.name))) files.push(absolutePath);
  }
  return files;
}

async function combinedProductionSource() {
  const files = await sourceFiles();
  assert.ok(files.length > 0, "the prototype must include inspectable static source files");
  return (await Promise.all(files.map(async (file) => `\n/* ${path.relative(APP_ROOT, file)} */\n${await readFile(file, "utf8")}`))).join("\n");
}

function textContent(markup) {
  return markup.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
}

test("the static shell exposes semantic landmarks and named accessible regions", async () => {
  const source = await combinedProductionSource();

  assert.match(source, /<header\b/i, "a semantic page header is required");
  assert.match(source, /<nav\b[^>]*aria-label\s*=/i, "primary navigation needs an accessible name");
  assert.match(source, /<main\b[^>]*id\s*=\s*["']main-content["']/i, "the primary content landmark must be targetable");
  assert.match(source, /<section\b[^>]*aria-labelledby\s*=/i, "view sections need heading relations");
  assert.match(source, /href\s*=\s*["']#main-content["']/i, "keyboard users need a skip link");
  assert.match(source, /aria-label\s*=\s*["'][^"']*(?:filter|fixture)[^"']*["']/i, "fixture/filter controls need an accessible name");
  assert.match(source, /aria-label\s*=\s*["'][^"']*(?:branch|lineage)[^"']*["']/i, "the branch DAG needs an accessible name");
  assert.match(source, /aria-live\s*=\s*["']polite["']/i, "selection changes need a polite status announcement");
});

test("the viewer carries the exact canonical Lab v1 read-only boundary", async () => {
  const source = await combinedProductionSource();
  assert.match(source, /Fixture-backed canonical Lab v1 viewer/);
  assert.match(source, /read-only/i);
  assert.match(source, /not a live Runtime or service/i);
  assert.doesNotMatch(source, /(?:connected|integrated)\s+(?:to|with)\s+(?:the\s+)?Runtime/i);
});

test("the document is responsive and CSS includes focus, overflow, and reduced-motion safeguards", async () => {
  const html = await readRequired("index.html");
  const css = await readRequired("styles.css");

  assert.match(html, /<meta\b[^>]*name\s*=\s*["']viewport["'][^>]*content\s*=\s*["'][^"']*width=device-width[^"']*["']/i);
  assert.match(css, /box-sizing\s*:\s*border-box/i);
  assert.match(css, /:focus-visible\b/i, "keyboard focus must remain visible");
  assert.match(css, /@media\s*\(\s*max-width\s*:/i, "a narrow layout breakpoint is required");
  assert.match(css, /@media\s*\(\s*prefers-reduced-motion\s*:\s*reduce\s*\)/i);
  assert.match(css, /overflow-x\s*:\s*(?:clip|hidden)/i, "the page needs an explicit horizontal overflow guard");
  assert.match(css, /min-width\s*:\s*0/i, "dense grid/flex children must be allowed to shrink");
  assert.match(css, /overflow-wrap\s*:\s*(?:anywhere|break-word)/i, "digest-like values must wrap on narrow screens");
});

test("interactive controls do not claim execution, protected-body fetches, or mutations", async () => {
  const source = await combinedProductionSource();
  const buttonMarkup = [...source.matchAll(/<button\b[\s\S]*?<\/button>/gi)].map(([button]) => button);

  assert.ok(buttonMarkup.length > 0, "fixture, view, run, and lineage selections should use native controls");
  for (const button of buttonMarkup) {
    const accessibleText = `${textContent(button)} ${button}`;
    assert.doesNotMatch(
      accessibleText,
      /\b(?:execute branch|run branch|create branch|fork run|fetch (?:protected )?bod(?:y|ies)|load (?:protected )?bod(?:y|ies)|reveal (?:protected )?bod(?:y|ies)|delete|mutate|save changes)\b/i,
    );
  }

  assert.doesNotMatch(source, /\bfetch\s*\(/, "fixture modules should be local imports, not pretend runtime reads");
  assert.doesNotMatch(source, /\b(?:XMLHttpRequest|WebSocket|EventSource)\b/);
  assert.doesNotMatch(source, /<form\b[^>]*method\s*=\s*["'](?:post|put|patch|delete)["']/i);
});

test("browser sources and package metadata have no external runtime URLs or dependencies", async () => {
  const source = await combinedProductionSource();
  const html = await readRequired("index.html");
  const packageJson = JSON.parse(await readRequired("package.json"));

  assert.doesNotMatch(source, /\bhttps?:\/\//i);
  for (const [, attribute, url] of html.matchAll(/<(?:script|link)\b[^>]*\b(src|href)\s*=\s*["']([^"']+)["']/gi)) {
    assert.doesNotMatch(url, /^(?:https?:)?\/\//i, `external ${attribute} resource is forbidden: ${url}`);
  }

  for (const dependencyKind of ["dependencies", "devDependencies", "optionalDependencies", "peerDependencies"]) {
    assert.deepEqual(packageJson[dependencyKind] ?? {}, {}, `${dependencyKind} must remain empty for the zero-dependency prototype`);
  }
  assert.equal(packageJson.type, "module");
  assert.match(packageJson.scripts?.test ?? "", /node\s+--test/);
  assert.match(packageJson.scripts?.build ?? "", /\bnode\b/);
  assert.match(packageJson.scripts?.serve ?? "", /\bnode\b/);
});

test("all JavaScript imports are local files or Node built-ins", async () => {
  const files = (await sourceFiles()).filter((file) => [".js", ".mjs"].includes(path.extname(file)));
  const importPattern = /(?:\bfrom\s*|\bimport\s*\()\s*["']([^"']+)["']/g;

  for (const file of files) {
    const source = await readFile(file, "utf8");
    for (const [, specifier] of source.matchAll(importPattern)) {
      assert.ok(
        specifier.startsWith("./") || specifier.startsWith("../") || specifier.startsWith("node:"),
        `${path.relative(APP_ROOT, file)} imports external package ${specifier}`,
      );
    }
  }
});

test("generated directories are locally ignored", async () => {
  const ignore = await readRequired(".gitignore");
  assert.match(ignore, /^(?:\.\/)?dist\/?$/m);
  assert.match(ignore, /^(?:\.\/)?\.artifacts\/?$/m);
});
