# Conservative source import comparison

**Status: Current for explicit compatibility manifests.**

Pysolate compares bounded, obvious static Python import declarations against caller declarations and the Host-bound execution profile before Guest work. The result is Host-authored, immutable after construction, exact-source-digest-bound evidence. It is suitable as a future `RunPlan` input; it is not a backend selector or an authority grant.

## Result contract

The canonical schema is `abi/v1/compatibility-result.schema.json`.

```json
{
  "schema_version": 1,
  "analyzer": "conservative-python-imports-v1",
  "status": "compatible",
  "syntax_checked": false,
  "source_sha256": "sha256:<exact source digest>",
  "profile": "base",
  "artifact_sha256": "sha256:<verified artifact digest>",
  "manifest_sha256": "sha256:<verified manifest digest>",
  "declared_imports": ["json", "statistics"],
  "observed_imports": ["json", "statistics"],
  "undeclared_imports": [],
  "unqualified_imports": [],
  "indeterminate_reasons": [],
  "evidence_sha256": "sha256:<canonical result digest>"
}
```

`artifact_sha256` and `manifest_sha256` are absent during the earliest Host-policy-only check and present together after artifact binding. `CompatibilityResult.Validate()` recomputes the canonical evidence digest and validates ordering, bounds, profile identity, status semantics, and paired artifact identity. Slice getters return copies.

## Status semantics

```text
unsupported
  if any obvious static import is absent from the caller declaration
  or absent from the current Host/profile import set

indeterminate
  if no known unsupported import exists, but the bounded scanner sees
  dynamic import/execution, relative import, lexical ambiguity,
  noncanonical import syntax, excessive source, or excessive import evidence

compatible
  only when all observed static roots are declared and admitted,
  and no indeterminate condition was observed
```

Known unsupported evidence takes precedence over indeterminate evidence: a request with both a definitely unavailable static import and dynamic construction is still `unsupported`.

## Recognized source forms

The versioned scanner recognizes roots from bounded forms such as:

```python
import json
import json.decoder as decoder, statistics
from pathlib import PurePosixPath
from xml.etree import ElementTree
```

It handles comments, quoted and triple-quoted strings, semicolon-separated statements, aliases, dotted module names, backslash continuation, and parenthesized multiline `from` imports. Strings and comments do not create import evidence.

Observed dotted imports are normalized to the root used by Host policy:

```text
json.decoder -> json
xml.etree    -> xml
```

## Indeterminate forms

The scanner fails closed for explicit forms including:

```python
__import__(name)
loader = __import__
import_module(name)
eval(source)
exec(source)
from .helpers import run
```

Reason vocabulary is frozen:

```text
dynamic_execution
dynamic_import
import_set_too_large
lexically_ambiguous
noncanonical_import
relative_import
source_too_large
```

Source is bounded to 1 MiB for this comparison. Observed import evidence is bounded to 1024 roots. Caller declarations remain bounded to 64 names.

## Admission order

```text
request/config decode
  -> typed requirements admission
  -> caller declaration / Host allowlist admission
  -> conservative source comparison (Host-policy identity)
  -> artifact + manifest + inventory + qualification verification
  -> profile binding
  -> conservative source comparison (artifact-bound identity)
  -> Factory / workspace / Broker / Guest
  -> Runner defensive comparison
```

An `unsupported` or `indeterminate` comparison returns the existing bounded `execution profile unsupported` / `profile_unsupported` contract. It does not produce `runtime_unsupported`, because it does not instruct Pysolate to select or launch another backend.

## Claim boundary

`compatible` means only:

```text
all import roots observed by conservative-python-imports-v1
are covered by caller declarations and current Host/profile policy,
and no explicit indeterminate construct was observed
```

It does **not** mean:

- Python syntax was checked;
- the exact Guest parser accepted the program;
- all dynamic imports were found;
- every transitive import is qualified;
- module initialization or arbitrary operations succeed;
- the program terminates or produces a valid result;
- the Host should automatically place the request on Pysolate;
- any filesystem, network, subprocess, shell, credential, or native authority was granted.

Caller declarations remain untrusted and mandatory for explicit compatibility admission. Source comparison can only reject or narrow them; it cannot add imports or authority.
