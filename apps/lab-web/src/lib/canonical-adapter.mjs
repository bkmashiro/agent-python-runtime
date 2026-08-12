const DIGEST = /^sha256:[0-9a-f]{64}$/;
const SCHEMA_FILES = Object.freeze([
  ["index", "lab-index.v1", "pysolate.lab-index.v1"],
  ["study", "study-summary.v1", "pysolate.study-summary.v1"],
  ["run", "run-detail.v1", "pysolate.run-detail.v1"],
  ["timeline", "timeline-page.v1", "pysolate.timeline-page.v1"],
  ["branch", "branch-dag.v1", "pysolate.branch-dag.v1"],
  ["workspace", "workspace-diff.v1", "pysolate.workspace-diff.v1"],
  ["comparison", "run-comparison.v1", "pysolate.run-comparison.v1"],
  ["problem", "problem.v1", "pysolate.problem.v1"],
  ["objectRef", "object-ref.v1", "pysolate.object-ref.v1"],
]);
const STATUS = new Set(["completed", "failed", "timed_out", "unsupported"]);
const EVIDENCE = new Set(["complete", "incomplete", "truncated"]);
const ORACLE = new Set(["passed", "failed", "not_run"]);
const PRIVACY = new Set(["portable", "private"]);
const AVAILABILITY = new Set(["available", "unavailable"]);
const REF_KINDS = new Set(["artifact", "capability_plan", "execution", "execution_profile", "invocation", "result", "workspace_tree"]);
const EVENT_TYPES = new Set(["execution.started", "capability.call", "execution.completed"]);
const PROBLEM_CODES = new Set(["none", "evidence_incomplete", "projection_truncated", "object_unavailable", "empty_projection"]);
const PROBLEM_SCOPES = new Set(["study", "run", "timeline", "branch", "workspace", "comparison", "reference"]);
const DIMENSIONS = new Set(["artifact", "profile", "workload", "treatment", "status", "result", "workspace", "capability_calls", "evidence_class", "evidence_completeness"]);
const DELTA_KINDS = new Set(["same", "changed", "left_only", "right_only", "unavailable"]);
const CHANGE_KINDS = new Set(["added", "removed", "modified"]);

function fail(message) {
  throw new TypeError(`Invalid canonical Lab v1 fixture: ${message}`);
}
function object(value, label) { if (!value || typeof value !== "object" || Array.isArray(value)) fail(`${label} must be an object`); return value; }
function array(value, label) { if (!Array.isArray(value)) fail(`${label} must be an array`); return value; }
function string(value, label) { if (typeof value !== "string") fail(`${label} must be a string`); return value; }
function nonEmpty(value, label) { string(value, label); if (!value) fail(`${label} must not be empty`); return value; }
function integer(value, label) { if (!Number.isSafeInteger(value) || value < 0) fail(`${label} must be a non-negative integer`); return value; }
function member(value, set, label) { if (!set.has(value)) fail(`${label} has unsupported value ${String(value)}`); return value; }
function exact(value, keys, label) {
  object(value, label);
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) fail(`${label} has unexpected or missing fields`);
}
function digest(value, label, allowEmpty = false) {
  string(value, label);
  if (allowEmpty && value === "") return value;
  if (!DIGEST.test(value)) fail(`${label} must be a lowercase sha256 digest`);
  return value;
}
function page(value, label) {
  exact(value, ["cursor", "next_cursor", "returned", "total", "truncated"], label);
  string(value.cursor, `${label}.cursor`); string(value.next_cursor, `${label}.next_cursor`);
  integer(value.returned, `${label}.returned`); integer(value.total, `${label}.total`);
  if (value.total < value.returned || value.truncated !== (value.total > value.returned)) fail(`${label} has inconsistent bounds`);
}
function envelope(value, kind, label) {
  exact(value, ["schema_version", "source_sha256", "generated_at_policy", ...kind.required], label);
  if (value.schema_version !== kind.schema) fail(`${label}.schema_version must be ${kind.schema}`);
  digest(value.source_sha256, `${label}.source_sha256`);
  if (value.generated_at_policy !== "omitted") fail(`${label} must omit generated timestamps`);
}
function validateRef(ref, label) {
  exact(ref, ["kind", "sha256", "privacy", "availability"], label);
  member(ref.kind, REF_KINDS, `${label}.kind`); digest(ref.sha256, `${label}.sha256`); member(ref.privacy, PRIVACY, `${label}.privacy`); member(ref.availability, AVAILABILITY, `${label}.availability`);
  if (ref.privacy === "private" && ref.availability === "available") fail(`${label} cannot expose a private available object`);
}
function validateStudy(value) {
  envelope(value, { schema: "pysolate.study-summary.v1", required: ["study_id", "evidence_class", "workload_count", "treatment_count", "status_totals", "prohibited_claims", "storage"] }, "study-summary");
  nonEmpty(value.study_id, "study-summary.study_id"); member(value.evidence_class, new Set(["current", "mechanism_only", "qualified_workload", "experimental_partial", "not_measured"]), "study-summary.evidence_class");
  integer(value.workload_count, "study-summary.workload_count"); integer(value.treatment_count, "study-summary.treatment_count");
  array(value.status_totals, "study-summary.status_totals");
  if (value.status_totals.length !== 4) fail("study-summary.status_totals must include all four statuses");
  const seen = new Set();
  for (const [index, total] of value.status_totals.entries()) { exact(total, ["status", "count"], `study-summary.status_totals[${index}]`); member(total.status, STATUS, `study-summary.status_totals[${index}].status`); if (seen.has(total.status)) fail("study-summary status totals must be unique"); seen.add(total.status); integer(total.count, `study-summary.status_totals[${index}].count`); }
  array(value.prohibited_claims, "study-summary.prohibited_claims"); if (value.prohibited_claims.length === 0 || value.prohibited_claims.some((claim) => typeof claim !== "string" || !claim)) fail("study-summary.prohibited_claims must be non-empty strings");
  exact(value.storage, ["logical_bytes", "stored_bytes", "object_count", "reused_object_count"], "study-summary.storage");
  for (const key of ["logical_bytes", "stored_bytes", "object_count", "reused_object_count"]) integer(value.storage[key], `study-summary.storage.${key}`);
  if (value.storage.stored_bytes > value.storage.logical_bytes || value.storage.reused_object_count > value.storage.object_count) fail("study-summary storage counters are inconsistent");
}
function validateRun(value) {
  envelope(value, { schema: "pysolate.run-detail.v1", required: ["run_id", "workload_id", "treatment", "status", "oracle_status", "evidence_class", "evidence_completeness", "refs", "problem_codes"] }, "run-detail");
  for (const key of ["run_id", "workload_id", "treatment"]) nonEmpty(value[key], `run-detail.${key}`);
  member(value.status, STATUS, "run-detail.status"); member(value.oracle_status, ORACLE, "run-detail.oracle_status"); member(value.evidence_class, new Set(["current", "mechanism_only", "qualified_workload", "experimental_partial", "not_measured"]), "run-detail.evidence_class"); member(value.evidence_completeness, EVIDENCE, "run-detail.evidence_completeness");
  if ((value.status === "completed" && value.oracle_status !== "passed") || (value.status === "failed" && value.oracle_status !== "failed") || ((value.status === "timed_out" || value.status === "unsupported") && value.oracle_status !== "not_run")) fail("run-detail status and oracle_status disagree");
  array(value.refs, "run-detail.refs"); if (value.refs.length !== 7) fail("run-detail.refs must contain seven references");
  const refs = new Set(); for (const [index, ref] of value.refs.entries()) { validateRef(ref, `run-detail.refs[${index}]`); if (refs.has(ref.kind)) fail("run-detail refs must have unique kinds"); refs.add(ref.kind); }
  if (refs.size !== REF_KINDS.size) fail("run-detail refs must cover every canonical object kind");
  array(value.problem_codes, "run-detail.problem_codes"); for (const code of value.problem_codes) member(code, PROBLEM_CODES, "run-detail.problem_codes item");
}
function validateTimeline(value, runId) {
  envelope(value, { schema: "pysolate.timeline-page.v1", required: ["run_id", "evidence_completeness", "events", "page"] }, "timeline-page");
  if (value.run_id !== runId) fail("timeline-page.run_id does not match run-detail"); member(value.evidence_completeness, EVIDENCE, "timeline-page.evidence_completeness"); array(value.events, "timeline-page.events");
  let previous = 0; for (const [index, event] of value.events.entries()) { exact(event, ["sequence", "parent_sequence", "type", "outcome", "capability", "operation_index", "arguments_sha256", "result_sha256"], `timeline-page.events[${index}]`); integer(event.sequence, "timeline event sequence"); integer(event.parent_sequence, "timeline event parent_sequence"); if (event.sequence !== previous + 1 || event.parent_sequence !== previous) fail("timeline events must be contiguous and ordered"); previous = event.sequence; member(event.type, EVENT_TYPES, "timeline event type"); member(event.outcome, new Set(["none", "ok", "error", "denied"]), "timeline event outcome"); nonEmpty(event.capability === "" ? "placeholder" : event.capability, "timeline event capability"); integer(event.operation_index, "timeline operation_index"); digest(event.arguments_sha256, "timeline arguments_sha256", true); digest(event.result_sha256, "timeline result_sha256", true); }
  page(value.page, "timeline-page.page"); if (value.page.returned !== value.events.length) fail("timeline-page returned count does not match events");
}
function validateBranch(value) {
  envelope(value, { schema: "pysolate.branch-dag.v1", required: ["nodes", "edges", "page"] }, "branch-dag"); array(value.nodes, "branch-dag.nodes"); array(value.edges, "branch-dag.edges");
  const nodes = new Set(); for (const [index, node] of value.nodes.entries()) { exact(node, ["run_id", "status", "evidence_completeness"], `branch-dag.nodes[${index}]`); nonEmpty(node.run_id, "branch node run_id"); if (nodes.has(node.run_id)) fail("branch node run_ids must be unique"); nodes.add(node.run_id); member(node.status, STATUS, "branch node status"); member(node.evidence_completeness, EVIDENCE, "branch node evidence_completeness"); }
  for (const [index, edge] of value.edges.entries()) { exact(edge, ["parent_run_id", "child_run_id", "fork_operation", "suffix_mode", "branch_sha256"], `branch-dag.edges[${index}]`); if (!nodes.has(edge.parent_run_id) || !nodes.has(edge.child_run_id) || edge.parent_run_id === edge.child_run_id) fail("branch edge references an invalid node pair"); integer(edge.fork_operation, "branch edge fork_operation"); if (edge.suffix_mode !== "override") fail("branch edge suffix_mode is unsupported"); digest(edge.branch_sha256, "branch edge branch_sha256"); }
  page(value.page, "branch-dag.page"); if (value.page.returned !== value.nodes.length) fail("branch-dag returned count does not match nodes");
  return nodes;
}
function validateSnapshot(value, label) { exact(value, ["present", "size_bytes", "executable", "sha256"], label); if (typeof value.present !== "boolean") fail(`${label}.present must be boolean`); integer(value.size_bytes, `${label}.size_bytes`); if (typeof value.executable !== "boolean") fail(`${label}.executable must be boolean`); digest(value.sha256, `${label}.sha256`, true); if (!value.present && (value.size_bytes !== 0 || value.sha256 !== "")) fail(`${label} absent snapshot must be empty`); if (value.present && !value.sha256) fail(`${label} present snapshot needs a digest`); }
function validateWorkspace(value, runId, nodeIds) { envelope(value, { schema: "pysolate.workspace-diff.v1", required: ["run_id", "base_run_id", "changes", "page"] }, "workspace-diff"); if (value.run_id !== runId || !nodeIds.has(value.base_run_id)) fail("workspace-diff run relation is invalid"); array(value.changes, "workspace-diff.changes"); const paths = new Set(); for (const [index, change] of value.changes.entries()) { exact(change, ["path", "kind", "before", "after"], `workspace-diff.changes[${index}]`); nonEmpty(change.path, "workspace path"); if (change.path.startsWith("/") || change.path.includes("\\") || change.path.split("/").some((part) => !part || part === "." || part === "..")) fail("workspace paths must be normalized Guest-relative paths"); if (paths.has(change.path)) fail("workspace paths must be unique"); paths.add(change.path); member(change.kind, CHANGE_KINDS, "workspace change kind"); validateSnapshot(change.before, "workspace change before"); validateSnapshot(change.after, "workspace change after"); if (change.kind === "added" && (change.before.present || !change.after.present)) fail("added workspace change has invalid snapshots"); if (change.kind === "removed" && (!change.before.present || change.after.present)) fail("removed workspace change has invalid snapshots"); if (change.kind === "modified" && (!change.before.present || !change.after.present)) fail("modified workspace change has invalid snapshots"); }
  page(value.page, "workspace-diff.page"); if (value.page.returned !== value.changes.length) fail("workspace-diff returned count does not match changes");
}
function validateComparison(value, nodeIds) { envelope(value, { schema: "pysolate.run-comparison.v1", required: ["comparison_id", "left_run_id", "right_run_id", "same_dimensions", "different_dimensions", "call_deltas", "workspace_deltas", "reason_codes", "page"] }, "run-comparison"); nonEmpty(value.comparison_id, "comparison_id"); if (!nodeIds.has(value.left_run_id) || !nodeIds.has(value.right_run_id) || value.left_run_id === value.right_run_id) fail("comparison run relation is invalid"); const same = new Set(value.same_dimensions); const different = new Set(value.different_dimensions); for (const [label, values] of [["same_dimensions", value.same_dimensions], ["different_dimensions", value.different_dimensions]]) { array(values, `comparison.${label}`); if (values.some((item) => typeof item !== "string" || !DIMENSIONS.has(item))) fail(`comparison.${label} has unsupported dimension`); if (new Set(values).size !== values.length) fail(`comparison.${label} must be unique`); } for (const item of same) if (different.has(item)) fail("comparison dimensions cannot overlap");
  array(value.call_deltas, "comparison.call_deltas"); for (const [index, delta] of value.call_deltas.entries()) { exact(delta, ["operation_index", "kind", "left_sha256", "right_sha256"], `comparison.call_deltas[${index}]`); integer(delta.operation_index, "comparison call operation_index"); member(delta.kind, DELTA_KINDS, "comparison call kind"); digest(delta.left_sha256, "comparison call left_sha256", true); digest(delta.right_sha256, "comparison call right_sha256", true); }
  array(value.workspace_deltas, "comparison.workspace_deltas"); for (const [index, delta] of value.workspace_deltas.entries()) { exact(delta, ["path", "kind"], `comparison.workspace_deltas[${index}]`); nonEmpty(delta.path, "comparison workspace path"); member(delta.kind, new Set(["same", "modified", "added", "removed", "unavailable"]), "comparison workspace kind"); }
  array(value.reason_codes, "comparison.reason_codes"); for (const reason of value.reason_codes) nonEmpty(reason, "comparison reason code"); page(value.page, "comparison.page"); if (value.page.returned !== value.call_deltas.length + value.workspace_deltas.length) fail("comparison returned count does not match visible deltas");
}
function validateProblem(value, runId, nodeIds) { const required = ["problem_id", "code", "severity", "scope"]; if ("run_id" in value) required.push("run_id"); if ("ref_sha256" in value) required.push("ref_sha256"); envelope(value, { schema: "pysolate.problem.v1", required }, "problem"); nonEmpty(value.problem_id, "problem_id"); member(value.code, PROBLEM_CODES, "problem.code"); member(value.severity, new Set(["info", "warning", "error"]), "problem.severity"); member(value.scope, PROBLEM_SCOPES, "problem.scope"); if ("run_id" in value && value.run_id !== null && !nodeIds.has(value.run_id)) fail("problem.run_id is unknown"); if ("ref_sha256" in value) digest(value.ref_sha256, "problem.ref_sha256"); }
function validateObjectRef(value) { envelope(value, { schema: "pysolate.object-ref.v1", required: ["ref"] }, "object-ref"); validateRef(value.ref, "object-ref.ref"); }
function validateIndex(value, fileDigests) { envelope(value, { schema: "pysolate.lab-index.v1", required: ["links", "capabilities", "page"] }, "lab-index"); array(value.links, "lab-index.links"); const expected = new Map([["study", "study-summary.v1"], ["run", "run-detail.v1"], ["timeline", "timeline-page.v1"], ["branch", "branch-dag.v1"], ["workspace", "workspace-diff.v1"], ["comparison", "run-comparison.v1"], ["reference", "object-ref.v1"], ["problem", "problem.v1"]]); for (const link of value.links) { exact(link, ["rel", "kind", "sha256"], "lab-index link"); if (expected.get(link.rel) !== link.kind || fileDigests.get(link.kind) !== link.sha256) fail("lab-index link does not match canonical file digest"); expected.delete(link.rel); } if (expected.size) fail("lab-index links are incomplete"); if (JSON.stringify(value.capabilities) !== JSON.stringify(["branch_dag", "comparison", "timeline", "workspace_diff"])) fail("lab-index capabilities are not canonical"); page(value.page, "lab-index.page"); if (value.page.returned !== value.links.length) fail("lab-index returned count does not match links"); }

/** Validate and return raw Go-produced canonical v1 JSON without creating a draft-shaped copy. */
export function validateCanonicalFixture(files, fixtureState = "unknown") {
  object(files, `${fixtureState} files`);
  const fileDigests = new Map();
  for (const [key, stem, schema] of SCHEMA_FILES) { if (!(key in files)) fail(`${fixtureState} is missing ${key}`); const value = files[key]; object(value, `${fixtureState}.${key}`); if (value.schema_version !== schema) fail(`${fixtureState}.${key} has the wrong schema`); fileDigests.set(stem, files.__sha256?.[key] ?? null); }
  if (!files.__sha256) fail(`${fixtureState} must provide source file sha256 metadata`);
  for (const [key] of SCHEMA_FILES) digest(files.__sha256[key], `${fixtureState}.__sha256.${key}`);
  for (const [key, stem] of SCHEMA_FILES) fileDigests.set(stem, files.__sha256[key]);
  const source = files.index.source_sha256; for (const [key] of SCHEMA_FILES) if (files[key].source_sha256 !== source) fail(`${fixtureState}.${key} source_sha256 does not match lab index`);
  validateIndex(files.index, fileDigests); validateStudy(files.study); validateRun(files.run); const nodeIds = validateBranch(files.branch); validateTimeline(files.timeline, files.run.run_id); validateWorkspace(files.workspace, files.run.run_id, nodeIds); validateComparison(files.comparison, nodeIds); validateProblem(files.problem, files.run.run_id, nodeIds); validateObjectRef(files.objectRef);
  if (files.timeline.page.truncated !== (files.run.evidence_completeness === "truncated" || files.timeline.page.total > files.timeline.page.returned)) fail(`${fixtureState} timeline truncation semantics are inconsistent`);
  if (files.run.evidence_completeness === "incomplete" && !files.run.problem_codes.includes("evidence_incomplete")) fail(`${fixtureState} incomplete run lacks evidence_incomplete problem code`);
  if (files.run.evidence_completeness === "truncated" && !files.run.problem_codes.includes("projection_truncated")) fail(`${fixtureState} truncated run lacks projection_truncated problem code`);
  if (files.run.refs.some((ref) => ref.availability === "unavailable") && !files.run.problem_codes.includes("object_unavailable")) fail(`${fixtureState} unavailable object lacks object_unavailable problem code`);
  return files;
}
export const CANONICAL_FILE_KEYS = Object.freeze(SCHEMA_FILES.map(([key]) => key));
