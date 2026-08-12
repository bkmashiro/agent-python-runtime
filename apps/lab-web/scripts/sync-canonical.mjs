import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = fileURLToPath(new URL("../src/canonical/", import.meta.url));
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

async function expectedSource() {
  const documents = {};
  for (const state of STATES) {
    documents[state] = {};
    for (const [key, file] of Object.entries(FILES)) {
      documents[state][key] = JSON.parse(await readFile(path.join(ROOT, state, file), "utf8"));
    }
  }
  return `// Generated from the tracked Go-produced canonical Lab v1 JSON; do not edit.\nexport const canonicalDocuments = Object.freeze(${JSON.stringify(documents)});\n`;
}

const target = path.join(ROOT, "data.mjs");
const expected = await expectedSource();
if (process.argv.includes("--check")) {
  const actual = await readFile(target, "utf8").catch(() => "");
  if (actual !== expected) {
    process.stderr.write("canonical data module is stale; run npm run sync:canonical\n");
    process.exitCode = 1;
  } else {
    process.stdout.write("Canonical data module matches tracked Go-produced JSON.\n");
  }
} else {
  await writeFile(target, expected, { encoding: "utf8", mode: 0o644 });
  process.stdout.write("Updated src/canonical/data.mjs.\n");
}
