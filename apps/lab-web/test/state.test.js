import assert from "node:assert/strict";
import test from "node:test";

import {
  comparisonCandidates,
  decodeLocationState,
  encodeLocationState,
  filterRuns,
  resolveSelectedRun,
  selectBranchRun,
} from "../src/state.js";

const RUNS = Object.freeze([
  Object.freeze({
    run_id: "run-source-capture",
    status: "completed",
    workload_id: "source-synthesis",
    treatment_id: "capture",
  }),
  Object.freeze({
    run_id: "run-source-replay",
    status: "failed",
    workload_id: "source-synthesis",
    treatment_id: "replay",
  }),
  Object.freeze({
    run_id: "run-analysis-direct",
    status: "completed",
    workload_id: "stateful-analysis",
    treatment_id: "direct",
  }),
  Object.freeze({
    run_id: "run-planning-direct",
    status: "running",
    workload_id: "bounded-planning",
    treatment_id: "direct",
  }),
]);

const ids = (runs) => runs.map(({ run_id: runId }) => runId);

test("run filters are exact, conjunctive, stable, and deterministic", () => {
  assert.deepEqual(
    ids(filterRuns(RUNS, { status: "completed", workload: "all", treatment: "all" })),
    ["run-source-capture", "run-analysis-direct"],
  );
  assert.deepEqual(
    ids(filterRuns(RUNS, { status: "all", workload: "source-synthesis", treatment: "all" })),
    ["run-source-capture", "run-source-replay"],
  );
  assert.deepEqual(
    ids(filterRuns(RUNS, { status: "all", workload: "all", treatment: "direct" })),
    ["run-analysis-direct", "run-planning-direct"],
  );

  const filters = {
    status: "failed",
    workload: "source-synthesis",
    treatment: "replay",
  };
  const first = filterRuns(RUNS, filters);
  const second = filterRuns(RUNS, filters);

  assert.deepEqual(ids(first), ["run-source-replay"]);
  assert.deepEqual(second, first);
  assert.notStrictEqual(first, second, "filtering should return a fresh result, not mutable shared state");
  assert.deepEqual(ids(RUNS), [
    "run-source-capture",
    "run-source-replay",
    "run-analysis-direct",
    "run-planning-direct",
  ]);
});

test("unknown filter values fail closed to an empty run list", () => {
  assert.deepEqual(filterRuns(RUNS, { status: "not-a-status", workload: "all", treatment: "all" }), []);
  assert.deepEqual(filterRuns(RUNS, { status: "all", workload: "not-a-workload", treatment: "all" }), []);
  assert.deepEqual(filterRuns(RUNS, { status: "all", workload: "all", treatment: "not-a-treatment" }), []);
});

test("selected run remains stable when visible and falls back after filter or fixture changes", () => {
  assert.equal(resolveSelectedRun(RUNS, "run-source-replay")?.run_id, "run-source-replay");

  const completed = filterRuns(RUNS, {
    status: "completed",
    workload: "all",
    treatment: "all",
  });
  assert.equal(
    resolveSelectedRun(completed, "run-source-replay")?.run_id,
    "run-source-capture",
    "a hidden selection should fall back to the first run in stable fixture order",
  );

  const replacementFixture = [
    {
      run_id: "run-private-placeholder",
      status: "failed",
      workload_id: "source-synthesis",
      treatment_id: "replay",
    },
  ];
  assert.equal(resolveSelectedRun(replacementFixture, "run-source-capture")?.run_id, "run-private-placeholder");
  assert.equal(resolveSelectedRun([], "run-source-capture"), null);
});

test("comparison candidates stay inside the current run context and never include the selected run", () => {
  const context = filterRuns(RUNS, {
    status: "all",
    workload: "source-synthesis",
    treatment: "all",
  });
  const selected = resolveSelectedRun(context, "run-source-capture");
  const candidates = comparisonCandidates(context, selected.run_id);

  assert.deepEqual(ids(candidates), ["run-source-replay"]);
  assert.equal(resolveSelectedRun(candidates, "run-source-replay")?.run_id, "run-source-replay");
  assert.equal(resolveSelectedRun(candidates, "run-source-capture")?.run_id, "run-source-replay");

  const onlySelected = filterRuns(RUNS, {
    status: "completed",
    workload: "source-synthesis",
    treatment: "capture",
  });
  assert.deepEqual(comparisonCandidates(onlySelected, "run-source-capture"), []);
  assert.equal(resolveSelectedRun(comparisonCandidates(onlySelected, "run-source-capture"), "run-source-replay"), null);
});

test("location state has a deterministic hash representation and round-trips reserved run IDs", () => {
  const state = {
    fixture: "branched",
    view: "comparison",
    runId: "run/child:2",
    compareRunId: "run/root:1",
  };
  const encoded = encodeLocationState(state);

  assert.equal(
    encoded,
    "#fixture=branched&view=comparison&run=run%2Fchild%3A2&compare=run%2Froot%3A1",
  );
  assert.deepEqual(decodeLocationState(encoded), state);
  assert.deepEqual(
    decodeLocationState("?fixture=branched&view=comparison&run=run%2Fchild%3A2&compare=run%2Froot%3A1"),
    state,
    "query and hash state should decode identically without a routing dependency",
  );
});

test("location decoding permits only known fixture and view states", () => {
  assert.deepEqual(decodeLocationState("#fixture=unknown&view=runtime-console&run=&compare="), {
    fixture: "ordinary",
    view: "overview",
    runId: null,
    compareRunId: null,
  });

  for (const fixture of ["ordinary", "branched", "incomplete", "truncated", "private"]) {
    assert.equal(decodeLocationState(`#fixture=${fixture}&view=runs`).fixture, fixture);
  }
  for (const view of ["overview", "runs", "timeline", "lineage", "workspace", "comparison"]) {
    assert.equal(decodeLocationState(`#fixture=ordinary&view=${view}`).view, view);
  }
});

test("selecting a branch node focuses its associated run without inventing unavailable relations", () => {
  const nodes = Object.freeze([
    Object.freeze({ node_id: "node-root", run_id: "run-root" }),
    Object.freeze({ node_id: "node-child-a", run_id: "run-child-a" }),
    Object.freeze({ node_id: "node-private", run_id: null }),
  ]);

  assert.equal(selectBranchRun(nodes, "node-child-a", "run-root"), "run-child-a");
  assert.equal(selectBranchRun(nodes, "node-private", "run-root"), "run-root");
  assert.equal(selectBranchRun(nodes, "node-missing", "run-root"), "run-root");
});
