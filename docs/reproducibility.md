# Guest artifact reproducibility

The controlled [`Guest reproducibility`](../.github/workflows/reproducibility.yml) workflow builds one commit twice on independent `ubuntu-24.04` jobs and requires byte-for-byte equality of the complete guest bundle.

## Acceptance rule

A run succeeds only when both clean builds have the same file set and every file has the same SHA-256:

- `agent-python-runtime.wasm`;
- `manifest.json`;
- `SHA256SUMS`;
- `THIRD_PARTY_NOTICES.md`;
- any future file added to either bundle.

Manifest equality alone is insufficient. The comparator never ignores custom sections, timestamps, VFS bytes, notices, or unknown extra files.

Run the controlled workflow on the private repository with:

```bash
gh workflow run reproducibility.yml --repo bkmashiro/agent-python-runtime --ref main
```

Inspect its final conclusion and download `reproducibility-report-<commit>` before claiming reproducibility.

## Stable time input

`SOURCE_DATE_EPOCH` is the selected commit's Git timestamp (`git show -s --format=%ct <commit>`), resolved through [`tools/source_date_epoch.py`](../tools/source_date_epoch.py). Both clean jobs pass the exact same positive integer into the build. The normal artifact workflow uses the same resolver, and `build-guest.sh` resolves `HEAD` when no explicit value is supplied.

This makes repeated builds of one commit use one stable time input. It does not hide other nondeterminism.

## Diagnostics without weaker equality

[`tools/compare_guest_builds.py`](../tools/compare_guest_builds.py) writes a JSON report before the workflow enforces success. The report contains:

- file-set, size, and SHA-256 comparisons;
- JSON Pointer differences for `manifest.json`;
- ordered Core Wasm section IDs, custom-section names, sizes, and payload digests;
- manifest-to-artifact size/digest validation;
- the common repository commit and `SOURCE_DATE_EPOCH`, when valid.

Wasm section data is diagnostic only. A bundle with any byte difference still has `exact_match: false`, even if the differing section is considered semantically irrelevant.

If a run fails, investigate in this order:

1. file set and manifest pointers;
2. Wasm section diagnostics;
3. packaged VFS traversal and metadata;
4. build-tree contamination such as generated `Lib` files;
5. absolute temporary paths or tool outputs embedded in binaries.

Do not add ignore lists or normalize the comparison report to make a mismatch pass. Normalize the producer, rebuild twice, and retain exact equality as the gate.
