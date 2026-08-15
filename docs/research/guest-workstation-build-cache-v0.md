# Guest workstation build cache v0

Status: **Experimental build acceleration; artifact verification remains mandatory**  
Date: 2026-08-15

## Purpose

The official Linux x86_64 Guest build may reuse the expensive CPython/WASI build layer
on `gpu31`. This is build-only state. It grants no Runtime authority and never replaces
final artifact verification or real-Guest tests.

## Identity

`pysolate.guest-build-cache-key.v0` binds:

- canonical `guest/build/sources.lock.json`;
- the exact marked CPython/WASI recipe in `build-guest.sh`;
- the identity and archive validator implementations;
- CPython patches;
- target, Host OS and Host architecture.

Guest bootstrap/application source is deliberately absent. A bootstrap-only edit keeps
the layer key but still rebuilds embedding, link, manifest, SBOM, checksums and probes.
Changing a locked source, toolchain, patch, recipe, identity implementation or Host
platform produces a different key.

## Storage and corruption boundary

The optional cache root must be an absolute, non-symlink directory and is forced to mode
0700. Publication is serialized with `flock`. A miss builds in a job-private `/tmp`
workspace and publishes a complete temporary directory by rename. `RESULT.READY` is
written only after `layer.tar` and `SHA256SUMS` exist.

A hit requires:

1. an exact key marker;
2. a valid layer checksum;
3. a bounded tar containing exactly the `downloads`, `tools`, and `cpython` roots;
4. no absolute/traversing members, escaping links, devices, FIFOs or unknown member
   types.

Invalid cache state becomes a miss and is replaced while holding the publish lock. The
maintenance step retains only the protected current key plus the newest other valid
64-hex key; unrelated or symlink entries are never removed. Every build records
`build-cache.json` as `off`, `miss`, or `hit` plus exact key and layer digest.

## Workstation workflow

From a clean checkout:

```bash
scripts/build-guest-workstation.sh \
  --cache-mode auto \
  --output /private/tmp/pysolate-workstation-result
```

The repository-owned driver archives exact clean `HEAD`, stages it through `shell2`, and
runs on `gpu31` in a private `/tmp` workspace. The remote worker emits:

- `dist/agent-python-runtime.wasm` and the complete verified distribution;
- `RESULT.READY` binding source commit/tree, builder, target, cache identity/disposition
  and measured build duration;
- `SHA256SUMS` over ready marker, log and every distribution file;
- `build.log`.

The driver retrieves and independently verifies the bundle locally, then removes only
that run's exact shared stage/output directories. The keyed cache remains. `refresh`
removes only the exact current cache-key entry under the publication lock; `off` bypasses
cache reads/writes.

## Qualification gate

Before v0 is accepted:

- one `refresh` build must report `miss`;
- a second exact-source `auto` build must report `hit`;
- both complete artifacts must have identical SHA-256;
- both evidence bundles must verify independently;
- the warm duration and speedup are reported as observed, not guaranteed;
- the warm artifact must pass the real Guest semantic E2E gate;
- mutation, key-drift, archive-safety and evidence-drift tests must pass.

Cache hit alone never qualifies an artifact.
