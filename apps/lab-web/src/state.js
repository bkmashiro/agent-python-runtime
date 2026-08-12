const FIXTURE_STATE_VALUES = Object.freeze([
  "ordinary",
  "branched",
  "incomplete",
  "truncated",
  "private",
]);

const VIEW_VALUES = Object.freeze([
  "overview",
  "runs",
  "timeline",
  "lineage",
  "workspace",
  "comparison",
]);

const FIXTURE_STATE_SET = new Set(FIXTURE_STATE_VALUES);
const VIEW_SET = new Set(VIEW_VALUES);

export const FIXTURE_STATES = FIXTURE_STATE_VALUES;
export const VIEWS = VIEW_VALUES;

function exactFilter(value, selectedValue) {
  return selectedValue === "all" || value === selectedValue;
}

/**
 * Apply the local run filters without reordering or mutating fixture data.
 * Unknown values naturally fail closed because matching is exact.
 */
export function filterRuns(runs, filters = {}) {
  if (!Array.isArray(runs)) return [];

  const {
    status = "all",
    workload = "all",
    treatment = "all",
  } = filters ?? {};

  if (![status, workload, treatment].every((value) => typeof value === "string")) {
    return [];
  }

  return runs.filter((run) => (
    run !== null
    && typeof run === "object"
    && exactFilter(run.status, status)
    && exactFilter(run.workload_id, workload)
    && exactFilter(run.treatment_id, treatment)
  ));
}

/**
 * Preserve a visible selection; otherwise choose the first run in fixture
 * order. An empty context has no selected run.
 */
export function resolveSelectedRun(runs, selectedRunId) {
  if (!Array.isArray(runs) || runs.length === 0) return null;

  return runs.find((run) => run?.run_id === selectedRunId) ?? runs[0] ?? null;
}

/** Return comparison choices from the already-filtered run context. */
export function comparisonCandidates(runs, selectedRunId) {
  if (!Array.isArray(runs)) return [];
  return runs.filter((run) => run?.run_id !== selectedRunId);
}

function knownValue(value, allowedValues, fallback) {
  return typeof value === "string" && allowedValues.has(value) ? value : fallback;
}

function optionalId(value) {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function normalizedLocationState(state = {}) {
  return {
    fixture: knownValue(state?.fixture, FIXTURE_STATE_SET, "ordinary"),
    view: knownValue(state?.view, VIEW_SET, "overview"),
    runId: optionalId(state?.runId),
    compareRunId: optionalId(state?.compareRunId),
  };
}

/** Encode navigation state in one canonical, dependency-free hash order. */
export function encodeLocationState(state = {}) {
  const normalized = normalizedLocationState(state);
  const parameters = new URLSearchParams();

  parameters.set("fixture", normalized.fixture);
  parameters.set("view", normalized.view);
  if (normalized.runId !== null) parameters.set("run", normalized.runId);
  if (normalized.compareRunId !== null) parameters.set("compare", normalized.compareRunId);

  return `#${parameters.toString()}`;
}

function locationParameters(locationState) {
  if (typeof locationState !== "string") return new URLSearchParams();

  const hashIndex = locationState.indexOf("#");
  const queryIndex = locationState.indexOf("?");
  let encoded = locationState;

  if (hashIndex >= 0) {
    encoded = locationState.slice(hashIndex + 1);
  } else if (queryIndex >= 0) {
    encoded = locationState.slice(queryIndex + 1);
  }

  const nestedDelimiter = encoded.search(/[?#]/);
  if (nestedDelimiter >= 0) encoded = encoded.slice(0, nestedDelimiter);

  return new URLSearchParams(encoded);
}

/** Decode either hash or query state with closed fixture/view vocabularies. */
export function decodeLocationState(locationState = "") {
  const parameters = locationParameters(locationState);
  return normalizedLocationState({
    fixture: parameters.get("fixture"),
    view: parameters.get("view"),
    runId: parameters.get("run"),
    compareRunId: parameters.get("compare"),
  });
}

/** Focus a branch node's linked run, retaining the current run if unavailable. */
export function selectBranchRun(nodes, nodeId, currentRunId) {
  if (!Array.isArray(nodes)) return currentRunId ?? null;

  const node = nodes.find((candidate) => candidate?.node_id === nodeId);
  return optionalId(node?.run_id) ?? currentRunId ?? null;
}
