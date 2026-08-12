# Workload evaluation v1

Status: **Observed, bounded, mechanism-only study.** The evaluation contracts and runner are implemented; the treatments and results below apply only to the exact three-workload corpus, signed Host source, qualified CPython/WASI artifact, canonical plan, and local environment bound by the private study. This is not a Runtime release claim.

## Question and claim boundary

The study asks whether the current research mechanisms can carry three fixed workloads through ordinary execution/capture, strict offline playback, Host-owned counterfactual branching, and one explicitly qualified deterministic-verification case while preserving exact task oracles and body-free evidence accounting.

The study may support only workload-qualified statements about:

- real Guest control-flow carriage;
- strict replay equivalence for the captured rows;
- observed divergence for the two sealed branch treatments;
- explicit unsupported treatments;
- task-oracle and evidence-completeness counts;
- content-addressed evidence reuse and physical/logical byte accounting.

The canonical plan, report and measurement summary prohibit these claims:

- arbitrary determinism;
- computer replacement;
- economic advantage;
- model quality;
- placement share;
- production readiness;
- token or latency benefit.

## Method

The frozen corpus contains:

1. `structured-source-v1`: two typed captured local sources, exact result and workspace oracles;
2. `stateful-local-v1`: one seeded workspace, zero Broker calls, exact result and workspace oracles;
3. `bounded-planning-v1`: one typed captured local source, bounded scoring trace and exact result oracle.

The plan expands the corpus against four ordered treatments: live capture, offline replay, counterfactual branch, and deterministic verification. Unsupported combinations remain offered rows. Supported rows execute in fresh CPython/WASI Guests. Live sources are loopback-only and closed before offline replay. Strict playback must consume the complete transcript and finalize successfully. Branch substitutions are Host-owned at typed capability-operation boundaries. Deterministic verification is admitted only for the workspace-free bounded-planning row with fixed captured input.

Setup is outside execution timing. Every supported row passes a task-specific oracle before required evidence publication. Raw rows and body-free row evidence remain in a caller-declared private `0700` study directory with `0600` files. A separate process rebuilds the report from canonical corpus, plan and raw study and must reproduce the exact report identity.

## Observed result

| Measure | Numerator | Denominator |
|---|---:|---:|
| Started rows | 9 | 12 offered |
| Completed rows | 9 | 12 offered |
| Failed rows | 0 | 12 offered |
| Timed-out rows | 0 | 12 offered |
| Unsupported rows | 3 | 12 offered |
| Oracle passed | 9 | 9 started |
| Evidence complete | 9 | 9 started |
| Replay equivalent | 4 | 4 replay-checked |
| Branch divergent | 2 | 2 branch-checked |
| Reused LabStore puts | 16 | 18 put attempts |

The replay denominator is four checks, not four distinct replay-treatment rows. Each of the three workloads contributes its planned offline-replay row; the stateful-local row compares against its no-Broker baseline while the two captured-source rows consume their sealed tapes. The fourth check is the bounded-planning deterministic-verification row, whose admitted fixed-input profile also requires two fresh Guests to consume the same captured tape and return equal results. Thus three offline-replay rows plus one deterministic-treatment replay prerequisite gives 4/4; this does not multiply the workload cohort or claim a fourth source semantics.

The body-free measurement summary records 28,215 logical bytes attempted and a 3,714-byte physical-store delta. These are local mechanism accounting values, not a compression, cost, or performance claim. Physical storage can exceed logical input for low-reuse shapes because object framing and privacy indexes add overhead.

The three unsupported rows are explicit rather than omitted:

- structured-source deterministic verification is outside v1;
- stateful-local branch has no typed capability boundary;
- stateful-local deterministic verification is denied because the treatment mounts a workspace.

## Independent checks

Acceptance requires all of the following:

- canonical corpus/plan/raw/report/measurement strict decoding and identity checks;
- exact 12-row conservation and no extra or missing row;
- independent report rebuild identity equality;
- one materialized body-free evidence file for each of the nine supported rows, with filename and bytes hashing to the report reference;
- no evidence reference for unsupported rows;
- portable report/measurement privacy scan;
- private directory/file mode checks;
- full Go tests, race detector, vet, command builds, Python tests and compile checks;
- independent post-fix review with no unresolved blocker.

## Negative evidence and limitations

- The stateful workload is not admitted to deterministic verification. Fresh-Guest equality in its ordinary no-Broker path does not promote it to deterministic support.
- The report does not contain timeline events, workspace entries, branch lineage, or underlying typed object identities. Lab v1 projection therefore emits empty views and distinct private/unavailable relation markers under `evidence_incomplete`; it does not invent available objects.
- Timing values are local diagnostics and are intentionally not published as comparative results.
- The workload cohort has three fixed programs and no model-generated quality dimension. It cannot estimate broad workload coverage, placement share, product economics, or user value.
- External reads are local loopback fixtures. The study does not exercise public-network drift, credentials, external writes, or ambiguous remote effects.
- Artifact identity and local gates establish consistency with the tested bytes, not authorship, universal portability, or semantic correctness beyond the named oracles.
- LabStore fault probes preserve immutable-object identity and fail-private behavior at the tested boundaries, but crash-leftover or live stages make aggregate traversal fail closed. There is no cross-process snapshot isolation or online orphan repair.

## Next decision

A read-only candidate audit for a possible successor study is recorded in
[workload-candidate-audit-v2.md](workload-candidate-audit-v2.md). It proposes a
ten-workload Direct-versus-Guest mechanism cohort and two pilots; it is not an
implemented corpus, approved execution roadmap, or evaluation result.

Do not introduce SQLite metadata for evaluation v1. The remaining measured store gap is online distinction between live and orphan filesystem stages, not demonstrated identity divergence or privacy downgrade. SQLite would add a second durability domain without removing external object stages.

Filesystem recovery hardening now provides exclusive cross-process writer ownership, shared readers, explicit offline orphan-stage audit/repair, and retention/sweep exclusion under exclusive lifecycle ownership. The crash/concurrency matrix was rerun after that change. Online aggregate traversal still cannot distinguish a live stage from an orphan and therefore remains fail-closed. Reconsider SQLite only if this explicit filesystem protocol later shows unacceptable metadata contention, transactional multi-record requirements, indexed-query cost, or recovery complexity.

The Runtime/Lab ownership boundary remains unchanged: Runtime owns fresh execution, admission, capability authority and bounded evidence contracts; the Experimental research layer stores and projects evidence but cannot authorize execution or reinterpret a digest as authority.
