# Research-store backend decision

Status: **Experimental** local Lab prototype. This is not a Runtime-core
database, a production multi-user store, or an authentication boundary.

## Decision

Use a bounded, typed directory CAS in `research/labstore` for the current local
prototype. Immutable object files are sharded by semantic kind and digest. A
small filesystem index holds mutable privacy classifications and named retention
roots. Reachability is computed from immutable object links and those roots; the
filesystem namespace is the object index for now.

This keeps the implementation in the Go standard library and outside every
Runtime dependency path. It also lets the prototype directly exercise the
required durability and privacy boundary before a query-engine dependency is
justified. The choice is deliberately provisional.

The identity input is domain-separated by `pysolate.labstore.content.v1`, exact
semantic kind, canonical sorted links, and body bytes. Thus equal prompt and
code bytes cannot alias. Privacy, labels, roots, reference counts, retention,
and access-control state are mutable metadata and are not content identity.

## Options considered

### Directory CAS plus append-only index

A directory CAS provides simple random reads, independent corruption scope,
exclusive publication, easy inspection, and natural deduplication. An
append-only edge/root index would make ingestion cheap and permit rebuilding
derived views after a crash.

The current variant uses canonical per-object privacy records and canonical
named-root records instead of an append-only log. Bounded directory scans derive
edge counts and reachability. This avoids log recovery, compaction, sequence,
and torn-tail rules while the object model is still Experimental. Its costs are
one inode and durability sequence per object, metadata sidecar growth, an
`O(objects + links)` query, and no atomic transaction across multiple roots.

### Single-file pack plus index

A pack can reduce inode use and framing overhead, improve sequential ingest,
and eventually compress repeated small headers. It needs a separately durable
offset index, recovery rules for a torn append, corruption-localization and
repack/compaction semantics. Deleting one unreachable object becomes a pack
rewrite or tombstone operation. A bug can affect a larger corruption domain.

This is a good next measurement when object count or inode pressure—not query
semantics—is the demonstrated bottleneck. It is premature for the present
prototype because the low-level recovery design would exceed the evidence.

### SQLite metadata with external or internal blobs

SQLite metadata plus external CAS blobs would provide transactional roots,
indexed lineage queries, concurrency control, and efficient reachability
bookkeeping while preserving streaming blob files. It still pays the external
blob inode cost and must coordinate database commits with blob publication and
garbage collection.

SQLite with internal BLOBs gives one transaction and one protected artifact,
but large body churn, backup, WAL, vacuum, corruption scope, streaming bounds,
and pack-like compaction need qualification. Either SQLite form introduces a
new dependency, migration policy, and a strict query-only open path that must
prove it creates no database, journal, WAL, lock, or migration. Such a
dependency may live in a future Lab, never in Runtime core.

SQLite is the likely reframe when concurrent writers, atomic multi-reference
updates, indexed DAG queries, or partial semantic search become actual
requirements.

## Measured fixtures

Measured on `darwin/arm64`, Go `go1.26.0`, with logical regular-file byte sizes
(not allocated filesystem blocks). The destination is operator-selected and
must be a new protected local path; temporary benchmark stores are not release
artifacts.

```bash
go run ./research/labstore/cmd/labstore-bench \
  -root /private/tmp/pysolate-labstore-benchmark
```

| Shape | Raw duplicated bytes | Stored bytes | Stored/raw | Objects | Reused puts | Index bytes | Ingest | Query |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| long sequential | 1,056,429 | 118,282 | 0.1120 | 133 | 508 | 9,357 | 5.035 s | 76.696 ms |
| branch children | 584,425 | 175,781 | 0.3008 | 198 | 256 | 25,566 | 7.850 s | 173.372 ms |
| shared swarm | 1,184,046 | 154,142 | 0.1302 | 211 | 573 | 17,471 | 7.723 s | 149.908 ms |
| low-reuse control | 526,163 | 651,870 | 1.2389 | 321 | 0 | 22,328 | 9.442 s | 177.259 ms |

The high-reuse fixtures demonstrate material deduplication. The low-reuse
control honestly shows a 23.89% storage overhead from typed framing, relations,
privacy records, and the retention root. These numbers do not support a claim
that the backend is highly optimized.

The command emits `pysolate.labstore-benchmark.v1` JSON with raw bytes, object
and index bytes, object/link/root counts, reused puts, ingest/query nanoseconds,
ratios, and signed savings for every shape.

## Stop and reframe thresholds

Keep this backend only for bounded, local, predominantly single-writer studies
at or below 100,000 objects. Re-measure a pack or SQLite variant before raising
that ceiling. Reframe the backend when any representative target workload meets
one of these conditions in two comparable runs:

- stored/raw is at least `0.85` for a workload expected to have high reuse;
- index bytes exceed `35%` of stored bytes;
- bounded stats or retention queries exceed `2 s` by 100,000 objects;
- durable ingest exceeds `50 ms` per newly published object at the target
  storage location;
- projected inode count exceeds 100,000, favoring a pack measurement;
- concurrent writers, atomic multi-root updates, or indexed DAG predicates are
  required, favoring a SQLite measurement;
- low-reuse work becomes representative and remains above `1.25` stored/raw,
  favoring bypass, packing, or a non-CAS retention policy.

The measured low-reuse ratio (`1.2389`) is close to the last threshold. That is
an explicit warning to remeasure with real workloads, not evidence to expand
scope now.

## Integrity, retention, and privacy boundary

- Object publication uses a synced `0600` same-directory stage, an exclusive
  hard-link publication, directory sync, and no overwrite. Existing objects are
  validated rather than repaired or replaced.
- Reads reject oversized/truncated/trailing frames, invalid UTF-8, duplicate,
  folded/unknown/noncanonical header keys, malformed references, wrong modes,
  symlinks, and digest mismatch. Store-relative names are derived only from
  fixed kinds and validated lowercase digests.
- Read-only open requires an existing complete layout and performs no creation,
  migration, repair, staging, pinning, or collection.
- Every write requires an explicit credential-absent declaration. Structured
  JSON additionally rejects common credential-bearing field names. This is a
  defense in depth check, **not** a secret detector; callers must redact before
  ingestion. Credentials remain forbidden even for private objects.
- Bodies default to no implicit export. `private` dominates conflicting
  classifications. Portable reads recursively re-check every reachable object,
  so tightening a child to private also blocks a previously portable parent.
- Pins are the retention roots. Incoming counts are diagnostic; graph
  reachability is authoritative. Collection validates all exact targets first
  and deletes only objects outside the transitive closure, so a pinned branch
  retains its parent, prefix, manifest, initial workspace tree, and file blobs.
- Missing privacy metadata fails safely to private. Object self-hashes prove
  byte consistency, not authorship or authorization. The Host must protect the
  store root and any trusted exported identity separately.

## Known gaps

The prototype has an in-process mutex but no cross-process writer lock or
multi-object transaction. A same-UID Host peer racing filesystem replacement is
outside the Agent threat boundary. Crash-leftover stage names fail strict scans
and require explicit operator recovery; read-only opens never clean them.
Encryption at rest, multi-user ACLs, authentication, remote replication,
schema migration, pack compaction, semantic query indexes, and provider/Agent
trace ingestion remain Proposed future Lab work.
