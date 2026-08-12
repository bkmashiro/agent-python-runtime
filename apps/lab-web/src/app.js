import { canonicalByState, CANONICAL_STATES } from "./canonical/index.mjs";
import { validateCanonicalFixture } from "./lib/canonical-adapter.mjs";

const state = {
  fixture: CANONICAL_STATES.includes(new URLSearchParams(location.hash.slice(1)).get("fixture"))
    ? new URLSearchParams(location.hash.slice(1)).get("fixture") : "ordinary",
  step: 0,
  playing: false,
  timer: null,
};

const $ = (selector) => document.querySelector(selector);
const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
const shortDigest = (value) => value ? `${value.slice(0, 15)}…${value.slice(-8)}` : "not recorded";
const titleCase = (value) => String(value || "none").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());

function model() {
  const documents = canonicalByState[state.fixture];
  validateCanonicalFixture(documents);
  return documents;
}

function stepsFor(documents) {
  return documents.timeline.events.map((event, index, events) => {
    const final = event.type === "execution.completed" || event.type === "execution.failed" || index === events.length - 1;
    const first = event.type === "execution.started" || index === 0;
    return {
      event,
      title: event.type === "capability.call" ? event.capability : event.type,
      kind: event.type === "capability.call" ? "Typed Host call" : event.type.startsWith("execution.") ? "Runtime lifecycle" : "Observed event",
      checkpoint: first ? "initial" : final ? "final" : "unavailable",
    };
  });
}

function explanation(step) {
  if (step.event.type === "execution.started") return {
    lead: "A fresh Guest execution starts with a frozen capability plan and an initial workspace identity.",
    detail: "The Runtime observes this boundary directly. Python locals and interpreter heap are deliberately outside the trace contract.",
  };
  if (step.event.type === "capability.call") return {
    lead: `Guest code requested the typed Host capability ${step.event.capability}.`,
    detail: "The Host validates the frozen grant, schema and budget, then records argument/result digests and outcome. The operation can be replayed as recorded evidence without silently repeating an external effect.",
  };
  if (step.event.type === "execution.completed") return {
    lead: "The Guest returned successfully and the Host finalized result and workspace evidence.",
    detail: "The final workspace checkpoint is compared with the initial checkpoint. This view shows only paths represented by the bounded canonical diff, not an invented full tree.",
  };
  if (step.event.type === "execution.failed") return {
    lead: "Execution reached a terminal failure boundary.",
    detail: "Failure and evidence completeness are separate: a failed run may still have valid partial evidence, while dropped evidence must remain explicit.",
  };
  return { lead: titleCase(step.event.type), detail: "This is an observed operation boundary from the canonical trace." };
}

function fields(rows) {
  return rows.map(([name, value, tone]) => `<div><dt>${esc(name)}</dt><dd class="${tone || ""}">${esc(value)}</dd></div>`).join("");
}

function renderTrace(documents, steps) {
  const run = documents.run;
  $("#fixture-select").value = state.fixture;
  $("#step-count").textContent = `${state.step + 1} / ${steps.length}`;
  $("#run-summary").innerHTML = `
    <strong>${esc(run.run_id)}</strong>
    <span>${esc(run.workload_id)}</span>
    <div><b class="${run.status}">${esc(titleCase(run.status))}</b><b>${esc(run.oracle_status === "passed" ? "Oracle passed" : `Oracle ${run.oracle_status}`)}</b><b class="${run.evidence_completeness}">${esc(titleCase(run.evidence_completeness))} evidence</b></div>`;
  $("#step-list").innerHTML = steps.map((step, index) => `
    <li><button type="button" data-step="${index}" class="${index === state.step ? "selected" : ""}" aria-current="${index === state.step ? "step" : "false"}">
      <span class="step-index">${String(index + 1).padStart(2, "0")}</span>
      <i class="step-line"></i>
      <span class="step-copy"><small>${esc(step.kind)}</small><strong>${esc(step.title)}</strong><em>${esc(step.event.outcome || "observed")}</em></span>
    </button></li>`).join("");
  const completeness = run.evidence_completeness;
  $("#trace-boundary").innerHTML = `<strong>${completeness === "complete" ? "Trace boundary" : `${titleCase(completeness)} trace`}</strong><p>${completeness === "complete" ? "All canonical operation-boundary events in this projection are present." : "Do not infer omitted events or state from this projection."}</p>`;
}

function renderStep(documents, steps) {
  const step = steps[state.step];
  const event = step.event;
  const copy = explanation(step);
  $("#step-kicker").textContent = `STEP ${String(state.step + 1).padStart(2, "0")} · ${step.kind}`;
  $("#step-title").textContent = step.title;
  $("#step-status").textContent = titleCase(event.outcome || "observed");
  $("#step-status").className = `status-pill ${event.outcome || "none"}`;
  $("#progress-bar").style.width = `${((state.step + 1) / steps.length) * 100}%`;
  $("#progress-label").textContent = `Step ${state.step + 1} of ${steps.length}`;
  $("#previous-step").disabled = state.step === 0;
  $("#next-step").disabled = state.step === steps.length - 1;
  $("#play-steps").textContent = state.playing ? "Ⅱ Pause" : "▶ Play";
  $("#step-explanation").innerHTML = `<strong>${esc(copy.lead)}</strong><p>${esc(copy.detail)}</p>`;
  $("#operation-sequence").textContent = `sequence ${event.sequence}`;
  $("#operation-title").textContent = step.title;
  $("#operation-fields").innerHTML = fields([
    ["Event type", event.type],
    ["Parent", event.parent_sequence ? `sequence ${event.parent_sequence}` : "root boundary"],
    ["Outcome", event.outcome || "not applicable", event.outcome === "ok" ? "good" : ""],
    ["Operation index", event.type === "capability.call" ? event.operation_index : "not applicable"],
  ]);
  $("#authority-fields").innerHTML = fields([
    ["Capability", event.capability || "none"],
    ["Arguments", shortDigest(event.arguments_sha256)],
    ["Result", shortDigest(event.result_sha256)],
    ["Evidence source", shortDigest(documents.run.source_sha256)],
  ]);
  $("#limitations-copy").textContent = step.checkpoint === "unavailable"
    ? "No filesystem checkpoint exists at this intermediate operation. The UI must not reconstruct one from the final diff."
    : step.checkpoint === "initial"
      ? "Initial workspace identity is verified. This fixture exposes only before-state metadata for paths that later changed."
      : "Final workspace identity and bounded changed-path metadata are verified. File bodies are not included in portable fixtures.";
  renderFilesystem(documents, step);
}

function renderFilesystem(documents, step) {
  const diff = documents.workspace;
  const unavailable = step.checkpoint === "unavailable";
  const privateState = state.fixture === "private";
  const truncated = documents.timeline.page.truncated || diff.page.truncated;
  $("#checkpoint-status").textContent = unavailable ? "NOT CHECKPOINTED" : `${step.checkpoint.toUpperCase()} CHECKPOINT`;
  $("#checkpoint-status").className = `checkpoint-pill ${unavailable ? "missing" : "verified"}`;
  $("#checkpoint-meta").innerHTML = unavailable
    ? `<strong>Intermediate state unavailable</strong><p>The Runtime did not capture a workspace manifest at this operation boundary.</p>`
    : `<strong>${step.checkpoint === "initial" ? "Before Run" : "After Run"}</strong><p>${truncated ? "Bounded / truncated projection" : "Verified changed-path projection"}</p>`;
  $("#tree-scope").textContent = unavailable ? "no checkpoint" : `${diff.changes.length} changed path${diff.changes.length === 1 ? "" : "s"}${truncated ? " · truncated" : ""}`;

  if (unavailable) {
    $("#filesystem-tree").innerHTML = `<div class="empty-tree"><span>∅</span><strong>No state at this step</strong><p>Move to Run start or completion.</p></div>`;
    renderFile(null, null, privateState);
    return;
  }
  const rows = diff.changes.map((change, index) => {
    const file = step.checkpoint === "initial" ? change.before : change.after;
    const present = Boolean(file.present);
    return `<button type="button" class="tree-file ${present ? "" : "absent"}" data-file="${index}" ${present ? "" : "disabled"}>
      <span class="tree-guide">└─</span><span class="file-icon">${present ? "▱" : "×"}</span>
      <span><strong>${esc(change.path)}</strong><small>${present ? `${file.size_bytes} bytes · ${shortDigest(file.sha256)}` : `${change.kind} · absent at this checkpoint`}</small></span>
    </button>`;
  }).join("");
  $("#filesystem-tree").innerHTML = `<div class="tree-root"><strong>▾ workspace</strong><span>evidence-scoped tree</span></div>${rows || `<div class="empty-tree"><strong>No changed paths</strong></div>`}`;
  renderFile(null, null, privateState);
  $("#filesystem-tree").querySelectorAll("[data-file]").forEach((button) => button.addEventListener("click", () => {
    const change = diff.changes[Number(button.dataset.file)];
    renderFile(change, step.checkpoint, privateState);
    $("#filesystem-tree").querySelectorAll(".tree-file").forEach((node) => node.classList.toggle("selected", node === button));
  }));
}

function renderFile(change, checkpoint, privateState) {
  if (!change) {
    $("#file-title").textContent = "Select an available file";
    $("#file-availability").textContent = "METADATA ONLY";
    $("#file-fields").innerHTML = fields([["Content", "Not carried in portable Lab fixture"], ["Scope", "Changed paths only"]]);
    $("#file-preview").hidden = true;
    return;
  }
  const file = checkpoint === "initial" ? change.before : change.after;
  $("#file-title").textContent = change.path;
  $("#file-availability").textContent = privateState ? "PRIVATE / UNAVAILABLE" : "BODY NOT BUNDLED";
  $("#file-fields").innerHTML = fields([
    ["State", file.present ? "present" : "absent"],
    ["Size", `${file.size_bytes} bytes`],
    ["Executable", file.executable ? "yes" : "no"],
    ["SHA-256", shortDigest(file.sha256)],
    ["Content", privateState ? "private and unavailable" : "requires authorized private object store"],
  ]);
  $("#file-preview").hidden = true;
}

function render() {
  stopPlayback();
  const documents = model();
  const steps = stepsFor(documents);
  state.step = Math.min(state.step, Math.max(0, steps.length - 1));
  if (!steps.length) {
    $("#fixture-select").value = state.fixture;
    $("#step-count").textContent = "0 / 0";
    $("#run-summary").innerHTML = `<strong>${esc(documents.run.run_id)}</strong><span>${esc(documents.run.workload_id)}</span><div><b class="${documents.run.status}">${esc(titleCase(documents.run.status))}</b><b class="${documents.run.evidence_completeness}">${esc(titleCase(documents.run.evidence_completeness))} evidence</b></div>`;
    $("#step-list").innerHTML = `<li class="empty-run">No portable timeline events are available for this recorded run.</li>`;
    $("#trace-boundary").innerHTML = `<strong>Evidence unavailable</strong><p>The UI does not infer steps from private or absent bodies.</p>`;
    $("#step-count").textContent = "0 / 0";
    $("#step-kicker").textContent = "NO PORTABLE EVENTS";
    $("#step-title").textContent = "Execution steps unavailable";
    $("#step-status").textContent = "UNAVAILABLE";
    $("#progress-label").textContent = "No steps";
    $("#progress-bar").style.width = "0";
    $("#previous-step").disabled = true;
    $("#next-step").disabled = true;
    $("#play-steps").disabled = true;
    $("#step-explanation").innerHTML = `<strong>Portable metadata exists, but this fixture exposes no timeline events.</strong><p>Protected or absent evidence is not reconstructed.</p>`;
    $("#operation-title").textContent = "No operation selected";
    $("#operation-fields").innerHTML = "";
    $("#authority-fields").innerHTML = fields([["Evidence completeness", documents.run.evidence_completeness], ["Problem", documents.run.problem_codes.join(", ") || "none"]]);
    $("#limitations-copy").textContent = "No Agent or Runtime steps can be replayed from this portable projection.";
    $("#checkpoint-status").textContent = "UNAVAILABLE";
    $("#checkpoint-meta").innerHTML = `<strong>Workspace tree unavailable</strong><p>Protected bodies are not copied into this viewer.</p>`;
    $("#tree-scope").textContent = "no portable tree";
    $("#filesystem-tree").innerHTML = `<div class="empty-tree"><span>∅</span><strong>No portable filesystem state</strong></div>`;
    renderFile(null, null, true);
    return;
  }
  $("#play-steps").disabled = false;
  renderTrace(documents, steps);
  renderStep(documents, steps);
  $("#step-list").querySelectorAll("[data-step]").forEach((button) => button.addEventListener("click", () => selectStep(Number(button.dataset.step))));
  $("#live-region").textContent = `Showing ${steps[state.step].title}, step ${state.step + 1} of ${steps.length}`;
}

function selectStep(index) {
  const steps = stepsFor(model());
  if (!steps.length) return;
  state.step = Math.max(0, Math.min(index, steps.length - 1));
  renderTrace(model(), steps);
  renderStep(model(), steps);
  $("#step-list").querySelectorAll("[data-step]").forEach((button) => button.addEventListener("click", () => selectStep(Number(button.dataset.step))));
  $("#live-region").textContent = `Step ${state.step + 1}: ${steps[state.step].title}`;
}

function stopPlayback() {
  if (state.timer) clearInterval(state.timer);
  state.timer = null;
  state.playing = false;
}

function togglePlayback() {
  const steps = stepsFor(model());
  if (!steps.length) return;
  if (state.playing) { stopPlayback(); selectStep(state.step); return; }
  if (state.step === steps.length - 1) state.step = 0;
  state.playing = true;
  selectStep(state.step);
  state.playing = true;
  $("#play-steps").textContent = "Ⅱ Pause";
  state.timer = setInterval(() => {
    if (state.step >= steps.length - 1) { stopPlayback(); selectStep(state.step); return; }
    state.step += 1;
    renderTrace(model(), steps);
    renderStep(model(), steps);
    $("#step-list").querySelectorAll("[data-step]").forEach((button) => button.addEventListener("click", () => selectStep(Number(button.dataset.step))));
  }, 1400);
}

$("#fixture-select").addEventListener("change", (event) => {
  stopPlayback();
  state.fixture = event.target.value;
  state.step = 0;
  state.selectedFile = null;
  history.replaceState(null, "", `#fixture=${encodeURIComponent(state.fixture)}`);
  render();
});
window.addEventListener("hashchange", () => {
  const fixture = new URLSearchParams(location.hash.slice(1)).get("fixture");
  if (!CANONICAL_STATES.includes(fixture) || fixture === state.fixture) return;
  stopPlayback();
  state.fixture = fixture;
  state.step = 0;
  state.selectedFile = null;
  render();
});
$("#previous-step").addEventListener("click", () => selectStep(state.step - 1));
$("#next-step").addEventListener("click", () => selectStep(state.step + 1));
$("#play-steps").addEventListener("click", togglePlayback);
window.addEventListener("keydown", (event) => {
  if (["INPUT", "SELECT", "TEXTAREA"].includes(document.activeElement?.tagName)) return;
  if (event.key === "ArrowLeft") selectStep(state.step - 1);
  if (event.key === "ArrowRight") selectStep(state.step + 1);
  if (event.key === " ") { event.preventDefault(); togglePlayback(); }
});
render();
