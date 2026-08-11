# Source compatibility

## Agent-facing contract

The Agent submits Python source. It does not maintain an imports array.

`runtime.BindAgentSource` derives static import roots and creates the Host-bound compatibility declaration before the request enters the Guest.

## Accepted import form

The PoC accepts single-line, absolute, module-level imports in the initial import preamble:

```python
from __future__ import annotations
import csv
import json, math as m
from collections import Counter

result = Counter([m.floor(1.2)])
```

Roots are deduplicated and sorted. `__future__` is syntax configuration and is not included in the profile import roots.

## Rejected forms

The Host rejects rather than guesses:

```python
result = __import__("json")      # dynamic
from .local import value         # relative
if condition:
    import json                   # nested
result = 1
import json                       # late
from x import (a, b)             # multiline
import json; result = 1           # compound
```

This restriction is intentionally smaller than Python grammar. It keeps the PoC scanner short and inspectable. Supporting every valid placement, string/comment edge case, alias grammar or metaprogramming pattern is not a goal.

## Profile admission

For source with imports:

1. the Host derives roots;
2. every root must be allowed by the selected `ExecutionProfile`;
3. the profile must be bound to the exact distribution artifact;
4. every allowed root must be listed as available and qualified by the distribution manifest;
5. the trusted CPython Guest validates the source contract again before executing it.

Import-free source can run without a compatibility declaration. Dynamic-import syntax is still rejected by the Host scanner.

## Security interpretation

Source admission prevents accidental compatibility drift; it is not the primary authority boundary. Even an admitted module receives no ambient network, process, package-manager, native-extension, credential or Host-path capability.
