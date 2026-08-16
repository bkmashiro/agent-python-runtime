# attrs-770 artifact-bound package profile

**Status:** SUPPORTED on 2026-08-16. One exact, non-default pure-Python package profile now carries the patched `attrs-770` source inside the Guest artifact, binds its provenance and VFS tree in schema-v4 distribution evidence, qualifies the natural operation in the restricted Agent-body phase, and passes the profile-bound semantic oracle twice.

Canonical body-safe result: [`../evidence/attrs-770-profile-v1.json`](../evidence/attrs-770-profile-v1.json).

Private source, patch body, build logs, artifacts, requests, responses and diagnostic slices remain under `~/.hermes/evidence/pysolate/attrs-770-profile-build-v1/`. They are not tracked.

## Result

The final artifact was built on `gpu31.doc.ic.ac.uk` from signed source commit `26f2dd5df98c74d28b9ae066bf122fef4c1f2604`:

| Identity | Value |
|---|---|
| Artifact profile | `attrs-770` |
| Artifact | `agent-python-runtime-attrs-770.wasm` |
| Artifact SHA-256 | `5f8793f8306379db7c1e679cabfb8c0c6958daa11a30dc4f6844cedfcf2ce143` |
| Artifact size | 52,804,711 bytes |
| Target | `wasm32-wasip1` |
| CPython | 3.14.0 |
| attrs base commit | `58d2adce57f2c4e447eb12b892ebbb09cccbdcc3` |
| attrs source archive | `sha256:62aacc4a0014118dfedcca0f59767e21ba85aff60d3ac2c7b67caf97bda22f2b` |
| exact private Agent patch | `sha256:fdbfbdbb113809ae7982eb85e221ae5ddfdac9774a787114424e6ed2785f236e` |
| final package tree | `sha256:f1e3b25ec86f639a4ce256f5c1216fd585527142a08a284cc5fd9c9de603229f` |

The producer requires the private patch as an explicit input, validates its digest before any cache lookup, applies it to the locked upstream archive, validates the resulting 20-file package tree, and stages that tree into `site-packages/attr`. The base profile remains unchanged and the normal workflow still builds only base.

## Profile-bound replay

| Lane | Process | Guest result | Physical Guest |
|---|---:|---|---|
| Natural oracle, Run 1 | 0 | `oracle=passed` | yes |
| Natural oracle, Run 2 | 0 | `oracle=passed` | yes |
| Base Host profile against attrs artifact | 2 | `execution profile artifact mismatch` | no |
| Attrs profile with undeclared source import | 2 | `execution profile source comparison failed` | no |
| Reflective importer, declared `json` root | 0 | `module=json` | yes |
| Reflective importer, undeclared `os` root | 0 | structured `ImportError` | yes |

Both natural-oracle Runs used zero Host capability calls, had distinct request identities, and returned empty discard receipts with unchanged initial/final workspace identity.

## Import boundary found during implementation

The first artifact honestly exposed a phase mismatch:

- build qualification executed `attr:generic_dynamic_class` in the trusted import preamble and reported it qualified;
- the same operation in the restricted Agent body failed with `KeyError('__import__')` at `types.new_class(... Generic[T] ...)`.

The fix does not restore ambient imports. A compatibility-bound Run now installs a declared sealed importer in body builtins:

1. the exact static import preamble must equal `compatibility.imports`;
2. the preamble imports are loaded before execution;
3. the native Guest seals the exact loaded-module snapshot;
4. body import machinery can request only a declared root;
5. the native audit hook requires the exact module to be in the sealed snapshot;
6. `eval` and `exec` remain absent, and late/nested/dynamic source imports remain rejected by the source contract.

Qualification now executes the `attr`, `types`, and `typing` operations under the same restricted-body builtins. The final manifest therefore binds operation evidence for:

- `attr:generic_dynamic_class`;
- `types:new_class`;
- `typing:generic_alias`.

The reflective controls show the boundary directly: declared `json` resolves; undeclared `os` returns `ImportError` even though the Agent constructs the `__import__` key indirectly.

## Build workflow

The bounded workstation builder now supports:

```sh
scripts/build-guest-workstation.sh \
  --output /absolute/private/evidence-root \
  --artifact-profile attrs-770 \
  --extension-patch /absolute/private/model.patch \
  --gateway shell3 \
  --cache-mode auto
```

`--gateway` is an explicit `shell2|shell3` enum; default remains `shell2`. This was required because shell2 temporarily lost its `/vol/bitbucket` mount while shell3 and gpu31 retained it. The worker trust root and canonical path checks were not relaxed.

An independent review of the initial implementation found seven artifact-verification gaps. The final signed build closes all seven within the bounded producer/consumer contract:

- `pysolate-httpd` now consumes only `runtime.VerifyDistributionArtifact` output rather than trusting reduced operator metadata;
- the staged `site-packages/attr` tree is rehashed after deterministic copy and before packing;
- Python selection validation accepts only the exact committed attrs lock rather than shape-valid operator identities;
- fresh-build supply-chain generation requires exact equality between manifest sources and the effective source lock;
- Python and Go consumers require the complete canonical eleven-row attrs source set, including CPython, toolchain, linked/build inputs and attrs;
- the Python artifact verifier rejects duplicate JSON keys, non-standard constants, unknown top-level fields and unknown fields in every Go-typed nested manifest object;
- a restored final-cache bundle reruns the artifact verifier and bundle-level supply-chain verifier before being accepted.

Two consecutive builds of `26f2dd5` exercised both final-cache states. The first was a miss; the second was a real hit, completed in 38,997 ms, emitted the artifact verifier's exact SHA, passed bundle verification, and returned the same artifact identity.

## Reproduction

Regenerate the public report from retained private evidence:

```sh
python3 scripts/attrs-770-profile.py \
  --build-root ~/.hermes/evidence/pysolate/attrs-770-profile-build-v1/formal-build-final-v3-hit \
  --run-root ~/.hermes/evidence/pysolate/attrs-770-profile-build-v1 \
  --output docs/evidence/attrs-770-profile-v1.json
```

The report contains no source, patch body, request body, traceback or private path.

## Decision and limits

The exact `attrs-770` artifact profile is supported. It is not a production default and does not authorize:

- a generic package installer, dependency resolver or registry;
- native-extension support;
- automatic package/shard selection;
- a scheduler, worker pool or physical Guest sharing mechanism;
- a full Open-SWE trajectory replay or SWE-bench score;
- compatibility claims beyond the named operation and pinned package tree.

The experiment closes the narrow natural-task oracle/profile gap. Any generalized pure-Python shard design remains a new decision and must start from more than one package/task cohort.
