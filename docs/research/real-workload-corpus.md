# Real workload corpus v1

Status: **Current** for the three fixed workload definitions, body-free descriptor identities and task-specific oracles. Execution qualification is **mechanism-only**. It does not establish arbitrary determinism, model quality, latency or token benefit, economic advantage, computer replacement, placement share, production readiness or general workload coverage.

## Families

- `structured-source-v1` reads the Host-approved demo catalog and benchmark manifest, ranks the catalog, writes `structured-report.json` and returns a bounded summary. Its counterfactual branch replaces the captured benchmark operation and must change result and workspace evidence.
- `stateful-local-v1` starts from the fixed `metrics.csv` seed, writes normalized and summary files and performs no capability call. Two fresh seeded Guests must produce the same semantic result and Runtime workspace identity. Branch is unsupported because there is no captured capability-operation boundary. Deterministic verification is unsupported while the workspace is mounted.
- `bounded-planning-v1` reads a Host-approved captured catalog, computes a bounded integer score trace and returns the selected candidate without mounting a workspace in its deterministic probe. Its branch replaces operation zero. Its deterministic result applies only to the fixed artifact, program and captured input.

Live source tests use process-local loopback HTTP servers only. Servers are closed before strict offline playback; unchanged hit counters prove playback did not initialize or contact the live source. There is no public-network test, generic HTTP capability, credential, write effect or Agent-controlled endpoint.

## Identity and privacy

`research/workloads/testdata/descriptors.json` is produced by the Go workload producer and compared byte-for-byte in tests. A descriptor includes only program/input/result digests, the seed-set identity, expected workspace path/kind/size/digest metadata, call count and explicit treatment dispositions. It contains no program, input, result, workspace or seed body, Host path, endpoint, credential, provider payload or authority.

`workspace_seed_sha256` is the domain-separated identity of the sorted seed path/body-digest set. It is not the seed body, a Runtime Capsule identity or authorization. Runtime workspace identities are obtained only from actual Guest execution.

Treatment absence is never interpreted as success. Each descriptor explicitly marks `live_capture`, `offline_playback`, `counterfactual_branch` and `deterministic` as `supported` or `unsupported` with a bounded reason.
