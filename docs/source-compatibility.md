# Static Import Agent Code contract

**Status: Current for requests carrying an explicit `compatibility` manifest.**

Profile-qualified Pysolate execution accepts a bounded Python subset: all Agent-authored imports must form one static, absolute, module-level preamble. Dynamic module resolution is not a supported Agent Code feature. A legacy request without `compatibility` remains available only for internal build probes and the pre-profile ABI; it is not evidence of profile-qualified Pysolate placement.

## Source shape

An optional module docstring may precede the import preamble:

```python
"""bounded agent program"""

import json
import json.decoder as decoder
from pathlib import PurePosixPath

result = decoder.JSONDecoder().decode(inputs["payload"])
```

Allowed import forms are:

```python
import root
import root.child as alias
from root.child import Name
```

All imports must be contiguous, module-level, absolute, and before executable statements. Dotted imports are normalized to their root for policy:

```text
json.decoder -> json
xml.etree    -> xml
```

The caller declaration is root-only and exact:

```text
AST import roots = compatibility.imports
```

Extra declarations are rejected as well as missing declarations. This prevents an unused declaration from widening the runtime module envelope.

## Rejected forms

The exact Guest validator rejects:

```python
__import__(name)
loader = __import__
importlib.import_module(name)
builtins.__import__(name)
eval(source)
exec(source)

if condition:
    import json

def run():
    import statistics

try:
    import optional_module
except ImportError:
    pass

from .helpers import run
from package import *

result = prepare()
import json
```

Importing `importlib`, `builtins`, or `__future__` from Agent Code is outside this profile-qualified subset. Nested code objects are defensively checked for import opcodes.

## Two validation layers

### Host conservative comparison

`static-agent-imports-v2` remains the bounded early diagnostic and immutable evidence producer. It runs before artifact access where possible, compares observed roots with the declaration and Host policy, and emits `compatible | unsupported | indeterminate`.

The canonical schema is `abi/v1/compatibility-result.schema.json`:

```json
{
  "schema_version": 2,
  "analyzer": "static-agent-imports-v2",
  "status": "compatible",
  "syntax_checked": false,
  "source_sha256": "sha256:<exact source digest>",
  "profile": "base",
  "artifact_sha256": "sha256:<verified artifact digest>",
  "manifest_sha256": "sha256:<verified manifest digest>",
  "declared_imports": ["json", "statistics"],
  "observed_imports": ["json", "statistics"],
  "undeclared_imports": [],
  "unused_declared_imports": [],
  "unqualified_imports": [],
  "indeterminate_reasons": [],
  "evidence_sha256": "sha256:<canonical result digest>"
}
```

Dynamic import/execution, relative imports, star imports, and imports outside the preamble are `unsupported`. Lexical ambiguity and evidence bounds remain `indeterminate`. Neither status selects another backend.

### Exact Guest preparation

The Host scanner is not authoritative for Python syntax. After a fresh or never-served Guest is checked out, but before workspace activation or Broker construction, the Host first executes bounded Host-authored trusted preparation without Broker authority, then calls:

```text
runtime_validate_source(exact request bytes)
```

The exact target CPython then:

1. decodes the exact request;
2. parses with `ast.parse`;
3. validates the static preamble and exact declaration equality;
4. compiles a business body with preamble nodes removed;
5. executes only the import preamble in a preparation namespace;
6. retains its alias bindings for the business body;
7. seals the modules loaded by bootstrap, trusted preparation, and the import preamble;
8. stores the compiled body bound to the exact request bytes.

Only after status `0` does the Host activate workspace mounts, construct the capability Broker, and execute the business body.

Status `1` maps to `agent source contract unsupported`; status `2` maps to `agent source invalid`. Neither creates a Broker or causes VM selection.

## CPython import gate

The pinned CPython 3.14 source is patched at build time to emit `agent_runtime.import` after absolute-name resolution and before `sys.modules` cache lookup. A native `PySys_AddAuditHook` registered before interpreter initialization enforces the sealed exact module-name set.

This placement matters: the ordinary upstream `import` audit event occurs only on the find/load path and can miss an already cached module. The Pysolate event is raised before cache access, so cached and newly loaded imports cross the same gate.

The Agent business body receives a per-execution builtins copy without:

```text
__import__
eval
exec
```

Imported libraries retain their normal builtins. If Agent code obtains another import callable indirectly, the native CPython gate still rejects any module outside the sealed preparation set.

A denied late import is an ordinary failed Pysolate Run. It never triggers automatic retry, VM upgrade, backend migration, or WASM-to-native continuation.

## Admission order

```text
request/config decode
  -> typed requirements admission
  -> declaration / Host allowlist admission
  -> conservative source comparison (Host-policy identity)
  -> artifact + manifest + inventory + qualification verification
  -> artifact-bound profile binding
  -> conservative source comparison (artifact-bound identity)
  -> Factory / fresh-or-never-served Guest checkout
  -> Host trusted preparation (no Broker)
  -> exact Guest AST/bytecode validation
  -> static preamble import + module-set seal
  -> workspace activation / Broker construction
  -> business-body execution
```

## Claim boundary

A successful source preparation proves:

```text
exact request bytes
+ exact target Guest parser/compiler
+ static absolute import preamble
+ exact caller root declaration
+ successful preloading
+ sealed runtime module set
```

It does not prove:

- complete theoretical transitive import closure;
- arbitrary operations on a qualified module;
- all data-dependent library paths succeed;
- program termination or business success;
- filesystem, network, shell, subprocess, credential, or native authority;
- that another backend should be selected automatically.

The CPython gate is compatibility enforcement, not the primary security sandbox. Real authority remains Host-owned through WASI, bounded workspace mounts, capabilities, effects, receipts, and transaction policy.

## Legacy internal path

Current artifact qualification uses fresh Guest requests without a `compatibility` manifest to dynamically probe one module per isolated invocation. Those requests retain the pre-profile ABI so the producer can generate `import-qualification.json`. They do not receive profile-qualified placement claims. A later dedicated qualification ABI can remove this legacy exception without exposing dynamic import to Agent Code.
