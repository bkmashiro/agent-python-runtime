# Pysolate: execution budgets and offline reproducibility

Approved continuation from `a32e5bb5`. Prior product/COW work is complete and its evidence remains in `cow-data-image.md`, `service.md` and Git history.

## Current pointer

**Complete, including follow-up.** Offline replay now reports privacy-safe first-mismatch locations and separates runtime failures. The mixed-load check passed 64 cycles per Linux preparation mode (1,024 HTTP requests), with natural GC, stable descriptors, and released execution/workspace resources. See `offline-replay.md` and `service.md`. No runtime GC or scheduler changes were needed.

## Desired outcomes

- [x] P1a: service-configured maximum execution duration, default unchanged; requests may shorten but never exceed the configured cap. Respect an earlier caller deadline. Bound a durable attempt rather than Run lifetime; do not weaken intent/outcome persistence. Verify timeout releases Guest and execution capacity. Workspace per-attempt ownership must be released while the persistent workspace remains usable.
- [x] P1b: explicit default-off early reads for ordinary HTTP Run only. Existing Host `AllowEarlyRead` remains authoritative. Do not combine with recorded/durable or silently claim workspace support.
- [x] P2: sensitive local JSON export and strict offline replay of ended durable Runs. No workspace, pending waits or unresolved effects; preserve seed, artifact identity and ordered calls. No network export API, no physical tool fallback, no guessing missing tool namespace metadata. Explicit local output path, private permissions and no overwrite. Reproduce output or original Python error; reject changed artifact, changed calls or missing/excess outcomes.
- [x] P3: bounded real Python/NumPy workload diagnosis, separating import from execution. Retain an optimization only with measurable end-to-end benefit and acceptable setup/memory cost. No forced per-request GC or broad package preloading by default.
- [x] P4: actual Guest and HTTP validation, offline replay after closing original DB, invalid/tampered cases, docs, signed commits/push with remote verification and clean worktree.

## Protected boundaries

No new default timeout or execution strategy, engine fork, heavy dependency, generic workflow/recording platform, mutable Guest reuse, new authority, automated external effects or paid service. Host tools must cooperate with context; do not claim arbitrary Go callbacks can be forcibly interrupted. Do not alter existing database schema merely to make export universal; support a provable subset and reject unsupported metadata.

Export is not anonymization or authenticity verification. Source, inputs and tool results may contain secrets. An explicitly selected local bundle is untrusted data and must stay inside normal sandbox/tool boundaries. Never upload it automatically. Core lifecycle APIs and persistent workspace ownership remain unchanged.

## Execution and completion

One writer per overlapping path. Small reusable modules only where needed; no extra demo suite or dashboard. Each completed slice gets relevant real tests and a signed commit; parent verifies remote HEAD. If an experiment has no net benefit, document the negative result and stop that lane. Continue until the goal is complete or a product/authority/resource decision needs the user.
