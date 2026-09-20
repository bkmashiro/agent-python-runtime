# Guest artifact profiles

`agent-core` is the supported default Guest profile. It intentionally keeps external services, credentials, package installation, sockets and subprocesses on the Host side.

## Locked contents

The profile declaration is `guest/build/profiles/agent-core.json`. Its native NumPy inputs remain locked separately in `numpy-core.lock.json`; compiler and CPython inputs are locked in `tools/build-inputs.lock.json`.

Current declared runtime:

- CPython 3.14.0;
- NumPy 1.26.0b1;
- target `wasm32-wasip1`.

No extra pure-Python dependency is currently included. Add one only when a real acceptance workload fails and a Host capability is not the appropriate boundary.

## Qualification

Run all declared probes against the real artifact:

```sh
python3 tools/probe-guest-modules.py \
  --guest dist/pysolate.wasm \
  --output /tmp/agent-core-qualification.json
```

The command executes representative JSON, CSV, text, regex, datetime, SHA-256, HTML, XML and NumPy operations inside the Guest. It also runs the real private-workspace acceptance with Host HTTP, read-only SQLite and idempotent external-write tools. A profile passes only when every declared probe exists, succeeds, and the observed CPython and NumPy versions match the profile.

The report records the exact artifact byte size and SHA-256. It is runtime evidence, not a committed mutable “latest” result.

## Native link versus repack

`build-guest.sh` has two explicit stages controlled by `PYSOLATE_RELINK`:

```sh
# Recompile runtime.c and relink CPython/NumPy, then pack the Python tree.
PYSOLATE_RELINK=1 bash build-guest.sh

# Reuse the existing raw core and only rebuild/precompile/pack the Python tree.
PYSOLATE_RELINK=0 bash build-guest.sh
```

The default is `1`, which is conservative for native changes. The reusable core is `build/guest/native/raw-core.wasm` unless `PYSOLATE_RAW_CORE` overrides it. Repack mode still validates the final Wasm.

Each build writes `dist/pysolate.manifest.json` with:

- artifact and raw-core size/SHA-256;
- deterministic package-tree digest;
- profile, build-input lock and native-package lock digests;
- CPython/NumPy declarations;
- statically registered native module names;
- profile policy.

The manifest contains no build timestamp or machine path. The raw-core and package-tree digests are stable identities for the two build stages. The exact packed artifact digest identifies one output, but `wasi-vfs` 0.6.3 does **not** currently produce byte-identical Wasm across equivalent pack runs: its Guest-side packer consumes unspecified `fd_readdir` order before Wizer snapshots pointer-linked storage. Qualification therefore binds to the exact packed digest, while cache/rebuild decisions compare the raw-core and package-tree digests separately.
