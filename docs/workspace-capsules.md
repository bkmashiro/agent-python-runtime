# Mounted workspaces and capsules

## Current status

Pysolate has two deliberately separate Host-owned workspace surfaces:

- `workspace_files` provisions three typed, in-memory text tools for receipt-oriented Code Mode experiments;
- `workspace` provisions a private rooted filesystem mounted at `/workspace` for ordinary Python file APIs.

They are mutually exclusive in one `apyrun` invocation. Neither can be selected or reconfigured by the Agent `RunRequest`.

## Mounted workspace

A Host operator can start from exactly one of:

- an empty workspace;
- a validated snapshot of a Host source directory;
- a complete Workspace Capsule.

A source directory is copied once into a private workspace. It is **not** a live bind mount: later Guest writes do not modify the source directory. Ingress accepts ordinary files and directories, rejects symbolic links, hard links, devices and filesystem-boundary crossings, and does not retain the Host source path.

The fresh CPython/WASI Guest sees:

```text
/workspace   private task filesystem
/tmp         separate per-Run scratch filesystem
```

The workspace enforces Host-selected bounds for entry count, total bytes, per-file bytes and path depth. A workspace lease has one writer, and its state can continue across fresh Guest instances when the Host keeps the same workspace alive.

## Workspace Capsule v1

A capsule is a deterministic, single-file, complete storage representation. It is not mounted or queried in place. Import validates the capsule and rehydrates a new private directory with a new Host-local `WorkspaceRef`.

The binary layout is:

```text
PYSOLATE-WORKSPACE-CAPSULE-V1\n
uint64 big-endian canonical-manifest length
canonical JSON manifest
file payloads in canonical path order
```

The manifest serializes:

- schema version;
- complete ordered directory and ordinary-file inventory, including empty directories;
- executable-bit semantics;
- file sizes and SHA-256 digests;
- workspace entry/byte/depth limits;
- a content tree digest;
- a workspace digest binding the tree and serialized limits.

The payload serializes every ordinary file byte, including binary files. Capsules intentionally exclude:

- Host paths and local `WorkspaceRef` values;
- leases and process-local manager state;
- `/tmp`;
- interpreter memory;
- capabilities, credentials and approval state.

Import rejects non-canonical manifests, unknown fields, traversal, `.git`, duplicate or unsorted entries, missing parents, unsupported entry kinds, digest mismatches, trailing bytes and any capsule whose serialized limits exceed the Host import ceiling.

## `apyrun` binding

Host config example:

```json
{
  "workspace": {
    "source_directory": "/absolute/host/input",
    "output_capsule": "/absolute/host/state.pwc",
    "disposition": "export_on_success",
    "limits": {
      "max_files": 4096,
      "max_bytes": 268435456,
      "max_file_bytes": 67108864,
      "max_depth": 32
    }
  }
}
```

A later invocation can restore the complete state:

```json
{
  "workspace": {
    "input_capsule": "/absolute/host/state.pwc",
    "output_capsule": "/absolute/host/next-state.pwc",
    "disposition": "export_on_success"
  }
}
```

`disposition` is required whenever the rooted workspace surface is configured:

- `export_on_success` stages and publishes only an `ok` response; a bounded Python-error response is inspected and reported as discarded;
- `export_on_response` stages and publishes either an `ok` or bounded Python-error response;
- `discard` forbids `output_capsule` and never serializes the final state.

Export policies require `output_capsule`. The output is written to a mode-`0600` temporary file while computing its exact byte SHA-256. The CLI then augments and revalidates the bounded response before atomically renaming the staged capsule. Parent-directory sync is best-effort, so this is atomic publication rather than a claim of crash durability. A runtime timeout, infrastructure failure, runner-close failure, invalid augmented response or response-budget failure does not publish a partial capsule. The original capsule remains a valid previous revision when a different output path is used. Generic `Manager.ExportCapsule` callers must provide their own temporary-file publication if they require failure atomicity.

Every bounded rooted-workspace response carries Host-authored `workspace_receipt` evidence containing the disposition policy and decision, the Host-bound request SHA-256, initial and final workspace SHA-256 identities, final tree identity and size/count metadata. An exported receipt additionally binds the exact capsule bytes. It contains no Host path. The receipt records storage disposition; it is not a transaction commit, rollback claim, or proof that external effects succeeded.

## Deliberate non-goals

Current capsules do not provide:

- a live database-backed filesystem;
- compression, chunking, deduplication or content-addressed blob storage;
- base/overlay layering, whiteouts or filesystem-level COW;
- automatic sync-back to the Host source directory;
- Git repository semantics;
- per-syscall receipts;
- transactional commit/rollback or side-effect classification.

Those may build on the current full-state capsule and rooted filesystem, but are not implied by it.
