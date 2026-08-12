import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const TARGET_ROOT = fileURLToPath(new URL("../src/canonical/", import.meta.url));
const SOURCE_ROOT = fileURLToPath(new URL("../../../research/labview/testdata/canonical/", import.meta.url));
const STATES = Object.freeze(["ordinary", "branched", "incomplete", "truncated", "private"]);
const FILES = Object.freeze({
  index: "lab-index.v1.json",
  study: "study-summary.v1.json",
  run: "run-detail.v1.json",
  timeline: "timeline-page.v1.json",
  branch: "branch-dag.v1.json",
  workspace: "workspace-diff.v1.json",
  comparison: "run-comparison.v1.json",
  problem: "problem.v1.json",
  objectRef: "object-ref.v1.json",
});

async function sourceDocuments() {
  const documents = {};
  const rawByPath = new Map();
  for (const state of STATES) {
    documents[state] = {};
    for (const [key, file] of Object.entries(FILES)) {
      const relative = path.join(state, file);
      const raw = await readFile(path.join(SOURCE_ROOT, relative), "utf8");
      documents[state][key] = JSON.parse(raw);
      rawByPath.set(relative, raw);
    }
  }
  return { documents, rawByPath };
}

function moduleSource(documents) {
  return `// Generated from the tracked Go-produced canonical Lab v1 JSON; do not edit.\nexport const canonicalDocuments = Object.freeze(${JSON.stringify(documents)});\n`;
}

const { documents, rawByPath } = await sourceDocuments();
const expectedModule = moduleSource(documents);
const moduleTarget = path.join(TARGET_ROOT, "data.mjs");
if (process.argv.includes("--check")) {
  const drift = [];
  for (const [relative, expected] of rawByPath) {
    const actual = await readFile(path.join(TARGET_ROOT, relative), "utf8").catch(() => "");
    if (actual !== expected) drift.push(relative.split(path.sep).join("/"));
  }
  const actualModule = await readFile(moduleTarget, "utf8").catch(() => "");
  if (actualModule !== expectedModule) drift.push("data.mjs");
  if (drift.length) {
    process.stderr.write(`canonical Web snapshot is stale: ${drift.join(", ")}\nrun npm run sync:canonical\n`);
    process.exitCode = 1;
  } else {
    process.stdout.write(`Canonical Web snapshot matches ${rawByPath.size} Go-produced JSON files.\n`);
  }
} else {
  for (const [relative, raw] of rawByPath) {
    const target = path.join(TARGET_ROOT, relative);
    await mkdir(path.dirname(target), { recursive: true });
    await writeFile(target, raw, { encoding: "utf8", mode: 0o644 });
  }
  await writeFile(moduleTarget, expectedModule, { encoding: "utf8", mode: 0o644 });
  process.stdout.write(`Updated ${rawByPath.size} canonical JSON files and src/canonical/data.mjs.\n`);
}
