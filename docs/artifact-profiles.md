# Guest artifact profiles

`agent-core` is the supported default Guest profile. It intentionally keeps external services, credentials, package installation, sockets and subprocesses on the Host side.

## Locked contents

The profile declaration is `guest/build/profiles/agent-core.json`. Its native NumPy inputs remain locked separately in `numpy-core.lock.json`; compiler and CPython inputs are locked in `tools/build-inputs.lock.json`.

Current declared runtime:

- CPython 3.14.0;
- NumPy 1.26.0b1;
- PyYAML 6.0.3, pure Python only;
- target `wasm32-wasip1`.

PyYAML is the only extra pure-Python dependency. Its source archive, version, SHA-256 and MIT license are pinned in `tools/build-inputs.lock.json`; the build copies no `_yaml` native extension and packages its metadata and license. External services, credentials, package installation, sockets and subprocesses remain Host concerns.

## Qualification

Run all declared probes against the real artifact:

```sh
python3 tools/probe-guest-modules.py \
  --guest dist/pysolate.wasm \
  --output /tmp/agent-core-qualification.json
```

The command executes representative JSON, JSONL, CSV, text, regex, datetime, SHA-256, HTML, XML, TOML, INI, URL/Base64, repository-code, safe YAML and NumPy operations inside the Guest. It also runs the original private-workspace acceptance with Host HTTP, read-only SQLite and idempotent external-write tools, plus the common-usecase acceptance for local imports, configuration editing, data normalization and a named Host market-data tool. A profile passes only when every declared probe exists, succeeds, and the observed CPython, NumPy and PyYAML versions match the profile.

The report records the exact artifact byte size and SHA-256. It is runtime evidence, not a committed mutable “latest” result.

## Build and prebuilt consumption

The supported entry points are intentionally small:

```sh
make bootstrap        # one-time pinned Linux x86_64 CPython/WASI + NumPy inputs
make guest            # native relink plus VFS packaging
make repack           # reuse raw-core.wasm; update only packaged Python content
make verify-artifact  # verify manifest, digest, size, profile and Wasm header
make artifact-bundle  # create a deterministic two-file .tar.gz bundle
make artifact-install BUNDLE=/path/or/https-url/pysolate-agent-core.tar.gz
```

`artifact-install` accepts only a local path or HTTPS URL. It rejects links,
path traversal, additional archive entries, oversized files and a mismatched
manifest before replacing `dist/pysolate.wasm` and its manifest. This allows a
consumer to use a qualified prebuilt without installing the compiler toolchain.
The repository does not silently select a mutable “latest” build; a release or
internal distribution channel must provide an explicit bundle URL.

The setup scripts use `curl`, so conventional `http_proxy`, `https_proxy` and
`no_proxy` environment variables are inherited. For example, a WSL build can
point them at an explicitly reachable Clash Verge listener before running
`make bootstrap`. No proxy address or credentials are persisted by Pysolate.

## Native link versus repack

`build-guest.sh` has two explicit stages controlled by `PYSOLATE_RELINK`:

```sh
make guest   # PYSOLATE_RELINK=1
make repack  # PYSOLATE_RELINK=0
```

Guest bootstrap changes and pure-Python package changes use the repack path; they do not require relinking CPython or NumPy. The build still checks that the pinned PyYAML source, package metadata and license are present before packing.

The default is `1`, which is conservative for native changes. The reusable core is `build/guest/native/raw-core.wasm` unless `PYSOLATE_RAW_CORE` overrides it. Repack mode still validates the final Wasm.

Each build writes `dist/pysolate.manifest.json` with:

- artifact and raw-core size/SHA-256;
- deterministic package-tree digest;
- profile, build-input lock and native-package lock digests;
- CPython/NumPy declarations;
- statically registered native module names;
- profile policy.

The manifest contains no build timestamp or machine path. The raw-core and package-tree digests are stable identities for the two build stages. The exact packed artifact digest identifies one output, but `wasi-vfs` 0.6.3 does **not** currently produce byte-identical Wasm across equivalent pack runs: its Guest-side packer consumes unspecified `fd_readdir` order before Wizer snapshots pointer-linked storage. Qualification therefore binds to the exact packed digest, while cache/rebuild decisions compare the raw-core and package-tree digests separately.
