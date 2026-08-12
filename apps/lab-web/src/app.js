import { canonicalByState, CANONICAL_STATES } from "./canonical/index.mjs";
import { validateCanonicalFixture } from "./lib/canonical-adapter.mjs";
import { decodeLocationState, encodeLocationState } from "./state.js";

for (const state of CANONICAL_STATES) validateCanonicalFixture(canonicalByState[state], state);

const LABELS = {
  ordinary: { title: "Offline workload evidence study", subtitle: "A complete bounded lifecycle projection with one task, one treatment, and portable evidence metadata." },
  branched: { title: "Counterfactual branch study", subtitle: "A parent/child run relation with bounded capability and workspace deltas." },
  incomplete: { title: "Incomplete evidence study", subtitle: "The task failed; the projection remains inspectable without upgrading it to complete evidence." },
  truncated: { title: "Bounded projection study", subtitle: "The run completed, while one or more canonical pages explicitly report omitted records." },
  private: { title: "Private evidence study", subtitle: "Portable metadata remains visible; private result and workspace objects remain unavailable." },
};
const VIEW_TITLES = { overview: "Study overview", runs: "Run detail", timeline: "Capability timeline", lineage: "Branch DAG", workspace: "Workspace diff", comparison: "Comparison" };
const STATUS_LABELS = { completed: "Completed", failed: "Failed", timed_out: "Timed out", unsupported: "Unsupported" };
const EVIDENCE_LABELS = { complete: "Complete", incomplete: "Incomplete", truncated: "Truncated" };
const escapeHtml = (value) => String(value ?? "").replace(/[&<>'"]/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[character]));
const shortDigest = (value) => value ? `${value.slice(0, 19)}…${value.slice(-8)}` : "—";
const formatBytes = (value) => `${(Number(value) / 1024).toFixed(value % 1024 ? 1 : 0)} KB`;
const fixtureLabel = (state) => state === "private" ? "private / unavailable" : state.replace("_", " ");
const statusClass = (value) => `tone-${String(value).replaceAll("_", "-")}`;
const badge = (text, tone = "neutral") => `<span class="badge ${tone}">${escapeHtml(text)}</span>`;
const metric = (value, label, tone = "") => `<div class="metric"><strong class="metric-value ${tone}">${escapeHtml(value)}</strong><span class="metric-label">${escapeHtml(label)}</span></div>`;
const emptyState = (title, copy) => `<div class="empty-state"><span class="empty-icon">∅</span><strong>${escapeHtml(title)}</strong><p>${escapeHtml(copy)}</p></div>`;
const panel = (title, copy, body, className = "") => `<section class="panel ${className}" aria-labelledby="${title.toLowerCase().replaceAll(/[^a-z0-9]+/g, "-")}-heading"><div class="panel-heading"><div><p class="eyebrow">Canonical object</p><h3 id="${title.toLowerCase().replaceAll(/[^a-z0-9]+/g, "-")}-heading">${escapeHtml(title)}</h3></div>${copy ? `<p class="panel-copy">${escapeHtml(copy)}</p>` : ""}</div>${body}</section>`;
const table = (headers, rows, className = "") => `<div class="table-wrap"><table class="data-table ${className}"><thead><tr>${headers.map((header) => `<th scope="col">${escapeHtml(header)}</th>`).join("")}</tr></thead><tbody>${rows || `<tr><td colspan="${headers.length}">${emptyState("No records in projection", "This canonical page is explicitly empty; the viewer does not invent missing records.")}</td></tr>`}</tbody></table></div>`;

const model = { ...decodeLocationState(window.location.hash), runId: null, compareRunId: null };
function fixture() { return canonicalByState[model.fixture] ?? canonicalByState.ordinary; }
function runNodes() { return fixture().branch.nodes; }
function selectedRunId() { const ids = runNodes().map((node) => node.run_id); return ids.includes(model.runId) ? model.runId : fixture().run.run_id; }
function announce(text) { document.querySelector("#live-region").textContent = text; }
function navigate(changes = {}) { Object.assign(model, changes); if (changes.fixture && !changes.runId) model.runId = null; window.history.replaceState(null, "", encodeLocationState(model)); render(); }
function labelRun(node) { return node.run_id === fixture().run.run_id ? `${node.run_id} · focused` : node.run_id; }

function renderHeader(data) {
  const info = LABELS[model.fixture];
  document.querySelector("#study-title").textContent = info.title;
  document.querySelector("#study-subtitle").textContent = info.subtitle;
  document.querySelector("#study-badges").innerHTML = [badge("pysolate.lab-index.v1", "violet"), badge("mechanism-only", "blue"), badge("read-only", "green"), badge(fixtureLabel(model.fixture), model.fixture === "private" ? "rose" : "amber")].join("");
  document.querySelector("#rail-state").innerHTML = `<span class="rail-state-key">${escapeHtml(model.fixture)}</span><span>${escapeHtml(data.run.evidence_completeness)} projection</span>`;
  document.querySelector("#focus-title").textContent = selectedRunId();
  document.querySelector("#focus-meta").textContent = `${data.run.workload_id} / ${data.run.treatment} · ${STATUS_LABELS[data.run.status]} · oracle ${data.run.oracle_status}`;
  document.querySelector("#focus-digest").textContent = `source ${shortDigest(data.index.source_sha256)}`;
  document.querySelector("#fixture-select").value = model.fixture;
  document.querySelectorAll(".view-tab").forEach((button) => { button.setAttribute("aria-current", button.dataset.view === model.view ? "page" : "false"); });
  document.querySelectorAll(".route-list button").forEach((button) => button.classList.toggle("active", button.dataset.view === model.view));
}
function renderNotices(data) {
  const notices = [];
  if (data.run.evidence_completeness === "incomplete") notices.push(["warning", "Evidence incomplete", "Task/oracle status is visible, but the evidence projection is not complete. Do not read this as a successful study."]);
  if (data.run.evidence_completeness === "truncated") notices.push(["warning", "Projection truncated", `This page returns ${data.timeline.page.returned} of ${data.timeline.page.total} timeline records and exposes a next cursor. No “load more” action is implied.`]);
  if (data.run.refs.some((ref) => ref.availability === "unavailable")) notices.push(["private", "Private / unavailable", "One or more canonical object references are private. Only policy-safe metadata is shown; protected bodies are not copied or fetched."]);
  if (data.problem.code !== "none") notices.push(["info", `Problem · ${data.problem.code}`, `Canonical problem object scope: ${data.problem.scope}.`]);
  document.querySelector("#status-notices").innerHTML = notices.map(([kind, title, copy]) => `<div class="notice ${kind}"><span class="notice-mark">${kind === "private" ? "▣" : kind === "warning" ? "!" : "i"}</span><div><strong>${escapeHtml(title)}</strong><p>${escapeHtml(copy)}</p></div></div>`).join("");
}
function overview(data) {
  const totals = Object.fromEntries(data.study.status_totals.map(({ status, count }) => [status, count]));
  const refs = data.run.refs;
  return `<div class="view-stack">
    <div class="section-heading"><div><p class="eyebrow">01 / orientation</p><h2 id="view-title">Study overview</h2></div><p>Start here: identify what the projection contains before reading a run as evidence.</p></div>
    <div class="metric-band">${metric(data.study.workload_count, "workload")}${metric(data.study.treatment_count, "treatment")}${metric(totals.completed, "completed", "green")}${metric(totals.failed, "failed", totals.failed ? "rose" : "")}${metric(data.study.storage.object_count, "objects referenced")}</div>
    <div class="overview-grid">
      ${panel("Study contract", "study-summary.v1", `<dl class="definition-list"><div><dt>study_id</dt><dd>${escapeHtml(data.study.study_id)}</dd></div><div><dt>evidence_class</dt><dd>${escapeHtml(data.study.evidence_class)}</dd></div><div><dt>projection source</dt><dd>${shortDigest(data.study.source_sha256)}</dd></div><div><dt>generated_at_policy</dt><dd>${escapeHtml(data.study.generated_at_policy)}</dd></div></dl>`)}
      ${panel("Status semantics", "task ≠ oracle ≠ evidence", `<div class="legend-list"><div><span class="legend-dot blue"></span><div><strong>Task status</strong><p>${STATUS_LABELS[data.run.status]} — execution outcome.</p></div></div><div><span class="legend-dot violet"></span><div><strong>Oracle status</strong><p>${escapeHtml(data.run.oracle_status)} — bounded result check.</p></div></div><div><span class="legend-dot amber"></span><div><strong>Evidence status</strong><p>${EVIDENCE_LABELS[data.run.evidence_completeness]} — projection completeness.</p></div></div></div>`)}
    </div>
    ${panel("Canonical object inventory", "linked from lab-index.v1", table(["relation", "object", "sha256"], data.index.links.map((link) => `<tr><td><span class="relation">${escapeHtml(link.rel)}</span></td><td>${escapeHtml(link.kind)}</td><td class="digest">${shortDigest(link.sha256)}</td></tr>`).join("")))}
    ${panel("Prohibited claims", "guardrail carried by study-summary.v1", `<div class="claim-grid">${data.study.prohibited_claims.map((claim) => `<span>× ${escapeHtml(claim.replaceAll("_", " "))}</span>`).join("")}</div>`)}
  </div>`;
}
function runs(data) {
  const nodes = runNodes();
  const refs = data.run.refs.map((ref) => `<tr><td>${escapeHtml(ref.kind)}</td><td>${badge(ref.privacy, ref.privacy === "private" ? "rose" : "neutral")}</td><td>${badge(ref.availability, ref.availability === "unavailable" ? "rose" : "green")}</td><td class="digest">${shortDigest(ref.sha256)}</td></tr>`).join("");
  return `<div class="view-stack"><div class="section-heading"><div><p class="eyebrow">02 / inspection</p><h2 id="view-title">Run detail</h2></div><p>The canonical run object keeps task outcome, oracle outcome, evidence completeness, and reference policy separate.</p></div>
    <div class="run-id-row"><span class="run-id">${escapeHtml(data.run.run_id)}</span>${badge(STATUS_LABELS[data.run.status], statusClass(data.run.status))}${badge(`oracle ${data.run.oracle_status}`, data.run.oracle_status === "passed" ? "green" : "rose")}${badge(EVIDENCE_LABELS[data.run.evidence_completeness], data.run.evidence_completeness === "complete" ? "green" : "amber")}</div>
    <div class="run-grid"><div class="run-fields"><h3>Run fields</h3><dl class="definition-list"><div><dt>workload_id</dt><dd>${escapeHtml(data.run.workload_id)}</dd></div><div><dt>treatment</dt><dd>${escapeHtml(data.run.treatment)}</dd></div><div><dt>evidence_class</dt><dd>${escapeHtml(data.run.evidence_class)}</dd></div><div><dt>problem_codes</dt><dd>${data.run.problem_codes.length ? data.run.problem_codes.map((code) => badge(code, "amber")).join(" ") : "none"}</dd></div><div><dt>source_sha256</dt><dd class="digest">${shortDigest(data.run.source_sha256)}</dd></div></dl></div>
      <div class="node-picker"><h3>Projection nodes</h3><p class="muted">Select a canonical branch node to focus its relation.</p>${nodes.map((node) => `<button type="button" class="node-button ${node.run_id === selectedRunId() ? "selected" : ""}" data-run="${escapeHtml(node.run_id)}"><span class="node-dot ${statusClass(node.status)}"></span><span><b>${escapeHtml(labelRun(node))}</b><small>${STATUS_LABELS[node.status]} · ${EVIDENCE_LABELS[node.evidence_completeness]}</small></span></button>`).join("")}</div></div>
    ${panel("Reference policy", "metadata only · no protected body", table(["kind", "privacy", "availability", "digest"], refs))}
  </div>`;
}
function timeline(data) {
  const rows = data.timeline.events.map((event) => `<tr><td class="sequence">${event.sequence.toString().padStart(2, "0")}</td><td><span class="event-type">${escapeHtml(event.type)}</span>${event.capability ? `<small class="event-capability">${escapeHtml(event.capability)}</small>` : ""}</td><td>${badge(event.outcome, event.outcome === "ok" ? "green" : "neutral")}</td><td class="mono">${event.arguments_sha256 ? shortDigest(event.arguments_sha256) : "—"}</td><td class="mono">${event.result_sha256 ? shortDigest(event.result_sha256) : "—"}</td></tr>`).join("");
  return `<div class="view-stack"><div class="section-heading"><div><p class="eyebrow">03 / observation</p><h2 id="view-title">Capability timeline</h2></div><p>Ordered event metadata from timeline-page.v1. Empty means empty, not hidden success.</p></div>
    <div class="timeline-summary">${metric(data.timeline.events.length, "events returned")}${metric(data.timeline.page.total, "events total")}${metric(data.timeline.page.truncated ? "YES" : "NO", "page truncated", data.timeline.page.truncated ? "amber" : "green")}${metric(data.timeline.page.next_cursor || "none", "next cursor")}</div>
    ${panel("Event stream", "sequence / parent relation preserved", table(["seq", "event", "outcome", "arguments", "result"], rows, "timeline-table"))}
  </div>`;
}
function lineage(data) {
  const nodes = data.branch.nodes;
  const edges = data.branch.edges;
  const nodeRows = nodes.map((node) => `<tr><td><button type="button" class="inline-link" data-run="${escapeHtml(node.run_id)}">${escapeHtml(node.run_id)}</button></td><td>${badge(STATUS_LABELS[node.status], statusClass(node.status))}</td><td>${badge(EVIDENCE_LABELS[node.evidence_completeness], node.evidence_completeness === "complete" ? "green" : "amber")}</td></tr>`).join("");
  const edgeRows = edges.map((edge) => `<tr><td class="mono">${escapeHtml(edge.parent_run_id)}</td><td class="arrow">→</td><td class="mono">${escapeHtml(edge.child_run_id)}</td><td>fork op ${edge.fork_operation}</td><td>${escapeHtml(edge.suffix_mode)}</td></tr>`).join("");
  const visual = nodes.map((node, index) => `<div class="dag-node ${node.run_id === selectedRunId() ? "selected" : ""}" style="--dag-index:${index}"><span class="dag-marker">${index === 0 ? "R" : "C"}</span><div><b>${escapeHtml(node.run_id)}</b><small>${STATUS_LABELS[node.status]} · ${EVIDENCE_LABELS[node.evidence_completeness]}</small></div></div>`).join(edges.length ? `<div class="dag-connector" aria-hidden="true">↓</div>` : "");
  return `<div class="view-stack"><div class="section-heading"><div><p class="eyebrow">04 / lineage</p><h2 id="view-title">Branch DAG</h2></div><p>Branch nodes and edge metadata are inspectable; clicking only changes local focus.</p></div>
    <div class="dag-stage" role="img" aria-label="Branch lineage diagram">${visual}</div>
    ${panel("Node table", "accessible DAG representation", table(["run", "task status", "evidence"], nodeRows))}
    ${panel("Edges", edges.length ? "canonical parent → child relation" : "no branch edges in this projection", table(["parent", "", "child", "fork", "suffix"], edgeRows))}
  </div>`;
}
function workspace(data) {
  const rows = data.workspace.changes.map((change) => `<tr><td><span class="path">${escapeHtml(change.path)}</span></td><td>${badge(change.kind, change.kind === "modified" ? "amber" : "blue")}</td><td>${snapshotCell(change.before)}</td><td>${snapshotCell(change.after)}</td></tr>`).join("");
  return `<div class="view-stack"><div class="section-heading"><div><p class="eyebrow">05 / artifact view</p><h2 id="view-title">Workspace diff</h2></div><p>Guest-relative path and digest metadata only. File bodies and Host paths are intentionally absent.</p></div>
    <div class="diff-context"><span>base <b>${escapeHtml(data.workspace.base_run_id)}</b></span><span class="arrow">→</span><span>focused <b>${escapeHtml(data.workspace.run_id)}</b></span><span class="spacer"></span>${badge(`${data.workspace.changes.length} changes`, data.workspace.changes.length ? "blue" : "neutral")}${data.workspace.page.truncated ? badge("projection truncated", "amber") : ""}</div>
    ${panel("File metadata delta", "workspace-diff.v1", table(["Guest-relative path", "kind", "before", "after"], rows, "workspace-table"))}
    <div class="privacy-note"><span class="notice-mark">▤</span><p><strong>Privacy boundary</strong> This viewer never reads or reconstructs protected workspace bodies. Digests are equality metadata, not authority or correctness.</p></div>
  </div>`;
}
function snapshotCell(snapshot) { return `<div class="snapshot"><span class="snapshot-state ${snapshot.present ? "present" : "absent"}">${snapshot.present ? "present" : "absent"}</span><span class="mono">${snapshot.present ? `${formatBytes(snapshot.size_bytes)} · ${shortDigest(snapshot.sha256)}` : "—"}</span></div>`; }
function comparison(data) {
  const comparison = data.comparison;
  const callRows = comparison.call_deltas.map((delta) => `<tr><td>operation ${delta.operation_index}</td><td>${badge(delta.kind, delta.kind === "changed" ? "amber" : "neutral")}</td><td class="mono">${shortDigest(delta.left_sha256)}</td><td class="mono">${shortDigest(delta.right_sha256)}</td></tr>`).join("");
  const workspaceRows = comparison.workspace_deltas.map((delta) => `<tr><td class="path">${escapeHtml(delta.path)}</td><td>${badge(delta.kind, delta.kind === "modified" ? "amber" : "neutral")}</td></tr>`).join("");
  return `<div class="view-stack"><div class="section-heading"><div><p class="eyebrow">06 / interpretation</p><h2 id="view-title">Comparison</h2></div><p>A bounded relation with explicit sameness, difference, and reason codes — not a quality or performance claim.</p></div>
    <div class="compare-pair"><div><span class="pair-label">LEFT</span><b>${escapeHtml(comparison.left_run_id)}</b></div><span class="pair-arrow">⇄</span><div><span class="pair-label">RIGHT</span><b>${escapeHtml(comparison.right_run_id)}</b></div></div>
    <div class="comparison-grid">${panel("Same dimensions", "no observed delta", comparison.same_dimensions.length ? `<div class="chip-list">${comparison.same_dimensions.map((item) => `<span>${escapeHtml(item)}</span>`).join("")}</div>` : emptyState("No same dimensions", "The canonical comparison does not claim sameness."))}${panel("Different dimensions", "bounded delta", comparison.different_dimensions.length ? `<div class="chip-list different">${comparison.different_dimensions.map((item) => `<span>${escapeHtml(item)}</span>`).join("")}</div>` : emptyState("No different dimensions", "The canonical comparison does not claim a difference."))}</div>
    <div class="comparison-grid">${panel("Capability call deltas", "digest equality only", table(["operation", "kind", "left", "right"], callRows))}${panel("Workspace deltas", "path metadata only", table(["path", "kind"], workspaceRows))}</div>
    ${panel("Reason codes", "why this comparison exists", `<div class="reason-list">${comparison.reason_codes.map((reason) => `<span><b>↳</b> ${escapeHtml(reason)}</span>`).join("")}</div>`)}
  </div>`;
}
function render() {
  const data = fixture();
  renderHeader(data); renderNotices(data);
  const view = VIEW_TITLES[model.view] ? model.view : "overview";
  document.querySelector("#view-root").innerHTML = ({ overview, runs, timeline, lineage, workspace, comparison }[view])(data);
  document.querySelector("#view-title").textContent = VIEW_TITLES[view];
  document.querySelectorAll("[data-view]").forEach((element) => element.addEventListener("click", () => navigate({ view: element.dataset.view })));
  document.querySelectorAll("[data-run]").forEach((element) => element.addEventListener("click", () => navigate({ runId: element.dataset.run, view: "runs" })));
  announce(`${VIEW_TITLES[view]} opened for ${model.fixture}`);
}

document.querySelector("#fixture-select").addEventListener("change", (event) => navigate({ fixture: event.target.value, view: "overview", runId: null, compareRunId: null }));
window.addEventListener("hashchange", () => {
  Object.assign(model, decodeLocationState(window.location.hash));
  render();
});
render();
