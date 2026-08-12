# Evaluation runner and measurement contract

Status: **Current for the mechanism-only Track D runner contract.** The local study measures only the fixed three-workload corpus and admitted treatments. It does not establish model quality, token or latency benefit, economic advantage, arbitrary determinism, computer replacement or production readiness.

## Row lifecycle

The runner expands rows in corpus order, plan treatment order and repetition order. Every offered combination receives the deterministic identity `sha256(workload_id + treatment + repetition)`. Unsupported combinations remain explicit rows and never enter setup or execution.

Supported rows move through Host-owned phases:

1. setup (outside execution timing);
2. execution;
3. task-specific oracle;
4. required evidence publication;
5. terminal finalization.

Out-of-order transitions reject. A valid required-evidence failure is permanent. Invalid recorder input does not mutate terminal state. Completed, failed, timed-out and unsupported totals must conserve exactly to offered rows.

## Measurements

Wall-clock phase values are local diagnostics only. They are not a portable latency benchmark and are not summarized as a universal performance claim.

The body-free measurement document publishes explicit numerator and denominator counts for:

- started and terminal statuses;
- oracle passes and evidence completeness;
- replay checks and equivalent replays;
- branch checks and divergent branches;
- LabStore put attempts and reused puts;
- attempted logical bytes and resulting physical-store byte deltas.

Physical stored bytes may exceed logical bytes because filesystem objects and privacy indexes have framing overhead. No storage-saving claim is inferred from either value.

## Identities and privacy

The plan binds the exact Host commit, qualified Guest artifact and manifest, workload corpus and semantic Runtime profile. Corpus, plan, raw study, report and measurement documents are strict canonical JSON with SHA-256 identities.

Raw rows and per-row body-free evidence documents remain under a caller-declared absolute private root with directory mode `0700` and file mode `0600`. The writer uses rooted filesystem operations, rejects symlink escape and refuses to overwrite an existing study directory. Portable report rows contain only identities and bounded metadata; they do not contain workload source, inputs, prompts, workspace bodies, endpoints, credentials, provider payloads or Host paths.

An independent process must decode the stored corpus, plan and raw study, expand rows again, rebuild the report and reproduce the exact canonical report identity before the study is accepted.
