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
    "output_capsule": "/absolute/host/next-state.pwc"
  }
}
```

The output is written to a mode-`0600` temporary file, synced, and atomically renamed only after the Guest returns a bounded response and the fresh runner closes. A runtime timeout or infrastructure failure does not publish a partial capsule; a bounded Python-error response may publish the resulting snapshot, which is storage evidence rather than an automatic commit. The original capsule remains a valid previous revision when a different output path is used.

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
