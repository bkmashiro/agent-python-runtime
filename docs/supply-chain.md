# Guest supply chain

Pysolate only binds an execution profile to a verified Guest distribution.

A distribution contains:

```text
agent-python-runtime.wasm
manifest.json
import-inventory.json
import-qualification.json
SHA256SUMS
SBOM and notices
```

The CLI checks:

- manifest schema and artifact filename;
- artifact and manifest digests;
- declared artifact profile;
- import inventory digest and roots;
- import qualification digest and qualified roots;
- selected Host profile roots are qualified by that exact artifact.

The Agent cannot select a manifest, artifact, import inventory or Host profile through `RunRequest`.

The active PoC does not implement a registry, updater, migration protocol, remote artifact service or release promotion database. Distribution creation and publication remain build/release concerns. The runtime consumes one already selected local distribution and fails closed when its sidecars disagree.
