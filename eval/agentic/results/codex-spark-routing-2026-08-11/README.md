# Codex Spark routing development evidence

This directory records two clean development replicates of the six-task balanced routing diagnostic using:

- model: `gpt-5.3-codex-spark`
- transport: standalone Codex CLI `0.146.0`
- reasoning: `xhigh`
- sandbox: `read-only`
- treatment: `hybrid-two-stage-prebound-compact-json-v4`
- real CPython/WASI Guest for the Python and Hybrid-Python arms

## Result

Across 12 trials per condition:

| Condition | Passed | Provider calls | Total Codex CLI tokens |
| --- | ---: | ---: | ---: |
| Direct | 11/12 | 31 | 523,521 |
| Python/Pysolate | 10/12 | 12 | 193,471 |
| Hybrid | 5/12 | 35 | 574,248 |

Nine Direct/Python pairs passed on both arms. Within those matched-pass pairs, Python used 62.15% fewer total Codex CLI tokens and 60.87% fewer provider calls.

The result supports a bounded development claim: a single Pysolate workflow can preserve task success on this corpus while collapsing multiple model/Host round trips. It does **not** support enabling the current two-stage Hybrid router, which passed only 5/12 trials.

## Boundaries

This evidence is not decision-eligible and does not establish:

- Computer replacement rate;
- latency reduction;
- profile-qualified placement;
- general workload success rate;
- production model-routing quality.

The corpus has only six tasks and two clean replicates. Codex CLI token totals include its standalone session context and reasoning usage, so they are suitable for the paired comparison here but are not API billing estimates.

`summary.json` contains the machine-readable aggregate, exact identities, prohibited claims, and SHA-256 digest for every trial and regret artifact. Each trial artifact is independently validated by the repository tests.
